package download

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/aria2"
	"cyronmanager/internal/disk"
	"cyronmanager/internal/media"
	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

type Manager struct {
	ctx      context.Context
	cancel   context.CancelFunc
	store    *store.Store
	tools    *tools.Manager
	media    *media.Downloader
	dataDir  string
	emit     func(string, any)
	settings types.Settings

	mu             sync.Mutex
	ariaMu         sync.Mutex
	stallMu        sync.Mutex
	stalls         map[string]*stallState
	running        map[string]context.CancelFunc
	deleted        map[string]bool
	pending        map[string]bool
	activeSlots    map[string]bool
	schedulePaused map[string]bool
	schedulerOff   bool
	aria           *aria2.Client
	closed         bool
	mediaGate      *limiter
	dispatchWake   chan struct{}
	lastSample     time.Time
	shutdownHook   func()
	wg             sync.WaitGroup
	taskWG         sync.WaitGroup
}

func New(ctx context.Context, st *store.Store, tm *tools.Manager, settings types.Settings, dataDir string, emit func(string, any)) *Manager {
	maxConcurrent := settings.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}
	runCtx, cancel := context.WithCancel(ctx)
	m := &Manager{
		ctx:            runCtx,
		cancel:         cancel,
		store:          st,
		tools:          tm,
		media:          media.New(tm, dataDir),
		dataDir:        dataDir,
		emit:           emit,
		settings:       settings,
		stalls:         make(map[string]*stallState),
		running:        make(map[string]context.CancelFunc),
		deleted:        make(map[string]bool),
		pending:        make(map[string]bool),
		activeSlots:    make(map[string]bool),
		schedulePaused: make(map[string]bool),
		mediaGate:      newLimiter(maxConcurrent),
		dispatchWake:   make(chan struct{}, 1),
	}
	m.wg.Add(1)
	go m.dispatchLoop()
	return m
}

func (m *Manager) SetShutdownHook(hook func()) {
	m.mu.Lock()
	m.shutdownHook = hook
	m.mu.Unlock()
}

func (m *Manager) Start() {
	m.restore()
	m.wg.Add(2)
	go m.loop()
	go m.schedulerLoop()
}

func (m *Manager) restore() {
	tasks, err := m.store.ListTasks(0)
	if err != nil {
		return
	}
	interrupted := make([]types.Task, 0)
	for _, t := range tasks {
		if t.Status == types.StatusActive || t.Status == types.StatusQueued {
			interrupted = append(interrupted, t)
		}
	}
	interrupted = m.dropDuplicateInterrupted(interrupted)
	_ = m.store.ResetRunningTasks()
	if !m.settings.AutoStart {
		return
	}
	connections := m.settings.Connections
	if connections < 1 {
		connections = 8
	}
	for _, t := range interrupted {
		if t.Connections < 1 {
			t.Connections = connections
		}
		if t.Engine == "ytdlp" && t.Container == "" {
			if t.Quality == "1440" || t.Quality == "2160" {
				t.Container = "mkv"
			} else {
				t.Container = normalizeContainer("", t.Quality)
			}
		}
		t.Status = types.StatusQueued
		t.Speed = 0
		t.ETA = 0
		t.UpdatedAt = time.Now().UnixMilli()
		_ = m.store.UpdateTask(&t)
		m.runTask(t.ID)
	}
}

func (m *Manager) Settings() types.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *Manager) UpdateSettings(settings types.Settings) error {
	m.mu.Lock()
	m.settings = settings
	aria := m.aria
	m.mu.Unlock()

	m.media.SetProxy(settings.Proxy)
	m.mediaGate.setLimit(settings.MaxConcurrent)
	m.wakeDispatcher()
	if aria != nil {
		if err := aria.SetProxy(settings.Proxy); err != nil {
			return err
		}
		if err := aria.SetConnections(settings.Connections); err != nil {
			return err
		}
		if err := aria.SetMaxConcurrent(settings.MaxConcurrent); err != nil {
			return err
		}
	}
	go m.applySchedule()
	return nil
}

func (m *Manager) Add(req types.AddRequest) (types.Task, error) {
	task, _, err := m.AddChecked(req)
	return task, err
}

