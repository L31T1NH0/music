package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputWidth := max(10, msg.Width-4)
		m.searchInput.Width = inputWidth
		m.selInput.Width = inputWidth
		m.prog.Width = max(10, msg.Width-22)
		return m, nil

	case initialQueryMsg:
		query := strings.TrimSpace(m.searchInput.Value())
		if query == "" {
			return m, nil
		}
		return m.startSearch(query)

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.cancelOperations()
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case searchDoneMsg:
		if msg.requestID != m.searchRequestID {
			return m, nil
		}
		m.searchTask = nil
		if errors.Is(msg.err, context.Canceled) {
			if m.state == stateSearching {
				m.state = stateSearch
				m.searchInput.Focus()
				m.statusMsg = "busca cancelada"
				m.statusIsError = false
			}
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.state = stateSearch
			m.searchInput.Focus()
			if msg.err != nil {
				m.statusMsg = "erro na busca: " + msg.err.Error()
				m.statusIsError = true
			} else {
				m.statusMsg = fmt.Sprintf("nenhum resultado para %q", msg.query)
				m.statusIsError = false
			}
			return m, nil
		}

		m.tracks = msg.tracks
		m.lastQuery = msg.query
		m.resultPage = 0
		history, historyErr := saveHistory(m.history, msg.query)
		m.history = history
		m.historyPos = -1
		m.state = stateResults
		m.selInput.SetValue("")
		m.selInput.Focus()
		if msg.err != nil {
			m.statusMsg = "resultados parciais: " + msg.err.Error()
			m.statusIsError = true
		} else if historyErr != nil {
			m.statusMsg = "resultados encontrados; histórico não foi salvo: " + historyErr.Error()
			m.statusIsError = true
		} else {
			m.statusMsg = ""
			m.statusIsError = false
		}
		return m, nil

	case playerReadyMsg:
		if msg.requestID != m.playerReqID {
			msg.control.cleanupUnadoptedResource()
			msg.session.stopProcess(2 * time.Second)
			return m, nil
		}
		msg.control.adoptResource()
		m.playerTask = nil
		m.player = msg.session
		m.currentTrack = trackPointer(msg.session.track)
		m.paused = false
		m.bassPending = false
		history, err := savePlayHistory(m.playHistory, msg.session.track)
		m.playHistory = history
		m.state = statePlaying
		if err != nil {
			m.statusMsg = "tocando; histórico não foi salvo: " + err.Error()
			m.statusIsError = true
		} else {
			m.statusMsg = ""
			m.statusIsError = false
		}
		return m, tea.Batch(
			waitTrackCmd(msg.session),
			pollPlayerCmd(msg.session),
		)

	case playerErrMsg:
		if msg.requestID != m.playerReqID {
			return m, nil
		}
		m.playerTask = nil
		m.player = nil
		m.currentTrack = nil
		m.pos, m.duration = 0, 0
		m.paused = false
		m.bassPending = false
		if msg.canceled {
			if m.state == stateLoading {
				m.state = stateSearch
				m.searchInput.Focus()
				m.statusMsg = "carregamento cancelado"
				m.statusIsError = false
			}
			return m, nil
		}

		m.statusMsg = fmt.Sprintf("não foi possível tocar %q: %v", truncate(msg.track.Title, 40), msg.err)
		m.statusIsError = true
		if len(m.queue) > 0 {
			failedStatus := m.statusMsg + "; tentando a próxima"
			nextModel, cmd := m.playNext()
			next := nextModel.(Model)
			next.statusMsg = failedStatus
			next.statusIsError = true
			return next, cmd
		}
		m.state = stateSearch
		m.searchInput.Focus()
		return m, nil

	case trackEndedMsg:
		if m.player == nil || msg.sessionID != m.player.id {
			return m, nil
		}
		m.player = nil
		m.currentTrack = nil
		m.pos, m.duration = 0, 0
		m.paused = false
		m.bassPending = false
		if msg.err != nil {
			m.statusMsg = "player encerrou com erro: " + msg.err.Error()
			m.statusIsError = true
		}
		if len(m.queue) > 0 {
			return m.playNext()
		}
		m.state = stateSearch
		m.searchInput.Focus()
		return m, nil

	case playerPollMsg:
		if m.player == nil || msg.sessionID != m.player.id {
			return m, nil
		}
		return m, pollPlayerCmd(m.player)

	case playerStatusMsg:
		if m.player == nil || msg.sessionID != m.player.id {
			return m, nil
		}
		if msg.err != nil {
			m.statusMsg = "falha ao consultar player: " + msg.err.Error()
			m.statusIsError = true
		} else {
			m.pos = msg.pos
			m.duration = msg.duration
			m.paused = msg.paused
			m.volume = msg.volume
		}
		return m, schedulePlayerPoll(msg.sessionID)

	case playerControlDoneMsg:
		if m.player == nil || msg.sessionID != m.player.id {
			return m, nil
		}
		if msg.bassChange {
			m.bassPending = false
			if msg.err != nil {
				m.bassBoost = !msg.bassEnabled
				m.statusMsg = "falha ao alterar graves: " + msg.err.Error()
				m.statusIsError = true
			} else if msg.bassEnabled {
				m.statusMsg = "reforço de graves ligado (+5 dB)"
				m.statusIsError = false
			} else {
				m.statusMsg = "reforço de graves desligado"
				m.statusIsError = false
			}
			return m, nil
		}
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("falha em %s: %v", msg.action, msg.err)
			m.statusIsError = true
		}
		return m, nil

	case downloadDoneMsg:
		download, exists := m.downloads[msg.requestID]
		if !exists || download.Track.URL != msg.track.URL {
			return m, nil
		}
		download.Done = true
		download.Path = msg.path
		download.Err = msg.err
		download.control = nil
		switch {
		case msg.canceled:
			download.Status = "cancelado"
			m.statusMsg = "download cancelado: " + truncate(msg.track.Title, 40)
			m.statusIsError = false
		case msg.err != nil:
			download.Status = "erro: " + msg.err.Error()
			m.statusMsg = "erro no download: " + msg.err.Error()
			m.statusIsError = true
		default:
			download.Status = "✓ salvo em " + msg.path
			m.statusMsg = download.Status
			m.statusIsError = false
			if m.localDirConfigured {
				folders, tracks, err := loadLocalListing(m.localDir, m.localBrowseDir)
				if err != nil {
					m.statusMsg += "; biblioteca local não pôde ser atualizada: " + oneLine(err.Error())
					m.statusIsError = true
				} else {
					m.localFolders = folders
					m.localTracks = tracks
				}
			}
		}
		m.downloads[msg.requestID] = download
		return m, nil

	case downloadProgressMsg:
		download, exists := m.downloads[msg.requestID]
		if !exists || download.Done || msg.closed {
			return m, nil
		}
		download.Status = "baixando " + truncate(download.Track.Title, 35) + " · " + msg.status
		m.downloads[msg.requestID] = download
		m.statusMsg = download.Status
		m.statusIsError = false
		return m, waitDownloadProgressCmd(msg.requestID, download.progress)

	case spinner.TickMsg:
		if m.state == stateSearching || m.state == stateLoading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m *Model) cancelOperations() {
	if m.searchTask != nil {
		m.searchTask.cancelOnly()
	}
	if m.playerTask != nil {
		m.playerTask.cancelOnly()
	}
	for _, download := range m.downloads {
		if download.control != nil && !download.Done {
			download.control.cancelOnly()
		}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateSearch:
		return m.handleSearchKey(msg)
	case stateResults:
		return m.handleResultsKey(msg)
	case statePlaying:
		return m.handlePlayerKey(msg)
	case stateSearching:
		if msg.Type == tea.KeyEsc {
			if m.searchTask != nil {
				m.searchTask.cancelOnly()
			}
			m.searchRequestID++
			m.searchTask = nil
			m.state = stateSearch
			m.searchInput.Focus()
			m.statusMsg = "busca cancelada"
			m.statusIsError = false
		}
	case stateLoading:
		if msg.Type == tea.KeyEsc {
			if m.playerTask != nil {
				m.playerTask.cancelOnly()
			}
			m.playerReqID++
			m.playerTask = nil
			m.currentTrack = nil
			m.state = stateSearch
			m.searchInput.Focus()
			m.statusMsg = "carregamento cancelado"
			m.statusIsError = false
		}
	}
	return m, nil
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab:
		m.src = nextSource(m.src)
		if m.src == sourceLocal && m.localDirConfigured {
			folders, tracks, err := loadLocalListing(m.localDir, m.localBrowseDir)
			if err != nil {
				m.statusMsg = "biblioteca local não pôde ser lida: " + oneLine(err.Error())
				m.statusIsError = true
			} else {
				m.localFolders = folders
				m.localTracks = tracks
				m.statusMsg = ""
				m.statusIsError = false
			}
			m.localPage = 0
		}
		m.historyPos = -1
		if m.src != sourceLocal || !m.localDirConfigured {
			m.statusMsg = ""
			m.statusIsError = false
		}
		m.updateSearchPlaceholder()
		return m, nil

	case tea.KeyCtrlS:
		if m.src == sourceLocal && m.localDirConfigured {
			m.localShowFolders = true
			m.localPage = 0
			m.searchInput.SetValue("")
			return m, nil
		}

	case tea.KeyCtrlF:
		if m.src == sourceLocal && m.localDirConfigured {
			m.localShowFolders = false
			m.localPage = 0
			m.searchInput.SetValue("")
			return m, nil
		}

	case tea.KeyUp:
		if m.src == sourceLocal && m.localDirConfigured && m.searchInput.Value() == "" {
			if m.localPage > 0 {
				m.localPage--
			}
			return m, nil
		}
		if m.src != sourceLocal && (m.searchInput.Value() == "" || m.historyPos >= 0) {
			return m.previousSearchHistory()
		}

	case tea.KeyDown:
		if m.src == sourceLocal && m.localDirConfigured && m.searchInput.Value() == "" {
			if (m.localPage+1)*m.localPageSize() < m.localVisibleCount() {
				m.localPage++
			}
			return m, nil
		}
		if m.src != sourceLocal && m.historyPos >= 0 {
			return m.nextSearchHistory()
		}

	case tea.KeySpace:
		if m.player != nil && m.searchInput.Value() == "" {
			m.paused = !m.paused
			return m, playerControlCmd(m.player, "pausar/retomar", []any{"set_property", "pause", m.paused})
		}

	case tea.KeyEnter:
		return m.submitSearchInput()
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	if msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		m.historyPos = -1
	}
	return m, cmd
}

