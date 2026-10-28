package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func loadConfig() (Config, error) {
	var cfg Config
	err := readJSONFile(configFile, &cfg, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("arquivo inválido: %w", err)
	}
	cfg.LocalDir = strings.TrimSpace(cfg.LocalDir)
	if cfg.Volume != nil && (*cfg.Volume < 0 || *cfg.Volume > 150) {
		return Config{}, fmt.Errorf("volume fora do intervalo 0–150")
	}
	if cfg.DownloadFormat != "" && cfg.DownloadFormat != "mp3" && cfg.DownloadFormat != "original" {
		return Config{}, fmt.Errorf("formato de download deve ser mp3 ou original")
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	cfg.LocalDir = strings.TrimSpace(cfg.LocalDir)
	return withFileLock(configFile, func() error {
		return writeJSONAtomic(configFile, cfg, 0o600)
	})
}