func (m *Manager) AddChecked(req types.AddRequest) (types.Task, bool, error) {
	rawURL := strings.TrimSpace(req.URL)
	engine, kind, source, title, err := classify(rawURL, req.Engine)
	if err != nil {
		return types.Task{}, false, err
	}
	quality := normalizeQuality(req.Quality)
	container := ""
	if engine == "ytdlp" {
		container = normalizeContainer(req.Container, quality)
		if container == "mp3" {
			quality = "audio"
		}
		if container == "mp4" && (quality == "1440" || quality == "2160") {
			return types.Task{}, false, errors.New("برای کیفیت 4K یا 2K فرمت MKV را انتخاب کنید")
		}
	}
	now := time.Now().UnixMilli()
	if strings.TrimSpace(req.Title) != "" {
		title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Source) != "" {
		source = strings.TrimSpace(req.Source)
	}
	category := normalizeCategory(req.Category)
	if category == "" {
		category = detectCategory(engine, kind, quality, container, title)
	}
	if !req.AllowDuplicate {
		for _, existing := range m.interruptedDuplicates(rawURL, engine, kind, quality, container) {
			return existing, true, nil
		}
	}
	settings := m.Settings()
	connections := settings.Connections
	if connections < 1 {
		connections = 8
	}
	cookies := strings.TrimSpace(req.Cookies)
	browserCookies := ""
	if req.Browser {
		browserCookies = cookies
		cookies = ""
	}
	task := types.Task{
		ID:             randomID(),
		URL:            rawURL,
		Title:          title,
		Engine:         engine,
		Kind:           kind,
		Status:         types.StatusQueued,
		Quality:        quality,
		Category:       category,
		Container:      container,
		Subtitles:      req.Subtitles && engine == "ytdlp",
		Playlist:       req.Playlist && engine == "ytdlp",
		Connections:    connections,
		Username:       strings.TrimSpace(req.Username),
		Password:       req.Password,
		Cookies:        cookies,
		BrowserCookies: browserCookies,
		Referrer:       strings.TrimSpace(req.Referrer),
		UserAgent:      strings.TrimSpace(req.UserAgent),
		MimeType:       strings.TrimSpace(req.MimeType),
		SuggestedName:  strings.TrimSpace(req.SuggestedName),
		Source:         source,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	task.Dir = settings.DownloadDir
	if err := m.store.SaveTask(&task); err != nil {
		return types.Task{}, false, err
	}
	m.emitTask(task)
	m.runTask(task.ID)
	return task, false, nil
}

func (m *Manager) AddTorrentFile(filePath, title string) (types.Task, error) {
	abs, err := filepath.Abs(strings.TrimSpace(filePath))
	if err != nil {
		return types.Task{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return types.Task{}, fmt.Errorf("فایل تورنت پیدا نشد: %w", err)
	}
	if info.IsDir() {
		return types.Task{}, errors.New("مسیر انتخاب‌شده یک پوشه است")
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(abs), strings.ToLower(filepath.Ext(abs)))
	}
	settings := m.Settings()
	connections := settings.Connections
	if connections < 1 {
		connections = 8
	}
	now := time.Now().UnixMilli()
	task := types.Task{
		ID:          randomID(),
		URL:         abs,
		Title:       title,
		Engine:      "aria2",
		Kind:        "torrent",
		Status:      types.StatusQueued,
		Quality:     "best",
		Category:    detectCategory("aria2", "torrent", "best", "", title),
		Connections: connections,
		Source:      "Torrent",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	task.Dir = settings.DownloadDir
	if err := m.store.SaveTask(&task); err != nil {
		return types.Task{}, err
	}
	m.emitTask(task)
	m.runTask(task.ID)
	return task, nil
}

func (m *Manager) runTask(id string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.pending[id] = true
	m.mu.Unlock()
	m.wakeDispatcher()
}

func (m *Manager) wakeDispatcher() {
	select {
	case m.dispatchWake <- struct{}{}:
	default:
	}
}

func (m *Manager) releaseSlot(id string) {
	m.mu.Lock()
	delete(m.activeSlots, id)
	m.mu.Unlock()
	m.wakeDispatcher()
}

func (m *Manager) maxConcurrentLocked() int {
	limit := m.settings.MaxConcurrent
	if limit < 1 {
		limit = 2
	}
	return limit
}

func (m *Manager) dispatchLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	m.dispatch()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.dispatchWake:
			m.dispatch()
		case <-ticker.C:
			m.dispatch()
		}
	}
}

func (m *Manager) dispatch() {
	if !scheduleActive(m.Settings()) {
		return
	}
	m.mu.Lock()
	if m.closed || len(m.pending) == 0 || len(m.activeSlots) >= m.maxConcurrentLocked() {
		m.mu.Unlock()
		return
	}
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	tasks := make([]types.Task, 0, len(ids))
	for _, id := range ids {
		task, err := m.store.GetTask(id)
		if err != nil || task.Status != types.StatusQueued || m.isDeleted(id) {
			m.mu.Lock()
			delete(m.pending, id)
			m.mu.Unlock()
			continue
		}
		tasks = append(tasks, task)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		if tasks[i].CreatedAt != tasks[j].CreatedAt {
			return tasks[i].CreatedAt < tasks[j].CreatedAt
		}
		return tasks[i].ID < tasks[j].ID
	})
	for _, task := range tasks {
		m.mu.Lock()
		if m.closed || !m.pending[task.ID] || len(m.activeSlots) >= m.maxConcurrentLocked() {
			m.mu.Unlock()
			return
		}
		delete(m.pending, task.ID)
		m.activeSlots[task.ID] = true
		m.taskWG.Add(1)
		m.mu.Unlock()
		go func(task types.Task) {
			defer m.taskWG.Done()
			m.startTask(task.ID)
			if task.Engine == "aria2" {
				current, err := m.store.GetTask(task.ID)
				if err == nil && current.Status == types.StatusActive {
					return
				}
			}
			m.releaseSlot(task.ID)
		}(task)
	}
}

func (m *Manager) startTask(id string) {
	if m.isDeleted(id) {
		return
	}
	task, err := m.store.GetTask(id)
	if err != nil || task.Status != types.StatusQueued {
		return
	}
	if task.Engine == "http" {
		m.startHTTPTask(task)
		return
	}
	if task.Engine == "aria2" {
		m.startAriaTask(task)
		return
	}
	m.startMediaTask(task)
}

