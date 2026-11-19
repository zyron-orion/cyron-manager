package tools

import (
	"os"
	"os/exec"
	"path/filepath"

	"cyronmanager/internal/types"
)

type Manager struct {
	dataDir string
	exeDir  string
	cwd     string
}

func New(dataDir string) *Manager {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	return &Manager{
		dataDir: dataDir,
		exeDir:  filepath.Dir(exe),
		cwd:     cwd,
	}
}

func (m *Manager) Find(name string) string {
	fileName := name + ".exe"
	dirs := []string{
		filepath.Join(m.exeDir, "bin"),
		filepath.Join(m.exeDir, "tools"),
		m.exeDir,
		filepath.Join(m.cwd, "bin"),
		filepath.Join(m.cwd, "tools"),
		filepath.Join(m.dataDir, "tools"),
	}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, fileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath(fileName); err == nil {
		return found
	}
	return ""
}

func (m *Manager) Status() types.ToolsStatus {
	aria := m.Find("aria2c")
	ytdlp := m.Find("yt-dlp")
	ffmpeg := m.Find("ffmpeg")
	return types.ToolsStatus{
		Aria2:        aria,
		Ytdlp:        ytdlp,
		FFmpeg:       ffmpeg,
		Ready:        aria != "" && ytdlp != "" && ffmpeg != "",
		MediaReady:   ytdlp != "" && ffmpeg != "",
		TorrentReady: aria != "",
		DataDir:      m.dataDir,
	}
}

func (m *Manager) FFmpegDir() string {
	ffmpeg := m.Find("ffmpeg")
	if ffmpeg == "" {
		return ""
	}
	return filepath.Dir(ffmpeg)
}

func (m *Manager) ToolsDir() string {
	for _, name := range []string{"aria2c", "yt-dlp", "ffmpeg"} {
		if found := m.Find(name); found != "" {
			return filepath.Dir(found)
		}
	}
	return filepath.Join(m.exeDir, "bin")
}
