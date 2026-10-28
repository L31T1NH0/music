package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	bassBoostFilter        = "lavfi=[bass=g=5:f=110:t=q:w=0.7,volume=-3dB,alimiter=limit=0.95:level=false]"
	bassBoostLabeledFilter = "@music-bass:" + bassBoostFilter
)

type mpvResponse struct {
	Data      json.RawMessage `json:"data"`
	Error     string          `json:"error"`
	RequestID uint64          `json:"request_id"`
}

type playerSession struct {
	id         uint64
	track      Track
	proc       *exec.Cmd
	socketDir  string
	socketPath string
	done       chan struct{}
	requestID  atomic.Uint64

	exitMu  sync.RWMutex
	exitErr error
	stop    sync.Once

	connMu  sync.Mutex
	conn    net.Conn
	scanner *bufio.Scanner
}

func newPlayerSession(id uint64, track Track, proc *exec.Cmd, socketDir, socketPath string) *playerSession {
	session := &playerSession{
		id:         id,
		track:      track,
		proc:       proc,
		socketDir:  socketDir,
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
	go session.reap()
	return session
}

func (session *playerSession) reap() {
	err := session.proc.Wait()
	session.exitMu.Lock()
	session.exitErr = err
	session.exitMu.Unlock()
	session.connMu.Lock()
	session.closeIPCLocked()
	session.connMu.Unlock()
	_ = os.Remove(session.socketPath)
	_ = os.Remove(session.socketDir)
	close(session.done)
}

func (session *playerSession) wait() error {
	if session == nil {
		return nil
	}
	<-session.done
	session.exitMu.RLock()
	defer session.exitMu.RUnlock()
	return session.exitErr
}

func (session *playerSession) stopProcess(timeout time.Duration) {
	if session == nil {
		return
	}
	session.stop.Do(func() {
		select {
		case <-session.done:
			return
		default:
		}
		if session.proc.Process != nil {
			_ = signalProcessGroup(session.proc, syscall.SIGINT)
		}
		select {
		case <-session.done:
		case <-time.After(timeout):
			if session.proc.Process != nil {
				_ = signalProcessGroup(session.proc, syscall.SIGKILL)
			}
			<-session.done
		}
	})
}

func (session *playerSession) call(command []any) (mpvResponse, error) {
	var response mpvResponse
	if session == nil {
		return response, fmt.Errorf("player não está ativo")
	}
	select {
	case <-session.done:
		return response, fmt.Errorf("player já terminou")
	default:
	}

	session.connMu.Lock()
	defer session.connMu.Unlock()
	select {
	case <-session.done:
		return response, fmt.Errorf("player já terminou")
	default:
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := session.connectIPCLocked(); err != nil {
			lastErr = err
			continue
		}
		requestID := session.requestID.Add(1)
		request := struct {
			Command   []any  `json:"command"`
			RequestID uint64 `json:"request_id"`
		}{
			Command:   command,
			RequestID: requestID,
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			return response, err
		}
		encoded = append(encoded, '\n')
		if err := session.conn.SetDeadline(time.Now().Add(700 * time.Millisecond)); err != nil {
			lastErr = err
			session.closeIPCLocked()
			continue
		}
		if _, err := session.conn.Write(encoded); err != nil {
			lastErr = err
			session.closeIPCLocked()
			continue
		}

		for session.scanner.Scan() {
			if err := json.Unmarshal(session.scanner.Bytes(), &response); err != nil {
				continue
			}
			if response.RequestID != requestID {
				continue
			}
			if response.Error != "success" {
				return response, fmt.Errorf("mpv: %s", oneLine(response.Error))
			}
			return response, nil
		}
		if err := session.scanner.Err(); err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("mpv encerrou a conexão sem responder")
		}
		session.closeIPCLocked()
	}
	return response, lastErr
}