func (m *Manager) startMediaTask(task types.Task) {
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
	if !m.media.Available() {
		m.fail(current, "yt-dlp.exe not found")
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
	ok, err := m.store.MarkActive(current.ID, current.GID, now)
	if err != nil || !ok {
		return
	}
	current.Status = types.StatusActive
	current.Error = ""
	current.UpdatedAt = now
	m.emitTask(current)

	if info, err := m.media.Probe(ctx, current.URL); err == nil && info.Title != "" && !m.isDeleted(current.ID) {
		updatedAt := time.Now().UnixMilli()
		_ = m.store.UpdateTitle(current.ID, info.Title, updatedAt)
		if latest, getErr := m.store.GetTask(current.ID); getErr == nil {
			m.emitTask(latest)
		}
	}

	lastSave := time.Now().Add(-time.Second)
	onProgress := func(downloaded, total, speed, eta int64, progress float64) {
		if ctx.Err() != nil || m.isDeleted(current.ID) {
			return
		}
		current.Downloaded = downloaded
		current.Total = total
		current.Speed = speed
		current.ETA = eta
		current.Progress = progress
		current.UpdatedAt = time.Now().UnixMilli()
		if time.Since(lastSave) > 700*time.Millisecond {
			lastSave = time.Now()
			_ = m.store.UpdateProgress(current.ID, downloaded, total, speed, eta, progress, current.UpdatedAt)
			m.emitTask(current)
		}
	}

	outPath, outSize, downloadErr := m.media.Download(ctx, current, media.Options{
		DownloadDir: current.Dir,
		Quality:     current.Quality,
		Container:   current.Container,
		Connections: current.Connections,
		Subtitles:   current.Subtitles,
		Playlist:    current.Playlist,
		Username:    current.Username,
		Password:    current.Password,
		Cookies:     cookiesFor(current),
		Referrer:    current.Referrer,
		UserAgent:   current.UserAgent,
		Proxy:       m.Settings().Proxy,
	}, onProgress)

	if m.isDeleted(current.ID) {
		return
	}
	latest, err := m.store.GetTask(current.ID)
	if err != nil {
		return
	}
	if latest.Status == types.StatusPaused || latest.Status == types.StatusCanceled {
		return
	}
	if downloadErr != nil {
		if ctx.Err() != nil {
			if latest.Status == types.StatusActive {
				latest.Status = types.StatusPaused
				latest.Speed = 0
				latest.ETA = 0
				latest.UpdatedAt = time.Now().UnixMilli()
				_ = m.store.UpdateTask(&latest)
				m.emitTask(latest)
			}
			return
		}
		m.fail(latest, downloadErr.Error())
		return
	}
	filePath := latest.FilePath
	downloaded, total := latest.Downloaded, latest.Total
	if outPath != "" {
		filePath = outPath
		if outSize > 0 {
			downloaded = outSize
			total = outSize
		} else if info, statErr := os.Stat(outPath); statErr == nil && !info.IsDir() && info.Size() > 0 {
			downloaded = info.Size()
			total = info.Size()
		}
	}
	updatedAt := time.Now().UnixMilli()
	completed, err := m.store.CompleteTask(latest.ID, filePath, downloaded, total, updatedAt)
	if err != nil || !completed {
		return
	}
	latest.Status = types.StatusCompleted
	latest.Progress = 100
	latest.Speed = 0
	latest.ETA = 0
	latest.Error = ""
	latest.FilePath = filePath
	latest.Downloaded = downloaded
	latest.Total = total
	latest.UpdatedAt = updatedAt
	if fresh, getErr := m.store.GetTask(latest.ID); getErr == nil {
		latest = fresh
	}
	m.hashCompleted(latest.ID, filePath)
	if fresh, getErr := m.store.GetTask(latest.ID); getErr == nil {
		latest = fresh
	}
	m.emitTask(latest)
	m.emitStats()
	m.maybeShutdown()
}

func (m *Manager) dropDuplicateInterrupted(tasks []types.Task) []types.Task {
	best := make(map[string]types.Task, len(tasks))
	order := make([]string, 0, len(tasks))
	for _, task := range tasks {
		key := duplicateTaskKey(task.URL, task.Engine, task.Kind, task.Quality, task.Container)
		existing, ok := best[key]
		if !ok {
			best[key] = task
			order = append(order, key)
			continue
		}
		keep, drop := existing, task
		if task.Downloaded > existing.Downloaded ||
			(task.Downloaded == existing.Downloaded && task.Progress > existing.Progress) ||
			(task.Downloaded == existing.Downloaded && task.Progress == existing.Progress && task.CreatedAt < existing.CreatedAt) {
			keep, drop = task, existing
		}
		best[key] = keep
		_ = m.store.DeleteTask(drop.ID)
	}
	out := make([]types.Task, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func (m *Manager) interruptedDuplicates(rawURL, engine, kind, quality, container string) []types.Task {
	tasks, err := m.store.ListTasks(0)
	if err != nil {
		return nil
	}
	key := duplicateTaskKey(rawURL, engine, kind, quality, container)
	out := make([]types.Task, 0, 1)
	for _, task := range tasks {
		if task.Status != types.StatusActive && task.Status != types.StatusQueued && task.Status != types.StatusPaused {
			continue
		}
		if duplicateTaskKey(task.URL, task.Engine, task.Kind, task.Quality, task.Container) == key {
			out = append(out, task)
		}
	}
	return out
}

func duplicateTaskKey(rawURL, engine, kind, quality, container string) string {
	return strings.Join([]string{rawURL, engine, kind, quality, container}, "\x00")
}

func (m *Manager) startAriaTask(task types.Task) {
	if m.isDeleted(task.ID) {
		return
	}
	initial, err := m.store.GetTask(task.ID)
	if err != nil || initial.Status != types.StatusQueued {
		return
	}
	aria, err := m.ensureAria()
	if err != nil {
		if m.ctx.Err() != nil {
			return
		}
		if current, getErr := m.store.GetTask(task.ID); getErr == nil {
			m.fail(current, err.Error())
		}
		return
	}
	current, err := m.store.GetTask(task.ID)
	if err != nil || current.Status != types.StatusQueued || m.isDeleted(current.ID) {
		return
	}
	if current.GID != "" {
		if st, tellErr := aria.TellStatus(current.GID); tellErr == nil {
			if st.Status == "paused" {
				if unpauseErr := aria.Unpause(current.GID); unpauseErr != nil {
					if m.ctx.Err() == nil {
						m.fail(current, unpauseErr.Error())
					}
					return
				}
				st.Status = "active"
			}
			updatedAt := time.Now().UnixMilli()
			switch st.Status {
			case "active", "waiting":
				if ok, updateErr := m.store.MarkActive(current.ID, current.GID, updatedAt); updateErr == nil && ok {
					current.Status = types.StatusActive
					current.Error = ""
					current.UpdatedAt = updatedAt
					m.emitTask(current)
					return
				}
			case "complete":
				total := aria2.ParseInt(st.TotalLength)
				done := aria2.ParseInt(st.CompletedLength)
				if total == 0 {
					total = done
				}
				filePath := ""
				if len(st.Files) > 0 {
					filePath = st.Files[0].Path
				}
				if ok, updateErr := m.store.CompleteTask(current.ID, filePath, done, total, updatedAt); updateErr == nil && ok {
					m.hashCompleted(current.ID, filePath)
					if fresh, getErr := m.store.GetTask(current.ID); getErr == nil {
						current = fresh
					}
					m.releaseSlot(current.ID)
					m.emitTask(current)
					m.emitStats()
					m.maybeShutdown()
					return
				}
			}
		}
	}
	connections := current.Connections
	if connections < 1 {
		connections = 8
	}
	options := map[string]string{
		"dir":                       current.Dir,
		"continue":                  "true",
		"auto-file-renaming":        "true",
		"max-connection-per-server": fmt.Sprintf("%d", connections),
		"split":                     fmt.Sprintf("%d", connections),
	}
	if current.Kind == "torrent" {
		options["seed-time"] = "0"
		options["bt-save-metadata"] = "true"
	}
	if current.Username != "" {
		options["http-user"] = current.Username
		options["ftp-user"] = current.Username
	}
	if current.Password != "" {
		options["http-passwd"] = current.Password
		options["ftp-passwd"] = current.Password
	}
	if cookies := cookiesFor(current); cookies != "" {
		options["header"] = "Cookie: " + cookies
	}
	if current.Referrer != "" {
		options["referer"] = current.Referrer
	}
	if current.UserAgent != "" {
		options["user-agent"] = current.UserAgent
	}
	var gid string
	if current.Kind == "torrent" && isLocalTorrentFile(current.URL) {
		data, readErr := os.ReadFile(current.URL)
		if readErr != nil {
			if m.ctx.Err() != nil || m.isDeleted(current.ID) {
				return
			}
			m.fail(current, readErr.Error())
			return
		}
		gid, err = aria.AddTorrent(base64.StdEncoding.EncodeToString(data), options)
	} else {
		gid, err = aria.AddURI([]string{current.URL}, options)
	}
	if err != nil {
		if m.ctx.Err() != nil || m.isDeleted(current.ID) {
			return
		}
		m.fail(current, err.Error())
		return
	}
	if m.ctx.Err() != nil || m.isDeleted(current.ID) {
		_ = aria.Remove(gid)
		return
	}
	updatedAt := time.Now().UnixMilli()
	ok, err := m.store.MarkActive(current.ID, gid, updatedAt)
	if err != nil {
		_ = aria.Remove(gid)
		m.fail(current, err.Error())
		return
	}
	if !ok {
		_ = aria.Remove(gid)
		return
	}
	current.GID = gid
	current.Status = types.StatusActive
	current.Error = ""
	current.UpdatedAt = updatedAt
	m.emitTask(current)
}

func (m *Manager) ensureAria() (*aria2.Client, error) {
	m.ariaMu.Lock()
	defer m.ariaMu.Unlock()
	m.mu.Lock()
	if m.aria != nil {
		aria := m.aria
		m.mu.Unlock()
		return aria, nil
	}
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("manager is closed")
	}
	settings := m.settings
	m.mu.Unlock()

	bin := m.tools.Find("aria2c")
	if bin == "" {
		return nil, errors.New("aria2c.exe not found; put it in the bin folder")
	}
	startCtx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	defer cancel()
	client, err := aria2.Start(startCtx, bin, aria2.Options{
		DownloadDir:   settings.DownloadDir,
		MaxConcurrent: settings.MaxConcurrent,
		Connections:   settings.Connections,
		Proxy:         settings.Proxy,
		DataDir:       m.dataDir,
	})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		client.Stop()
		return nil, errors.New("manager is closed")
	}
	m.aria = client
	m.mu.Unlock()
	return client, nil
}

type stallState struct {
	done    int64
	since   time.Time
	strikes int
}

func (m *Manager) clearStall(id string) {
	m.stallMu.Lock()
	delete(m.stalls, id)
	m.stallMu.Unlock()
}

func (m *Manager) stallTick(id string, done int64, speed int64, aria *aria2.Client, gid string) bool {
	m.stallMu.Lock()
	s := m.stalls[id]
	now := time.Now()
	if s == nil {
		s = &stallState{done: done, since: now}
		m.stalls[id] = s
	}
	if done > s.done {
		s.done = done
		s.since = now
		s.strikes = 0
	}
	elapsed := now.Sub(s.since)
	if speed > 0 || elapsed < 45*time.Second {
		m.stallMu.Unlock()
		return false
	}
	strikes := s.strikes
	s.strikes++
	s.done = done
	s.since = now
	m.stallMu.Unlock()
	if aria == nil || gid == "" {
		return strikes >= 1
	}
	if strikes == 0 {
		go func() {
			if err := aria.Pause(gid); err == nil {
				time.Sleep(400 * time.Millisecond)
				_ = aria.Unpause(gid)
			}
		}()
		return false
	}
	return true
}

func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	statsTicker := time.NewTicker(2 * time.Second)
	defer statsTicker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.pollAria()
		case <-statsTicker.C:
			m.emitStats()
		}
	}
}

