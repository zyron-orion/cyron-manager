package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cyronmanager/internal/bridge"
	"cyronmanager/internal/disk"
	"cyronmanager/internal/download"
	gh "cyronmanager/internal/github"
	"cyronmanager/internal/power"
	"cyronmanager/internal/proxy"
	"cyronmanager/internal/secure"
	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

const (
	toastAppID = "CYRON Manager"
	appVersion = "1.1.0"
)

type App struct {
	ctx            context.Context
	store          *store.Store
	manager        *download.Manager
	proxy          *proxy.Manager
	tools          *tools.Manager
	github         *gh.Client
	settings       types.Settings
	dataDir        string
	initErr        string
	startupWarning string

	mu              sync.Mutex
	lastClipboard   string
	clipboardCancel context.CancelFunc
	settingsValue   atomic.Value
	notified        sync.Map
	bridgeMu        sync.Mutex
	bridge          *bridge.Server
	bridgeKey       string
	bridgeErr       string
}

func NewApp() *App {
	app := &App{}
	app.settingsValue.Store(types.Settings{})
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	dataDir, err := appDataDir()
	if err != nil {
		a.initErr = err.Error()
		return
	}
	a.dataDir = dataDir
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		a.initErr = err.Error()
		return
	}
	st, err := store.Open(dataDir)
	if err != nil {
		a.initErr = err.Error()
		return
	}
	a.store = st
	defaultDir := defaultDownloadDir(dataDir)
	settings, err := st.GetSettings(defaultDir)
	if err != nil {
		a.initErr = err.Error()
		return
	}
	if settings.DownloadDir == "" {
		settings.DownloadDir = defaultDir
	}
	settings.GitHubToken = decodeToken(settings.GitHubToken)
	settings = sanitizeBrowserSettings(settings)
	if err := os.MkdirAll(settings.DownloadDir, 0o755); err != nil {
		settings.DownloadDir = defaultDir
		_ = os.MkdirAll(settings.DownloadDir, 0o755)
	}
	a.proxy = proxy.NewManager(st)
	if migrated, migrateErr := a.proxy.MigrateLegacy(&settings); migrateErr != nil {
		// The legacy value cannot be used. Warn once, then drop it so it does not
		// keep resurfacing on every start or block later settings saves.
		a.startupWarning = "پروکسی قبلی قابل تبدیل نبود و استفاده نشد: " + migrateErr.Error()
		settings.Proxy = ""
		stored := settings
		if encodedToken, tokenErr := encodeToken(settings.GitHubToken); tokenErr == nil {
			stored.GitHubToken = encodedToken
			_ = st.SaveSettings(stored)
		}
	} else if migrated {
		stored := settings
		if encodedToken, tokenErr := encodeToken(settings.GitHubToken); tokenErr == nil {
			stored.GitHubToken = encodedToken
			if saveErr := st.SaveSettings(stored); saveErr != nil {
				a.startupWarning = "تنظیمات پروکسی مهاجرت شد اما ذخیره نشد: " + saveErr.Error()
			}
		}
	}
	a.settings = settings
	a.settingsValue.Store(settings)
	_, _ = extractExtension(dataDir)
	_ = toast.SetAppData(toast.AppData{AppID: toastAppID})
	a.repairMediaTaskSizes()
	a.tools = tools.New(dataDir)
	a.github = gh.New(settings.GitHubToken)
	a.manager = download.New(ctx, st, a.tools, a.proxy, settings, dataDir, a.emit)
	a.manager.SetShutdownHook(func() {
		_ = power.Shutdown(60)
	})
	a.manager.Start()
	a.configureBridge()
	fitWindow(ctx)

	clipCtx, cancel := context.WithCancel(ctx)
	a.clipboardCancel = cancel
	go a.watchClipboard(clipCtx)
}

