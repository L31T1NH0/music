package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryCommandsAreExplicit(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"local hero soundtrack",
		"set fire to the rain",
		"pasta black metal",
	} {
		if _, ok := parseMusicDirInput(query, sourceYT); ok {
			t.Fatalf("ordinary query %q was treated as a directory command", query)
		}
	}
	if path, ok := parseMusicDirInput(":pasta /tmp/music", sourceYT); !ok || path != "/tmp/music" {
		t.Fatalf("explicit command = %q, %v", path, ok)
	}
}

func TestSearchLocalSkipsHiddenDirectoriesAndHonorsLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hidden := filepath.Join(root, ".private")
	if err := os.Mkdir(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "secret.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10; index++ {
		path := filepath.Join(root, "visible-"+itoa(index)+".mp3")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tracks, err := searchLocal(context.Background(), "mp3", root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 {
		t.Fatalf("got %d tracks, want 3", len(tracks))
	}
	for _, track := range tracks {
		if filepath.Dir(track.URL) == hidden {
			t.Fatalf("hidden track leaked into results: %#v", track)
		}
	}
}

func TestSearchLocalCanBeCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := searchLocal(ctx, "anything", t.TempDir(), searchLimit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
}

func TestNormalizeDirCanonicalizesSymlinkAndRejectsUserTilde(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "music-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("normalizeDir(symlink)=%q, want %q", got, root)
	}
	if _, err := normalizeDir("~someone/Music"); err == nil {
		t.Fatal("~user syntax was accepted as a path under the current home")
	}
}

func TestLocalListingIsCaseInsensitiveAndHidesFolders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"b.mp3", "A.mp3", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Visible"), 0o755); err != nil {
		t.Fatal(err)
	}

	tracks := listLocalTracks(root)
	if len(tracks) != 2 || tracks[0].Title != "A" || tracks[1].Title != "b" {
		t.Fatalf("tracks=%#v", tracks)
	}
	folders := listLocalFolders(root, root)
	if len(folders) != 1 || folders[0].Name != "Visible" {
		t.Fatalf("folders=%#v", folders)
	}
}

func TestLocalListingReportsReadErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if _, _, err := loadLocalListing(root, missing); err == nil {
		t.Fatal("missing local directory was presented as an empty library")
	}
	if _, _, err := loadLocalListing(root, filepath.Dir(root)); err == nil {
		t.Fatal("directory outside the library root was accepted")
	}
}