func (m *Manager) pollAria() {
	m.mu.Lock()
	aria := m.aria
	m.mu.Unlock()
	if aria == nil {
		return
	}
	tasks, err := m.store.ListTasks(0)
	if err != nil {
		return
	}
	for _, listed := range tasks {
		if listed.Engine != "aria2" {
			continue
		}
		if listed.GID == "" {
			if listed.Status == types.StatusQueued && time.Since(time.UnixMilli(listed.CreatedAt)) > 45*time.Second && !m.isDeleted(listed.ID) {
				current, getErr := m.store.GetTask(listed.ID)
				if getErr == nil && current.Status == types.StatusQueued {
					m.fail(current, "download engine did not start; try again")
				}
			}
			continue
		}
		t, err := m.store.GetTask(listed.ID)
		if err != nil || m.isDeleted(t.ID) {
			continue
		}
		if t.Status != types.StatusActive && t.Status != types.StatusQueued {
			continue
		}
		st, err := aria.TellStatus(t.GID)
		if err != nil {
			continue
		}
		total := aria2.ParseInt(st.TotalLength)
		done := aria2.ParseInt(st.CompletedLength)
		speed := aria2.ParseInt(st.DownloadSpeed)
		progress := 0.0
		if total > 0 {
			progress = float64(done) / float64(total) * 100
		}
		if len(st.Files) > 0 && st.Files[0].Path != "" {
			t.FilePath = st.Files[0].Path
			if t.Title == "" || t.Title == fallbackTitle(t.URL) {
				t.Title = filepath.Base(st.Files[0].Path)
			}
		}
		t.Total = total
		t.Downloaded = done
		t.Speed = speed
		t.Progress = progress
		t.UpdatedAt = time.Now().UnixMilli()
		if st.Status == "active" && m.stallTick(t.ID, done, speed, aria, t.GID) {
			m.clearStall(t.ID)
			gid := t.GID
			t.GID = ""
			_ = m.store.UpdateTask(&t)
			_ = aria.Remove(gid)
			m.fail(t, "download stalled: no data received for 45s")
			continue
		}
		if st.Status != "active" {
			m.clearStall(t.ID)
		}
		switch st.Status {
		case "active":
			t.Status = types.StatusActive
		case "waiting":
			t.Status = types.StatusQueued
		case "paused":
			t.Status = types.StatusPaused
			t.Speed = 0
			m.releaseSlot(t.ID)
		case "complete":
			if total == 0 {
				total = done
			}
			ok, err := m.store.CompleteTask(t.ID, t.FilePath, done, total, t.UpdatedAt)
			if err != nil || !ok {
				continue
			}
			t.Status = types.StatusCompleted
			t.Progress = 100
			t.Speed = 0
			t.ETA = 0
			t.Downloaded = done
			t.Total = total
			m.releaseSlot(t.ID)
		case "error":
			if current, getErr := m.store.GetTask(t.ID); getErr != nil || current.Status != types.StatusActive {
				continue
			}
			gid := t.GID
			t.GID = ""
			_ = m.store.UpdateTask(&t)
			if gid != "" {
				_ = aria.RemoveDownloadResult(gid)
			}
			m.fail(t, st.ErrorMessage)
			continue
		case "removed":
			if current, getErr := m.store.GetTask(t.ID); getErr != nil || current.Status != types.StatusActive {
				continue
			}
			t.Status = types.StatusCanceled
			t.Speed = 0
			m.releaseSlot(t.ID)
		}
		if st.Status == "active" && speed > 0 && total > done {
			t.ETA = (total - done) / speed
		} else {
			t.ETA = 0
		}
		if t.Status == types.StatusCompleted {
			m.hashCompleted(t.ID, t.FilePath)
			if fresh, getErr := m.store.GetTask(t.ID); getErr == nil {
				t = fresh
			}
			m.emitTask(t)
			m.emitStats()
			m.maybeShutdown()
			continue
		}
		_ = m.store.UpdateTask(&t)
		m.emitTask(t)
	}
}

