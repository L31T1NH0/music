package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) View() string {
	if m.width > 0 && m.width < 24 {
		return m.fitView("terminal estreito\nmínimo: 24 colunas")
	}
	if m.height > 0 && m.height < 7 {
		return m.fitView("terminal baixo\nmínimo: 7 linhas")
	}

	var view string
	switch m.state {
	case stateSearch:
		view = m.viewSearch()
	case stateSearching:
		view = m.viewSpinner("buscando", m.lastQuery)
	case stateResults:
		view = m.viewResults()
	case stateLoading:
		title := ""
		if m.currentTrack != nil {
			title = m.currentTrack.Title
		}
		view = m.viewSpinner("carregando", title)
	case statePlaying:
		view = m.viewPlayer()
	default:
		return ""
	}
	return m.fitView(view)
}

func (m Model) fitView(view string) string {
	lines := strings.Split(view, "\n")
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	if m.width > 0 {
		for index, line := range lines {
			if ansi.StringWidth(line) > m.width {
				lines[index] = ansi.Truncate(line, m.width, "")
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) contentWidth() int {
	if m.width <= 0 {
		return 76
	}
	return max(1, m.width-4)
}

func (m Model) viewHeader() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	center := lipgloss.NewStyle().Width(width).MaxWidth(width).Align(lipgloss.Center)

	ytStyle, scStyle, localStyle := sDim, sDim, sDim
	sourceName := "YouTube"
	switch m.src {
	case sourceSC:
		scStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
		sourceName = "SoundCloud"
	case sourceLocal:
		localStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
		sourceName = "Local"
	default:
		ytStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	}

	sourceBar := ""
	if width < 54 {
		sourceBar = sTitle.Render(sourceName) + sDim.Render("  (Tab)")
	} else {
		sourceBar = ytStyle.Render("YouTube") +
			sDim.Render("  ·  ") +
			scStyle.Render("SoundCloud") +
			sDim.Render("  ·  ") +
			localStyle.Render("Local") +
			sDim.Render("  (Tab)")
	}

	return "\n" +
		center.Render(sAccent.Render(" ♫ ")+sTitle.Render("music")) + "\n" +
		center.Render(sourceBar) + "\n"
}

func (m Model) viewSearch() string {
	var builder strings.Builder
	builder.WriteString(m.viewHeader())
	builder.WriteString("\n")

	if m.currentTrack != nil {
		icon, stateStyle := "▶", sPlaying
		action := "Enter para voltar"
		if m.paused {
			icon, stateStyle = "⏸", sPaused
			action = "Enter para retomar"
		}
		lineWidth := m.contentWidth()
		titleWidth := max(8, min(40, lineWidth/2))
		channelWidth := max(6, min(24, lineWidth-titleWidth-18))
		builder.WriteString("  " + stateStyle.Render(icon+"  "))
		builder.WriteString(inlineText(m.currentTrack.Title, titleWidth, sTitle))
		if lineWidth >= 52 {
			builder.WriteString(sDim.Render("  ·  ") + inlineText(m.currentTrack.Channel, channelWidth, sDim))
		}
		builder.WriteString(sDim.Render("  —  " + action))
		builder.WriteString("\n\n")
	}

	builder.WriteString(m.viewStatus())

	if m.src == sourceLocal && !m.localDirConfigured {
		builder.WriteString("  " + m.searchInput.View() + "\n\n")
		builder.WriteString("  " + hints(
			m.contentWidth(),
			kv("Enter", "salvar pasta"),
			kv(":pasta /caminho", "definir"),
			kv("Tab", "fonte"),
			kv("q", "sair"),
		) + "\n")
	} else if m.src == sourceLocal {
		builder.WriteString(m.viewLocalBrowse())
	} else {
		builder.WriteString("  " + m.searchInput.View() + "\n\n")
		builder.WriteString("  " + hints(
			m.contentWidth(),
			kv("Enter", "buscar"),
			kv("↑↓", "histórico de buscas"),
			kv(":pasta /caminho", "mudar pasta"),
			kv(":downloads", "ver tarefas"),
			kv("fila", "ver fila"),
			kv("historico", "faixas recentes"),
			kv("q", "sair"),
		) + "\n")
	}
	return builder.String()
}

func (m Model) viewStatus() string {
	if m.statusMsg == "" {
		return ""
	}
	style := sWarn
	if m.statusIsError {
		style = sErr
	}
	return "  " + style.Render(truncate(m.statusMsg, m.contentWidth())) + "\n\n"
}

func (m Model) viewLocalBrowse() string {
	var builder strings.Builder
	mode := "arquivos"
	if m.localShowFolders {
		mode = "pastas"
	}
	pathWidth := max(8, m.contentWidth()-25)
	builder.WriteString("  " + sDim.Render("pasta: ") + inlineText(formatLocalPath(m.localDir, m.localBrowseDir), pathWidth, sTitle))
	builder.WriteString(sDim.Render("  ·  ") + sTitle.Render(mode) + "\n\n")
	builder.WriteString("  " + m.searchInput.View() + "\n\n")

	if m.localShowFolders {
		start, end := m.localRange(len(m.localFolders))
		builder.WriteString("  " + sDim.Render(fmt.Sprintf("pastas %d-%d de %d", displayStart(start, end), end, len(m.localFolders))) + "\n\n")
		if len(m.localFolders) == 0 {
			builder.WriteString("  " + sDim.Render("nenhuma subpasta") + "\n")
		} else {
			for index := start; index < end; index++ {
				folder := m.localFolders[index]
				number := lipgloss.NewStyle().Width(3).Align(lipgloss.Right).Render(sNum.Render(strconv.Itoa(index + 1)))
				nameWidth := max(4, m.contentWidth()-12)
				name := fixedCell(folder.Name, nameWidth, sTitle)
				builder.WriteString(fmt.Sprintf("  %s  %s  %s\n", number, name, sDim.Render("pasta")))
			}
		}
	} else {
		start, end := m.localRange(len(m.localTracks))
		builder.WriteString("  " + sDim.Render(fmt.Sprintf("arquivos %d-%d de %d", displayStart(start, end), end, len(m.localTracks))) + "\n\n")
		if len(m.localTracks) == 0 {
			builder.WriteString("  " + sDim.Render("nenhuma música nesta pasta") + "\n")
		} else {
			for index := start; index < end; index++ {
				builder.WriteString(m.renderTrackRow(index, m.localTracks[index]))
			}
		}
	}

	builder.WriteString("\n")
	if m.localShowFolders {
		pairs := []kvPair{
			kv("1", "abrir"),
			kv("0", "raiz"),
			kv("Ctrl+F", "arquivos"),
			kv("historico", "recentes"),
		}
		if len(m.localFolders) > m.localPageSize() {
			pairs = append(pairs, kv("↑↓", "mais pastas"))
		}
		pairs = append(pairs, kv("q", "sair"))
		builder.WriteString("  " + hints(m.contentWidth(), pairs...) + "\n")
	} else {
		pairs := []kvPair{
			kv("1", "tocar"),
			kv("1-5 / 1,3", "fila"),
			kv(":buscar termo", "buscar sem selecionar"),
			kv("0", "raiz"),
			kv("Ctrl+S", "pastas"),
		}
		if len(m.localTracks) > m.localPageSize() {
			pairs = append(pairs, kv("↑↓", "mais músicas"))
		}
		pairs = append(pairs, kv("q", "sair"))
		builder.WriteString("  " + hints(m.contentWidth(), pairs...) + "\n")
	}
	return builder.String()
}

func (m Model) renderTrackRow(index int, track Track) string {
	number := lipgloss.NewStyle().Width(3).Align(lipgloss.Right).Render(sNum.Render(strconv.Itoa(index + 1)))
	titleWidth, channelWidth, showDuration := m.listWidths()
	title := fixedCell(track.Title, titleWidth, sTitle)
	row := fmt.Sprintf("  %s  %s", number, title)
	if channelWidth > 0 {
		row += "  " + fixedCell(track.Channel, channelWidth, sDim)
	}
	if showDuration {
		row += "  " + sDim.Render(fmt.Sprintf("%6s", truncate(track.Duration, 6)))
	}
	return row + "\n"
}

func (m Model) viewSpinner(action, subject string) string {
	var builder strings.Builder
	builder.WriteString(m.viewHeader())
	builder.WriteString("\n")
	builder.WriteString("  " + m.spin.View() + "  ")
	builder.WriteString(sDim.Render(action + " "))
	builder.WriteString(inlineText(subject, max(8, m.contentWidth()-16), sTitle))
	builder.WriteString("\n\n")
	builder.WriteString("  " + hints(m.contentWidth(), kv("Esc", "cancelar"), kv("Ctrl+C", "sair")) + "\n")
	return builder.String()
}

func (m Model) viewResults() string {
	var builder strings.Builder
	builder.WriteString("\n")
	queryWidth := max(6, m.contentWidth()-10)
	builder.WriteString("  " + sAccent.Render("─── "+truncate(m.lastQuery, queryWidth)+" ───") + "\n\n")

	start, end := m.resultRange()
	for index := start; index < end; index++ {
		builder.WriteString(m.renderTrackRow(index, m.tracks[index]))
	}

	builder.WriteString("\n")
	builder.WriteString(m.viewStatus())
	builder.WriteString("  " + sDim.Render("→  ") + m.selInput.View() + "\n\n")
	rangeEnd := min(end, start+5)
	secondPick := min(end, start+3)
	pairs := []kvPair{
		kv(fmt.Sprintf("%d–%d", start+1, end), "tocar"),
		kv(fmt.Sprintf("%d-%d / %d,%d", start+1, rangeEnd, start+1, secondPick), "fila"),
	}
	if downloadIndex := firstDownloadable(m.tracks, start, end); downloadIndex >= 0 {
		pairs = append(pairs, kv(fmt.Sprintf("d%d", downloadIndex+1), "download"))
	}
	if len(m.tracks) > m.resultPageSize() {
		pairs = append(pairs, kv("↑↓", "mais resultados"))
	}
	pairs = append(pairs, kv("Esc", "voltar"))
	builder.WriteString("  " + hints(m.contentWidth(), pairs...) + "\n")
	return builder.String()
}

func firstDownloadable(tracks []Track, start, end int) int {
	for index := start; index < end; index++ {
		if tracks[index].Source != sourceLocal {
			return index
		}
	}
	return -1
}

func (m Model) viewPlayer() string {
	if m.currentTrack == nil {
		return ""
	}

	icon, borderColor, iconStyle := "▶", lipgloss.Color("10"), sPlaying
	if m.paused {
		icon, borderColor, iconStyle = "⏸", lipgloss.Color("11"), sPaused
	}

	width := m.width
	if width <= 0 {
		width = 80
	}
	totalWidth := max(18, width-4)
	queuePanelWidth := 0
	if len(m.queue) > 0 && totalWidth >= 90 {
		queuePanelWidth = 32
	}
	playerPanelWidth := totalWidth - queuePanelWidth
	if queuePanelWidth > 0 {
		playerPanelWidth -= 2
	}

	progressBar := m.prog
	progressBar.Width = max(8, playerPanelWidth-20)

	var inner strings.Builder
	inner.WriteString("\n")
	playingStatus := iconStyle.Render(icon + "  tocando")
	if m.bassBoost {
		playingStatus += sDim.Render("  ·  ") + sAccent.Render("GRAVES +5 dB")
	}
	inner.WriteString("  " + playingStatus + "\n\n")
	inner.WriteString("  " + inlineText(m.currentTrack.Title, max(6, playerPanelWidth-8), sTitle) + "\n")
	if playerPanelWidth >= 30 {
		inner.WriteString("  " + inlineText(m.currentTrack.Channel, max(6, playerPanelWidth-8), sDim) + "\n")
	}
	inner.WriteString("\n")

	percentage := 0.0
	if m.duration > 0 {
		percentage = min(1.0, max(0.0, m.pos/m.duration))
	}
	inner.WriteString("  " + progressBar.ViewAs(percentage))
	if playerPanelWidth >= 34 {
		inner.WriteString(sDim.Render(fmt.Sprintf("  %s / %s", fmtDuration(m.pos), fmtDuration(m.duration))))
	}
	inner.WriteString("\n")

	if status := m.downloadStatusFor(m.currentTrack.URL); status != "" {
		style := sDim
		if strings.HasPrefix(status, "✓") {
			style = sOK
		} else if strings.HasPrefix(status, "erro") {
			style = sErr
		}
		inner.WriteString("\n  " + style.Render("⬇  "+truncate(status, max(8, playerPanelWidth-8))) + "\n")
	}
	if m.statusMsg != "" {
		style := sWarn
		if m.statusIsError {
			style = sErr
		}
		inner.WriteString("\n  " + style.Render(truncate(m.statusMsg, max(8, playerPanelWidth-8))) + "\n")
	}

	pauseLabel := "pausar"
	if m.paused {
		pauseLabel = "retomar"
	}
	pairs := []kvPair{
		kv("space", pauseLabel),
		kv("←→", "seek 10s"),
		kv("↑↓", fmt.Sprintf("vol %d%%", m.volume)),
		kv("S", "graves on/off"),
	}
	if m.currentTrack.Source != sourceLocal {
		pairs = append(pairs, kv("d", "download"))
	}
	pairs = append(
		pairs,
		kv("p", "pesquisar"),
		kv("q", "próxima/parar"),
		kv("x", "remover próxima"),
		kv("c", "limpar fila"),
	)
	inner.WriteString("\n  " + hints(max(8, playerPanelWidth-6), pairs...) + "\n")

	playerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(playerPanelWidth).
		MaxWidth(playerPanelWidth).
		Render(inner.String())

	if queuePanelWidth == 0 {
		return "\n" + lipgloss.NewStyle().PaddingLeft(2).Render(playerBox) + "\n"
	}

	var queueBuilder strings.Builder
	queueBuilder.WriteString("\n")
	queueBuilder.WriteString("  " + sAccent.Render("próximas") + "\n\n")
	maxVisible := 20
	if m.height > 0 {
		maxVisible = min(maxVisible, max(1, m.height-8))
	}
	for index, track := range m.queue {
		if index == maxVisible {
			queueBuilder.WriteString("  " + sDim.Render(fmt.Sprintf("+ %d mais…", len(m.queue)-maxVisible)) + "\n")
			break
		}
		number := sNum.Render(fmt.Sprintf("%2d.", index+1))
		title := truncate(track.Title, queuePanelWidth-8)
		queueBuilder.WriteString(fmt.Sprintf("  %s %s\n", number, sDim.Render(title)))
	}

	queueBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Width(queuePanelWidth).
		MaxWidth(queuePanelWidth).
		Height(max(1, lipgloss.Height(playerBox)-2)).
		Render(queueBuilder.String())

	combined := lipgloss.JoinHorizontal(lipgloss.Top, playerBox, "  ", queueBox)
	return "\n" + lipgloss.NewStyle().PaddingLeft(2).Render(combined) + "\n"
}

func (m Model) downloadStatusFor(trackURL string) string {
	id, exists := m.downloadByURL[trackURL]
	if !exists {
		return ""
	}
	return m.downloads[id].Status
}