func (m Model) submitSearchInput() (tea.Model, tea.Cmd) {
	query := strings.TrimSpace(m.searchInput.Value())
	if query == "" {
		if m.player != nil {
			m.state = statePlaying
			return m, pollPlayerCmd(m.player)
		}
		return m, nil
	}

	lower := strings.ToLower(query)
	switch lower {
	case "q", "sair", "exit", "quit", ":q", ":sair":
		m.cancelOperations()
		return m, tea.Quit
	case "fila", ":fila":
		m.searchInput.SetValue("")
		m.statusMsg = fmtQueueStatus(m.currentTrack, m.queue)
		m.statusIsError = false
		return m, nil
	case "historico", "histórico", "hist", ":historico", ":histórico":
		return m.showPlayHistory()
	}
	if updated, handled := m.handleQueueCommand(query); handled {
		return updated, nil
	}
	if lower == ":formato" || strings.HasPrefix(lower, ":formato ") {
		format := strings.TrimSpace(strings.TrimPrefix(lower, ":formato "))
		if format != "mp3" && format != "original" {
			m.statusMsg = "uso: :formato mp3 ou :formato original"
			m.statusIsError = true
			return m, nil
		}
		m.downloadFormat = format
		if err := saveConfig(m.config()); err != nil {
			m.statusMsg = "formato alterado apenas nesta sessão: " + err.Error()
			m.statusIsError = true
		} else {
			m.statusMsg = "formato de download: " + format
			m.statusIsError = false
		}
		m.searchInput.SetValue("")
		return m, nil
	}
	if lower == ":downloads" || lower == ":download cancelar" || lower == ":downloads cancelar" {
		active := 0
		var statuses []string
		for id, download := range m.downloads {
			if !download.Done && download.control != nil {
				active++
				if strings.HasSuffix(lower, "cancelar") {
					download.control.cancelOnly()
					download.Status = "cancelando " + truncate(download.Track.Title, 35) + "…"
					m.downloads[id] = download
				}
			}
			if len(statuses) < 3 {
				statuses = append(statuses, download.Status)
			}
		}
		if strings.HasSuffix(lower, "cancelar") {
			m.statusMsg = fmt.Sprintf("cancelando %d download(s)", active)
		} else if len(statuses) == 0 {
			m.statusMsg = "nenhum download nesta sessão"
		} else {
			m.statusMsg = strings.Join(statuses, "  ·  ")
		}
		m.statusIsError = false
		m.searchInput.SetValue("")
		return m, nil
	}

	forceSearch := false
	for _, prefix := range []string{":buscar ", ":search "} {
		if strings.HasPrefix(lower, prefix) {
			query = strings.TrimSpace(query[len(prefix):])
			forceSearch = true
			break
		}
	}
	if query == "" {
		m.statusMsg = "busca vazia"
		m.statusIsError = true
		return m, nil
	}

	if !forceSearch {
		if dir, ok := parseMusicDirInput(query, m.src); ok {
			return m.setLocalDir(dir)
		}
	}
	if m.src == sourceLocal && !m.localDirConfigured {
		m.statusMsg = "defina a pasta com :pasta /caminho"
		m.statusIsError = true
		return m, nil
	}

	if m.src == sourceLocal && !forceSearch {
		if query == "0" || strings.EqualFold(query, "raiz") {
			return m.openLocalRoot()
		}
		if folder, ok := parseLocalFolderChoice(query); ok {
			return m.openLocalFolder(folder)
		}
		if m.localShowFolders {
			if folder, err := strconv.Atoi(query); err == nil {
				return m.openLocalFolder(folder)
			}
		}
		if m.hasLocalTrackSelection(query) {
			return m.handleLocalTrackSelection(query)
		}
	}
	return m.startSearch(query)
}

