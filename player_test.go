package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestPlayerSessionHasSingleWaitOwner(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "5")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	socketDir := t.TempDir()
	session := newPlayerSession(1, Track{Title: "test"}, cmd, socketDir, filepath.Join(socketDir, "ipc.sock"))

	const waiters = 8
	var wait sync.WaitGroup
	wait.Add(waiters)
	for index := 0; index < waiters; index++ {
		go func() {
			defer wait.Done()
			_ = session.wait()
		}()
	}
	session.stopProcess(time.Second)
	wait.Wait()
	if session.proc.ProcessState == nil {
		t.Fatal("process was not reaped")
	}
}

func TestMPVCallRejectsErrorAndMatchesRequestID(t *testing.T) {
	t.Parallel()
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "ipc.sock")
	listener, err := net.Listen("unix", socketPath)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox does not allow Unix sockets")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	cmd := exec.Command("sleep", "5")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	session := newPlayerSession(11, Track{Title: "test"}, cmd, socketDir, socketPath)
	t.Cleanup(func() { session.stopProcess(time.Second) })

	go serveOneMPVResponse(t, listener, func(requestID uint64) []map[string]any {
		return []map[string]any{{
			"request_id": requestID,
			"error":      "property unavailable",
		}}
	})
	if _, err := session.call([]any{"get_property", "pid"}); err == nil {
		t.Fatal("mpv error response was accepted as success")
	}

	go serveOneMPVResponse(t, listener, func(requestID uint64) []map[string]any {
		return []map[string]any{
			{"request_id": requestID + 99, "error": "success", "data": 1},
			{"request_id": requestID, "error": "success", "data": 123},
		}
	})
	value, err := session.getFloat("pid")
	if err != nil {
		t.Fatal(err)
	}
	if value != 123 {
		t.Fatalf("value=%v, want 123", value)
	}
}

func serveOneMPVResponse(
	t *testing.T,
	listener net.Listener,
	responses func(uint64) []map[string]any,
) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Error(err)
		return
	}
	var request struct {
		RequestID uint64 `json:"request_id"`
	}
	if err := json.Unmarshal(line, &request); err != nil {
		t.Error(err)
		return
	}
	for _, response := range responses(request.RequestID) {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Error(err)
			return
		}
		encoded = append(encoded, '\n')
		if _, err := conn.Write(encoded); err != nil {
			t.Error(err)
			return
		}
	}
}

func TestFriendlyExecutableErrorDistinguishesFailureKinds(t *testing.T) {
	t.Parallel()
	missing := friendlyExecutableError("missing-binary", exec.ErrNotFound)
	if !strings.Contains(missing.Error(), "não encontrado") {
		t.Fatalf("missing error=%q", missing)
	}
	permission := friendlyExecutableError("mpv", &os.PathError{Op: "fork", Path: "mpv", Err: os.ErrPermission})
	if strings.Contains(permission.Error(), "não encontrado") || !strings.Contains(permission.Error(), "permission denied") {
		t.Fatalf("permission error=%q", permission)
	}
}

func TestPlayerLifecycleWithFakeMPV(t *testing.T) {
	if os.Getenv("MUSIC_FAKE_MPV") == "1" {
		runFakeMPV(t)
		return
	}

	probePath := filepath.Join(t.TempDir(), "probe.sock")
	probe, err := net.Listen("unix", probePath)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox does not allow Unix sockets")
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = probe.Close()

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	wrapper := filepath.Join(binDir, "mpv")
	script := fmt.Sprintf(
		"#!/bin/sh\nMUSIC_FAKE_MPV=1 exec %q -test.run='^TestPlayerLifecycleWithFakeMPV$' -- \"$@\"\n",
		testBinary,
	)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("PATH", oldPath) })

	ctx, control := newTaskControl(context.Background())
	track := Track{Title: "fake", URL: "fake://track", Source: sourceYT}
	message := startPlayingCmd(ctx, control, nil, 77, track, 80, true)()
	ready, ok := message.(playerReadyMsg)
	if !ok {
		t.Fatalf("start message=%#v", message)
	}
	session := ready.session
	ready.control.adoptResource()
	t.Cleanup(func() { session.stopProcess(time.Second) })
	if permission := directoryPermission(t, session.socketDir); permission != 0o700 {
		t.Fatalf("socket directory permission=%o", permission)
	}

	statusMessage := pollPlayerCmd(session)()
	status, ok := statusMessage.(playerStatusMsg)
	if !ok || status.err != nil {
		t.Fatalf("status=%#v", statusMessage)
	}
	if status.pos != 12 || status.duration != 120 || status.paused || status.volume != 80 {
		t.Fatalf("status=%#v", status)
	}
	if controlMessage := bassBoostControlCmd(session, false)(); controlMessage.(playerControlDoneMsg).err != nil {
		t.Fatalf("bass control=%#v", controlMessage)
	}
	if controlMessage := bassBoostControlCmd(session, true)(); controlMessage.(playerControlDoneMsg).err != nil {
		t.Fatalf("bass control=%#v", controlMessage)
	}
	if controlMessage := playerControlCmd(session, "pause", []any{"set_property", "pause", true})(); controlMessage.(playerControlDoneMsg).err != nil {
		t.Fatalf("control=%#v", controlMessage)
	}
	status = pollPlayerCmd(session)().(playerStatusMsg)
	if !status.paused {
		t.Fatal("pause state did not round-trip through IPC")
	}

	session.stopProcess(time.Second)
	ended := waitTrackCmd(session)().(trackEndedMsg)
	if ended.sessionID != 77 {
		t.Fatalf("ended=%#v", ended)
	}
}

