package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConfigRoundTripPreservesMutedVolume(t *testing.T) {
	dir := t.TempDir()
	oldConfig := configFile
	configFile = filepath.Join(dir, "config", "config.json")
	t.Cleanup(func() { configFile = oldConfig })

	volume := 0
	want := Config{LocalDir: dir, Volume: &volume, DownloadFormat: "original"}
	if err := saveConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalDir != dir || got.Volume == nil || *got.Volume != 0 || got.DownloadFormat != "original" {
		t.Fatalf("loadConfig() = %#v", got)
	}
	info, err := os.Stat(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("config permission = %o, want 600", permission)
	}
}

func TestLoadConfigReportsMalformedAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	oldConfig := configFile
	configFile = filepath.Join(dir, "config.json")
	t.Cleanup(func() { configFile = oldConfig })

	if err := os.WriteFile(configFile, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("malformed config was silently accepted")
	}
	if err := os.WriteFile(configFile, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("oversized config was silently accepted")
	}
}

func TestConcurrentHistorySavesDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	oldHistory := historyFile
	historyFile = filepath.Join(dir, "history.json")
	t.Cleanup(func() { historyFile = oldHistory })

	const count = 20
	var wait sync.WaitGroup
	errors := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := saveHistory(nil, "query-"+itoa(index))
			errors <- err
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	var saved []string
	data, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != count {
		t.Fatalf("saved %d history entries, want %d: %#v", len(saved), count, saved)
	}
	seen := make(map[string]bool, count)
	for _, query := range saved {
		seen[query] = true
	}
	for index := 0; index < count; index++ {
		if !seen["query-"+itoa(index)] {
			t.Fatalf("missing query-%d", index)
		}
	}
}

func TestHistoryNormalizationAndLimits(t *testing.T) {
	t.Parallel()
	input := make([]string, 0, maxHistory+5)
	input = append(input, " Test ", "test", "", "\x1b[31mquery")
	for index := 0; index < maxHistory+5; index++ {
		input = append(input, "q-"+itoa(index))
	}
	got := normalizeSearchHistory(input)
	if len(got) != maxHistory {
		t.Fatalf("len=%d, want %d", len(got), maxHistory)
	}
	if got[0] != "Test" || got[1] != "query" {
		t.Fatalf("unexpected normalization: %#v", got[:2])
	}
}

func TestPlayHistoryDropsMissingLocalFiles(t *testing.T) {
	t.Parallel()
	history := []Track{
		{Title: "missing", URL: filepath.Join(t.TempDir(), "missing.mp3"), Source: sourceLocal},
		{Title: "remote", URL: "https://example.invalid/track", Source: sourceYT},
	}
	got := normalizePlayHistory(history)
	if len(got) != 1 || got[0].Title != "remote" {
		t.Fatalf("normalizePlayHistory() = %#v", got)
	}
}

func TestExplicitXDGDataHomeDisablesLegacyImport(t *testing.T) {
	oldValue, existed := os.LookupEnv("XDG_DATA_HOME")
	if err := os.Setenv("XDG_DATA_HOME", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("XDG_DATA_HOME", oldValue)
		} else {
			_ = os.Unsetenv("XDG_DATA_HOME")
		}
	})
	if shouldReadLegacyHistory() {
		t.Fatal("explicit XDG_DATA_HOME should isolate legacy history")
	}
}

func TestMalformedHistoriesAreReported(t *testing.T) {
	dir := t.TempDir()
	oldSearch, oldPlay := historyFile, playHistoryFile
	historyFile = filepath.Join(dir, "search.json")
	playHistoryFile = filepath.Join(dir, "play.json")
	t.Cleanup(func() {
		historyFile = oldSearch
		playHistoryFile = oldPlay
	})

	if err := os.WriteFile(historyFile, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHistory(); err == nil {
		t.Fatal("malformed search history was silently accepted")
	}

	if err := os.WriteFile(playHistoryFile, []byte("[broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPlayHistory(); err == nil {
		t.Fatal("malformed play history was silently accepted")
	}
}