func (m *Manager) send(name string, payload any) {
	if m.emit != nil {
		m.emit(name, payload)
	}
}

func (m *Manager) emitStats() {
	if m.emit == nil {
		return
	}
	st, err := m.store.Stats()
	if err != nil {
		return
	}
	settings := m.Settings()
	total, free, _ := disk.Usage(settings.DownloadDir)
	st.DiskTotal = total
	st.DiskFree = free
	if total > free {
		st.DiskUsed = total - free
	}
	m.send("stats:update", st)
	if time.Since(m.lastSample) > 8*time.Second {
		m.lastSample = time.Now()
		_ = m.store.AddSpeedSample(time.Now().UnixMilli(), st.Speed)
	}
}

func (m *Manager) Pause(id string) (types.Task, error) {
	m.clearStall(id)
	t, err := m.store.GetTask(id)
	if err != nil {
		return t, err
	}
	if t.Status == types.StatusCompleted || t.Status == types.StatusCanceled {
		return t, errors.New("task is not running")
	}
	t.Status = types.StatusPaused
	t.Speed = 0
	t.ETA = 0
	t.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.UpdateTask(&t); err != nil {
		return t, err
	}
	m.mu.Lock()
	delete(m.pending, id)
	cancel := m.running[id]
	aria := m.aria
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if aria != nil && t.GID != "" {
		_ = aria.Pause(t.GID)
	}
	m.releaseSlot(id)
	m.emitTask(t)
	m.emitStats()
	return t, nil
}

func (m *Manager) MoveTaskInQueue(id, direction string) ([]types.Task, error) {
	if direction != "up" && direction != "down" {
		return nil, errors.New("queue direction must be up or down")
	}
	tasks, err := m.store.ListTasks(0)
	if err != nil {
		return nil, err
	}
	queued := make([]types.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == types.StatusQueued {
			queued = append(queued, task)
		}
	}
	sort.SliceStable(queued, func(i, j int) bool {
		if queued[i].Priority != queued[j].Priority {
			return queued[i].Priority > queued[j].Priority
		}
		if queued[i].CreatedAt != queued[j].CreatedAt {
			return queued[i].CreatedAt < queued[j].CreatedAt
		}
		return queued[i].ID < queued[j].ID
	})
	index := -1
	for i, task := range queued {
		if task.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, errors.New("task is not waiting in the queue")
	}
	target := index - 1
	if direction == "down" {
		target = index + 1
	}
	if target < 0 || target >= len(queued) {
		return queued, nil
	}
	queued[index], queued[target] = queued[target], queued[index]
	now := time.Now().UnixMilli()
	for i := range queued {
		priority := len(queued) - i
		if queued[i].Priority == priority {
			continue
		}
		if err := m.store.UpdatePriority(queued[i].ID, priority, now); err != nil {
			return nil, err
		}
		queued[i].Priority = priority
		queued[i].UpdatedAt = now
		m.emitTask(queued[i])
	}
	return queued, nil
}

