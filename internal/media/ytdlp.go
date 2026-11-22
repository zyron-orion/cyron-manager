package media

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cyronmanager/internal/proxy"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

type Downloader struct {
	tools   *tools.Manager
	dataDir string
}

type Info struct {
	Title     string  `json:"title"`
	ID        string  `json:"id"`
	Ext       string  `json:"ext"`
	Duration  float64 `json:"duration"`
	Webpage   string  `json:"webpage_url"`
	Extractor string  `json:"extractor"`
}

type Options struct {
	DownloadDir string
	Quality     string
	Container   string
	Connections int
	Subtitles   bool
	Playlist    bool
	Username    string
	Password    string
	Cookies     string
	Referrer    string
	UserAgent   string
	Proxy       string
}

var progressRe = regexp.MustCompile(`\[download\]\s+([0-9.]+)%\s+of\s+~?\s*([0-9.]+)([KMGT]i?B)(?:\s+at\s+([0-9.]+)([KMGT]i?B)/s)?(?:\s+ETA\s+([0-9:]+))?`)

func New(tm *tools.Manager, dataDir string) *Downloader {
	return &Downloader{tools: tm, dataDir: dataDir}
}

func (d *Downloader) Available() bool {
	return d.tools.Find("yt-dlp") != ""
}

func (d *Downloader) Probe(ctx context.Context, url, proxyURL string) (Info, error) {
	bin := d.tools.Find("yt-dlp")
	if bin == "" {
		return Info{}, errors.New("yt-dlp.exe not found")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	probeArgs := []string{
		"--dump-single-json",
		"--no-warnings",
		"--no-playlist",
		"--skip-download",
		"--no-colors",
	}
	proxyConfig, cleanupProxyConfig, err := writeProxyConfig(d.dataDir, "probe-"+sanitizeID(url), proxyURL)
	if err != nil {
		return Info{}, err
	}
	defer cleanupProxyConfig()
	probeArgs = appendProxyConfig(probeArgs, proxyConfig)
	probeArgs = append(probeArgs, url)
	cmd := exec.CommandContext(probeCtx, bin, probeArgs...)
	cmd.Env = proxy.CleanEnv(os.Environ())
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(out, &info); err != nil {
		return Info{}, err
	}
	if info.Title == "" {
		info.Title = "Media"
	}
	return info, nil
}

func (d *Downloader) Download(ctx context.Context, task types.Task, opts Options, onProgress func(downloaded, total, speed int64, eta int64, progress float64)) (string, int64, error) {
	bin := d.tools.Find("yt-dlp")
	if bin == "" {
		return "", 0, errors.New("yt-dlp.exe not found")
	}
	if err := os.MkdirAll(opts.DownloadDir, 0o755); err != nil {
		return "", 0, err
	}
	printDir := filepath.Join(d.dataDir, "paths")
	if err := os.MkdirAll(printDir, 0o755); err != nil {
		return "", 0, err
	}
	pathFile := filepath.Join(printDir, task.ID+".txt")
	_ = os.Remove(pathFile)

	ffmpegDir := d.tools.FFmpegDir()
	if opts.Quality == "audio" && ffmpegDir == "" {
		return "", 0, errors.New("ffmpeg is required for MP3 downloads")
	}
	container := opts.Container
	if container == "" {
		container = "mp4"
	}
	if opts.Quality == "audio" {
		container = "mp3"
	}
	connections := opts.Connections
	if connections < 1 {
		connections = 8
	}
	if connections > 4 {
		connections = 4
	}
	format := formatFor(opts.Quality, container, ffmpegDir != "")
	proxyConfig, cleanupProxyConfig, err := writeProxyConfig(d.dataDir, task.ID, opts.Proxy)
	if err != nil {
		return "", 0, err
	}
	defer cleanupProxyConfig()
	args := []string{
		"--ignore-config",
		"--newline",
		"--no-colors",
		"--continue",
		"--no-overwrites",
		"--print-to-file", "after_move:filepath", pathFile,
		"--progress",
		"-f", format,
		"--concurrent-fragments", strconv.Itoa(connections),
		"--http-chunk-size", "10M",
		"--throttled-rate", "100K",
		"--fragment-retries", "20",
		"--retries", "20",
		"--retry-sleep", "2",
		"--socket-timeout", "20",
		"-o", filepath.Join(opts.DownloadDir, "%(title).180B [%(id)s].%(ext)s"),
	}
	if opts.Playlist {
		args = append(args, "--yes-playlist")
	} else {
		args = append(args, "--no-playlist")
	}
	if opts.Subtitles {
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-langs", "fa,en")
	}
	if ffmpegDir != "" {
		args = append(args, "--ffmpeg-location", ffmpegDir)
		if opts.Quality == "audio" {
			args = append(args, "-x", "--audio-format", "mp3", "--audio-quality", "0")
		} else {
			args = append(args, "--merge-output-format", container)
		}
		if opts.Subtitles {
			args = append(args, "--embed-subs")
		}
	}
	args = appendProxyConfig(args, proxyConfig)
	if opts.Username != "" {
		args = append(args, "--username", opts.Username)
	}
	if opts.Password != "" {
		args = append(args, "--password", opts.Password)
	}
	if cookies := strings.TrimSpace(opts.Cookies); cookies != "" {
		args = append(args, "--add-headers", "Cookie: "+cookies)
	}
	if referrer := strings.TrimSpace(opts.Referrer); referrer != "" {
		args = append(args, "--referer", referrer)
	}
	if userAgent := strings.TrimSpace(opts.UserAgent); userAgent != "" {
		args = append(args, "--user-agent", userAgent)
	}
	args = append(args, task.URL)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = proxy.CleanEnv(os.Environ())
	hideConsole(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", 0, err
	}
	if err := cmd.Start(); err != nil {
		return "", 0, err
	}

	var outputMu sync.Mutex
	var progressMu sync.Mutex
	var lastLines []string
	lastEmit := time.Now().Add(-time.Second)
	totalFromLine := int64(0)
	var lastActivity atomic.Int64
	var progressDone atomic.Bool
	var stallKilled atomic.Bool
	lastActivity.Store(time.Now().UnixNano())
	stopWatchdog := make(chan struct{})
	var watchdogOnce sync.Once
	stopWatch := func() { watchdogOnce.Do(func() { close(stopWatchdog) }) }
	defer stopWatch()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatchdog:
				return
			case <-ticker.C:
				if progressDone.Load() || stallKilled.Load() {
					continue
				}
				last := lastActivity.Load()
				if last == 0 || time.Since(time.Unix(0, last)) < 60*time.Second {
					continue
				}
				stallKilled.Store(true)
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			}
		}
	}()

	handleLine := func(line string) {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			return
		}
		outputMu.Lock()
		lastLines = append(lastLines, line)
		if len(lastLines) > 8 {
			lastLines = lastLines[len(lastLines)-8:]
		}
		outputMu.Unlock()
		m := progressRe.FindStringSubmatch(line)
		if m == nil {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		pct, _ := strconv.ParseFloat(m[1], 64)
		value, _ := strconv.ParseFloat(m[2], 64)
		total := int64(value * unitMultiplier(m[3]))
		if total > 0 {
			totalFromLine = total
		}
		downloaded := int64(float64(totalFromLine) * pct / 100)
		speed := int64(0)
		if m[4] != "" {
			s, _ := strconv.ParseFloat(m[4], 64)
			speed = int64(s * unitMultiplier(m[5]))
		}
		eta := parseETA(m[6])
		if pct >= 99 {
			progressDone.Store(true)
		} else {
			progressDone.Store(false)
			lastActivity.Store(time.Now().UnixNano())
		}
		if time.Since(lastEmit) > 500*time.Millisecond {
			lastEmit = time.Now()
			onProgress(downloaded, totalFromLine, speed, eta, pct)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			handleLine(scanner.Text())
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			handleLine(scanner.Text())
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()
	stopWatch()
	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}
	if waitErr != nil {
		outputMu.Lock()
		var errLine string
		for _, line := range lastLines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "error:") || strings.Contains(lower, "unable to") {
				errLine = line
			}
		}
		tail := strings.Join(lastLines, " | ")
		outputMu.Unlock()
		if stallKilled.Load() {
			return "", 0, fmt.Errorf("download stalled: no data for 60 seconds")
		}
		if errLine != "" {
			return "", 0, fmt.Errorf("%s", errLine)
		}
		if tail == "" {
			tail = waitErr.Error()
		}
		return "", 0, fmt.Errorf("%s", tail)
	}

	var paths []string
	var totalSize int64
	if b, err := os.ReadFile(pathFile); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			outPath := strings.TrimSpace(line)
			if outPath == "" || strings.HasSuffix(strings.ToLower(outPath), ".part") {
				continue
			}
			info, statErr := os.Stat(outPath)
			if statErr != nil || info.IsDir() || info.Size() <= 0 {
				continue
			}
			paths = append(paths, outPath)
			totalSize += info.Size()
		}
	}
	if len(paths) == 0 || totalSize <= 0 {
		return "", 0, errors.New("yt-dlp finished without reporting a valid output file")
	}
	onProgress(totalSize, totalSize, 0, 0, 100)
	if len(paths) > 1 {
		return opts.DownloadDir, totalSize, nil
	}
	return paths[0], totalSize, nil
}