func (a *App) repairMediaTaskSizes() {
	if a.store == nil {
		return
	}
	tasks, err := a.store.ListTasks(0)
	if err != nil {
		return
	}
	for _, task := range tasks {
		if task.Status != types.StatusCompleted || task.FilePath == "" {
			continue
		}
		info, err := os.Stat(task.FilePath)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		if task.Downloaded == info.Size() && task.Total >= info.Size() {
			continue
		}
		task.Downloaded = info.Size()
		if task.Total < info.Size() {
			task.Total = info.Size()
		}
		task.Progress = 100
		task.Speed = 0
		task.ETA = 0
		_ = a.store.UpdateTask(&task)
	}
}

func (a *App) focusWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func (a *App) shutdown(ctx context.Context) {
	if a.clipboardCancel != nil {
		a.clipboardCancel()
	}
	a.bridgeMu.Lock()
	if a.bridge != nil {
		_ = a.bridge.Close()
		a.bridge = nil
	}
	a.bridgeMu.Unlock()
	if a.manager != nil {
		a.manager.Close()
	}
	if a.store != nil {
		a.store.Close()
	}
}

func (a *App) emit(event string, payload any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, event, payload)
	if event == "task:update" {
		if task, ok := payload.(types.Task); ok && task.Status == types.StatusCompleted {
			a.notifyCompleted(task)
		}
	}
}

func (a *App) ready() bool {
	return a.store != nil && a.manager != nil
}

func (a *App) GetInitialState() types.InitialState {
	state := types.InitialState{
		Settings:     a.settings,
		Tasks:        []types.Task{},
		SpeedHistory: emptyHistory(),
	}
	if !a.ready() {
		state.Error = a.initErr
		return state
	}
	tasks, err := a.store.ListTasks(0)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.Tasks = tasks
	if a.proxy != nil {
		state.ProxyProfiles = a.proxy.List()
	}
	if a.startupWarning != "" {
		state.Warning = a.startupWarning
	}
	state.Stats = a.stats()
	state.SpeedHistory = a.manager.SpeedHistory("week")
	if a.tools != nil {
		state.Tools = a.tools.Status()
	}
	return state
}

func (a *App) GetTasks() ([]types.Task, error) {
	if !a.ready() {
		return nil, errors.New(a.initErr)
	}
	return a.store.ListTasks(0)
}

func (a *App) VerifyTask(id string) (types.Task, error) {
	return a.manager.Verify(id)
}

func (a *App) GetStats() (types.Stats, error) {
	if !a.ready() {
		return types.Stats{}, errors.New(a.initErr)
	}
	return a.stats(), nil
}

func (a *App) stats() types.Stats {
	st, err := a.store.Stats()
	if err != nil {
		return st
	}
	total, free, _ := disk.Usage(a.settings.DownloadDir)
	st.DiskTotal = total
	st.DiskFree = free
	if total > free {
		st.DiskUsed = total - free
	}
	return st
}

func (a *App) GetSpeedHistory(period string) ([]types.SpeedPoint, error) {
	if !a.ready() {
		return emptyHistory(), errors.New(a.initErr)
	}
	points := a.manager.SpeedHistory(period)
	if len(points) == 0 {
		points = emptyHistory()
	}
	return points, nil
}

func (a *App) AddDownload(req types.AddRequest) (types.Task, error) {
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	return a.manager.Add(req)
}

func (a *App) AddDownloadChecked(req types.AddRequest) (types.AddResult, error) {
	if !a.ready() {
		return types.AddResult{}, errors.New(a.initErr)
	}
	task, duplicate, err := a.manager.AddChecked(req)
	if err != nil {
		return types.AddResult{}, err
	}
	return types.AddResult{Task: task, Duplicate: duplicate}, nil
}

func (a *App) PauseTask(id string) (types.Task, error) {
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	return a.manager.Pause(id)
}

func (a *App) ResumeTask(id string) (types.Task, error) {
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	return a.manager.Resume(id)
}

func (a *App) CancelTask(id string) (types.Task, error) {
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	return a.manager.Cancel(id)
}

func (a *App) RemoveTask(id string) error {
	if !a.ready() {
		return errors.New(a.initErr)
	}
	err := a.manager.Remove(id)
	if err == nil {
		a.notified.Delete(id)
	}
	return err
}