func (m *Manager) Resume(id string) (types.Task, error) {
	t, err := m.store.GetTask(id)
	if err != nil {
		return t, err
	}
	if t.Status == types.StatusCompleted {
		return t, errors.New("task is already completed")
	}
	if t.Status == types.StatusActive || t.Status == types.StatusQueued {
		return t, nil
	}
	settings := m.Settings()
	if t.Status == types.StatusError {
		t.Retries = 0
		if settings.Connections > 0 {
			t.Connections = settings.Connections
		}
	}
	t.Status = types.StatusQueued
	t.Error = ""
	t.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.UpdateTask(&t); err != nil {
		return t, err
	}
	m.emitTask(t)
	if !scheduleActive(settings) {
		m.mu.Lock()
		m.schedulePaused[t.ID] = true
		m.mu.Unlock()
		return t, nil
	}
	m.runTask(t.ID)
	return t, nil
}

func (m *Manager) Cancel(id string) (types.Task, error) {
	m.clearStall(id)
	t, err := m.store.GetTask(id)
	if err != nil {
		return t, err
	}
	if t.Status == types.StatusCompleted {
		return t, errors.New("task is already completed")
	}
	if t.Status == types.StatusCanceled {
		return t, nil
	}
	t.Status = types.StatusCanceled
	t.Speed = 0
	t.ETA = 0
	t.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.UpdateTask(&t); err != nil {
		return t, err
	}
	m.mu.Lock()
	delete(m.pending, id)
	cancel := m.running[id]
	aria := m.aria
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if aria != nil && t.GID != "" {
		_ = aria.Remove(t.GID)
	}
	m.releaseSlot(id)
	m.emitTask(t)
	m.emitStats()
	return t, nil
}

func (m *Manager) Remove(id string) error {
	return m.RemoveWithFile(id, false)
}