func (session *playerSession) connectIPCLocked() error {
	if session.conn != nil {
		return nil
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.Dial("unix", session.socketPath)
	if err != nil {
		return err
	}
	session.conn = conn
	session.scanner = bufio.NewScanner(conn)
	session.scanner.Buffer(make([]byte, 1024), 64<<10)
	return nil
}

func (session *playerSession) closeIPCLocked() {
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.conn = nil
	session.scanner = nil
}

func (session *playerSession) getFloat(property string) (float64, error) {
	response, err := session.call([]any{"get_property", property})
	if err != nil {
		return 0, err
	}
	var value float64
	if err := json.Unmarshal(response.Data, &value); err != nil {
		return 0, fmt.Errorf("propriedade %s inválida: %w", property, err)
	}
	return value, nil
}

func (session *playerSession) getBool(property string) (bool, error) {
	response, err := session.call([]any{"get_property", property})
	if err != nil {
		return false, err
	}
	var value bool
	if err := json.Unmarshal(response.Data, &value); err != nil {
		return false, fmt.Errorf("propriedade %s inválida: %w", property, err)
	}
	return value, nil
}

func startPlayingCmd(
	ctx context.Context,
	control *taskControl,
	oldSession *playerSession,
	requestID uint64,
	track Track,
	volume int,
	bassBoost bool,
) tea.Cmd {
	return func() tea.Msg {
		defer control.finish()
		if oldSession != nil {
			oldSession.stopProcess(3 * time.Second)
		}
		select {
		case <-ctx.Done():
			return playerErrMsg{requestID: requestID, track: track, err: ctx.Err(), canceled: true}
		default:
		}

		socketDir, err := os.MkdirTemp("", fmt.Sprintf("music-mpv-%d-", os.Getpid()))
		if err != nil {
			return playerErrMsg{requestID: requestID, track: track, err: fmt.Errorf("criar socket: %w", err)}
		}
		if err := os.Chmod(socketDir, 0o700); err != nil {
			_ = os.Remove(socketDir)
			return playerErrMsg{requestID: requestID, track: track, err: fmt.Errorf("proteger socket: %w", err)}
		}
		socketPath := filepath.Join(socketDir, "ipc.sock")

		arguments := []string{
			"--no-video",
			"--no-terminal",
			"--really-quiet",
			"--audio-display=no",
			"--input-ipc-server=" + socketPath,
			fmt.Sprintf("--volume=%d", volume),
		}
		if bassBoost {
			arguments = append(arguments, "--af="+bassBoostLabeledFilter)
		}
		arguments = append(arguments, "--", track.URL)
		cmd := exec.Command("mpv", arguments...)
		configureProcessGroup(cmd)
		if err := cmd.Start(); err != nil {
			_ = os.RemoveAll(socketDir)
			return playerErrMsg{requestID: requestID, track: track, err: friendlyExecutableError("mpv", err)}
		}
		session := newPlayerSession(requestID, track, cmd, socketDir, socketPath)
		control.setResourceCleanup(func() { session.stopProcess(2 * time.Second) })
		handedOff := false
		defer func() {
			if !handedOff {
				control.cleanupUnadoptedResource()
			}
		}()
		go func() {
			<-ctx.Done()
			control.cleanupUnadoptedResource()
		}()

		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(75 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				session.stopProcess(2 * time.Second)
				return playerErrMsg{requestID: requestID, track: track, err: ctx.Err(), canceled: true}
			case <-session.done:
				exitErr := session.wait()
				if exitErr == nil {
					exitErr = fmt.Errorf("processo terminou sem iniciar a reprodução")
				}
				return playerErrMsg{
					requestID: requestID,
					track:     track,
					err:       fmt.Errorf("mpv encerrou antes de iniciar: %w", exitErr),
				}
			case <-deadline.C:
				session.stopProcess(2 * time.Second)
				return playerErrMsg{requestID: requestID, track: track, err: fmt.Errorf("timeout ao conectar ao mpv")}
			case <-ticker.C:
				if _, err := os.Stat(socketPath); err != nil {
					continue
				}
				if _, err := session.getFloat("pid"); err == nil {
					handedOff = true
					return playerReadyMsg{requestID: requestID, session: session, control: control}
				}
			}
		}
	}
}

func friendlyExecutableError(name string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s não encontrado no PATH", name)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("não foi possível executar %s: %s", name, oneLine(pathErr.Err.Error()))
	}
	return fmt.Errorf("não foi possível executar %s: %s", name, oneLine(err.Error()))
}

func waitTrackCmd(session *playerSession) tea.Cmd {
	return func() tea.Msg {
		return trackEndedMsg{sessionID: session.id, err: session.wait()}
	}
}

func schedulePlayerPoll(sessionID uint64) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return playerPollMsg{sessionID: sessionID}
	})
}

func pollPlayerCmd(session *playerSession) tea.Cmd {
	return func() tea.Msg {
		position, err := session.getFloat("playback-time")
		if err != nil && !strings.Contains(err.Error(), "property unavailable") {
			return playerStatusMsg{sessionID: session.id, err: err}
		}
		duration, durationErr := session.getFloat("duration")
		if durationErr != nil && !strings.Contains(durationErr.Error(), "property unavailable") {
			return playerStatusMsg{sessionID: session.id, err: durationErr}
		}
		paused, pauseErr := session.getBool("pause")
		if pauseErr != nil {
			return playerStatusMsg{sessionID: session.id, err: pauseErr}
		}
		volume, volumeErr := session.getFloat("volume")
		if volumeErr != nil {
			return playerStatusMsg{sessionID: session.id, err: volumeErr}
		}
		return playerStatusMsg{
			sessionID: session.id,
			pos:       max(0, position),
			duration:  max(0, duration),
			paused:    paused,
			volume:    min(150, max(0, int(volume+0.5))),
		}
	}
}

func playerControlCmd(session *playerSession, action string, command []any) tea.Cmd {
	return func() tea.Msg {
		_, err := session.call(command)
		return playerControlDoneMsg{sessionID: session.id, action: action, err: err}
	}
}

func bassBoostControlCmd(session *playerSession, enabled bool) tea.Cmd {
	return func() tea.Msg {
		_, err := session.call(bassBoostIPCCommand(enabled))
		return playerControlDoneMsg{
			sessionID:   session.id,
			action:      "alterar reforço de graves",
			err:         err,
			bassChange:  true,
			bassEnabled: enabled,
		}
	}
}

func bassBoostIPCCommand(enabled bool) []any {
	if enabled {
		return []any{"af", "add", bassBoostLabeledFilter}
	}
	return []any{"af", "remove", "@music-bass"}
}
