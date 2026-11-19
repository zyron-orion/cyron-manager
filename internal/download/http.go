package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cyronmanager/internal/types"
)

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const httpIdleTimeout = 60 * time.Second

type httpPartMeta struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Total        int64  `json:"total"`
}

type httpReadChunk struct {
	data []byte
	err  error
}

func (m *Manager) startHTTPTask(task types.Task) {
	if !m.mediaGate.acquire() {
		return
	}
	defer m.mediaGate.release()
	if m.isDeleted(task.ID) {
		return
	}
	current, err := m.store.GetTask(task.ID)
	if err != nil || current.Status != types.StatusQueued {
		return
	}

	ctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return
	}
	m.running[current.ID] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.running, current.ID)
		m.mu.Unlock()
		cancel()
	}()

	now := time.Now().UnixMilli()
	ok, err := m.store.MarkActive(current.ID, "", now)
	if err != nil {
		m.fail(current, err.Error())
		return
	}
	if !ok {
		return
	}
	current.Status = types.StatusActive
	current.Error = ""
	current.Speed = 0
	current.ETA = 0
	current.UpdatedAt = now
	m.emitTask(current)
	m.emitStats()

	settings := m.Settings()
	if err := os.MkdirAll(current.Dir, 0o755); err != nil {
		m.fail(current, err.Error())
		return
	}
	finalPath := uniqueHTTPPath(current.Dir, httpFilename(current))
	partPath := finalPath + ".part"
	metaPath := partPath + ".meta"
	if _, statErr := os.Stat(partPath); errors.Is(statErr, os.ErrNotExist) {
		_ = os.Remove(metaPath)
	}
	multiHandled, multiErr := m.tryHTTPMulti(ctx, current, settings, partPath, finalPath)
	if multiHandled {
		if multiErr == nil || errors.Is(multiErr, errHTTPMultiCanceled) {
			return
		}
		if !errors.Is(multiErr, errHTTPRangeUnsupported) {
			m.fail(current, multiErr.Error())
			return
		}
	}
	defer m.cleanupHTTPMeta(current.ID, metaPath)

	offset := int64(0)
	if info, statErr := os.Stat(partPath); statErr == nil && info.Size() > 0 {
		offset = info.Size()
	}
	meta, metaOK := readHTTPPartMeta(metaPath)
	if offset > 0 && (!metaOK || httpMetaValidator(meta) == "" || (meta.Total > 0 && offset > meta.Total)) {
		if removeErr := os.Remove(partPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			m.fail(current, removeErr.Error())
			return
		}
		_ = os.Remove(metaPath)
		offset = 0
		meta = httpPartMeta{}
		metaOK = false
	}

	requestCtx, requestCancel := context.WithCancel(ctx)
	defer requestCancel()
	client := m.newHTTPClient(settings)
	restarted := false
	var resp *http.Response
	for {
		rangeHeader := ""
		ifRange := ""
		if offset > 0 {
			rangeHeader = fmt.Sprintf("bytes=%d-", offset)
			ifRange = httpMetaValidator(meta)
		}
		req, err := m.newHTTPRequest(requestCtx, current, rangeHeader, ifRange)
		if err != nil {
			m.fail(current, err.Error())
			return
		}
		resp, err = client.Do(req)
		if err != nil {
			if ctx.Err() != nil || m.isDeleted(current.ID) {
				return
			}
			m.fail(current, err.Error())
			return
		}
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
			_ = resp.Body.Close()
			if metaOK && meta.Total > 0 && offset == meta.Total {
				_ = os.Remove(metaPath)
				if renameErr := os.Rename(partPath, finalPath); renameErr != nil {
					m.fail(current, renameErr.Error())
					return
				}
				m.completeHTTPTask(current, finalPath, offset, offset)
				return
			}
			_ = os.Remove(partPath)
			_ = os.Remove(metaPath)
			if restarted {
				m.fail(current, fmt.Sprintf("HTTP 416: partial download cannot be resumed (%d of %d bytes)", offset, meta.Total))
				return
			}
			restarted = true
			offset = 0
			meta = httpPartMeta{}
			metaOK = false
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			message := strings.TrimSpace(string(detail))
			if message == "" {
				message = resp.Status
			}
			m.fail(current, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, message))
			return
		}
		break
	}
	defer resp.Body.Close()

	truncate := offset == 0 || resp.StatusCode != http.StatusPartialContent
	if truncate {
		offset = 0
	}
	total := int64(-1)
	if resp.ContentLength >= 0 {
		total = offset + resp.ContentLength
	} else if !truncate && metaOK && meta.Total > 0 {
		total = meta.Total
	}
	nextMeta := httpPartMeta{Total: total}
	if !truncate {
		nextMeta.ETag = meta.ETag
		nextMeta.LastModified = meta.LastModified
	}
	if etag := strings.TrimSpace(resp.Header.Get("ETag")); etag != "" {
		nextMeta.ETag = etag
	}
	if modified := strings.TrimSpace(resp.Header.Get("Last-Modified")); modified != "" {
		nextMeta.LastModified = modified
	}
	if truncate {
		if removeErr := os.Remove(partPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			m.fail(current, removeErr.Error())
			return
		}
	}
	if err := writeHTTPPartMeta(metaPath, nextMeta); err != nil {
		m.fail(current, err.Error())
		return
	}

	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	file, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		m.fail(current, err.Error())
		return
	}

	written := offset
	started := time.Now()
	lastEmit := time.Now()
	lastSave := time.Now()
	readCh := make(chan httpReadChunk, 1)
	go func() {
		buf := make([]byte, 256*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case readCh <- httpReadChunk{data: chunk}:
				case <-requestCtx.Done():
					return
				}
			}
			if readErr != nil {
				select {
				case readCh <- httpReadChunk{err: readErr}:
				case <-requestCtx.Done():
				}
				return
			}
		}
	}()
	idle := time.NewTimer(httpIdleTimeout)
	defer idle.Stop()