func (m *Manager) RemoveWithFile(id string, deleteFile bool) error {
	m.clearStall(id)
	t, err := m.store.GetTask(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.deleted[id] = true
	delete(m.pending, id)
	cancel := m.running[id]
	aria := m.aria
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if aria != nil && t.GID != "" {
		_ = aria.Remove(t.GID)
	}
	if err := m.store.DeleteTask(id); err != nil {
		m.mu.Lock()
		delete(m.deleted, id)
		m.mu.Unlock()
		return err
	}
	m.releaseSlot(id)
	m.send("task:removed", map[string]string{"id": id})
	m.emitStats()
	if !deleteFile {
		return nil
	}
	return removeTaskFiles(t, m.Settings().DownloadDir)
}

func removeTaskFiles(task types.Task, fallbackDir string) error {
	dir := strings.TrimSpace(task.Dir)
	if dir == "" {
		dir = fallbackDir
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	baseDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	baseDir = filepath.Clean(baseDir)
	candidates := make([]string, 0, 10)
	seen := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(baseDir, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return
		}
		if !seen[abs] {
			seen[abs] = true
			candidates = append(candidates, abs)
		}
	}
	filePath := strings.TrimSpace(task.FilePath)
	add(filePath)
	title := filepath.Base(strings.TrimSpace(task.Title))
	if filePath == "" && title != "" && title != "." && title != string(os.PathSeparator) {
		add(filepath.Join(baseDir, title))
	}
	for _, base := range append([]string(nil), candidates...) {
		for _, suffix := range []string{".aria2", ".part", ".part.meta", ".part.multi", ".part.multi.json"} {
			add(base + suffix)
		}
	}
	var errs []error
	for _, path := range candidates {
		err = nil
		for attempt := 0; attempt < 5; attempt++ {
			info, statErr := os.Lstat(path)
			if errors.Is(statErr, os.ErrNotExist) {
				err = nil
				break
			}
			if statErr != nil {
				err = statErr
				break
			}
			if info.IsDir() {
				err = os.RemoveAll(path)
			} else {
				err = os.Remove(path)
			}
			if err == nil || errors.Is(err, os.ErrNotExist) {
				err = nil
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func shouldFallbackHTTP(message string) bool {
	lower := strings.ToLower(message)
	if strings.TrimSpace(lower) == "" {
		return true
	}
	for _, marker := range []string{
		"ssl", "tls", "certificate", "revocation", "handshake",
		"did not start", "aria2", "jsonrpc", "deadline exceeded", "context deadline",
		"operation completed successfully", "stalled",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) fallbackToHTTP(t types.Task) bool {
	if t.Engine != "aria2" || t.Kind != "file" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(t.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if t.Status == types.StatusPaused || t.Status == types.StatusCanceled || t.Status == types.StatusCompleted {
		return false
	}
	m.removeAriaPartial(t)
	now := time.Now().UnixMilli()
	t.Engine = "http"
	t.Status = types.StatusQueued
	t.GID = ""
	t.Error = ""
	t.Progress = 0
	t.Downloaded = 0
	t.Total = 0
	t.Speed = 0
	t.ETA = 0
	t.Retries = 0
	t.FilePath = ""
	t.UpdatedAt = now
	if err := m.store.UpdateTask(&t); err != nil {
		return false
	}
	m.emitTask(t)
	m.emitStats()
	m.runTask(t.ID)
	return true
}

func (m *Manager) removeAriaPartial(t types.Task) {
	if strings.TrimSpace(t.FilePath) == "" || strings.TrimSpace(t.Dir) == "" {
		return
	}
	dir, err := filepath.Abs(t.Dir)
	if err != nil {
		return
	}
	filePath, err := filepath.Abs(t.FilePath)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(dir, filePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return
	}
	if info, err := os.Stat(filePath); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(filePath)
	}
	_ = os.Remove(filePath + ".aria2")
}

func (m *Manager) fail(t types.Task, message string) {
	if m.isDeleted(t.ID) || m.ctx.Err() != nil {
		return
	}
	m.releaseSlot(t.ID)
	m.clearStall(t.ID)
	if shouldFallbackHTTP(message) && m.fallbackToHTTP(t) {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = "download engine reported an error"
	}
	t.Error = message
	t.Speed = 0
	t.ETA = 0
	t.UpdatedAt = time.Now().UnixMilli()
	maxRetries := 0
	if m.Settings().AutoRetry {
		maxRetries = 3
	}
	if t.Retries < maxRetries && !isPermanentError(message) {
		t.Retries++
		if t.Connections > 1 {
			t.Connections = (t.Connections + 1) / 2
		}
		t.Status = types.StatusQueued
		_ = m.store.UpdateTask(&t)
		m.emitTask(t)
		m.emitStats()
		delay := time.Duration(t.Retries) * 4 * time.Second
		time.AfterFunc(delay, func() { m.runTask(t.ID) })
		return
	}
	t.Status = types.StatusError
	_ = m.store.UpdateTask(&t)
	m.emitTask(t)
	m.emitStats()
}

func isPermanentError(message string) bool {
	lower := strings.ToLower(message)
	permanent := []string{
		"no such file", "unsupported url", "no video formats", "ffmpeg is required",
		"private video", "members-only", "requires authentication", "login required",
		"copyright", "http error 404", "http error 410", "not available in your country",
	}
	for _, part := range permanent {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

func hashFile(path string) (string, string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", 0, err
	}
	defer file.Close()
	sha := sha256.New()
	sum := md5.New()
	size, err := io.Copy(io.MultiWriter(sha, sum), file)
	if err != nil {
		return "", "", 0, err
	}
	return hex.EncodeToString(sha.Sum(nil)), hex.EncodeToString(sum.Sum(nil)), size, nil
}

func (m *Manager) hashCompleted(id, filePath string) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return
	}
	sha, sum, size, err := hashFile(filePath)
	if err != nil {
		return
	}
	_ = m.store.UpdateHashes(id, sha, sum, size, time.Now().UnixMilli())
}

func (m *Manager) Verify(id string) (types.Task, error) {
	t, err := m.store.GetTask(id)
	if err != nil {
		return t, err
	}
	if t.Status != types.StatusCompleted {
		return t, errors.New("بررسی سلامت فقط برای دانلود کامل انجام می‌شود")
	}
	if strings.TrimSpace(t.FilePath) == "" {
		return t, errors.New("این تسک فایل ذخیره‌شده ندارد")
	}
	info, err := os.Stat(t.FilePath)
	if err != nil {
		return t, fmt.Errorf("فایل پیدا نشد: %w", err)
	}
	if info.IsDir() {
		return t, errors.New("برای پوشه Playlist نمی‌توان هش تک‌فایل گرفت")
	}
	sha, sum, size, err := hashFile(t.FilePath)
	if err != nil {
		return t, err
	}
	updatedAt := time.Now().UnixMilli()
	if err := m.store.UpdateHashes(t.ID, sha, sum, size, updatedAt); err != nil {
		return t, err
	}
	t.SHA256 = sha
	t.MD5 = sum
	t.Downloaded = size
	if t.Total < size {
		t.Total = size
	}
	t.UpdatedAt = updatedAt
	m.emitTask(t)
	return t, nil
}

func normalizeCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case types.CategoryVideo:
		return types.CategoryVideo
	case types.CategoryAudio:
		return types.CategoryAudio
	case types.CategoryArchive:
		return types.CategoryArchive
	case types.CategoryProgram:
		return types.CategoryProgram
	case types.CategoryOther:
		return types.CategoryOther
	default:
		return ""
	}
}

func detectCategory(engine, kind, quality, container, title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))
	if index := strings.LastIndex(lower, "."); index >= 0 && index < len(lower)-1 {
		ext := strings.TrimSpace(lower[index+1:])
		switch ext {
		case "mp4", "mkv", "webm", "avi", "mov", "m4v", "flv", "ts", "mpeg", "mpg":
			return types.CategoryVideo
		case "mp3", "m4a", "flac", "wav", "ogg", "opus", "aac":
			return types.CategoryAudio
		case "zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso", "dmg":
			return types.CategoryArchive
		case "exe", "msi", "apk", "deb", "rpm", "appimage", "pkg":
			return types.CategoryProgram
		}
	}
	if kind == "torrent" {
		return types.CategoryOther
	}
	if engine == "ytdlp" {
		if quality == "audio" || container == "mp3" {
			return types.CategoryAudio
		}
		return types.CategoryVideo
	}
	return types.CategoryOther
}

func normalizeQuality(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "360", "480", "720", "1080", "1440", "2160", "audio":
		return strings.ToLower(strings.TrimSpace(quality))
	default:
		return "best"
	}
}

func normalizeContainer(container, quality string) string {
	if quality == "audio" {
		return "mp3"
	}
	switch strings.ToLower(strings.TrimSpace(container)) {
	case "mkv":
		return "mkv"
	case "mp3":
		return "mp3"
	default:
		return "mp4"
	}
}

func cookiesFor(task types.Task) string {
	if cookies := strings.TrimSpace(task.BrowserCookies); cookies != "" {
		return cookies
	}
	return strings.TrimSpace(task.Cookies)
}

func (m *Manager) isDeleted(id string) bool {
	m.mu.Lock()
	deleted := m.deleted[id]
	m.mu.Unlock()
	return deleted
}

func (m *Manager) emitTask(t types.Task) {
	if m.isDeleted(t.ID) {
		return
	}
	if m.emit != nil {
		m.send("task:update", t)
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mediaGate.close()
	for _, cancel := range m.running {
		cancel()
	}
	aria := m.aria
	m.mu.Unlock()
	m.taskWG.Wait()
	m.wg.Wait()
	if aria != nil {
		aria.Stop()
	}
}

func (m *Manager) SpeedHistory(period string) []types.SpeedPoint {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -7)
	if period == "last" {
		start = start.AddDate(0, 0, -7)
		end = end.AddDate(0, 0, -7)
	}
	points, err := m.store.DailySpeeds(start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil
	}
	return fillDays(points, start)
}