func (m Model) startSearch(query string) (tea.Model, tea.Cmd) {
	query = oneLine(query)
	if query == "" {
		m.statusMsg = "busca vazia"
		m.statusIsError = true
		return m, nil
	}
	if m.searchTask != nil {
		m.searchTask.cancelOnly()
	}
	m.searchRequestID++
	ctx, control := newTimedTaskControl(context.Background(), 30*time.Second)
	m.searchTask = control
	m.state = stateSearching
	m.lastQuery = query
	m.searchInput.Blur()
	m.statusMsg = ""
	m.statusIsError = false
	return m, tea.Batch(
		m.spin.Tick,
		searchCmd(ctx, control, m.searchRequestID, query, m.src, m.localBrowseDir),
	)
}

func (m Model) showPlayHistory() (tea.Model, tea.Cmd) {
	if len(m.playHistory) == 0 {
		m.searchInput.SetValue("")
		m.statusMsg = "histórico vazio"
		m.statusIsError = false
		return m, nil
	}
	m.tracks = append([]Track(nil), m.playHistory...)
	m.lastQuery = "histórico"
	m.resultPage = 0
	m.state = stateResults
	m.searchInput.Blur()
	m.selInput.SetValue("")
	m.selInput.Focus()
	m.statusMsg = ""
	m.statusIsError = false
	return m, nil
}