func (a *App) DeleteTasks(ids []string, deleteFile bool) error {
	if !a.ready() {
		return errors.New(a.initErr)
	}
	var errs []error
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := a.manager.RemoveWithFile(id, deleteFile); err != nil {
			errs = append(errs, err)
			continue
		}
		a.notified.Delete(id)
	}
	return errors.Join(errs...)
}

func (a *App) MoveTaskInQueue(id, direction string) ([]types.Task, error) {
	if !a.ready() {
		return nil, errors.New(a.initErr)
	}
	return a.manager.MoveTaskInQueue(id, direction)
}

func (a *App) AddTorrentPath(path string) (types.Task, error) {
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return types.Task{}, errors.New("torrent path is empty")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".torrent") {
		return types.Task{}, errors.New("only .torrent files can be added this way")
	}
	return a.manager.AddTorrentFile(path, "")
}

func (a *App) GetBridgeInfo() types.BridgeInfo {
	port := a.settings.BrowserPort
	if port < 1024 || port > 65535 {
		port = bridge.DefaultPort
	}
	info := types.BridgeInfo{
		Enabled:          a.settings.BrowserBridgeEnabled,
		Address:          "http://" + bridge.Address(port),
		Port:             port,
		ExtensionID:      bridge.ExtensionID,
		ExtensionVersion: extensionVersion,
	}
	if a.dataDir != "" {
		info.ExtensionFolder = filepath.Join(a.dataDir, "browser-extension")
	}
	a.bridgeMu.Lock()
	if a.bridge != nil {
		info.Running = a.bridge.Running()
		if info.Running {
			info.Error = a.bridge.Error()
		} else if a.bridge.Error() != "" {
			info.Error = a.bridge.Error()
		}
	}
	if info.Error == "" {
		info.Error = a.bridgeErr
	}
	a.bridgeMu.Unlock()
	return info
}

func (a *App) OpenExtensionFolder() error {
	if a.dataDir == "" {
		return errors.New("app data folder is not ready")
	}
	dir, err := extractExtension(a.dataDir)
	if err != nil {
		return err
	}
	return openFolder(dir)
}

func (a *App) GetSettings() (types.Settings, error) {
	if !a.ready() {
		return a.settings, errors.New(a.initErr)
	}
	settings, err := a.store.GetSettings(a.settings.DownloadDir)
	if err != nil {
		return settings, err
	}
	settings.GitHubToken = decodeToken(settings.GitHubToken)
	return settings, nil
}

func (a *App) proxyManager() (*proxy.Manager, error) {
	if a.proxy == nil {
		if a.initErr != "" {
			return nil, errors.New(a.initErr)
		}
		return nil, errors.New("proxy manager is not ready")
	}
	return a.proxy, nil
}

func (a *App) proxyTestContext() (context.Context, context.CancelFunc) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, 45*time.Second)
}

func (a *App) GetProxyProfiles() ([]types.ProxyProfileView, error) {
	manager, err := a.proxyManager()
	if err != nil {
		return []types.ProxyProfileView{}, err
	}
	return manager.List(), nil
}

func (a *App) SaveProxyProfile(input types.ProxyProfileInput) (types.ProxyProfileView, error) {
	manager, err := a.proxyManager()
	if err != nil {
		return types.ProxyProfileView{}, err
	}
	return manager.Save(input)
}

func (a *App) DeleteProxyProfile(id string) error {
	manager, err := a.proxyManager()
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	next := a.settings
	if strings.TrimSpace(next.ProxyDefaultID) == id {
		next.ProxyDefaultID = ""
	}
	fallbacks := make([]string, 0, len(next.ProxyFallbackIDs))
	for _, fallback := range next.ProxyFallbackIDs {
		if strings.TrimSpace(fallback) != id {
			fallbacks = append(fallbacks, fallback)
		}
	}
	next.ProxyFallbackIDs = fallbacks
	stored := next
	encodedToken, err := encodeToken(stored.GitHubToken)
	if err != nil {
		return err
	}
	stored.GitHubToken = encodedToken
	if err := manager.Delete(id); err != nil {
		return err
	}
	if err := a.store.SaveSettings(stored); err != nil {
		return err
	}
	if err := a.manager.UpdateSettings(next); err != nil {
		return err
	}
	a.settings = next
	a.settingsValue.Store(next)
	return nil
}

