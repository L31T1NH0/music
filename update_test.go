package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEnterOnSecondPageSelectsFirstVisibleTrack(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = stateResults
	model.resultPage = 1
	model.tracks = testTracks(20, sourceYT)

	updatedModel, _ := model.handleResultsKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedModel.(Model)
	if updated.currentTrack == nil || updated.currentTrack.Title != "faixa 11" {
		t.Fatalf("currentTrack=%#v, want faixa 11", updated.currentTrack)
	}
	updated.playerTask.cancelOnly()
}

func TestBeginningTrackResetsPausedState(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.paused = true
	updatedModel, _ := model.beginTrack(testTracks(1, sourceYT)[0])
	updated := updatedModel.(Model)
	if updated.paused {
		t.Fatal("paused state leaked into a new player process")
	}
	if updated.state != stateLoading {
		t.Fatalf("state=%v, want stateLoading", updated.state)
	}
	updated.playerTask.cancelOnly()
}

func TestBassBoostToggleIsSerializedAndRollsBackOnError(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = statePlaying
	model.player = &playerSession{id: 9}
	model.currentTrack = trackPointer(testTracks(1, sourceYT)[0])

	got, cmd := model.handlePlayerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	updated := got.(Model)
	if !updated.bassBoost || !updated.bassPending || cmd == nil {
		t.Fatalf("bass was not scheduled: enabled=%v pending=%v cmd=%v", updated.bassBoost, updated.bassPending, cmd)
	}

	got, cmd = updated.handlePlayerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated = got.(Model)
	if !updated.bassBoost || !updated.bassPending || cmd != nil {
		t.Fatal("a second toggle was allowed while the first was pending")
	}

	got, _ = updated.Update(playerControlDoneMsg{
		sessionID:   9,
		bassChange:  true,
		bassEnabled: true,
	})
	updated = got.(Model)
	if !updated.bassBoost || updated.bassPending || !strings.Contains(updated.statusMsg, "ligado") {
		t.Fatalf("successful bass toggle=%#v", updated)
	}

	got, cmd = updated.handlePlayerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	updated = got.(Model)
	if updated.bassBoost || !updated.bassPending || cmd == nil {
		t.Fatal("bass disable was not scheduled")
	}
	got, _ = updated.Update(playerControlDoneMsg{
		sessionID:   9,
		bassChange:  true,
		bassEnabled: false,
		err:         errors.New("filter failed"),
	})
	updated = got.(Model)
	if !updated.bassBoost || updated.bassPending || !updated.statusIsError {
		t.Fatal("failed bass toggle did not restore the prior state")
	}
}

func TestBassBoostSurvivesTrackChanges(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.bassBoost = true
	updatedModel, _ := model.beginTrack(testTracks(1, sourceYT)[0])
	updated := updatedModel.(Model)
	if !updated.bassBoost {
		t.Fatal("bass boost was reset while changing tracks")
	}
	updated.playerTask.cancelOnly()
}

func TestStaleSearchAndDownloadMessagesAreIgnored(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.searchRequestID = 2
	got, _ := model.Update(searchDoneMsg{
		requestID: 1,
		query:     "stale",
		tracks:    testTracks(1, sourceYT),
	})
	updated := got.(Model)
	if len(updated.tracks) != 0 || updated.lastQuery != "" {
		t.Fatalf("stale search mutated model: %#v", updated.tracks)
	}

	got, _ = model.Update(downloadDoneMsg{
		requestID: 99,
		track:     testTracks(1, sourceYT)[0],
		path:      "/tmp/stale.mp3",
	})
	updated = got.(Model)
	if updated.statusMsg != "" {
		t.Fatalf("stale download mutated status: %q", updated.statusMsg)
	}
}

func TestDownloadCompletionUpdatesOnlyMatchingTask(t *testing.T) {
	t.Parallel()
	model := testModel()
	tracks := testTracks(2, sourceYT)
	model.currentTrack = trackPointer(tracks[1])
	model.downloads[1] = downloadState{ID: 1, Track: tracks[0], Status: "baixando"}
	model.downloadByURL[tracks[0].URL] = 1

	got, _ := model.Update(downloadDoneMsg{requestID: 1, track: tracks[0], path: "/music/a.mp3"})
	updated := got.(Model)
	if updated.currentTrack.Title != tracks[1].Title {
		t.Fatal("download completion changed the current track")
	}
	if status := updated.downloadStatusFor(tracks[0].URL); !strings.Contains(status, "/music/a.mp3") {
		t.Fatalf("matching download status=%q", status)
	}
	if status := updated.downloadStatusFor(tracks[1].URL); status != "" {
		t.Fatalf("unrelated track received status %q", status)
	}
}

func TestDownloadProgressUpdatesOnlyActiveMatchingTask(t *testing.T) {
	t.Parallel()
	model := testModel()
	track := testTracks(1, sourceYT)[0]
	progress := make(chan string)
	model.downloads[7] = downloadState{
		ID:       7,
		Track:    track,
		Status:   "baixando",
		progress: progress,
	}

	got, cmd := model.Update(downloadProgressMsg{requestID: 7, status: "42% · 1 MiB/s"})
	updated := got.(Model)
	if !strings.Contains(updated.downloads[7].Status, "42%") || cmd == nil {
		t.Fatalf("progress was not applied: %#v", updated.downloads[7])
	}

	done := updated.downloads[7]
	done.Done = true
	updated.downloads[7] = done
	got, cmd = updated.Update(downloadProgressMsg{requestID: 7, status: "stale"})
	updated = got.(Model)
	if strings.Contains(updated.downloads[7].Status, "stale") || cmd != nil {
		t.Fatal("late progress mutated a completed download")
	}
	close(progress)
}

