package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func defaultMusicDir() string {
	if configured := xdgMusicDir(); configured != "" {
		return configured
	}
	for _, name := range []string{"Músicas", "Music"} {
		candidate := filepath.Join(home(), name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join(home(), "Music")
}

func xdgMusicDir() string {
	data, err := os.ReadFile(filepath.Join(userConfigDir(), "user-dirs.dirs"))
	if err != nil || len(data) > 64<<10 {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_MUSIC_DIR=") {
			continue
		}
		value := strings.Trim(strings.TrimPrefix(line, "XDG_MUSIC_DIR="), "\"'")
		value = strings.ReplaceAll(value, "${HOME}", home())
		value = strings.ReplaceAll(value, "$HOME", home())
		dir, err := normalizeDir(value)
		if err == nil {
			return dir
		}
	}
	return ""
}

func nextSource(src source) source {
	switch src {
	case sourceYT:
		return sourceSC
	case sourceSC:
		return sourceLocal
	default:
		return sourceYT
	}
}

func (m *Model) updateSearchPlaceholder() {
	switch m.src {
	case sourceSC:
		m.searchInput.Placeholder = "buscar no soundcloud…"
	case sourceLocal:
		if m.localDirConfigured {
			m.searchInput.Placeholder = "buscar na pasta local…"
		} else {
			m.searchInput.Placeholder = "pasta local de música…"
		}
	default:
		m.searchInput.Placeholder = "pesquisar…"
	}
}

func (m Model) setLocalDir(input string) (tea.Model, tea.Cmd) {
	dir, err := normalizeDir(input)
	if err != nil {
		m.statusMsg = "pasta inválida: " + oneLine(input)
		m.statusIsError = true
		return m, nil
	}
	folders, tracks, err := loadLocalListing(dir, dir)
	if err != nil {
		m.statusMsg = "não foi possível ler a pasta: " + oneLine(err.Error())
		m.statusIsError = true
		return m, nil
	}
	config := m.config()
	config.LocalDir = dir
	if err := saveConfig(config); err != nil {
		m.statusMsg = "erro ao salvar pasta local: " + err.Error()
		m.statusIsError = true
		return m, nil
	}
	m.localDir = dir
	m.localBrowseDir = dir
	m.localDirConfigured = true
	m.localFolders = folders
	m.localTracks = tracks
	m.localPage = 0
	m.searchInput.SetValue("")
	m.updateSearchPlaceholder()
	m.statusMsg = "pasta local definida: " + dir
	m.statusIsError = false
	return m, nil
}

func (m Model) openLocalFolder(n int) (tea.Model, tea.Cmd) {
	if n < 1 || n > len(m.localFolders) {
		m.statusMsg = fmt.Sprintf("pasta inválida (1–%d)", len(m.localFolders))
		m.statusIsError = true
		m.searchInput.SetValue("")
		return m, nil
	}
	folder := m.localFolders[n-1]
	folders, tracks, err := loadLocalListing(m.localDir, folder.Path)
	if err != nil {
		m.statusMsg = "não foi possível abrir a pasta: " + oneLine(err.Error())
		m.statusIsError = true
		m.searchInput.SetValue("")
		return m, nil
	}
	m.localBrowseDir = folder.Path
	m.localFolders = folders
	m.localTracks = tracks
	m.localPage = 0
	m.localShowFolders = false
	m.searchInput.SetValue("")
	m.statusMsg = ""
	m.statusIsError = false
	return m, nil
}

func (m Model) openLocalRoot() (tea.Model, tea.Cmd) {
	folders, tracks, err := loadLocalListing(m.localDir, m.localDir)
	if err != nil {
		m.statusMsg = "não foi possível abrir a raiz: " + oneLine(err.Error())
		m.statusIsError = true
		m.searchInput.SetValue("")
		return m, nil
	}
	m.localBrowseDir = m.localDir
	m.localFolders = folders
	m.localTracks = tracks
	m.localPage = 0
	m.localShowFolders = false
	m.searchInput.SetValue("")
	m.statusMsg = ""
	m.statusIsError = false
	return m, nil
}

func (m Model) handleLocalTrackSelection(choice string) (tea.Model, tea.Cmd) {
	lower := strings.ToLower(choice)
	if strings.HasPrefix(lower, "d") && len(lower) > 1 {
		n, err := strconv.Atoi(lower[1:])
		if err == nil && n >= 1 && n <= len(m.localTracks) {
			m.searchInput.SetValue("")
			m.statusMsg = "arquivo local"
			m.statusIsError = false
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("número inválido (1–%d)", len(m.localTracks))
		m.statusIsError = true
		m.searchInput.SetValue("")
		return m, nil
	}

	indices, err := parseSelection(choice, len(m.localTracks))
	if err != nil {
		m.statusMsg = err.Error()
		m.statusIsError = true
		m.searchInput.SetValue("")
		return m, nil
	}
	m.searchInput.SetValue("")
	selected := make([]Track, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, m.localTracks[index])
	}
	return m.enqueueTracks(selected)
}

func (m Model) localRange(total int) (int, int) {
	size := m.localPageSize()
	start := m.localPage * size
	if start >= total {
		start = max(0, ((total-1)/size)*size)
	}
	end := min(total, start+size)
	return start, end
}

func (m Model) localVisibleCount() int {
	if m.localShowFolders {
		return len(m.localFolders)
	}
	return len(m.localTracks)
}

func (m Model) hasLocalTrackSelection(choice string) bool {
	return looksLikeSelection(choice)
}

func loadLocalListing(root, current string) ([]FolderItem, []Track, error) {
	folders, err := readLocalFolders(root, current)
	if err != nil {
		return nil, nil, err
	}
	tracks, err := readLocalTracks(current)
	if err != nil {
		return nil, nil, err
	}
	return folders, tracks, nil
}

func listLocalFolders(root, current string) []FolderItem {
	folders, _ := readLocalFolders(root, current)
	return folders
}

func readLocalFolders(root, current string) ([]FolderItem, error) {
	root = filepath.Clean(root)
	current = filepath.Clean(current)
	if !isInsideDir(root, current) {
		return nil, fmt.Errorf("pasta fora da biblioteca")
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}

	var folders []FolderItem
	if current != root {
		parent := filepath.Dir(current)
		if isInsideDir(root, parent) {
			folders = append(folders, FolderItem{Name: "..", Path: parent})
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		folders = append(folders, FolderItem{
			Name: name,
			Path: filepath.Join(current, name),
		})
	}
	sort.SliceStable(folders, func(i, j int) bool {
		if folders[i].Name == ".." {
			return true
		}
		if folders[j].Name == ".." {
			return false
		}
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})
	return folders, nil
}

func listLocalTracks(dir string) []Track {
	tracks, _ := readLocalTracks(dir)
	return tracks
}

func readLocalTracks(dir string) ([]Track, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if isAudioFile(path) {
			paths = append(paths, path)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool {
		return strings.ToLower(paths[i]) < strings.ToLower(paths[j])
	})

	tracks := make([]Track, 0, len(paths))
	for _, path := range paths {
		tracks = append(tracks, Track{
			Title:    oneLine(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))),
			Channel:  "Local",
			Duration: "local",
			URL:      path,
			Source:   sourceLocal,
		})
	}
	return tracks, nil
}