func (a *App) TestProxyProfile(id, testURL string) (types.ProxyTestResult, error) {
	manager, err := a.proxyManager()
	if err != nil {
		return types.ProxyTestResult{}, err
	}
	ctx, cancel := a.proxyTestContext()
	defer cancel()
	result, err := manager.Test(ctx, id, testURL)
	if err != nil {
		return result, errors.New("پروفایل پروکسی پیدا نشد")
	}
	return result, nil
}

func (a *App) TestProxyInput(input types.ProxyProfileInput, testURL string) (types.ProxyTestResult, error) {
	manager, err := a.proxyManager()
	if err != nil {
		return types.ProxyTestResult{}, err
	}
	ctx, cancel := a.proxyTestContext()
	defer cancel()
	return manager.TestInput(ctx, input, testURL)
}

func (a *App) SaveSettings(settings types.Settings) (types.Settings, error) {
	if !a.ready() {
		return settings, errors.New(a.initErr)
	}
	settings.DownloadDir = strings.TrimSpace(settings.DownloadDir)
	if settings.DownloadDir == "" {
		settings.DownloadDir = defaultDownloadDir(a.dataDir)
	}
	if !filepath.IsAbs(settings.DownloadDir) {
		return settings, errors.New("download folder must be an absolute path")
	}
	if err := os.MkdirAll(settings.DownloadDir, 0o755); err != nil {
		return settings, fmt.Errorf("cannot create download folder: %w", err)
	}
	if settings.MaxConcurrent < 1 || settings.MaxConcurrent > 8 {
		settings.MaxConcurrent = 2
	}
	if settings.Connections < 1 || settings.Connections > 16 {
		settings.Connections = 8
	}
	manager, proxyErr := a.proxyManager()
	if proxyErr != nil {
		return settings, proxyErr
	}
	// Validate everything that can fail before touching stored state.
	testURL, testErr := proxy.ValidateTestURL(settings.ProxyTestURL)
	if testErr != nil {
		return settings, testErr
	}
	settings.ProxyTestURL = testURL
	settings.ProxyDefaultID = strings.TrimSpace(settings.ProxyDefaultID)
	if settings.ProxyDefaultID != "" {
		if _, ok := manager.Get(settings.ProxyDefaultID); !ok {
			return settings, errors.New("پروفایل پروکسی پیش‌فرض پیدا نشد")
		}
	}
	seen := make(map[string]bool, len(settings.ProxyFallbackIDs))
	fallbacks := make([]string, 0, len(settings.ProxyFallbackIDs))
	for _, id := range settings.ProxyFallbackIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || id == settings.ProxyDefaultID {
			continue
		}
		if _, ok := manager.Get(id); !ok {
			continue
		}
		seen[id] = true
		fallbacks = append(fallbacks, id)
	}
	settings.ProxyFallbackIDs = fallbacks
	settings.ProxyBypass = proxy.ParseBypass(settings.ProxyBypass).String()
	// A legacy free-text proxy can only come from an older database, because the
	// UI now edits profiles. Convert it into a real profile so it keeps working,
	// then clear it so it is never stored again.
	if legacy := strings.TrimSpace(settings.Proxy); legacy != "" {
		if input, parseErr := proxy.ParseLegacyProxy(legacy); parseErr == nil {
			view, found := manager.FindByEndpoint(input.Protocol, input.Host, input.Port, input.Username)
			if !found {
				if saved, saveErr := manager.Save(input); saveErr == nil {
					view = saved
				}
			}
			if view.ID != "" && settings.ProxyDefaultID == "" {
				settings.ProxyDefaultID = view.ID
			}
		}
	}
	settings.Proxy = ""
	if settings.ScheduleEnabled {
		startMinutes, ok := parseClock(settings.ScheduleStart)
		if !ok {
			return settings, errors.New("schedule start time is invalid (use HH:MM)")
		}
		stopMinutes, ok := parseClock(settings.ScheduleStop)
		if !ok {
			return settings, errors.New("schedule stop time is invalid (use HH:MM)")
		}
		if startMinutes == stopMinutes {
			return settings, errors.New("schedule start and stop must differ")
		}
	} else {
		settings.ScheduleStart = ""
		settings.ScheduleStop = ""
	}
	settings, err := normalizeBrowserSettings(settings)
	if err != nil {
		return settings, err
	}
	stored := settings
	encodedToken, err := encodeToken(settings.GitHubToken)
	if err != nil {
		return settings, fmt.Errorf("cannot protect GitHub token: %w", err)
	}
	stored.GitHubToken = encodedToken
	previous := a.settings
	if err := a.manager.UpdateSettings(settings); err != nil {
		_ = a.manager.UpdateSettings(previous)
		return settings, err
	}
	if err := a.store.SaveSettings(stored); err != nil {
		_ = a.manager.UpdateSettings(previous)
		return settings, err
	}
	a.settings = settings
	a.settingsValue.Store(settings)
	a.github.SetToken(settings.GitHubToken)
	a.configureBridge()
	a.emit("settings:update", settings)
	return settings, nil
}