readLoop:
	for {
		select {
		case <-ctx.Done():
			_ = file.Close()
			requestCancel()
			_ = resp.Body.Close()
			return
		case <-idle.C:
			_ = file.Close()
			requestCancel()
			_ = resp.Body.Close()
			if ctx.Err() != nil || m.isDeleted(current.ID) {
				return
			}
			m.fail(current, fmt.Sprintf("download stalled: no data received for %d seconds", int(httpIdleTimeout/time.Second)))
			return
		case chunk := <-readCh:
			if len(chunk.data) > 0 {
				if ctx.Err() != nil || m.isDeleted(current.ID) {
					_ = file.Close()
					requestCancel()
					_ = resp.Body.Close()
					return
				}
				if _, writeErr := file.Write(chunk.data); writeErr != nil {
					_ = file.Close()
					requestCancel()
					_ = resp.Body.Close()
					m.fail(current, writeErr.Error())
					return
				}
				written += int64(len(chunk.data))
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(httpIdleTimeout)
				if time.Since(lastEmit) >= 500*time.Millisecond {
					current.Downloaded = written
					current.Total = maxInt64(total, 0)
					current.Speed = httpSpeed(written-offset, started)
					current.Progress = httpProgress(written, total)
					current.ETA = httpETA(written, total, current.Speed)
					current.UpdatedAt = time.Now().UnixMilli()
					m.emitTask(current)
					if time.Since(lastSave) >= time.Second {
						lastSave = time.Now()
						_ = m.store.UpdateTask(&current)
					}
					lastEmit = time.Now()
				}
			}
			if errors.Is(chunk.err, io.EOF) {
				break readLoop
			}
			if chunk.err != nil {
				_ = file.Close()
				requestCancel()
				_ = resp.Body.Close()
				if ctx.Err() != nil || m.isDeleted(current.ID) {
					return
				}
				m.fail(current, chunk.err.Error())
				return
			}
		}
	}
	if err := file.Close(); err != nil {
		m.fail(current, err.Error())
		return
	}
	if ctx.Err() != nil || m.isDeleted(current.ID) {
		return
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		m.fail(current, err.Error())
		return
	}
	if total < 0 {
		total = written
	}
	_ = os.Remove(metaPath)
	m.completeHTTPTask(current, finalPath, written, total)
}

