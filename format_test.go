package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestParseSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		max     int
		want    []int
		wantErr bool
	}{
		{name: "single", input: "2", max: 3, want: []int{1}},
		{name: "range and list", input: "1-3,5", max: 5, want: []int{0, 1, 2, 4}},
		{name: "deduplicates", input: "1,1,1-2", max: 3, want: []int{0, 1}},
		{name: "descending", input: "3-1", max: 3, wantErr: true},
		{name: "out of range", input: "1-4", max: 3, wantErr: true},
		{name: "malformed part", input: "1,lixo", max: 3, wantErr: true},
		{name: "empty part", input: "1,,2", max: 3, wantErr: true},
		{name: "too many", input: "1-101", max: 101, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSelection(test.input, test.max)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseSelection(%q) error = %v, wantErr=%v", test.input, err, test.wantErr)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseSelection(%q) = %#v, want %#v", test.input, got, test.want)
			}
		})
	}
}

func TestParseSelectionPathologicalRangeReturnsImmediately(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, err := parseSelection("1-"+strconvMaxInt(), 10)
	if err == nil {
		t.Fatal("expected an out-of-range error")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("pathological range took %s", elapsed)
	}
}

func strconvMaxInt() string {
	if strconvIntSize := 32 << (^uint(0) >> 63); strconvIntSize == 64 {
		return "9223372036854775807"
	}
	return "2147483647"
}

func TestSafeTextRemovesTerminalControls(t *testing.T) {
	t.Parallel()
	input := "faixa\x1b[31m vermelha\x1b[0m \x1b]52;c;Y29kZXg=\a fim\x00\u061c\u200f\u202e\u2067"
	got := safeText(input)
	for _, forbidden := range []string{"\x1b", "\a", "\x00", "]52", "\u061c", "\u200f", "\u202e", "\u2067"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("safeText preserved %q in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "faixa") || !strings.Contains(got, "fim") {
		t.Fatalf("safeText lost ordinary text: %q", got)
	}
}

func TestTruncateUsesTerminalCellWidth(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"abcdef", "界界界", "👩‍💻 coding"} {
		got := truncate(input, 4)
		if width := ansi.StringWidth(got); width > 4 {
			t.Fatalf("truncate(%q) width=%d: %q", input, width, got)
		}
	}
}

func TestHintsWrapWithinWidth(t *testing.T) {
	t.Parallel()
	const width = 20
	rendered := hints(width, kv("Enter", "buscar"), kv("↑↓", "histórico"), kv("q", "sair"))
	for _, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("hint line width=%d > %d: %q", got, width, line)
		}
	}
}

func TestFmtDuration(t *testing.T) {
	t.Parallel()
	if got := fmtDuration(249.9); got != "4:09" {
		t.Fatalf("got %q", got)
	}
	if got := fmtDuration(math.SmallestNonzeroFloat64); got != "0:00" {
		t.Fatalf("got %q", got)
	}
	if got := fmtDuration(0); got != "—" {
		t.Fatalf("got %q", got)
	}
}

func FuzzParseSelectionNeverEscapesBounds(f *testing.F) {
	for _, seed := range []string{"1", "1-3,5", "0", "3-1", "1,,2", "1-9223372036854775807"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 128 {
			t.Skip()
		}
		indices, err := parseSelection(input, 30)
		if err != nil {
			return
		}
		if len(indices) > maxSelection {
			t.Fatalf("returned %d indices", len(indices))
		}
		seen := make(map[int]bool, len(indices))
		for _, index := range indices {
			if index < 0 || index >= 30 {
				t.Fatalf("out-of-bounds index %d from %q", index, input)
			}
			if seen[index] {
				t.Fatalf("duplicate index %d from %q", index, input)
			}
			seen[index] = true
		}
	})
}

func FuzzSafeTextHasNoTerminalControls(f *testing.F) {
	for _, seed := range []string{"plain", "\x1b[31mred", "\x1b]52;c;Y29kZXg=\a", "\x00\x7f"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		output := safeText(input)
		for _, r := range output {
			if r == '\x1b' || r == '\a' || r == '\x00' {
				t.Fatalf("unsafe rune %U survived in %q", r, output)
			}
		}
	})
}