func (a *App) configureBridge() {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	settings := a.settings
	key := browserBridgeKey(settings)
	if a.bridge != nil && a.bridge.Running() && a.bridgeKey == key && settings.BrowserBridgeEnabled {
		a.bridgeErr = ""
		return
	}
	if a.bridge != nil {
		_ = a.bridge.Close()
		a.bridge = nil
		a.bridgeKey = ""
	}
	a.bridgeErr = ""
	if !settings.BrowserBridgeEnabled {
		return
	}
	if a.manager == nil || a.store == nil {
		a.bridgeErr = "download manager is not ready"
		return
	}
	server := bridge.New(bridge.Config{
		Port:           settings.BrowserPort,
		CaptureMode:    settings.BrowserCaptureMode,
		Categories:     settings.BrowserCaptureCategories,
		ForwardCookies: settings.BrowserForwardCookies,
		AppVersion:     appVersion,
	}, a.manager, a.store)
	if err := server.Start(); err != nil {
		a.bridgeErr = err.Error()
		return
	}
	a.bridge = server
	a.bridgeKey = key
}

func (a *App) notifyCompleted(task types.Task) {
	settings, _ := a.settingsValue.Load().(types.Settings)
	if !settings.NotifyOnComplete {
		return
	}
	if _, loaded := a.notified.LoadOrStore(task.ID, time.Now()); loaded {
		return
	}
	time.AfterFunc(30*time.Minute, func() { a.notified.Delete(task.ID) })
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = strings.TrimSpace(task.URL)
	}
	if title == "" {
		title = "دانلود"
	}
	go func() {
		note := toast.Notification{
			AppID: toastAppID,
			Title: "دانلود کامل شد",
			Body:  title,
			Audio: toast.Default,
		}
		_ = note.Push()
	}()
}

func (a *App) ChooseDownloadDir() (string, error) {
	if a.ctx == nil {
		return "", errors.New("app is not ready")
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose download folder",
		DefaultDirectory: a.settings.DownloadDir,
	})
	if err != nil {
		return "", err
	}
	return dir, nil
}

func (a *App) OpenDownloadDir() error {
	return openFolder(a.settings.DownloadDir)
}

func (a *App) OpenTaskFile(id string) error {
	if !a.ready() {
		return errors.New(a.initErr)
	}
	t, err := a.store.GetTask(id)
	if err != nil {
		return err
	}
	if t.FilePath != "" {
		if info, err := os.Stat(t.FilePath); err == nil && !info.IsDir() {
			return exec.Command("explorer.exe", "/select,"+t.FilePath).Start()
		}
	}
	return a.openTaskDir(t)
}

func (a *App) OpenTaskDir(id string) error {
	if !a.ready() {
		return errors.New(a.initErr)
	}
	t, err := a.store.GetTask(id)
	if err != nil {
		return err
	}
	return a.openTaskDir(t)
}

