package main

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:extension
var extensionFiles embed.FS

func extractExtension(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("app data folder is empty")
	}
	target := filepath.Join(dataDir, "browser-extension")
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	err := fs.WalkDir(extensionFiles, "extension", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel("extension", path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := extensionFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	return target, nil
}
