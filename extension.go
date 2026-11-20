package main

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const extensionVersion = "1.1.0"

//go:embed browser-extension/manifest.json browser-extension/*.js browser-extension/*.css browser-extension/*.html browser-extension/icons browser-extension/_locales
var extensionFiles embed.FS

func extractExtension(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("app data folder is empty")
	}
	target := filepath.Join(dataDir, "browser-extension")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	err := fs.WalkDir(extensionFiles, "browser-extension", func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel("browser-extension", source)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := extensionFiles.ReadFile(source)
		if err != nil {
			return err
		}
		if current, readErr := os.ReadFile(destination); readErr == nil && bytes.Equal(current, data) {
			return nil
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	return target, nil
}