func (a *App) openTaskDir(t types.Task) error {
	if filePath := strings.TrimSpace(t.FilePath); filePath != "" {
		if info, err := os.Stat(filePath); err == nil {
			if info.IsDir() {
				return openFolder(filePath)
			}
			return openFolder(filepath.Dir(filePath))
		}
	}
	dir := strings.TrimSpace(t.Dir)
	if dir == "" {
		dir = a.settings.DownloadDir
	}
	return openFolder(dir)
}

func (a *App) OpenToolsFolder() error {
	if a.tools == nil {
		return errors.New("tools not available")
	}
	dir := a.tools.ToolsDir()
	_ = os.MkdirAll(dir, 0o755)
	return openFolder(dir)
}

func (a *App) GetToolsStatus() types.ToolsStatus {
	if a.tools == nil {
		return types.ToolsStatus{}
	}
	return a.tools.Status()
}

func (a *App) SearchGitHub(query string) ([]types.GitHubRepo, error) {
	if a.github == nil {
		return nil, errors.New("GitHub client is not ready")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.github.SearchRepos(ctx, query)
}

func (a *App) GetGitHubReleases(owner, repo string) ([]types.GitHubRelease, error) {
	if a.github == nil {
		return nil, errors.New("GitHub client is not ready")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.github.Releases(ctx, owner, repo)
}

func (a *App) DownloadGitHubRepo(owner, repo, branch, title string) (types.Task, error) {
	if a.github == nil {
		return types.Task{}, errors.New("GitHub client is not ready")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	rawURL, fallback, err := a.github.RepoArchive(ctx, owner, repo, branch)
	if err != nil {
		return types.Task{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = fallback
	}
	return a.manager.Add(types.AddRequest{URL: rawURL, Engine: "http", Quality: "best", Title: title, Source: "GitHub"})
}

func (a *App) DownloadGitHubTag(owner, repo, tag, title string) (types.Task, error) {
	if a.github == nil {
		return types.Task{}, errors.New("GitHub client is not ready")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	rawURL, fallback, err := a.github.TagArchive(ctx, owner, repo, tag)
	if err != nil {
		return types.Task{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = fallback
	}
	return a.manager.Add(types.AddRequest{URL: rawURL, Engine: "http", Quality: "best", Title: title, Source: "GitHub"})
}

func (a *App) DownloadGitHubAsset(asset types.GitHubAsset, repoName, tag string) (types.Task, error) {
	if a.github == nil {
		return types.Task{}, errors.New("GitHub client is not ready")
	}
	if err := gh.ValidateAsset(asset); err != nil {
		return types.Task{}, err
	}
	title := asset.Name
	if title == "" {
		title = "GitHub asset"
	}
	source := "GitHub"
	if repoName != "" {
		source = "GitHub · " + repoName
	}
	return a.manager.Add(types.AddRequest{
		URL:     asset.DownloadURL,
		Engine:  "http",
		Quality: "best",
		Title:   title,
		Source:  source,
	})
}

func (a *App) OpenExternal(rawURL string) {
	if isHTTPURL(rawURL) && a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, rawURL)
	}
}

func (a *App) AddTorrentFile(title string) (types.Task, error) {
	if a.ctx == nil {
		return types.Task{}, errors.New("app is not ready")
	}
	if !a.ready() {
		return types.Task{}, errors.New(a.initErr)
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "انتخاب فایل تورنت",
		Filters: []runtime.FileFilter{
			{DisplayName: "Torrent (*.torrent)", Pattern: "*.torrent"},
		},
	})
	if err != nil {
		return types.Task{}, err
	}
	if strings.TrimSpace(path) == "" {
		return types.Task{}, nil
	}
	return a.manager.AddTorrentFile(path, title)
}

func browserBridgeKey(settings types.Settings) string {
	return fmt.Sprintf("%t|%d|%s|%s|%t",
		settings.BrowserBridgeEnabled,
		settings.BrowserPort,
		settings.BrowserCaptureMode,
		strings.Join(settings.BrowserCaptureCategories, ","),
		settings.BrowserForwardCookies,
	)
}

func sanitizeBrowserSettings(settings types.Settings) types.Settings {
	if settings.BrowserPort < 1024 || settings.BrowserPort > 65535 {
		settings.BrowserPort = bridge.DefaultPort
	}
	settings.BrowserCaptureMode = normalizeCaptureMode(settings.BrowserCaptureMode)
	settings.BrowserCaptureCategories = normalizeCaptureCategories(settings.BrowserCaptureCategories)
	return settings
}

func normalizeBrowserSettings(settings types.Settings) (types.Settings, error) {
	if settings.BrowserPort == 0 {
		settings.BrowserPort = bridge.DefaultPort
	}
	if settings.BrowserPort < 1024 || settings.BrowserPort > 65535 {
		return settings, errors.New("browser bridge port must be between 1024 and 65535")
	}
	settings.BrowserCaptureMode = normalizeCaptureMode(settings.BrowserCaptureMode)
	settings.BrowserCaptureCategories = normalizeCaptureCategories(settings.BrowserCaptureCategories)
	return settings, nil
}

func normalizeCaptureMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto":
		return "auto"
	case "ask":
		return "ask"
	case "off":
		return "off"
	default:
		return "auto"
	}
}

func normalizeCaptureCategories(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, 5)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case types.CategoryVideo, types.CategoryAudio, types.CategoryArchive, types.CategoryProgram, types.CategoryOther:
			if !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	if len(out) == 0 {
		return []string{types.CategoryVideo, types.CategoryAudio, types.CategoryArchive, types.CategoryProgram, types.CategoryOther}
	}
	return out
}

