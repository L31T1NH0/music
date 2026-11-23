package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMusicSyncSetupPreservesConfigAndToken(t *testing.T) {
	old := configFile
	configFile = filepath.Join(t.TempDir(), "config.json")
	defer func() { configFile = old }()
	volume := 55
	if err := saveConfig(Config{LocalDir: "/tmp/music", Volume: &volume, DownloadFormat: "mp3"}); err != nil {
		t.Fatal(err)
	}
	if err := configureMusicSync("https://domenyk-api.vercel.app"); err != nil {
		t.Fatal(err)
	}
	first, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if first.LocalDir != "/tmp/music" || *first.Volume != 55 || len(first.MusicSync.Token) != 64 {
		t.Fatal("setup lost settings")
	}
	first.MusicSync.ProtectionBypass = "test-bypass"
	if err := saveConfig(first); err != nil {
		t.Fatal(err)
	}
	if err := configureMusicSync("https://domenyk-api.vercel.app"); err != nil {
		t.Fatal(err)
	}
	second, _ := loadConfig()
	if second.MusicSync.ProtectionBypass != "test-bypass" {
		t.Fatal("setup lost Vercel credential")
	}
	if first.MusicSync.Token != second.MusicSync.Token {
		t.Fatal("setup rotated token unexpectedly")
	}
	info, _ := os.Stat(configFile)
	if info.Mode().Perm() != 0600 {
		t.Fatal("token file must be private")
	}
	for _, cfg := range []MusicSyncConfig{{APIURL: "http://example.com", Token: first.MusicSync.Token}, {APIURL: "https://user:secret@example.com", Token: first.MusicSync.Token}, {APIURL: "https://example.com/path", Token: first.MusicSync.Token}, {APIURL: "https://example.com", Token: "bad"}, {APIURL: "https://example.com", Token: first.MusicSync.Token, ProtectionBypass: "invalid\r\nheader"}} {
		if validateMusicSync(cfg) == nil {
			t.Fatal("accepted invalid sync config")
		}
	}
}
func TestListenedDeltaIgnoresPauseSeeksAndStalls(t *testing.T) {
	for _, test := range []struct {
		before, after, elapsed float64
		playing                bool
		want                   float64
	}{
		{0, .25, .3, true, .25}, {0, 40, .25, true, 0}, {10, 0, .25, true, 0}, {0, 1, 1, false, 0}, {0, 8, 8, true, 0},
	} {
		if got := listenedDelta(test.before, test.after, test.elapsed, test.playing); got != test.want {
			t.Fatalf("%#v: %f", test, got)
		}
	}
}
func TestMusicSyncSendsAuthenticationAndRefusesRedirects(t *testing.T) {
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-vercel-protection-bypass") != "test-bypass" {
			t.Error("missing Vercel protection credential")
		}
		if r.URL.Path != "/v1/music/events" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing authentication")
		}
		var event musicEvent
		if json.NewDecoder(r.Body).Decode(&event) != nil || event.Title != "Oceano" {
			t.Error("invalid payload")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	err := sendMusicEvent(MusicSyncConfig{APIURL: server.URL, Token: strings.Repeat("a", 64), ProtectionBypass: "test-bypass"}, musicEvent{Title: "Oceano"})
	if err == nil || redirected != 0 {
		t.Fatal("followed redirect with device credential")
	}
}
func TestMusicSyncStartsPausesAndStopsWithoutExposingLocalPath(t *testing.T) {
	model := testModel()
	model.musicSync = MusicSyncConfig{APIURL: "https://example.com", Token: strings.Repeat("a", 64)}
	model.player = &playerSession{id: 7, track: Track{Title: "Oceano", Source: sourceLocal, URL: "/home/private/Music/oceano.mp3"}}
	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	next := updated.(Model)
	if cmd == nil || next.music.event.URL != "" || len(next.music.event.SessionID) != 64 || !next.music.event.Playing {
		t.Fatal("failed to start private local report")
	}
	next.music.lastSent = time.Now().Add(-16 * time.Second)
	next.music.lastPoll = time.Now().Add(-time.Second)
	updated, cmd = next.Update(playerStatusMsg{sessionID: 7, pos: 1, volume: 80})
	next = updated.(Model)
	if next.music.event.ListenedSeconds < .9 || cmd == nil {
		t.Fatal("listening not accumulated")
	}
	updated, _ = next.Update(playerStatusMsg{sessionID: 7, pos: 1, paused: true, volume: 80})
	next = updated.(Model)
	if next.music.event.Playing {
		t.Fatal("pause not reported")
	}
	sequence := next.music.event.Sequence
	// A stale message from another session cannot change the active playback.
	updated, _ = next.Update(playerStatusMsg{sessionID: 8, pos: 90, volume: 80})
	next = updated.(Model)
	if next.music.event.Sequence != sequence {
		t.Fatal("stale player changed report")
	}
	next.player = nil
	next.music.lastPlaying = true
	updated, cmd = next.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	next = updated.(Model)
	if next.music.event.Playing || cmd == nil {
		t.Fatal("stop not reported")
	}
}
