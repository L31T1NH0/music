package main

import (
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
)

func testModel() Model {
	search := textinput.New()
	search.Focus()
	selection := textinput.New()
	model := Model{
		state:            stateSearch,
		src:              sourceYT,
		searchInput:      search,
		selInput:         selection,
		historyPos:       -1,
		volume:           defaultVol,
		prog:             progress.New(progress.WithoutPercentage()),
		spin:             spinner.New(),
		downloads:        make(map[uint64]downloadState),
		downloadByURL:    make(map[string]uint64),
		downloadFormat:   "mp3",
		localShowFolders: false,
	}
	return model
}

func testTracks(count int, src source) []Track {
	tracks := make([]Track, count)
	for index := range tracks {
		tracks[index] = Track{
			Title:    "faixa " + itoa(index+1),
			Channel:  "artista",
			Duration: "3:00",
			URL:      "https://example.invalid/" + itoa(index+1),
			Source:   src,
		}
	}
	return tracks
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[position:])
}