func parseClock(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	hour := 0
	minute := 0
	for i, ch := range value {
		if i == 2 {
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	hour = int(value[0]-'0')*10 + int(value[1]-'0')
	minute = int(value[3]-'0')*10 + int(value[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func (a *App) watchClipboard(ctx context.Context) {
	if a.ctx == nil {
		return
	}
	initial, err := runtime.ClipboardGetText(a.ctx)
	if err == nil {
		a.mu.Lock()
		a.lastClipboard = strings.TrimSpace(initial)
		a.mu.Unlock()
	}
	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			settings, _ := a.settingsValue.Load().(types.Settings)
			if !settings.ClipboardWatch {
				continue
			}
			text, err := runtime.ClipboardGetText(a.ctx)
			if err != nil {
				continue
			}
			text = strings.TrimSpace(text)
			if !isDownloadableURL(text) {
				continue
			}
			a.mu.Lock()
			if text == a.lastClipboard {
				a.mu.Unlock()
				continue
			}
			a.lastClipboard = text
			a.mu.Unlock()
			a.emit("clipboard:url", map[string]string{"url": text})
			if settings.AutoAddClipboard {
				if _, err := a.manager.Add(types.AddRequest{URL: text, Engine: "auto", Quality: "best"}); err == nil {
					a.emit("clipboard:added", nil)
				}
			}
		}
	}
}

func fitWindow(ctx context.Context) {
	screens, err := runtime.ScreenGetAll(ctx)
	if err != nil {
		return
	}
	for _, screen := range screens {
		if !screen.IsCurrent {
			continue
		}
		if screen.Size.Width < 1300 || screen.Size.Height < 900 {
			runtime.WindowMaximise(ctx)
		}
		return
	}
}

func decodeToken(value string) string {
	if value == "" || !strings.HasPrefix(value, secure.Prefix) {
		return value
	}
	token, err := secure.Unprotect(value)
	if err != nil {
		return ""
	}
	return token
}

func encodeToken(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return secure.Protect(value)
}

func appDataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "CYRON"), nil
}

func defaultDownloadDir(dataDir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads", "CYRON")
	}
	return filepath.Join(dataDir, "downloads")
}

func emptyHistory() []types.SpeedPoint {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -6)
	out := make([]types.SpeedPoint, 0, 7)
	for i := 0; i < 7; i++ {
		out = append(out, types.SpeedPoint{Day: start.AddDate(0, 0, i).Format("2006-01-02")})
	}
	return out
}

func openFolder(dir string) error {
	if dir == "" {
		return errors.New("folder is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return exec.Command("explorer.exe", dir).Start()
}

func isDownloadableURL(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return false
	}
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "magnet:") || isHTTPURL(value)
}

func isHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