func TestCanceledUnadoptedPlayerIsCleanedUp(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "5")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	socketDir := t.TempDir()
	session := newPlayerSession(9, Track{Title: "queued-ready"}, cmd, socketDir, filepath.Join(socketDir, "ipc.sock"))
	_, control := newTaskControl(context.Background())
	control.setResourceCleanup(func() { session.stopProcess(time.Second) })
	control.finish()

	control.cancelAndWait(2 * time.Second)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("unadopted player survived cancellation")
	}
}

func TestFinishedTasksReleaseContextsButPreservePendingResources(t *testing.T) {
	t.Parallel()
	ctx, control := newTimedTaskControl(context.Background(), time.Minute)
	control.finish()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("finished task retained its timeout context")
	}

	resourceCtx, resourceControl := newTaskControl(context.Background())
	resourceControl.setResourceCleanup(func() {})
	resourceControl.finish()
	select {
	case <-resourceCtx.Done():
		t.Fatal("pending resource was canceled before model adoption")
	default:
	}
	resourceControl.adoptResource()
	select {
	case <-resourceCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("adopted resource did not release its handoff context")
	}
}

func TestBassBoostIPCCommandsAreLabeled(t *testing.T) {
	t.Parallel()
	if got, want := bassBoostIPCCommand(true), []any{"af", "add", bassBoostLabeledFilter}; !reflect.DeepEqual(got, want) {
		t.Fatalf("enable command=%#v, want %#v", got, want)
	}
	if got, want := bassBoostIPCCommand(false), []any{"af", "remove", "@music-bass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disable command=%#v, want %#v", got, want)
	}
}

func TestRealMPVBassBoostToggle(t *testing.T) {
	if os.Getenv("MUSIC_REAL_MPV_TEST") != "1" {
		t.Skip("set MUSIC_REAL_MPV_TEST=1 to exercise the installed mpv")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv is not installed")
	}

	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "ipc.sock")
	cmd := exec.Command(
		"mpv",
		"--no-config",
		"--no-terminal",
		"--really-quiet",
		"--no-video",
		"--ao=null",
		"--idle=yes",
		"--input-ipc-server="+socketPath,
		"av://lavfi:anullsrc=r=48000:cl=stereo",
	)
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	session := newPlayerSession(91, Track{Title: "real mpv"}, cmd, socketDir, socketPath)
	t.Cleanup(func() { session.stopProcess(time.Second) })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := session.getFloat("pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real mpv IPC did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if message := bassBoostControlCmd(session, true)().(playerControlDoneMsg); message.err != nil {
		t.Fatalf("enable bass: %v", message.err)
	}
	filters, err := session.call([]any{"get_property", "af"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(filters.Data, []byte("music-bass")) {
		t.Fatalf("labeled filter was not installed: %s", filters.Data)
	}

	if message := bassBoostControlCmd(session, false)().(playerControlDoneMsg); message.err != nil {
		t.Fatalf("disable bass: %v", message.err)
	}
	filters, err = session.call([]any{"get_property", "af"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(filters.Data, []byte("music-bass")) {
		t.Fatalf("labeled filter was not removed: %s", filters.Data)
	}
}

func directoryPermission(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func runFakeMPV(t *testing.T) {
	t.Helper()
	socketPath := ""
	hasBassFilter := false
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--input-ipc-server=") {
			socketPath = strings.TrimPrefix(arg, "--input-ipc-server=")
		}
		if arg == "--af="+bassBoostLabeledFilter {
			hasBassFilter = true
		}
	}
	if socketPath == "" {
		t.Fatal("missing --input-ipc-server")
	}
	if !hasBassFilter {
		t.Fatal("missing labeled bass filter")
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	paused := false
	volume := 80.0
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)
		for {
			var request struct {
				Command   []any  `json:"command"`
				RequestID uint64 `json:"request_id"`
			}
			if err := decoder.Decode(&request); err != nil {
				break
			}
			response := map[string]any{"request_id": request.RequestID, "error": "success"}
			if len(request.Command) >= 2 {
				command, _ := request.Command[0].(string)
				property, _ := request.Command[1].(string)
				switch command {
				case "get_property":
					switch property {
					case "pid":
						response["data"] = float64(os.Getpid())
					case "playback-time":
						response["data"] = 12.0
					case "duration":
						response["data"] = 120.0
					case "pause":
						response["data"] = paused
					case "volume":
						response["data"] = volume
					default:
						response["error"] = "property unavailable"
					}
				case "set_property":
					if len(request.Command) >= 3 {
						switch property {
						case "pause":
							paused, _ = request.Command[2].(bool)
						case "volume":
							volume, _ = request.Command[2].(float64)
						}
					}
				}
			}
			if err := encoder.Encode(response); err != nil {
				break
			}
		}
		_ = conn.Close()
	}
}