func (m Model) previousSearchHistory() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		return m, nil
	}
	if m.historyPos < len(m.history)-1 {
		m.historyPos++
	}
	m.searchInput.SetValue(m.history[m.historyPos])
	m.searchInput.CursorEnd()
	return m, nil
}

func (m Model) nextSearchHistory() (tea.Model, tea.Cmd) {
	if m.historyPos > 0 {
		m.historyPos--
		m.searchInput.SetValue(m.history[m.historyPos])
	} else {
		m.historyPos = -1
		m.searchInput.SetValue("")
	}
	m.searchInput.CursorEnd()
	return m, nil
}

func (m Model) handleResultsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = stateSearch
		m.searchInput.Focus()
		return m, nil
	case tea.KeyUp:
		if m.resultPage > 0 {
			m.resultPage--
		}
		return m, nil
	case tea.KeyDown:
		if (m.resultPage+1)*m.resultPageSize() < len(m.tracks) {
			m.resultPage++
		}
		return m, nil
	case tea.KeyEnter:
		choice := strings.TrimSpace(m.selInput.Value())
		if choice == "" {
			start, end := m.resultRange()
			if end == 0 {
				return m, nil
			}
			choice = strconv.Itoa(start + 1)
		}
		return m.handleSelection(choice)
	}

	var cmd tea.Cmd
	m.selInput, cmd = m.selInput.Update(msg)
	return m, cmd
}

