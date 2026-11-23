package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type MusicSyncConfig struct {
	APIURL           string `json:"api_url"`
	Token            string `json:"token"`
	ProtectionBypass string `json:"protection_bypass,omitempty"`
}
type musicEvent struct {
	SessionID       string    `json:"sessionId"`
	Sequence        int64     `json:"sequence"`
	ObservedAt      time.Time `json:"observedAt"`
	Title           string    `json:"title"`
	Artist          string    `json:"artist"`
	Cover           string    `json:"cover"`
	URL             string    `json:"url"`
	Playing         bool      `json:"playing"`
	ListenedSeconds float64   `json:"listenedSeconds"`
}
type musicState struct {
	event       musicEvent
	playerID    uint64
	lastPoll    time.Time
	lastPos     float64
	lastPlaying bool
	lastSent    time.Time
}
type musicSyncDoneMsg struct{ err error }

func randomMusicToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func validateMusicSync(cfg MusicSyncConfig) error {
	if strings.ContainsAny(cfg.ProtectionBypass, "\r\n") || len(cfg.ProtectionBypass) > 4096 {
		return fmt.Errorf("credencial da proteção Vercel inválida")
	}
	if cfg.APIURL == "" && cfg.Token == "" {
		return nil
	}
	u, err := url.Parse(cfg.APIURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return fmt.Errorf("music_sync.api_url deve ser a origem HTTPS da API")
	}
	token, err := hex.DecodeString(cfg.Token)
	if err != nil || len(token) != 32 {
		return fmt.Errorf("token de sincronização inválido")
	}
	return nil
}
func configureMusicSync(apiURL string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := cfg.MusicSync.Token
	if token == "" {
		token, err = randomMusicToken()
		if err != nil {
			return err
		}
	}
	cfg.MusicSync.APIURL = strings.TrimRight(apiURL, "/")
	cfg.MusicSync.Token = token
	if err = validateMusicSync(cfg.MusicSync); err != nil {
		return err
	}
	if err = saveConfig(cfg); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(token))
	fmt.Printf("Token salvo em %s (permissão 0600).\nConfigure na Vercel da API e faça o redeploy:\nMUSIC_DEVICE_TOKEN_SHA256=%x\n", configFile, digest)
	return nil
}
func sendMusicEvent(cfg MusicSyncConfig, event musicEvent) error {
	if cfg.APIURL == "" {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(cfg.APIURL, "/")+"/v1/music/events", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("endereço de sincronização inválido")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	if cfg.ProtectionBypass != "" {
		req.Header.Set("x-vercel-protection-bypass", cfg.ProtectionBypass)
	}
	client := &http.Client{Timeout: 9 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("API de música indisponível")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
	if response.StatusCode != 200 {
		return fmt.Errorf("sincronização de música: HTTP %d", response.StatusCode)
	}
	return nil
}
func musicSyncCmd(cfg MusicSyncConfig, event musicEvent) tea.Cmd {
	return func() tea.Msg { return musicSyncDoneMsg{err: sendMusicEvent(cfg, event)} }
}
func listenedDelta(previousPos, pos, elapsed float64, playing bool) float64 {
	delta := pos - previousPos
	if math.IsNaN(delta) || math.IsInf(delta, 0) || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) || !playing || elapsed <= 0 || elapsed > 5 || delta < 0 || delta > elapsed+1 {
		return 0
	}
	return math.Min(delta, elapsed)
}

// Wrap updates so every existing stop, skip and playback path shares reporting.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := message.(musicSyncDoneMsg); ok {
		if done.err != nil {
			m.statusMsg = done.err.Error()
			m.statusIsError = true
		}
		return m, nil
	}
	updated, cmd := m.update(message)
	next := updated.(Model)
	if next.musicSync.APIURL == "" {
		return next, cmd
	}
	now := time.Now().UTC()
	state := next.music
	if next.player != nil && next.player.id != state.playerID {
		id, err := randomMusicToken()
		if err != nil {
			next.statusMsg = "não foi possível iniciar sincronização"
			next.statusIsError = true
			return next, cmd
		}
		track := next.player.track
		publicURL := track.URL
		if track.Source == sourceLocal {
			publicURL = ""
		}
		cover := track.Cover
		if u, err := url.Parse(cover); err != nil || u.Scheme != "https" || u.Hostname() == "" {
			cover = ""
		}
		state = musicState{playerID: next.player.id, event: musicEvent{SessionID: id, Title: track.Title, Artist: track.Artist, Cover: cover, URL: publicURL}, lastPoll: now, lastPlaying: true}
	}
	if state.event.SessionID == "" {
		return next, cmd
	}
	playing := next.player != nil && !next.paused
	if status, ok := message.(playerStatusMsg); ok && next.player != nil && status.sessionID == next.player.id && status.err == nil {
		state.event.ListenedSeconds += listenedDelta(state.lastPos, next.pos, now.Sub(state.lastPoll).Seconds(), state.lastPlaying && playing)
		state.lastPos, state.lastPoll = next.pos, now
	}
	if playing != state.lastPlaying {
		state.lastPoll = now
		state.lastPos = next.pos
	}
	changed := playing != state.lastPlaying
	state.lastPlaying = playing
	if state.lastSent.IsZero() || changed || (next.player != nil && now.Sub(state.lastSent) >= 15*time.Second) {
		state.event.Sequence++
		state.event.Playing, state.event.ObservedAt = playing, now
		state.lastSent = now
		cmd = tea.Batch(cmd, musicSyncCmd(next.musicSync, state.event))
	}
	next.music = state
	return next, cmd
}