func fillDays(points []types.SpeedPoint, start time.Time) []types.SpeedPoint {
	byDay := make(map[string]int64, len(points))
	for _, p := range points {
		byDay[p.Day] = p.Speed
	}
	out := make([]types.SpeedPoint, 0, 7)
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, types.SpeedPoint{Day: day, Speed: byDay[day]})
	}
	return out
}

func (m *Manager) schedulerLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	m.applySchedule()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.applySchedule()
		}
	}
}

func (m *Manager) applySchedule() {
	settings := m.Settings()
	within := scheduleActive(settings)
	m.mu.Lock()
	changed := within == m.schedulerOff
	var toResume []string
	if changed {
		m.schedulerOff = !within
		if within {
			for id := range m.schedulePaused {
				toResume = append(toResume, id)
			}
			m.schedulePaused = make(map[string]bool)
		}
	}
	m.mu.Unlock()
	if !changed {
		return
	}
	if within {
		for _, id := range toResume {
			task, err := m.store.GetTask(id)
			if err != nil || task.Status != types.StatusPaused {
				continue
			}
			_, _ = m.Resume(id)
		}
		if m.emit != nil {
			m.send("schedule:update", map[string]bool{"paused": false})
		}
		return
	}
	tasks, err := m.store.ListTasks(0)
	if err != nil {
		return
	}
	for _, t := range tasks {
		if t.Status != types.StatusActive && t.Status != types.StatusQueued {
			continue
		}
		if _, err := m.Pause(t.ID); err != nil {
			continue
		}
		m.mu.Lock()
		m.schedulePaused[t.ID] = true
		m.mu.Unlock()
	}
	if m.emit != nil {
		m.send("schedule:update", map[string]bool{"paused": true})
	}
}

func scheduleActive(settings types.Settings) bool {
	if !settings.ScheduleEnabled {
		return true
	}
	startM, startOK := parseScheduleTime(settings.ScheduleStart)
	stopM, stopOK := parseScheduleTime(settings.ScheduleStop)
	if !startOK || !stopOK || startM == stopM {
		return true
	}
	now := time.Now()
	return scheduleWindowActive(now.Hour()*60+now.Minute(), startM, stopM)
}

func scheduleWindowActive(minutes, startM, stopM int) bool {
	if startM < stopM {
		return minutes >= startM && minutes < stopM
	}
	return minutes >= startM || minutes < stopM
}

func parseScheduleTime(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(value[3:])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func (m *Manager) maybeShutdown() {
	settings := m.Settings()
	if !settings.ShutdownOnComplete || m.shutdownHook == nil {
		return
	}
	time.AfterFunc(5*time.Second, func() {
		if m.ctx.Err() != nil {
			return
		}
		pending, err := m.store.CountPending()
		if err != nil || pending > 0 {
			return
		}
		if !m.Settings().ShutdownOnComplete {
			return
		}
		if m.emit != nil {
			m.send("shutdown:notify", nil)
		}
		time.Sleep(2 * time.Second)
		if m.ctx.Err() != nil {
			return
		}
		m.shutdownHook()
	})
}

func isLocalTorrentFile(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" || strings.HasPrefix(lower, "magnet:") ||
		strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	return strings.HasSuffix(lower, ".torrent")
}

func classify(rawURL, requested string) (engine, kind, source, title string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", "", "", errors.New("link is empty")
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "magnet:") {
		return "aria2", "torrent", "Torrent", "Magnet download", nil
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", "", "", errors.New("enter a valid http, https, or magnet link")
	}
	if u.Host == "" {
		return "", "", "", "", errors.New("link has no host")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		base = host
	}
	title = fallbackTitle(rawURL)
	if base != "" && base != "/" && base != "." {
		if decoded, err := url.PathUnescape(base); err == nil {
			title = decoded
		}
	}
	source = host

	if requested == "http" {
		return "http", "file", source, title, nil
	}
	if requested == "aria2" {
		return "aria2", "file", source, title, nil
	}
	if requested == "ytdlp" || requested == "video" {
		return "ytdlp", "video", source, title, nil
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".torrent") {
		return "aria2", "torrent", source, title, nil
	}
	preferredEngine := "http"
	lowerPath := strings.ToLower(u.Path)
	for _, ext := range []string{".mp4", ".mkv", ".webm", ".mov", ".avi", ".mp3", ".m4a", ".flac", ".zip", ".rar", ".7z", ".pdf", ".iso", ".exe", ".msi"} {
		if strings.HasSuffix(lowerPath, ext) {
			return preferredEngine, "file", source, title, nil
		}
	}
	if isMediaHost(host) {
		return "ytdlp", "video", source, title, nil
	}
	return preferredEngine, "file", source, title, nil
}

func isMediaHost(host string) bool {
	mediaHosts := []string{
		"youtube.com", "youtu.be", "instagram.com", "tiktok.com", "twitter.com", "x.com",
		"facebook.com", "fb.watch", "vimeo.com", "dailymotion.com", "twitch.tv", "soundcloud.com",
		"bilibili.com", "reddit.com", "redd.it", "pinterest.com", "linkedin.com", "rumble.com",
		"odysee.com", "vimeo.com", "streamable.com", "ted.com", "bbc.co.uk", "cnn.com",
		"aparat.com", "namasha.com", "filimo.com", "spotify.com", "bandcamp.com",
	}
	for _, mediaHost := range mediaHosts {
		if host == mediaHost || strings.HasSuffix(host, "."+mediaHost) {
			return true
		}
	}
	return false
}

func fallbackTitle(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "Download"
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = u.Hostname()
	}
	if name == "" {
		name = "Download"
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	return name
}

func randomID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