func (m Model) handleSelection(choice string) (tea.Model, tea.Cmd) {
	lower := strings.ToLower(strings.TrimSpace(choice))
	switch lower {
	case "q", "sair", "exit", ":q":
		m.cancelOperations()
		return m, tea.Quit
	case "fila", ":fila":
		m.statusMsg = fmtQueueStatus(m.currentTrack, m.queue)
		m.statusIsError = false
		m.selInput.SetValue("")
		return m, nil
	}
	if updated, handled := m.handleQueueCommand(choice); handled {
		updated.selInput.SetValue("")
		return updated, nil
	}

	if strings.HasPrefix(lower, "d") && len(lower) > 1 {
		number, err := strconv.Atoi(strings.TrimSpace(lower[1:]))
		if err != nil || number < 1 || number > len(m.tracks) {
			m.statusMsg = fmt.Sprintf("download inválido; use d1 até d%d", len(m.tracks))
			m.statusIsError = true
			m.selInput.SetValue("")
			return m, nil
		}
		m.selInput.SetValue("")
		return m.startDownload(m.tracks[number-1])
	}

	indices, err := parseSelection(choice, len(m.tracks))
	if err != nil {
		m.statusMsg = err.Error()
		m.statusIsError = true
		m.selInput.SetValue("")
		return m, nil
	}
	selected := make([]Track, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, m.tracks[index])
	}
	m.selInput.SetValue("")
	return m.enqueueTracks(selected)
}

func (m Model) enqueueTracks(tracks []Track) (tea.Model, tea.Cmd) {
	if len(tracks) == 0 {
		return m, nil
	}
	if len(m.queue)+len(tracks) > maxQueueSize {
		m.statusMsg = fmt.Sprintf("fila limitada a %d faixas", maxQueueSize)
		m.statusIsError = true
		return m, nil
	}
	m.queue = append(m.queue, tracks...)
	m.statusMsg = fmt.Sprintf("%d faixa(s) adicionada(s) à fila", len(tracks))
	m.statusIsError = false
	if m.currentTrack == nil && m.playerTask == nil {
		return m.playNext()
	}
	m.state = statePlaying
	if m.player != nil {
		return m, pollPlayerCmd(m.player)
	}
	return m, nil
}

func (m Model) playNext() (tea.Model, tea.Cmd) {
	if len(m.queue) == 0 {
		m.currentTrack = nil
		m.state = stateSearch
		m.searchInput.Focus()
		return m, nil
	}
	next := m.queue[0]
	m.queue = m.queue[1:]
	return m.beginTrack(next)
}

func (m Model) beginTrack(track Track) (tea.Model, tea.Cmd) {
	oldPlayer := m.player
	m.player = nil
	if m.playerTask != nil {
		m.playerTask.cancelOnly()
	}
	m.playerReqID++
	ctx, control := newTaskControl(context.Background())
	m.playerTask = control
	m.currentTrack = trackPointer(track)
	m.pos, m.duration = 0, 0
	m.paused = false
	m.bassPending = false
	m.state = stateLoading
	m.searchInput.Blur()
	m.selInput.Blur()
	return m, tea.Batch(
		m.spin.Tick,
		startPlayingCmd(ctx, control, oldPlayer, m.playerReqID, track, m.volume, m.bassBoost),
	)
}

func trackPointer(track Track) *Track {
	copy := track
	return &copy
}

func (m Model) startDownload(track Track) (tea.Model, tea.Cmd) {
	if track.Source == sourceLocal {
		m.statusMsg = "a faixa já é um arquivo local"
		m.statusIsError = false
		return m, nil
	}
	if existingID, exists := m.downloadByURL[track.URL]; exists {
		existing := m.downloads[existingID]
		m.statusMsg = existing.Status
		m.statusIsError = existing.Err != nil
		return m, nil
	}

	destination := defaultMusicDir()
	if m.localDirConfigured {
		destination = m.localDir
	}
	m.downloadReqID++
	ctx, control := newTimedTaskControl(context.Background(), 5*time.Minute)
	progress := make(chan string, 16)
	download := downloadState{
		ID:       m.downloadReqID,
		Track:    track,
		Status:   "baixando " + truncate(track.Title, 50) + "…",
		control:  control,
		progress: progress,
	}
	m.downloads[download.ID] = download
	m.downloadByURL[track.URL] = download.ID
	m.statusMsg = download.Status
	m.statusIsError = false
	return m, tea.Batch(
		downloadCmd(ctx, control, download.ID, track, destination, m.downloadFormat, progress),
		waitDownloadProgressCmd(download.ID, progress),
	)
}

