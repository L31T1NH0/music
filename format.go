package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) resultPageSize() int {
	if m.height <= 0 {
		return pageSize
	}
	return min(pageSize, max(1, m.height-10))
}

func (m Model) localPageSize() int {
	if m.height <= 0 {
		return pageSize
	}
	return min(pageSize, max(1, m.height-13))
}

func (m Model) resultRange() (int, int) {
	size := m.resultPageSize()
	start := m.resultPage * size
	if start >= len(m.tracks) {
		start = max(0, ((len(m.tracks)-1)/size)*size)
	}
	end := min(len(m.tracks), start+size)
	return start, end
}

// listWidths returns terminal-cell widths for the title and channel columns.
// A zero channel width means the channel should be hidden. Duration is hidden
// only on very narrow terminals.
func (m Model) listWidths() (titleWidth, channelWidth int, showDuration bool) {
	width := m.width
	if width <= 0 {
		width = 80
	}
	if width < 28 {
		return max(4, width-7), 0, false
	}
	if width < 50 {
		return max(8, width-15), 0, true
	}

	channelWidth = min(20, max(10, (width-17)/3))
	titleWidth = max(12, width-17-channelWidth)
	return titleWidth, channelWidth, true
}

func displayStart(start, end int) int {
	if end == 0 {
		return 0
	}
	return start + 1
}

func parseSelection(choice string, maxIdx int) ([]int, error) {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return nil, fmt.Errorf("informe uma seleção")
	}
	if maxIdx < 1 {
		return nil, fmt.Errorf("não há itens para selecionar")
	}

	parts := strings.Split(choice, ",")
	out := make([]int, 0, min(len(parts), maxSelection))
	seen := make(map[int]struct{}, min(maxIdx, maxSelection))
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return nil, fmt.Errorf("seleção vazia entre vírgulas")
		}

		if strings.Contains(part, "-") {
			if strings.Count(part, "-") != 1 {
				return nil, fmt.Errorf("intervalo inválido: %q", part)
			}
			bounds := strings.SplitN(part, "-", 2)
			first, firstErr := strconv.Atoi(strings.TrimSpace(bounds[0]))
			last, lastErr := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if firstErr != nil || lastErr != nil {
				return nil, fmt.Errorf("intervalo inválido: %q", part)
			}
			if first < 1 || last < 1 || first > maxIdx || last > maxIdx {
				return nil, fmt.Errorf("intervalo fora de 1–%d: %q", maxIdx, part)
			}
			if first > last {
				return nil, fmt.Errorf("intervalo decrescente: %q", part)
			}
			if last-first+1 > maxSelection || len(out)+last-first+1 > maxSelection {
				return nil, fmt.Errorf("selecione no máximo %d itens por vez", maxSelection)
			}
			for item := first; item <= last; item++ {
				index := item - 1
				if _, exists := seen[index]; exists {
					continue
				}
				seen[index] = struct{}{}
				out = append(out, index)
			}
			continue
		}

		item, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("item inválido: %q", part)
		}
		if item < 1 || item > maxIdx {
			return nil, fmt.Errorf("item fora de 1–%d: %d", maxIdx, item)
		}
		index := item - 1
		if _, exists := seen[index]; exists {
			continue
		}
		if len(out) == maxSelection {
			return nil, fmt.Errorf("selecione no máximo %d itens por vez", maxSelection)
		}
		seen[index] = struct{}{}
		out = append(out, index)
	}
	return out, nil
}

func looksLikeSelection(input string) bool {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return false
	}
	if strings.HasPrefix(input, "d") {
		input = strings.TrimSpace(strings.TrimPrefix(input, "d"))
	}
	for _, r := range input {
		if !unicode.IsDigit(r) && r != ',' && r != '-' && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func fmtDuration(seconds float64) string {
	if seconds <= 0 {
		return "—"
	}
	minutes := int(seconds) / 60
	remaining := int(seconds) % 60
	return fmt.Sprintf("%d:%02d", minutes, remaining)
}

func safeText(text string) string {
	text = strings.ToValidUTF8(ansi.Strip(text), "�")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		case r == 0x061c || r == 0x200e || r == 0x200f:
			return -1
		case r >= 0x202a && r <= 0x202e:
			return -1
		case r >= 0x2066 && r <= 0x2069:
			return -1
		default:
			return r
		}
	}, text)
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(safeText(text)), " ")
}

func truncate(text string, width int) string {
	text = oneLine(text)
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(text, width, "…")
}

func fixedCell(text string, width int, style lipgloss.Style) string {
	width = max(1, width)
	return style.Inline(true).Width(width).MaxWidth(width).Render(truncate(text, width))
}

func inlineText(text string, width int, style lipgloss.Style) string {
	width = max(1, width)
	return style.Inline(true).MaxWidth(width).Render(truncate(text, width))
}

func sanitizeTrack(track Track) Track {
	track.Title = oneLine(track.Title)
	track.Channel = oneLine(track.Channel)
	track.Duration = oneLine(track.Duration)
	track.URL = strings.TrimSpace(strings.ToValidUTF8(track.URL, ""))
	return track
}

type kvPair struct{ key, val string }

func kv(key, value string) kvPair { return kvPair{key, value} }

func hints(width int, pairs ...kvPair) string {
	if width <= 0 {
		width = 76
	}
	separator := sDim.Render("  ·  ")
	var lines []string
	current := ""
	currentWidth := 0
	for _, pair := range pairs {
		key := oneLine(pair.key)
		value := oneLine(pair.val)
		token := sKey.Render(key) + " " + sDim.Render(value)
		tokenWidth := ansi.StringWidth(key) + 1 + ansi.StringWidth(value)
		extra := tokenWidth
		if current != "" {
			extra += 5
		}
		if current != "" && currentWidth+extra > width {
			lines = append(lines, current)
			current = token
			currentWidth = tokenWidth
			continue
		}
		if current != "" {
			current += separator
			currentWidth += 5
		}
		current += token
		currentWidth += tokenWidth
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n  ")
}

func fmtQueueStatus(current *Track, queue []Track) string {
	if current == nil && len(queue) == 0 {
		return "fila vazia"
	}
	parts := make([]string, 0, min(len(queue)+1, 7))
	if current != nil {
		parts = append(parts, "▶ "+truncate(current.Title, 30))
	}
	const visible = 5
	for index, track := range queue {
		if index == visible {
			parts = append(parts, fmt.Sprintf("+%d", len(queue)-visible))
			break
		}
		parts = append(parts, fmt.Sprintf("%d. %s", index+1, truncate(track.Title, 30)))
	}
	return strings.Join(parts, "  ·  ")
}
