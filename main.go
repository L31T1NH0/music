package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	maxHistory     = 50
	maxPlayHistory = 100
	searchLimit    = 30
	pageSize       = 10
	defaultVol     = 80
	maxQueueSize   = 500
	maxSelection   = 100
)

type source int

const (
	sourceYT source = iota
	sourceSC
	sourceLocal
)

var (
	version               = "devel"
	historyFile           = filepath.Join(userDataDir(), "music", "search_history.json")
	playHistoryFile       = filepath.Join(userDataDir(), "music", "play_history.json")
	legacyHistoryFile     = filepath.Join(home(), ".local", "share", "ytmusic", "history.json")
	legacyPlayHistoryFile = filepath.Join(home(), ".local", "share", "ytmusic", "play_history.json")
	configFile            = filepath.Join(userConfigDir(), "music", "config.json")
)

func home() string {
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return h
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func userConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return dir
	}
	return filepath.Join(home(), ".config")
}

func userDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" && filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(home(), ".local", "share")
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	sAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	sPlaying = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	sPaused  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	sDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	sOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	sNum     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	sErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	sKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
)

// ── Types ─────────────────────────────────────────────────────────────────────

type Track struct {
	Title    string
	Artist   string `json:",omitempty"`
	Cover    string `json:",omitempty"`
	Channel  string
	Duration string
	URL      string
	Source   source
}

type Config struct {
	MusicSync      MusicSyncConfig `json:"music_sync,omitempty"`
	LocalDir       string          `json:"local_dir"`
	Volume         *int            `json:"volume,omitempty"`
	DownloadFormat string          `json:"download_format,omitempty"`
}

type FolderItem struct {
	Name string
	Path string
}

type appState int

const (
	stateSearch appState = iota
	stateSearching
	stateResults
	stateLoading
	statePlaying
)

// ── Messages ──────────────────────────────────────────────────────────────────

type (
	searchDoneMsg struct {
		requestID uint64
		tracks    []Track
		query     string
		src       source
		err       error
	}
	playerReadyMsg struct {
		requestID uint64
		session   *playerSession
		control   *taskControl
	}
	playerErrMsg struct {
		requestID uint64
		track     Track
		err       error
		canceled  bool
	}
	trackEndedMsg struct {
		sessionID uint64
		err       error
	}
	playerPollMsg   struct{ sessionID uint64 }
	playerStatusMsg struct {
		sessionID uint64
		pos       float64
		duration  float64
		paused    bool
		volume    int
		err       error
	}
	playerControlDoneMsg struct {
		sessionID   uint64
		action      string
		err         error
		bassChange  bool
		bassEnabled bool
	}
	downloadDoneMsg struct {
		requestID uint64
		track     Track
		path      string
		err       error
		canceled  bool
	}
	downloadProgressMsg struct {
		requestID uint64
		status    string
		closed    bool
	}
	initialQueryMsg struct{}
)

type downloadState struct {
	ID       uint64
	Track    Track
	Status   string
	Path     string
	Done     bool
	Err      error
	control  *taskControl
	progress <-chan string
}

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	musicSync          MusicSyncConfig
	music              musicState
	state              appState
	width              int
	height             int
	src                source
	localDir           string
	localBrowseDir     string
	localDirConfigured bool
	localFolders       []FolderItem
	localTracks        []Track
	localPage          int
	localShowFolders   bool

	searchInput textinput.Model
	history     []string
	historyPos  int
	playHistory []Track

	tracks          []Track
	lastQuery       string
	resultPage      int
	selInput        textinput.Model
	searchRequestID uint64
	searchTask      *taskControl

	queue        []Track
	currentTrack *Track
	player       *playerSession
	playerTask   *taskControl
	playerReqID  uint64

	pos         float64
	duration    float64
	paused      bool
	volume      int
	bassBoost   bool
	bassPending bool

	prog          progress.Model
	spin          spinner.Model
	statusMsg     string
	statusIsError bool

	downloadReqID  uint64
	downloads      map[uint64]downloadState
	downloadByURL  map[string]uint64
	downloadFormat string

	initialQuery string
}