func (m Model) handleQueueCommand(input string) (Model, bool) {
	normalized := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(input), ":"))
	fields := strings.Fields(normalized)
	if len(fields) == 0 || fields[0] != "fila" {
		return m, false
	}
	if len(fields) == 1 {
		m.statusMsg = fmtQueueStatus(m.currentTrack, m.queue)
		m.statusIsError = false
		return m, true
	}

	switch fields[1] {
	case "limpar", "clear":
		removed := len(m.queue)
		m.queue = nil
		m.statusMsg = fmt.Sprintf("fila limpa (%d removida(s))", removed)
		m.statusIsError = false
	case "remover", "remove":
		if len(fields) != 3 {
			m.statusMsg = "uso: :fila remover N"
			m.statusIsError = true
			return m, true
		}
		index, err := strconv.Atoi(fields[2])
		if err != nil || index < 1 || index > len(m.queue) {
			m.statusMsg = fmt.Sprintf("posição inválida na fila (1–%d)", len(m.queue))
			m.statusIsError = true
			return m, true
		}
		removed := m.queue[index-1]
		m.queue = append(m.queue[:index-1], m.queue[index:]...)
		m.statusMsg = "removida: " + truncate(removed.Title, 50)
		m.statusIsError = false
	case "mover", "move":
		if len(fields) != 4 {
			m.statusMsg = "uso: :fila mover ORIGEM DESTINO"
			m.statusIsError = true
			return m, true
		}
		from, fromErr := strconv.Atoi(fields[2])
		to, toErr := strconv.Atoi(fields[3])
		if fromErr != nil || toErr != nil || from < 1 || to < 1 || from > len(m.queue) || to > len(m.queue) {
			m.statusMsg = fmt.Sprintf("posições válidas: 1–%d", len(m.queue))
			m.statusIsError = true
			return m, true
		}
		track := m.queue[from-1]
		m.queue = append(m.queue[:from-1], m.queue[from:]...)
		to--
		m.queue = append(m.queue, Track{})
		copy(m.queue[to+1:], m.queue[to:])
		m.queue[to] = track
		m.statusMsg = fmt.Sprintf("faixa movida de %d para %d", from, to+1)
		m.statusIsError = false
	default:
		m.statusMsg = "use :fila, :fila limpar, :fila remover N ou :fila mover A B"
		m.statusIsError = true
	}
	return m, true
}

func (m Model) handlePlayerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.player == nil {
		m.state = stateSearch
		m.searchInput.Focus()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyLeft:
		return m, playerControlCmd(m.player, "retroceder", []any{"seek", -10, "relative"})
	case tea.KeyRight:
		return m, playerControlCmd(m.player, "avançar", []any{"seek", 10, "relative"})
	case tea.KeyUp:
		m.volume = min(150, m.volume+5)
		return m, playerControlCmd(m.player, "alterar volume", []any{"set_property", "volume", m.volume})
	case tea.KeyDown:
		m.volume = max(0, m.volume-5)
		return m, playerControlCmd(m.player, "alterar volume", []any{"set_property", "volume", m.volume})
	case tea.KeySpace:
		m.paused = !m.paused
		return m, playerControlCmd(m.player, "pausar/retomar", []any{"set_property", "pause", m.paused})
	case tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return m, nil
		}
		switch strings.ToLower(string(msg.Runes[0])) {
		case "p":
			m.state = stateSearch
			m.searchInput.SetValue("")
			m.searchInput.Focus()
		case "q":
			if len(m.queue) > 0 {
				return m.playNext()
			}
			oldPlayer := m.player
			m.player = nil
			m.currentTrack = nil
			m.pos, m.duration = 0, 0
			m.paused = false
			m.bassPending = false
			m.state = stateSearch
			m.searchInput.Focus()
			return m, stopPlayerCmd(oldPlayer)
		case "d":
			if m.currentTrack != nil {
				return m.startDownload(*m.currentTrack)
			}
		case "s":
			if m.bassPending {
				m.statusMsg = "alteração de graves em andamento"
				m.statusIsError = false
				return m, nil
			}
			m.bassBoost = !m.bassBoost
			m.bassPending = true
			if m.bassBoost {
				m.statusMsg = "ligando reforço de graves…"
			} else {
				m.statusMsg = "desligando reforço de graves…"
			}
			m.statusIsError = false
			return m, bassBoostControlCmd(m.player, m.bassBoost)
		case "c":
			m.queue = nil
			m.statusMsg = "fila limpa"
			m.statusIsError = false
		case "x":
			if len(m.queue) == 0 {
				m.statusMsg = "fila vazia"
			} else {
				removed := m.queue[0]
				m.queue = m.queue[1:]
				m.statusMsg = "removida da fila: " + truncate(removed.Title, 45)
			}
			m.statusIsError = false
		}
	}
	return m, nil
}

func stopPlayerCmd(session *playerSession) tea.Cmd {
	return func() tea.Msg {
		session.stopProcess(3 * time.Second)
		return nil
	}
}