func TestInvalidLocalNumberIsAnErrorNotASearch(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.src = sourceLocal
	model.localDirConfigured = true
	model.localBrowseDir = t.TempDir()
	model.localTracks = testTracks(2, sourceLocal)
	model.searchInput.SetValue("99")

	got, _ := model.submitSearchInput()
	updated := got.(Model)
	if updated.state == stateSearching {
		t.Fatal("invalid local number started a recursive search")
	}
	if !updated.statusIsError || !strings.Contains(updated.statusMsg, "1–2") {
		t.Fatalf("status=%q, error=%v", updated.statusMsg, updated.statusIsError)
	}
}

func TestForcedNumericLocalSearchRemainsAvailable(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.src = sourceLocal
	model.localDirConfigured = true
	model.localBrowseDir = t.TempDir()
	model.localTracks = testTracks(2, sourceLocal)
	model.searchInput.SetValue(":buscar 99")

	got, _ := model.submitSearchInput()
	updated := got.(Model)
	if updated.state != stateSearching || updated.lastQuery != "99" {
		t.Fatalf("state=%v query=%q", updated.state, updated.lastQuery)
	}
	updated.searchTask.cancelOnly()
}

func TestPlayerFailureAutomaticallyAdvancesQueue(t *testing.T) {
	t.Parallel()
	model := testModel()
	failed := testTracks(1, sourceYT)[0]
	next := testTracks(2, sourceYT)[1]
	model.state = stateLoading
	model.playerReqID = 7
	model.currentTrack = trackPointer(failed)
	model.queue = []Track{next}

	got, _ := model.Update(playerErrMsg{requestID: 7, track: failed, err: errors.New("bad media")})
	updated := got.(Model)
	if updated.state != stateLoading || updated.currentTrack == nil || updated.currentTrack.Title != next.Title {
		t.Fatalf("did not advance: state=%v track=%#v", updated.state, updated.currentTrack)
	}
	if !strings.Contains(updated.statusMsg, "tentando a próxima") {
		t.Fatalf("status=%q", updated.statusMsg)
	}
	updated.playerTask.cancelOnly()
}

func TestQueueCommandsRemoveMoveAndClear(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.queue = testTracks(3, sourceYT)

	model, handled := model.handleQueueCommand(":fila mover 3 1")
	if !handled || model.queue[0].Title != "faixa 3" {
		t.Fatalf("move failed: %#v", model.queue)
	}
	model, handled = model.handleQueueCommand(":fila remover 2")
	if !handled || len(model.queue) != 2 || model.queue[1].Title != "faixa 2" {
		t.Fatalf("remove failed: %#v", model.queue)
	}
	model, handled = model.handleQueueCommand(":fila limpar")
	if !handled || len(model.queue) != 0 {
		t.Fatalf("clear failed: %#v", model.queue)
	}
}

func TestSearchHistoryNavigation(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.history = []string{"new", "old"}

	got, _ := model.previousSearchHistory()
	model = got.(Model)
	if model.searchInput.Value() != "new" {
		t.Fatalf("got %q", model.searchInput.Value())
	}
	got, _ = model.previousSearchHistory()
	model = got.(Model)
	if model.searchInput.Value() != "old" {
		t.Fatalf("got %q", model.searchInput.Value())
	}
	got, _ = model.nextSearchHistory()
	model = got.(Model)
	if model.searchInput.Value() != "new" {
		t.Fatalf("got %q", model.searchInput.Value())
	}
}

func TestEscapeCancelsLoadingAndInvalidatesRequest(t *testing.T) {
	t.Parallel()
	model := testModel()
	model.state = stateLoading
	model.playerReqID = 10
	_, control := newTaskControl(context.Background())
	model.playerTask = control
	model.currentTrack = trackPointer(testTracks(1, sourceYT)[0])

	got, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated := got.(Model)
	if updated.state != stateSearch || updated.currentTrack != nil || updated.playerReqID != 11 {
		t.Fatalf("unexpected canceled state: %#v", updated)
	}
	select {
	case <-control.done:
		t.Fatal("canceling should not impersonate task cleanup")
	default:
	}
	control.finish()
}

func TestDownloadCancellationCommandCancelsActiveTasks(t *testing.T) {
	t.Parallel()
	model := testModel()
	ctx, control := newTaskControl(context.Background())
	track := testTracks(1, sourceYT)[0]
	model.downloads[1] = downloadState{
		ID:      1,
		Track:   track,
		Status:  "baixando",
		control: control,
	}
	model.downloadByURL[track.URL] = 1
	model.searchInput.SetValue(":download cancelar")

	got, _ := model.submitSearchInput()
	updated := got.(Model)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("download context was not canceled")
	}
	if !strings.Contains(updated.downloads[1].Status, "cancelando") {
		t.Fatalf("status=%q", updated.downloads[1].Status)
	}
	control.finish()
}