func parseLocalFolderChoice(input string) (int, bool) {
	input = strings.TrimSpace(strings.ToLower(input))
	if len(input) < 2 {
		return 0, false
	}
	if input[0] != 'p' && input[0] != 'f' {
		return 0, false
	}
	n, err := strconv.Atoi(input[1:])
	return n, err == nil
}

func isInsideDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func formatLocalPath(root, current string) string {
	rel, err := filepath.Rel(root, current)
	if err != nil || rel == "." {
		return filepath.Base(root)
	}
	return filepath.Join(filepath.Base(root), rel)
}

func parseMusicDirInput(input string, src source) (string, bool) {
	lower := strings.ToLower(input)
	for _, prefix := range []string{":pasta ", ":local ", ":dir ", ":set "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(input[len(prefix):]), true
		}
	}
	if src == sourceLocal && looksLikePath(input) {
		return input, true
	}
	return "", false
}

func normalizeDir(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", os.ErrInvalid
	}
	if input == "~" {
		input = home()
	} else if strings.HasPrefix(input, "~/") {
		input = filepath.Join(home(), strings.TrimPrefix(input, "~/"))
	} else if strings.HasPrefix(input, "~") {
		return "", fmt.Errorf("use ~ apenas como ~/caminho")
	}
	dir, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func looksLikePath(input string) bool {
	return strings.HasPrefix(input, "/") || strings.HasPrefix(input, "~") || strings.HasPrefix(input, ".")
}

func isAudioFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".wma", ".alac":
		return true
	default:
		return false
	}
}
