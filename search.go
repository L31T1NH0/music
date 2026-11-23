package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

var errSearchLimitReached = errors.New("search limit reached")

func searchCmd(
	ctx context.Context,
	control *taskControl,
	requestID uint64,
	query string,
	src source,
	localDir string,
) tea.Cmd {
	return func() tea.Msg {
		defer control.finish()
		var (
			tracks []Track
			err    error
		)
		if src == sourceLocal {
			tracks, err = searchLocal(ctx, query, localDir, searchLimit)
		} else {
			tracks, err = searchYTDLP(ctx, query, searchLimit, src)
		}
		return searchDoneMsg{
			requestID: requestID,
			tracks:    tracks,
			query:     query,
			src:       src,
			err:       err,
		}
	}
}

func downloadCmd(
	ctx context.Context,
	control *taskControl,
	requestID uint64,
	track Track,
	destination string,
	format string,
	progress chan<- string,
) tea.Cmd {
	return func() tea.Msg {
		defer control.finish()
		defer close(progress)
		if track.Source == sourceLocal {
			return downloadDoneMsg{requestID: requestID, track: track, path: track.URL}
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return downloadDoneMsg{
				requestID: requestID,
				track:     track,
				err:       fmt.Errorf("criar pasta de download: %w", err),
			}
		}

		outputTemplate := filepath.Join(destination, "%(title)s [%(id)s].%(ext)s")
		arguments := []string{
			"--no-playlist",
			"--trim-filenames", "180",
			"--no-warnings",
			"--no-colors",
			"--progress",
			"--newline",
			"--progress-template", "download:music-progress:%(progress._percent_str)s|%(progress._speed_str)s|ETA %(progress._eta_str)s",
			"--print", "after_move:filepath",
			"-o", outputTemplate,
		}
		if format == "original" {
			arguments = append([]string{"-f", "bestaudio/best"}, arguments...)
		} else {
			arguments = append([]string{"-x", "--audio-format", "mp3", "--audio-quality", "0"}, arguments...)
		}
		arguments = append(arguments, "--", track.URL)
		cmd := exec.CommandContext(ctx, "yt-dlp", arguments...)
		configureProcessGroup(cmd)
		var stderr cappedBuffer
		stderr.limit = 64 << 10
		cmd.Stderr = &stderr
		output := &downloadOutput{progress: progress}
		cmd.Stdout = output
		err := cmd.Run()
		savedPath, outputErr := output.finish(destination)
		if outputErr != nil && err == nil {
			err = outputErr
		}
		if err != nil {
			if ctx.Err() != nil {
				return downloadDoneMsg{
					requestID: requestID,
					track:     track,
					err:       ctx.Err(),
					canceled:  errors.Is(ctx.Err(), context.Canceled),
				}
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				detail := truncate(oneLine(stderr.String()), 180)
				if detail != "" {
					err = fmt.Errorf("yt-dlp: %s", detail)
				} else {
					err = fmt.Errorf("yt-dlp encerrou com código %d", exitErr.ExitCode())
				}
			} else {
				err = friendlyExecutableError("yt-dlp", err)
			}
			return downloadDoneMsg{requestID: requestID, track: track, err: err}
		}
		return downloadDoneMsg{requestID: requestID, track: track, path: savedPath}
	}
}

func waitDownloadProgressCmd(requestID uint64, progress <-chan string) tea.Cmd {
	return func() tea.Msg {
		status, ok := <-progress
		return downloadProgressMsg{requestID: requestID, status: status, closed: !ok}
	}
}

func sendDownloadProgress(progress chan<- string, status string) {
	parts := strings.Split(status, "|")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	status = strings.Join(parts, " · ")
	status = truncate(status, 80)
	if status == "" {
		return
	}
	select {
	case progress <- status:
	default:
	}
}

type cappedBuffer struct {
	data  bytes.Buffer
	limit int
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := buffer.limit - buffer.data.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.data.Write(data)
	}
	return written, nil
}

func (buffer *cappedBuffer) String() string {
	return buffer.data.String()
}

type downloadOutput struct {
	mu       sync.Mutex
	pending  []byte
	saved    string
	err      error
	progress chan<- string
}

func (output *downloadOutput) Write(data []byte) (int, error) {
	written := len(data)
	output.mu.Lock()
	defer output.mu.Unlock()
	output.pending = append(output.pending, data...)
	for {
		newline := bytes.IndexByte(output.pending, '\n')
		if newline < 0 {
			break
		}
		output.consumeLine(output.pending[:newline])
		output.pending = output.pending[newline+1:]
	}
	if len(output.pending) > 256<<10 {
		output.pending = nil
		if output.err == nil {
			output.err = fmt.Errorf("linha de saída do yt-dlp excede o limite")
		}
	}
	return written, nil
}

