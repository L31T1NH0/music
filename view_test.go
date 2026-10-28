package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func assertViewFits(t *testing.T, model Model) {
	t.Helper()
	rendered := model.View()
	lines := strings.Split(rendered, "\n")
	if model.height > 0 && len(lines) > model.height {
		t.Fatalf("view has %d lines for height %d", len(lines), model.height)
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); model.width > 0 && width > model.width {
			t.Fatalf("line %d has width %d for terminal %d: %q", index, width, model.width, line)
		}
	}
}

func TestAllViewsFitTerminalDimensions(t *testing.T) {
	t.Parallel()
	for _, width := range []int{1, 10, 20, 24, 40, 80, 120} {
		for _, height := range []int{1, 6, 10, 16, 30} {
			base := testModel()
			base.width = width
			base.height = height
			base.localDirConfigured = true
			base.localDir = "/music"
			base.localBrowseDir = "/music"
			base.localTracks = testTracks(30, sourceLocal)

			search := base
			search.state = stateSearch
			assertViewFits(t, search)

			results := base
			results.state = stateResults
			results.lastQuery = "uma busca bastante longa"
			results.tracks = testTracks(30, sourceYT)
			assertViewFits(t, results)

			local := base
			local.state = stateSearch
			local.src = sourceLocal
			assertViewFits(t, local)

			player := base
			player.state = statePlaying
			player.currentTrack = trackPointer(testTracks(1, sourceYT)[0])
			player.queue = testTracks(25, sourceYT)
			player.duration = 200
			player.pos = 50
			assertViewFits(t, player)
		}
	}
}

func TestUntrustedMetadataDoesNotReachViewAsTerminalControl(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = stateResults
	model.width = 80
	model.height = 24
	model.lastQuery = "query"
	model.tracks = []Track{{
		Title:    "track\x1b]52;c;Y29kZXg=\a",
		Channel:  "\x1b[31mred\x1b[0m",
		Duration: "1:00",
		URL:      "https://example.invalid",
		Source:   sourceYT,
	}}
	rendered := model.View()
	if strings.Contains(rendered, "]52") || strings.Contains(rendered, "\a") {
		t.Fatalf("terminal control reached view: %q", rendered)
	}
}

func TestResultsShowDownloadFeedbackAndMixedHistoryHint(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = stateResults
	model.width = 100
	model.height = 24
	model.lastQuery = "histórico"
	model.tracks = testTracks(2, sourceYT)
	model.tracks[0].Source = sourceLocal
	model.statusMsg = "✓ salvo em /music/song.mp3"
	rendered := ansi.Strip(model.View())
	if !strings.Contains(rendered, "salvo em /music/song.mp3") {
		t.Fatalf("download feedback missing: %q", rendered)
	}
	if !strings.Contains(rendered, "d2") || !strings.Contains(rendered, "download") {
		t.Fatalf("mixed-source download hint missing: %q", rendered)
	}
}

func TestPlayerShowsBassBoostStateAndShortcut(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = statePlaying
	model.width = 80
	model.height = 24
	model.currentTrack = trackPointer(testTracks(1, sourceYT)[0])
	model.bassBoost = true
	rendered := ansi.Strip(model.View())
	if !strings.Contains(rendered, "GRAVES +5 dB") || !strings.Contains(rendered, "S graves on/off") {
		t.Fatalf("bass state or shortcut missing: %q", rendered)
	}
}