func (m *Manager) completeHTTPTask(task types.Task, filePath string, downloaded, total int64) {
	now := time.Now().UnixMilli()
	ok, err := m.store.CompleteTask(task.ID, filePath, downloaded, total, now)
	if err != nil || !ok {
		return
	}
	m.hashCompleted(task.ID, filePath)
	fresh, err := m.store.GetTask(task.ID)
	if err == nil {
		task = fresh
	} else {
		task.Status = types.StatusCompleted
		task.Progress = 100
		task.Downloaded = downloaded
		task.Total = total
		task.Speed = 0
		task.ETA = 0
		task.FilePath = filePath
		task.UpdatedAt = now
	}
	m.emitTask(task)
	m.emitStats()
	m.maybeShutdown()
}

func (m *Manager) newHTTPClient(settings types.Settings) *http.Client {
	return &http.Client{Transport: m.httpTransport(settings)}
}

func (m *Manager) newHTTPRequest(ctx context.Context, task types.Task, byteRange, ifRange string) (*http.Request, error) {
	settings := m.Settings()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, task.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "identity")
	if task.Username != "" {
		req.SetBasicAuth(task.Username, task.Password)
	}
	if cookies := strings.TrimSpace(task.Cookies); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	if ifRange != "" {
		req.Header.Set("If-Range", ifRange)
	}
	if token := strings.TrimSpace(settings.GitHubToken); token != "" && isGitHubHost(req.URL.Hostname()) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (m *Manager) httpTransport(settings types.Settings) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 20 * time.Second
	transport.ResponseHeaderTimeout = 45 * time.Second
	transport.MaxIdleConnsPerHost = 16
	if proxy := strings.TrimSpace(settings.Proxy); proxy != "" {
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		if parsed, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return transport
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".github.com") || strings.HasSuffix(host, ".githubusercontent.com")
}

func httpMetaValidator(meta httpPartMeta) string {
	etag := strings.TrimSpace(meta.ETag)
	if etag != "" && !strings.HasPrefix(strings.ToUpper(etag), "W/") {
		return etag
	}
	return strings.TrimSpace(meta.LastModified)
}

func readHTTPPartMeta(path string) (httpPartMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return httpPartMeta{}, false
	}
	var meta httpPartMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return httpPartMeta{}, false
	}
	return meta, true
}

func writeHTTPPartMeta(path string, meta httpPartMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (m *Manager) cleanupHTTPMeta(taskID, metaPath string) {
	if m.isDeleted(taskID) {
		_ = os.Remove(metaPath)
		return
	}
	task, err := m.store.GetTask(taskID)
	if err == nil && (task.Status == types.StatusPaused || task.Status == types.StatusActive) {
		return
	}
	_ = os.Remove(metaPath)
}

func httpFilename(task types.Task) string {
	name := strings.TrimSpace(task.Title)
	if name == "" {
		if parsed, err := url.Parse(task.URL); err == nil {
			name = filepath.Base(parsed.Path)
			if decoded, err := url.PathUnescape(name); err == nil {
				name = decoded
			}
		}
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" || name == "." || name == ".." {
		name = "download"
	}
	lowerURL := strings.ToLower(task.URL)
	if filepath.Ext(name) == "" && (strings.Contains(lowerURL, ".zip") || strings.Contains(lowerURL, "codeload.github.com")) {
		name += ".zip"
	}
	if len(name) > 140 {
		ext := filepath.Ext(name)
		name = strings.TrimRight(name[:140-len(ext)], ". ") + ext
	}
	return name
}

func uniqueHTTPPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if _, partErr := os.Stat(path + ".part"); errors.Is(partErr, os.ErrNotExist) {
				return path
			}
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext))
}

func httpSpeed(bytes int64, started time.Time) int64 {
	elapsed := time.Since(started).Seconds()
	if elapsed < 0.2 || bytes <= 0 {
		return 0
	}
	return int64(float64(bytes) / elapsed)
}

func httpProgress(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	progress := float64(done) / float64(total) * 100
	if progress > 100 {
		return 100
	}
	return progress
}

func httpETA(done, total, speed int64) int64 {
	if total <= done || speed <= 0 {
		return 0
	}
	return (total - done) / speed
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