func (output *downloadOutput) consumeLine(raw []byte) {
	line := oneLine(string(raw))
	if strings.HasPrefix(line, "music-progress:") {
		sendDownloadProgress(output.progress, strings.TrimPrefix(line, "music-progress:"))
		return
	}
	if line != "" {
		output.saved = line
	}
}

func (output *downloadOutput) finish(fallback string) (string, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(output.pending) > 0 {
		output.consumeLine(output.pending)
		output.pending = nil
	}
	if output.saved == "" {
		output.saved = fallback
	}
	return output.saved, output.err
}

func searchYTDLP(ctx context.Context, query string, limit int, src source) ([]Track, error) {
	prefix := fmt.Sprintf("ytsearch%d:%s", limit, query)
	if src == sourceSC {
		prefix = fmt.Sprintf("scsearch%d:%s", limit, query)
	}

	cmd := exec.CommandContext(
		ctx,
		"yt-dlp",
		prefix,
		"--dump-json",
		"--flat-playlist",
		"--no-warnings",
		"--quiet",
	)
	configureProcessGroup(cmd)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			detail := truncate(oneLine(string(exitErr.Stderr)), 180)
			if detail != "" {
				err = fmt.Errorf("yt-dlp: %s", detail)
			} else {
				err = fmt.Errorf("yt-dlp encerrou com código %d", exitErr.ExitCode())
			}
		} else {
			return nil, friendlyExecutableError("yt-dlp", err)
		}
	}

	tracks := make([]Track, 0, limit)
	for _, rawLine := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			continue
		}

		trackURL := ""
		if webpageURL, ok := data["webpage_url"].(string); ok && webpageURL != "" {
			trackURL = webpageURL
		} else if id, ok := data["id"].(string); ok && id != "" && src == sourceYT {
			trackURL = "https://www.youtube.com/watch?v=" + id
		}
		if trackURL == "" {
			continue
		}

		title, _ := data["title"].(string)
		if oneLine(title) == "" {
			title = "Sem título"
		}
		channel := ""
		for _, key := range []string{"channel", "uploader"} {
			if candidate, ok := data[key].(string); ok && oneLine(candidate) != "" {
				channel = candidate
				break
			}
		}
		if channel == "" {
			channel = "?"
		}
		duration := "—"
		if seconds, ok := data["duration"].(float64); ok {
			duration = fmtDuration(seconds)
		}
		artist, _ := data["artist"].(string)
		cover, _ := data["thumbnail"].(string)
		if cover == "" {
			if thumbnails, ok := data["thumbnails"].([]any); ok && len(thumbnails) > 0 {
				if thumbnail, ok := thumbnails[len(thumbnails)-1].(map[string]any); ok {
					cover, _ = thumbnail["url"].(string)
				}
			}
		}
		tracks = append(tracks, sanitizeTrack(Track{
			Artist: artist, Cover: cover,
			Title:    title,
			Channel:  channel,
			Duration: duration,
			URL:      trackURL,
			Source:   src,
		}))
		if len(tracks) == limit {
			break
		}
	}
	return tracks, err
}

func searchLocal(ctx context.Context, query, dir string, limit int) ([]Track, error) {
	terms := strings.Fields(strings.ToLower(oneLine(query)))
	if len(terms) == 0 {
		return nil, fmt.Errorf("busca local vazia")
	}

	root := filepath.Clean(dir)
	paths := make([]string, 0, limit)
	var firstWalkError error
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			if firstWalkError == nil {
				firstWalkError = walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !isAudioFile(path) {
			return nil
		}
		haystack := strings.ToLower(path)
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				return nil
			}
		}
		paths = append(paths, path)
		if len(paths) == limit {
			return errSearchLimitReached
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errSearchLimitReached) {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return nil, walkErr
		}
		if firstWalkError == nil {
			firstWalkError = walkErr
		}
	}

	tracks := make([]Track, 0, len(paths))
	for _, path := range paths {
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil || relative == "." {
			relative = "Local"
		}
		tracks = append(tracks, sanitizeTrack(Track{
			Title:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Channel:  relative,
			Duration: "local",
			URL:      path,
			Source:   sourceLocal,
		}))
	}
	return tracks, firstWalkError
}
