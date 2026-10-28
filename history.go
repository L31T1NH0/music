package main

import (
	"errors"
	"os"
	"strings"
)

func loadHistory() ([]string, error) {
	var history []string
	if err := readJSONFile(historyFile, &history, 1<<20); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if !shouldReadLegacyHistory() {
			return nil, nil
		}
		if legacyErr := readJSONFile(legacyHistoryFile, &history, 1<<20); legacyErr != nil {
			if errors.Is(legacyErr, os.ErrNotExist) {
				return nil, nil
			}
			return nil, legacyErr
		}
	}
	return normalizeSearchHistory(history), nil
}

func saveHistory(memory []string, query string) ([]string, error) {
	query = oneLine(query)
	if query == "" {
		return normalizeSearchHistory(memory), nil
	}

	var saved []string
	err := withFileLock(historyFile, func() error {
		var disk []string
		if err := readJSONFile(historyFile, &disk, 1<<20); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		combined := make([]string, 0, 1+len(disk)+len(memory))
		combined = append(combined, query)
		combined = append(combined, disk...)
		combined = append(combined, memory...)
		saved = normalizeSearchHistory(combined)
		return writeJSONAtomic(historyFile, saved, 0o600)
	})
	if err != nil {
		return normalizeSearchHistory(append([]string{query}, memory...)), err
	}
	return saved, nil
}

func normalizeSearchHistory(history []string) []string {
	out := make([]string, 0, min(len(history), maxHistory))
	seen := make(map[string]struct{}, len(history))
	for _, query := range history {
		query = oneLine(query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
		if len(out) == maxHistory {
			break
		}
	}
	return out
}

func loadPlayHistory() ([]Track, error) {
	var history []Track
	if err := readJSONFile(playHistoryFile, &history, 4<<20); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if !shouldReadLegacyHistory() {
			return nil, nil
		}
		if legacyErr := readJSONFile(legacyPlayHistoryFile, &history, 4<<20); legacyErr != nil {
			if errors.Is(legacyErr, os.ErrNotExist) {
				return nil, nil
			}
			return nil, legacyErr
		}
	}
	return normalizePlayHistory(history), nil
}

func shouldReadLegacyHistory() bool {
	return strings.TrimSpace(os.Getenv("XDG_DATA_HOME")) == ""
}

func savePlayHistory(memory []Track, track Track) ([]Track, error) {
	track = sanitizeTrack(track)
	if track.URL == "" || track.Title == "" {
		return normalizePlayHistory(memory), nil
	}

	var saved []Track
	err := withFileLock(playHistoryFile, func() error {
		var disk []Track
		if err := readJSONFile(playHistoryFile, &disk, 4<<20); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		combined := make([]Track, 0, 1+len(disk)+len(memory))
		combined = append(combined, track)
		combined = append(combined, disk...)
		combined = append(combined, memory...)
		saved = normalizePlayHistory(combined)
		return writeJSONAtomic(playHistoryFile, saved, 0o600)
	})
	if err != nil {
		return normalizePlayHistory(append([]Track{track}, memory...)), err
	}
	return saved, nil
}

func normalizePlayHistory(history []Track) []Track {
	out := make([]Track, 0, min(len(history), maxPlayHistory))
	seen := make(map[string]struct{}, len(history))
	for _, track := range history {
		track = sanitizeTrack(track)
		if track.URL == "" || track.Title == "" || !validSource(track.Source) {
			continue
		}
		if track.Source == sourceLocal {
			info, err := os.Stat(track.URL)
			if err != nil || info.IsDir() {
				continue
			}
		}
		if _, exists := seen[track.URL]; exists {
			continue
		}
		seen[track.URL] = struct{}{}
		out = append(out, track)
		if len(out) == maxPlayHistory {
			break
		}
	}
	return out
}

func validSource(src source) bool {
	return src == sourceYT || src == sourceSC || src == sourceLocal
}
