package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/proxy"
	"cyronmanager/internal/types"
)

const (
	httpMultiMinSize        = 1 << 20
	httpMultiMinSegment     = 256 << 10
	httpMultiMaxConnections = 16
	httpMultiStateSuffix    = ".json"
)

var (
	errHTTPMultiCanceled    = errors.New("http multi download canceled")
	errHTTPRangeUnsupported = errors.New("server does not support byte ranges")
	errDownloadStalled      = errors.New("download stalled")
)

type httpMultiSegment struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Done  int64 `json:"done"`
}

type httpMultiState struct {
	URL       string             `json:"url"`
	Total     int64              `json:"total"`
	Validator string             `json:"validator,omitempty"`
	Segments  []httpMultiSegment `json:"segments"`
}

type httpMultiSession struct {
	mu    sync.Mutex
	state httpMultiState
	file  *os.File
}

func (s *httpMultiSession) snapshot() httpMultiState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	st.Segments = append([]httpMultiSegment(nil), s.state.Segments...)
	return st
}

func (s *httpMultiSession) completed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var done int64
	for _, segment := range s.state.Segments {
		done += segment.Done
	}
	return done
}

func (s *httpMultiSession) segment(index int) httpMultiSegment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Segments[index]
}

func (s *httpMultiSession) done(index int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Segments[index].Done
}

func (s *httpMultiSession) add(index int, n int64) {
	s.mu.Lock()
	s.state.Segments[index].Done += n
	s.mu.Unlock()
}

func (s *httpMultiSession) total() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Total
}

func (s *httpMultiSession) validator() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Validator
}

func httpConnections(task types.Task) int {
	n := task.Connections
	if n < 1 {
		return 1
	}
	if n > httpMultiMaxConnections {
		return httpMultiMaxConnections
	}
	return n
}

func httpSegmentCount(connections int, total int64) int {
	if total < httpMultiMinSize {
		return 0
	}
	maxBySize := int(total / httpMultiMinSegment)
	if maxBySize < 2 {
		return 0
	}
	if connections > maxBySize {
		connections = maxBySize
	}
	if connections > httpMultiMaxConnections {
		connections = httpMultiMaxConnections
	}
	if connections < 2 {
		return 0
	}
	return connections
}

func (m *Manager) tryHTTPMulti(ctx context.Context, task types.Task, route *proxy.RoutePlan, partPath, finalPath string) (bool, error) {
	multiPath := partPath + ".multi"
	statePath := multiPath + httpMultiStateSuffix

	state, stateExists, stateOK := loadHTTPMultiState(statePath)
	if stateExists {
		if stateOK && validHTTPMultiState(state, task.URL, multiPath) {
			_ = os.Remove(partPath)
			return m.runHTTPMulti(ctx, task, route, state, multiPath, statePath, finalPath)
		}
		_ = os.Remove(multiPath)
		_ = os.Remove(statePath)
	} else if info, err := os.Stat(multiPath); err == nil && info.Size() > 0 {
		_ = os.Remove(multiPath)
	}

	if info, err := os.Stat(partPath); err == nil && info.Size() > 0 {
		return false, nil
	}

	connections := httpConnections(task)
	if connections < 2 {
		return false, nil
	}
	total, validator, ok, err := m.probeHTTPRange(ctx, task, route)
	if err != nil {
		if ctx.Err() != nil || m.isDeleted(task.ID) {
			return true, errHTTPMultiCanceled
		}
		return false, nil
	}
	if !ok {
		return false, nil
	}
	segments := httpSegmentCount(connections, total)
	if segments < 2 {
		return false, nil
	}

	_ = os.Remove(partPath)
	state = newHTTPMultiState(task.URL, total, validator, segments)
	return m.runHTTPMulti(ctx, task, route, state, multiPath, statePath, finalPath)
}

func (m *Manager) probeHTTPRange(ctx context.Context, task types.Task, route *proxy.RoutePlan) (int64, string, bool, error) {
	req, err := m.newHTTPRequest(ctx, task, "bytes=0-0", "")
	if err != nil {
		return 0, "", false, err
	}
	client := m.newHTTPClient(route)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, "", false, nil
	}
	start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || start != 0 || end < start || total <= end {
		return 0, "", false, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return total, httpRangeValidator(resp.Header), true, nil
}

