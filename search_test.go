package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installFakeCommand(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("PATH", oldPath) })
}

func TestSearchYTDLPParsesAndSanitizesMetadata(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `printf '%s\n' '{"id":"abc","title":"safe\u001b]52;c;bad\u0007","channel":"Artist","duration":65,"webpage_url":"https://example.invalid/abc"}'`)

	tracks, err := searchYTDLP(context.Background(), "query", 1, sourceYT)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks=%#v", tracks)
	}
	if tracks[0].Title != "safe" || tracks[0].Duration != "1:05" {
		t.Fatalf("track=%#v", tracks[0])
	}
}

func TestSearchYTDLPReportsToolErrors(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `printf '%s\n' 'network unavailable' >&2; exit 7`)
	_, err := searchYTDLP(context.Background(), "query", 1, sourceYT)
	if err == nil || !strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("error=%v", err)
	}
}

func TestSearchYTDLPPreservesPartialResults(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `
printf '%s\n' '{"id":"abc","title":"Song","duration":65}'
printf '%s\n' 'another result is unavailable' >&2
exit 1
`)
	tracks, err := searchYTDLP(context.Background(), "query", 2, sourceYT)
	if len(tracks) != 1 || tracks[0].URL != "https://www.youtube.com/watch?v=abc" {
		t.Fatalf("partial results lost: %#v", tracks)
	}
	if err == nil || !strings.Contains(err.Error(), "another result is unavailable") {
		t.Fatalf("error=%v", err)
	}
}

func TestSearchYTDLPCancellationStopsProcessGroup(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `sleep 10`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := searchYTDLP(ctx, "query", 1, sourceYT)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestDownloadUsesUniqueNoPlaylistTemplateAndReturnsPath(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `
seen_id=0
seen_no_playlist=0
seen_separator=0
for arg in "$@"; do
  case "$arg" in
    *'%(id)s'*) seen_id=1 ;;
    --no-playlist) seen_no_playlist=1 ;;
    --) seen_separator=1 ;;
  esac
done
[ "$seen_id" -eq 1 ]
[ "$seen_no_playlist" -eq 1 ]
[ "$seen_separator" -eq 1 ]
printf '%s\n' '/music/Song [abc].mp3'
`)
	ctx, control := newTimedTaskControl(context.Background(), time.Second)
	track := Track{Title: "Song", URL: "https://example.invalid/abc", Source: sourceYT}
	progress := make(chan string, 16)
	message := downloadCmd(ctx, control, 42, track, t.TempDir(), "mp3", progress)()
	done, ok := message.(downloadDoneMsg)
	if !ok {
		t.Fatalf("message=%#v", message)
	}
	if done.err != nil || done.path != "/music/Song [abc].mp3" || done.requestID != 42 {
		t.Fatalf("download=%#v", done)
	}
	select {
	case <-control.done:
	default:
		t.Fatal("download task did not signal cleanup")
	}
}

func TestDownloadOriginalPreservesAudioWithoutExtraction(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `
seen_format=0
seen_extract=0
previous=
for arg in "$@"; do
  if [ "$previous" = "-f" ] && [ "$arg" = "bestaudio/best" ]; then
    seen_format=1
  fi
  if [ "$arg" = "-x" ]; then
    seen_extract=1
  fi
  previous=$arg
done
[ "$seen_format" -eq 1 ]
[ "$seen_extract" -eq 0 ]
printf '%s\n' '/music/Song [abc].webm'
`)
	ctx, control := newTimedTaskControl(context.Background(), time.Second)
	track := Track{Title: "Song", URL: "https://example.invalid/abc", Source: sourceSC}
	progress := make(chan string, 16)
	message := downloadCmd(ctx, control, 43, track, t.TempDir(), "original", progress)()
	done, ok := message.(downloadDoneMsg)
	if !ok {
		t.Fatalf("message=%#v", message)
	}
	if done.err != nil || done.path != "/music/Song [abc].webm" {
		t.Fatalf("download=%#v", done)
	}
}

func TestDownloadStreamsSanitizedProgress(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `
printf '%s\n' 'music-progress: 42.0%|1MiB/s|ETA 00:03'
printf '%s\n' '/music/Song [abc].mp3'
`)
	ctx, control := newTimedTaskControl(context.Background(), time.Second)
	track := Track{Title: "Song", URL: "https://example.invalid/abc", Source: sourceYT}
	progress := make(chan string, 16)
	message := downloadCmd(ctx, control, 44, track, t.TempDir(), "mp3", progress)()
	done := message.(downloadDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	status, ok := <-progress
	if !ok || status != "42.0% · 1MiB/s · ETA 00:03" {
		t.Fatalf("progress=%q, open=%v", status, ok)
	}
	if _, stillOpen := <-progress; stillOpen {
		t.Fatal("progress channel was not closed")
	}
}

func TestDownloadCancellationStopsProcessGroup(t *testing.T) {
	installFakeCommand(t, "yt-dlp", `sleep 10`)
	ctx, control := newTaskControl(context.Background())
	track := Track{Title: "Song", URL: "https://example.invalid/abc", Source: sourceYT}
	progress := make(chan string, 16)
	go func() {
		time.Sleep(50 * time.Millisecond)
		control.cancelOnly()
	}()
	start := time.Now()
	message := downloadCmd(ctx, control, 45, track, t.TempDir(), "mp3", progress)()
	done := message.(downloadDoneMsg)
	if !done.canceled || !errors.Is(done.err, context.Canceled) {
		t.Fatalf("download=%#v, want canceled", done)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("download cancellation took %s", elapsed)
	}
}