func appendProxyConfig(args []string, configPath string) []string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return args
	}
	return append(args, "--config-locations", configPath)
}

const proxyConfigName = "proxy.conf"

func writeProxyConfig(dir, id, proxyURL string) (string, func(), error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return "", func() {}, nil
	}
	configDir := filepath.Join(dir, "proxycfg")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(configDir, sanitizeID(id)+"-"+proxyConfigName)
	content := "--proxy " + proxyURL + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

func sanitizeID(id string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, id)
	if safe == "" {
		return "task"
	}
	if len(safe) > 48 {
		safe = safe[:48]
	}
	return safe
}

func formatFor(quality, container string, hasFFmpeg bool) string {
	height := ""
	switch quality {
	case "360", "480", "720", "1080", "1440", "2160":
		height = quality
	}
	if !hasFFmpeg {
		if height == "" {
			return "b"
		}
		return fmt.Sprintf("b[height<=%s]/b", height)
	}
	if container == "mkv" {
		if height == "" {
			return "bv*+ba/b"
		}
		return fmt.Sprintf("bv*[height<=%s]+ba/b[height<=%s]", height, height)
	}
	if quality == "audio" {
		return "ba[acodec^=mp4a]/ba/b"
	}
	if height == "" {
		return "bv*[vcodec^=avc1]+ba[acodec^=mp4a]/b[vcodec^=avc1][acodec^=mp4a]/b[vcodec^=avc1]"
	}
	return fmt.Sprintf("bv*[vcodec^=avc1][height<=%s]+ba[acodec^=mp4a]/b[vcodec^=avc1][height<=%s][acodec^=mp4a]/b[vcodec^=avc1][height<=%s]", height, height, height)
}

func unitMultiplier(unit string) float64 {
	u := strings.ToUpper(unit)
	switch {
	case strings.HasPrefix(u, "K"):
		return 1024
	case strings.HasPrefix(u, "M"):
		return 1024 * 1024
	case strings.HasPrefix(u, "G"):
		return 1024 * 1024 * 1024
	case strings.HasPrefix(u, "T"):
		return 1024 * 1024 * 1024 * 1024
	}
	return 1
}

func parseETA(value string) int64 {
	if value == "" {
		return 0
	}
	parts := strings.Split(value, ":")
	var seconds int64
	for _, p := range parts {
		n, _ := strconv.ParseInt(p, 10, 64)
		seconds = seconds*60 + n
	}
	return seconds
}

func CopyLimited(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}