func (m *Manager) runHTTPMulti(ctx context.Context, task types.Task, route *proxy.RoutePlan, state httpMultiState, multiPath, statePath, finalPath string) (bool, error) {
	file, err := os.OpenFile(multiPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return true, err
	}
	if err := file.Truncate(state.Total); err != nil {
		_ = file.Close()
		return true, err
	}
	session := &httpMultiSession{state: state, file: file}
	if err := saveHTTPMultiState(statePath, session.snapshot()); err != nil {
		_ = file.Close()
		return true, err
	}

	client := m.newHTTPClient(route)
	base := session.completed()
	started := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastSave := time.Now()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil || m.isDeleted(task.ID) {
					return
				}
				snap := session.snapshot()
				done := session.completed()
				speed := httpSpeed(done-base, started)
				task.Downloaded = done
				task.Total = snap.Total
				task.Speed = speed
				task.Progress = httpProgress(done, snap.Total)
				task.ETA = httpETA(done, snap.Total, speed)
				task.UpdatedAt = time.Now().UnixMilli()
				m.emitTask(task)
				if time.Since(lastSave) >= time.Second {
					lastSave = time.Now()
					_ = saveHTTPMultiState(statePath, snap)
					_ = m.store.UpdateProgress(task.ID, done, snap.Total, speed, task.ETA, task.Progress, task.UpdatedAt)
				}
			}
		}
	}()

	errCh := make(chan error, len(state.Segments))
	var wg sync.WaitGroup
	for index := range state.Segments {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if workerErr := m.downloadHTTPRange(runCtx, task, client, session, index); workerErr != nil {
				select {
				case errCh <- workerErr:
				default:
				}
				cancel()
			}
		}(index)
	}
	wg.Wait()
	stopMonitor()
	<-monitorDone

	_ = saveHTTPMultiState(statePath, session.snapshot())

	if ctx.Err() != nil || m.isDeleted(task.ID) {
		_ = file.Close()
		return true, errHTTPMultiCanceled
	}

	var firstErr error
	close(errCh)
	for workerErr := range errCh {
		if workerErr != nil {
			firstErr = workerErr
			break
		}
	}
	if firstErr != nil {
		_ = file.Close()
		if errors.Is(firstErr, errHTTPRangeUnsupported) {
			_ = os.Remove(statePath)
			_ = os.Remove(multiPath)
			return true, errHTTPRangeUnsupported
		}
		return true, firstErr
	}
	if session.completed() != state.Total {
		_ = file.Close()
		return true, fmt.Errorf("multi download incomplete: %d of %d bytes", session.completed(), state.Total)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return true, err
	}
	if err := file.Close(); err != nil {
		return true, err
	}
	if err := os.Rename(multiPath, finalPath); err != nil {
		return true, err
	}
	_ = os.Remove(statePath)
	m.completeHTTPTask(task, finalPath, state.Total, state.Total)
	return true, nil
}

func (m *Manager) downloadHTTPRange(ctx context.Context, task types.Task, client *http.Client, session *httpMultiSession, index int) error {
	const maxAttempts = 8
	attempts := 0
	buf := make([]byte, 256*1024)

retrySegment:
	for {
		if ctx.Err() != nil || m.isDeleted(task.ID) {
			return errHTTPMultiCanceled
		}
		segment := session.segment(index)
		length := segment.End - segment.Start + 1
		if segment.Done >= length {
			return nil
		}
		from := segment.Start + segment.Done
		req, err := m.newHTTPRequest(ctx, task, fmt.Sprintf("bytes=%d-%d", from, segment.End), session.validator())
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil || m.isDeleted(task.ID) {
				return errHTTPMultiCanceled
			}
			attempts++
			if attempts > maxAttempts {
				return fmt.Errorf("range request failed: %w", err)
			}
			if waitErr := waitHTTPRetry(ctx, attempts); waitErr != nil {
				return waitErr
			}
			continue
		}

		if resp.StatusCode == http.StatusPartialContent {
			start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
			if !ok || start != from || end < start || end > segment.End || total != session.total() {
				_ = resp.Body.Close()
				return errHTTPRangeUnsupported
			}
			offset := from
			readFailure := readWithIdle(ctx, resp.Body, httpIdleTimeout, buf, func(chunk []byte) error {
				if ctx.Err() != nil || m.isDeleted(task.ID) {
					return errHTTPMultiCanceled
				}
				if offset+int64(len(chunk)) > segment.End+1 {
					return fmt.Errorf("range response exceeded segment boundary")
				}
				written, writeErr := session.file.WriteAt(chunk, offset)
				if writeErr != nil {
					return writeErr
				}
				if written != len(chunk) {
					return io.ErrShortWrite
				}
				session.add(index, int64(len(chunk)))
				offset += int64(len(chunk))
				attempts = 0
				return nil
			})
			_ = resp.Body.Close()
			if readFailure != nil {
				if ctx.Err() != nil || m.isDeleted(task.ID) {
					return errHTTPMultiCanceled
				}
				if errors.Is(readFailure, errDownloadStalled) {
					readFailure = fmt.Errorf("download stalled: no data received for %d seconds", int(httpIdleTimeout/time.Second))
				}
				attempts++
				if attempts > maxAttempts {
					return fmt.Errorf("range read failed: %w", readFailure)
				}
				if waitErr := waitHTTPRetry(ctx, attempts); waitErr != nil {
					return waitErr
				}
				continue retrySegment
			}
			if session.done(index) >= length {
				return nil
			}
			attempts++
			if attempts > maxAttempts {
				return fmt.Errorf("range ended before the full segment was received")
			}
			if waitErr := waitHTTPRetry(ctx, attempts); waitErr != nil {
				return waitErr
			}
			continue
		}

		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return errHTTPRangeUnsupported
		}
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return fmt.Errorf("range %d-%d rejected: %s", from, segment.End, resp.Status)
		}
		message := strings.TrimSpace(string(detail))
		if message == "" {
			message = resp.Status
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			attempts++
			if attempts > maxAttempts {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
			}
			if waitErr := waitHTTPRetry(ctx, attempts); waitErr != nil {
				return waitErr
			}
			continue
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}
}

func waitHTTPRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * 500 * time.Millisecond
	if delay > 4*time.Second {
		delay = 4 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errHTTPMultiCanceled
	case <-timer.C:
		return nil
	}
}

func newHTTPMultiState(rawURL string, total int64, validator string, count int) httpMultiState {
	state := httpMultiState{
		URL:       rawURL,
		Total:     total,
		Validator: validator,
		Segments:  make([]httpMultiSegment, 0, count),
	}
	base := total / int64(count)
	remainder := total % int64(count)
	start := int64(0)
	for i := 0; i < count; i++ {
		length := base
		if int64(i) < remainder {
			length++
		}
		if length <= 0 {
			continue
		}
		end := start + length - 1
		state.Segments = append(state.Segments, httpMultiSegment{Start: start, End: end})
		start = end + 1
	}
	return state
}

func validHTTPMultiState(state httpMultiState, rawURL, multiPath string) bool {
	if state.URL != rawURL || state.Total <= 0 || len(state.Segments) < 2 || len(state.Segments) > 64 {
		return false
	}
	info, err := os.Stat(multiPath)
	if err != nil || info.Size() != state.Total {
		return false
	}
	next := int64(0)
	for _, segment := range state.Segments {
		length := segment.End - segment.Start + 1
		if segment.Start != next || segment.End < segment.Start || segment.Done < 0 || segment.Done > length {
			return false
		}
		next = segment.End + 1
	}
	return next == state.Total
}

func loadHTTPMultiState(path string) (httpMultiState, bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return httpMultiState{}, false, false
	}
	var state httpMultiState
	if err := json.Unmarshal(data, &state); err != nil {
		return httpMultiState{}, true, false
	}
	return state, true, true
}

func saveHTTPMultiState(path string, state httpMultiState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".multi-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func parseContentRange(value string) (int64, int64, int64, bool) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "bytes ") {
		return 0, 0, 0, false
	}
	value = strings.TrimSpace(value[len("bytes "):])
	slash := strings.IndexByte(value, '/')
	if slash <= 0 || slash == len(value)-1 {
		return 0, 0, 0, false
	}
	rangePart := strings.TrimSpace(value[:slash])
	totalPart := strings.TrimSpace(value[slash+1:])
	if totalPart == "*" {
		return 0, 0, 0, false
	}
	dash := strings.IndexByte(rangePart, '-')
	if dash <= 0 || dash == len(rangePart)-1 {
		return 0, 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(rangePart[:dash]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	end, err := strconv.ParseInt(strings.TrimSpace(rangePart[dash+1:]), 10, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	total, err := strconv.ParseInt(totalPart, 10, 64)
	if err != nil || total <= 0 || start < 0 || end < start || end >= total {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func httpRangeValidator(header http.Header) string {
	etag := strings.TrimSpace(header.Get("ETag"))
	if etag != "" && !strings.HasPrefix(strings.ToUpper(etag), "W/") {
		return etag
	}
	return strings.TrimSpace(header.Get("Last-Modified"))
}

func readWithIdle(ctx context.Context, reader io.Reader, timeout time.Duration, buf []byte, consume func([]byte) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := make(chan idleReadChunk, 1)
	go func() {
		readBuf := make([]byte, len(buf))
		for {
			n, readErr := reader.Read(readBuf)
			if n > 0 {
				chunk := append([]byte(nil), readBuf[:n]...)
				select {
				case chunks <- idleReadChunk{data: chunk}:
				case <-runCtx.Done():
					return
				}
			}
			if readErr != nil {
				select {
				case chunks <- idleReadChunk{err: readErr}:
				case <-runCtx.Done():
				}
				return
			}
		}
	}()
	idle := time.NewTimer(timeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return errDownloadStalled
		case chunk := <-chunks:
			if len(chunk.data) > 0 {
				if err := consume(chunk.data); err != nil {
					return err
				}
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(timeout)
			}
			if chunk.err != nil {
				if errors.Is(chunk.err, io.EOF) {
					return nil
				}
				return chunk.err
			}
		}
	}
}

type idleReadChunk struct {
	data []byte
	err  error
}