func newModel(initialQuery string) Model {
	cfg, cfgErr := loadConfig()
	searchHistory, searchHistoryErr := loadHistory()
	playHistory, playHistoryErr := loadPlayHistory()
	localDir := defaultMusicDir()
	localBrowseDir := localDir
	localDirConfigured := false
	var initialWarnings []string
	if cfgErr != nil {
		initialWarnings = append(initialWarnings, "configuração ignorada: "+cfgErr.Error())
	}
	if searchHistoryErr != nil {
		initialWarnings = append(initialWarnings, "histórico de buscas ignorado: "+searchHistoryErr.Error())
	}
	if playHistoryErr != nil {
		initialWarnings = append(initialWarnings, "histórico de reprodução ignorado: "+playHistoryErr.Error())
	}
	volume := defaultVol
	downloadFormat := "mp3"
	if cfg.LocalDir != "" {
		if dir, err := normalizeDir(cfg.LocalDir); err == nil {
			localDir = dir
			localBrowseDir = dir
			localDirConfigured = true
		} else {
			initialWarnings = append(initialWarnings, "pasta configurada não está disponível: "+cfg.LocalDir)
		}
	}
	if cfg.Volume != nil && *cfg.Volume >= 0 && *cfg.Volume <= 150 {
		volume = *cfg.Volume
	}
	if cfg.DownloadFormat == "original" || cfg.DownloadFormat == "mp3" {
		downloadFormat = cfg.DownloadFormat
	}
	localFolders, localTracks, localListingErr := loadLocalListing(localDir, localBrowseDir)
	if localListingErr != nil {
		localFolders, localTracks = nil, nil
		if localDirConfigured {
			initialWarnings = append(initialWarnings, "biblioteca local não pôde ser lida: "+oneLine(localListingErr.Error()))
		}
	}

	si := textinput.New()
	si.Placeholder = "pesquisar..."
	si.CharLimit = 200
	si.Width = 60
	si.Focus()
	if initialQuery != "" {
		si.SetValue(oneLine(initialQuery))
	}

	sel := textinput.New()
	sel.Placeholder = "1"
	sel.CharLimit = 50
	sel.Width = 60

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent

	prog := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(),
	)

	return Model{
		musicSync:          cfg.MusicSync,
		state:              stateSearch,
		src:                sourceYT,
		localDir:           localDir,
		localBrowseDir:     localBrowseDir,
		localDirConfigured: localDirConfigured,
		localFolders:       localFolders,
		localTracks:        localTracks,
		searchInput:        si,
		selInput:           sel,
		history:            searchHistory,
		historyPos:         -1,
		playHistory:        playHistory,
		volume:             volume,
		prog:               prog,
		spin:               sp,
		statusMsg:          strings.Join(initialWarnings, "; "),
		statusIsError:      len(initialWarnings) > 0,
		downloads:          make(map[uint64]downloadState),
		downloadByURL:      make(map[string]uint64),
		downloadFormat:     downloadFormat,
		initialQuery:       strings.TrimSpace(initialQuery),
	}
}

func (m Model) Init() tea.Cmd {
	if m.initialQuery != "" {
		return tea.Batch(textinput.Blink, func() tea.Msg { return initialQueryMsg{} })
	}
	return textinput.Blink
}

func (m *Model) shutdown() error {
	if m.searchTask != nil {
		m.searchTask.cancelAndWait(2 * time.Second)
	}
	if m.playerTask != nil {
		m.playerTask.cancelAndWait(3 * time.Second)
	}
	for _, download := range m.downloads {
		if download.control != nil && !download.Done {
			download.control.cancelAndWait(3 * time.Second)
		}
	}
	if m.player != nil {
		m.player.stopProcess(3 * time.Second)
	}
	if m.music.event.SessionID != "" {
		m.music.event.Sequence++
		m.music.event.Playing = false
		m.music.event.ObservedAt = time.Now().UTC()
		if err := sendMusicEvent(m.musicSync, m.music.event); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	return saveConfig(m.config())
}

func (m Model) config() Config {
	volume := m.volume
	localDir := ""
	if m.localDirConfigured {
		localDir = m.localDir
	}
	return Config{
		MusicSync:      m.musicSync,
		LocalDir:       localDir,
		Volume:         &volume,
		DownloadFormat: m.downloadFormat,
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--sync-setup" {
		if err := configureMusicSync(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "-h", "--help":
			fmt.Println("uso: music [termos da busca] | music --sync-setup https://sua-api.vercel.app")
			fmt.Println("abre a interface e inicia a busca quando termos são informados")
			return
		case "-v", "--version":
			fmt.Println("music " + version)
			return
		}
	}
	initialQuery := strings.Join(os.Args[1:], " ")

	p := tea.NewProgram(
		newModel(initialQuery),
		tea.WithAltScreen(),
	)

	finalModel, err := p.Run()
	var shutdownErr error
	if model, ok := finalModel.(Model); ok {
		shutdownErr = model.shutdown()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	if shutdownErr != nil {
		fmt.Fprintf(os.Stderr, "erro ao salvar configuração: %v\n", shutdownErr)
		os.Exit(1)
	}
}
