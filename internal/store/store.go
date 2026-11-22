package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/types"
)

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, "downloads.db")
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) RawDB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			url TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			engine TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			quality TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			gid TEXT NOT NULL DEFAULT '',
			file_path TEXT NOT NULL DEFAULT '',
			dir TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			progress REAL NOT NULL DEFAULT 0,
			downloaded INTEGER NOT NULL DEFAULT 0,
			total INTEGER NOT NULL DEFAULT 0,
			speed INTEGER NOT NULL DEFAULT 0,
			eta INTEGER NOT NULL DEFAULT 0,
			container TEXT NOT NULL DEFAULT '',
			subtitles INTEGER NOT NULL DEFAULT 0,
			playlist INTEGER NOT NULL DEFAULT 0,
			connections INTEGER NOT NULL DEFAULT 0,
			retries INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			md5 TEXT NOT NULL DEFAULT '',
			referrer TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			suggested_name TEXT NOT NULL DEFAULT '',
			browser_cookies TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			started_at INTEGER NOT NULL DEFAULT 0,
			completed_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE TABLE IF NOT EXISTS proxy_profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			password_protected TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS speed_samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			speed INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_speed_ts ON speed_samples(ts)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	columns := map[string]string{
		"category":           "TEXT NOT NULL DEFAULT ''",
		"priority":           "INTEGER NOT NULL DEFAULT 0",
		"container":          "TEXT NOT NULL DEFAULT ''",
		"subtitles":          "INTEGER NOT NULL DEFAULT 0",
		"playlist":           "INTEGER NOT NULL DEFAULT 0",
		"connections":        "INTEGER NOT NULL DEFAULT 0",
		"retries":            "INTEGER NOT NULL DEFAULT 0",
		"username":           "TEXT NOT NULL DEFAULT ''",
		"password":           "TEXT NOT NULL DEFAULT ''",
		"cookies":            "TEXT NOT NULL DEFAULT ''",
		"sha256":             "TEXT NOT NULL DEFAULT ''",
		"md5":                "TEXT NOT NULL DEFAULT ''",
		"referrer":           "TEXT NOT NULL DEFAULT ''",
		"user_agent":         "TEXT NOT NULL DEFAULT ''",
		"mime_type":          "TEXT NOT NULL DEFAULT ''",
		"suggested_name":     "TEXT NOT NULL DEFAULT ''",
		"browser_cookies":    "TEXT NOT NULL DEFAULT ''",
		"started_at":         "INTEGER NOT NULL DEFAULT 0",
		"completed_at":       "INTEGER NOT NULL DEFAULT 0",
		"proxy_mode":         "TEXT NOT NULL DEFAULT 'inherit'",
		"proxy_profile_id":   "TEXT NOT NULL DEFAULT ''",
		"proxy_profile_name": "TEXT NOT NULL DEFAULT ''",
	}
	for name, definition := range columns {
		exists, err := s.hasColumn("tasks", name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.Exec("ALTER TABLE tasks ADD COLUMN " + name + " " + definition); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`UPDATE tasks SET completed_at=updated_at WHERE status=? AND completed_at=0`, types.StatusCompleted)
	if err != nil {
		return err
	}
	return s.encryptLegacySecrets()
}

func (s *Store) encryptLegacySecrets() error {
	rows, err := s.db.Query(`SELECT id,password,cookies FROM tasks
		WHERE (password<>'' AND password NOT LIKE 'dpapi:%' AND password NOT LIKE 'aes:%')
		   OR (cookies<>'' AND cookies NOT LIKE 'dpapi:%' AND cookies NOT LIKE 'aes:%')`)
	if err != nil {
		return err
	}
	type pending struct {
		id, password, cookies string
	}
	var updates []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.password, &p.cookies); err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range updates {
		password, err := protectSecret(p.password)
		if err != nil {
			return err
		}
		cookies, err := protectSecret(p.cookies)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE tasks SET password=?, cookies=? WHERE id=?`, password, cookies, p.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) SaveTask(t *types.Task) error {
	browserCookies, err := protectBrowserCookies(t.BrowserCookies)
	if err != nil {
		return err
	}
	password, err := protectSecret(t.Password)
	if err != nil {
		return err
	}
	cookies, err := protectSecret(t.Cookies)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO tasks (
		id,url,title,engine,kind,status,quality,category,priority,source,gid,file_path,dir,error,
		progress,downloaded,total,speed,eta,container,subtitles,playlist,connections,retries,
		proxy_mode,proxy_profile_id,proxy_profile_name,
		username,password,cookies,sha256,md5,referrer,user_agent,mime_type,suggested_name,browser_cookies,
		created_at,started_at,completed_at,updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET
		url=excluded.url, title=excluded.title, engine=excluded.engine, kind=excluded.kind,
		status=excluded.status, quality=excluded.quality, category=excluded.category,
		priority=excluded.priority, source=excluded.source, gid=excluded.gid,
		file_path=excluded.file_path, dir=excluded.dir, error=excluded.error,
		progress=excluded.progress, downloaded=excluded.downloaded, total=excluded.total,
		speed=excluded.speed, eta=excluded.eta, container=excluded.container,
		subtitles=excluded.subtitles, playlist=excluded.playlist, connections=excluded.connections,
		retries=excluded.retries, proxy_mode=excluded.proxy_mode,
		proxy_profile_id=excluded.proxy_profile_id, proxy_profile_name=excluded.proxy_profile_name,
		username=excluded.username, password=excluded.password,
		cookies=excluded.cookies, sha256=excluded.sha256, md5=excluded.md5,
		referrer=excluded.referrer, user_agent=excluded.user_agent, mime_type=excluded.mime_type,
		suggested_name=excluded.suggested_name, browser_cookies=excluded.browser_cookies,
		started_at=excluded.started_at, completed_at=excluded.completed_at, updated_at=excluded.updated_at`,
		t.ID, t.URL, t.Title, t.Engine, t.Kind, t.Status, t.Quality, t.Category, t.Priority,
		t.Source, t.GID, t.FilePath, t.Dir, t.Error, t.Progress, t.Downloaded, t.Total, t.Speed, t.ETA,
		t.Container, t.Subtitles, t.Playlist, t.Connections, t.Retries,
		t.ProxyMode, t.ProxyProfileID, t.ProxyProfileName,
		t.Username, password, cookies, t.SHA256, t.MD5,
		t.Referrer, t.UserAgent, t.MimeType, t.SuggestedName, browserCookies,
		t.CreatedAt, t.StartedAt, t.CompletedAt, t.UpdatedAt)
	return err
}

func (s *Store) UpdateTask(t *types.Task) error {
	browserCookies, err := protectBrowserCookies(t.BrowserCookies)
	if err != nil {
		return err
	}
	password, err := protectSecret(t.Password)
	if err != nil {
		return err
	}
	cookies, err := protectSecret(t.Cookies)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE tasks SET
		url=?, title=?, engine=?, kind=?, status=?, quality=?, category=?, priority=?,
		source=?, gid=?, file_path=?, dir=?, error=?, progress=?, downloaded=?, total=?, speed=?, eta=?,
		container=?, subtitles=?, playlist=?, connections=?, retries=?,
		proxy_mode=?, proxy_profile_id=?, proxy_profile_name=?,
		username=?, password=?, cookies=?, sha256=?, md5=?, referrer=?, user_agent=?, mime_type=?,
		suggested_name=?, browser_cookies=?,
		started_at=?, completed_at=?, updated_at=? WHERE id=?`,
		t.URL, t.Title, t.Engine, t.Kind, t.Status, t.Quality, t.Category, t.Priority,
		t.Source, t.GID, t.FilePath, t.Dir, t.Error, t.Progress, t.Downloaded, t.Total, t.Speed, t.ETA,
		t.Container, t.Subtitles, t.Playlist, t.Connections, t.Retries,
		t.ProxyMode, t.ProxyProfileID, t.ProxyProfileName,
		t.Username, password, cookies, t.SHA256, t.MD5,
		t.Referrer, t.UserAgent, t.MimeType, t.SuggestedName, browserCookies,
		t.StartedAt, t.CompletedAt, t.UpdatedAt, t.ID)
	return err
}

func (s *Store) UpdatePriority(id string, priority int, updatedAt int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET priority=?, updated_at=? WHERE id=?`, priority, updatedAt, id)
	return err
}

func (s *Store) UpdateHashes(id, sha256, md5 string, size, updatedAt int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET sha256=?, md5=?, downloaded=?,
		total=CASE WHEN total > ? THEN total ELSE ? END, updated_at=? WHERE id=?`,
		sha256, md5, size, size, size, updatedAt, id)
	return err
}

func (s *Store) UpdateProgress(id string, downloaded, total, speed, eta int64, progress float64, updatedAt int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET downloaded=?, total=?, speed=?, eta=?, progress=?, updated_at=? WHERE id=?`,
		downloaded, total, speed, eta, progress, updatedAt, id)
	return err
}

func (s *Store) UpdateTitle(id, title string, updatedAt int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET title=?, updated_at=? WHERE id=?`, title, updatedAt, id)
	return err
}

func (s *Store) MarkActive(id, gid string, updatedAt int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET gid=?, status=?, error='', completed_at=0,
		started_at=CASE WHEN started_at=0 THEN ? ELSE started_at END, updated_at=?
		WHERE id=? AND status=?`,
		gid, types.StatusActive, updatedAt, updatedAt, id, types.StatusQueued)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) CompleteTask(id, filePath string, downloaded, total, updatedAt int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET status=?, progress=100, speed=0, eta=0, error='', file_path=?,
		downloaded=?, total=?, completed_at=?, updated_at=?, browser_cookies='', password='', cookies=''
		WHERE id=? AND status=?`,
		types.StatusCompleted, filePath, downloaded, total, updatedAt, updatedAt, id, types.StatusActive)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) GetTask(id string) (types.Task, error) {
	var t types.Task
	var subtitles, playlist int
	var browserCookies, password, cookies string
	err := s.db.QueryRow(`SELECT id,url,title,engine,kind,status,quality,category,priority,source,gid,file_path,dir,error,
		progress,downloaded,total,speed,eta,container,subtitles,playlist,connections,retries,
		proxy_mode,proxy_profile_id,proxy_profile_name,
		username,password,cookies,sha256,md5,referrer,user_agent,mime_type,suggested_name,browser_cookies,
		created_at,started_at,completed_at,updated_at FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.URL, &t.Title, &t.Engine, &t.Kind, &t.Status, &t.Quality, &t.Category, &t.Priority,
			&t.Source, &t.GID,
			&t.FilePath, &t.Dir, &t.Error, &t.Progress, &t.Downloaded, &t.Total, &t.Speed, &t.ETA,
			&t.Container, &subtitles, &playlist, &t.Connections, &t.Retries,
			&t.ProxyMode, &t.ProxyProfileID, &t.ProxyProfileName,
			&t.Username, &password, &cookies, &t.SHA256, &t.MD5,
			&t.Referrer, &t.UserAgent, &t.MimeType, &t.SuggestedName, &browserCookies,
			&t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.UpdatedAt)
	t.Subtitles = subtitles == 1
	t.Playlist = playlist == 1
	t.BrowserCookies = revealBrowserCookies(browserCookies)
	t.Password = revealSecret(password)
	t.Cookies = revealSecret(cookies)
	return t, err
}

func (s *Store) ListTasks(limit int) ([]types.Task, error) {
	query := `SELECT id,url,title,engine,kind,status,quality,category,priority,source,gid,file_path,dir,error,
		progress,downloaded,total,speed,eta,container,subtitles,playlist,connections,retries,
		proxy_mode,proxy_profile_id,proxy_profile_name,
		username,password,cookies,sha256,md5,referrer,user_agent,mime_type,suggested_name,browser_cookies,
		created_at,started_at,completed_at,updated_at FROM tasks ORDER BY created_at DESC`
	args := make([]any, 0, 1)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]types.Task, 0)
	for rows.Next() {
		var t types.Task
		var subtitles, playlist int
		var browserCookies, password, cookies string
		if err := rows.Scan(&t.ID, &t.URL, &t.Title, &t.Engine, &t.Kind, &t.Status, &t.Quality,
			&t.Category, &t.Priority, &t.Source, &t.GID, &t.FilePath, &t.Dir, &t.Error,
			&t.Progress, &t.Downloaded, &t.Total, &t.Speed, &t.ETA, &t.Container, &subtitles,
			&playlist, &t.Connections, &t.Retries,
			&t.ProxyMode, &t.ProxyProfileID, &t.ProxyProfileName,
			&t.Username, &password, &cookies,
			&t.SHA256, &t.MD5, &t.Referrer, &t.UserAgent, &t.MimeType, &t.SuggestedName,
			&browserCookies, &t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Subtitles = subtitles == 1
		t.Playlist = playlist == 1
		t.BrowserCookies = revealBrowserCookies(browserCookies)
		t.Password = revealSecret(password)
		t.Cookies = revealSecret(cookies)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) CountPending() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status IN (?,?)`,
		types.StatusActive, types.StatusQueued).Scan(&count)
	return count, err
}

func (s *Store) DeleteTask(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

func (s *Store) ResetRunningTasks() error {
	_, err := s.db.Exec(`UPDATE tasks SET status=?, speed=0, eta=0, updated_at=? WHERE status IN (?,?)`,
		types.StatusPaused, time.Now().UnixMilli(), types.StatusActive, types.StatusQueued)
	return err
}

func (s *Store) Stats() (types.Stats, error) {
	var st types.Stats
	rows, err := s.db.Query(`SELECT status, COUNT(*), COALESCE(SUM(total),0), COALESCE(SUM(speed),0) FROM tasks GROUP BY status`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		var total, speed int64
		if err := rows.Scan(&status, &count, &total, &speed); err != nil {
			return st, err
		}
		st.Total += count
		st.Speed += speed
		switch status {
		case types.StatusActive:
			st.Active += count
		case types.StatusQueued:
			st.Queued += count
		case types.StatusPaused:
			st.Paused += count
		case types.StatusCompleted:
			st.Completed += count
			st.CompletedBytes += total
		case types.StatusError:
			st.Errors += count
		}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	_ = rows.Close()
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(total),0) FROM tasks WHERE status=? AND completed_at>=?`,
		types.StatusCompleted, dayStart.UnixMilli()).Scan(&st.DailyBytes)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(total),0) FROM tasks WHERE status=? AND completed_at>=?`,
		types.StatusCompleted, monthStart.UnixMilli()).Scan(&st.MonthlyBytes)
	_ = s.db.QueryRow(`SELECT CAST(COALESCE(SUM(total),0) / NULLIF(SUM(
		CASE WHEN completed_at>started_at THEN (completed_at-started_at)/1000 ELSE 0 END),0) AS INTEGER)
		FROM tasks WHERE status=? AND completed_at>started_at AND started_at>0`, types.StatusCompleted).Scan(&st.AvgSpeed)
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(speed),0) FROM speed_samples WHERE ts>=?`,
		now.Add(-30*24*time.Hour).UnixMilli()).Scan(&st.PeakSpeed)
	return st, nil
}

func (s *Store) AddSpeedSample(ts int64, speed int64) error {
	_, err := s.db.Exec(`INSERT INTO speed_samples (ts, speed) VALUES (?,?)`, ts, speed)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	_, _ = s.db.Exec(`DELETE FROM speed_samples WHERE ts < ?`, cutoff)
	return nil
}

func (s *Store) DailySpeeds(from, to int64) ([]types.SpeedPoint, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', ts/1000, 'unixepoch', 'localtime') as day, MAX(speed)
		FROM speed_samples WHERE ts >= ? AND ts < ? GROUP BY day ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]types.SpeedPoint, 0, 7)
	for rows.Next() {
		var p types.SpeedPoint
		if err := rows.Scan(&p.Day, &p.Speed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func defaultSettings(downloadDir string) types.Settings {
	return types.Settings{
		DownloadDir:              downloadDir,
		MaxConcurrent:            2,
		Connections:              8,
		ProxyTestURL:             types.DefaultProxyTestURL,
		ClipboardWatch:           true,
		AutoAddClipboard:         false,
		AutoStart:                true,
		AutoRetry:                true,
		NotifyOnComplete:         true,
		BrowserBridgeEnabled:     true,
		BrowserPort:              DefaultBrowserPort,
		BrowserCaptureMode:       "auto",
		BrowserCaptureCategories: defaultBrowserCategories(),
	}
}

const DefaultBrowserPort = 17872

func defaultBrowserCategories() []string {
	return []string{
		types.CategoryVideo,
		types.CategoryAudio,
		types.CategoryArchive,
		types.CategoryProgram,
		types.CategoryOther,
	}
}

func (s *Store) GetSettings(downloadDir string) (types.Settings, error) {
	settings := defaultSettings(downloadDir)
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "downloadDir":
			settings.DownloadDir = value
		case "maxConcurrent":
			_ = json.Unmarshal([]byte(value), &settings.MaxConcurrent)
		case "connections":
			_ = json.Unmarshal([]byte(value), &settings.Connections)
		case "proxy":
			settings.Proxy = value
		case "proxyDefaultId":
			settings.ProxyDefaultID = value
		case "proxyBypass":
			settings.ProxyBypass = value
		case "proxyTestUrl":
			settings.ProxyTestURL = value
		case "proxyFallbackIds":
			_ = json.Unmarshal([]byte(value), &settings.ProxyFallbackIDs)
		case "proxyAllowDirectFallback":
			_ = json.Unmarshal([]byte(value), &settings.ProxyAllowDirectFallback)
		case "clipboardWatch":
			_ = json.Unmarshal([]byte(value), &settings.ClipboardWatch)
		case "autoAddClipboard":
			_ = json.Unmarshal([]byte(value), &settings.AutoAddClipboard)
		case "autoStart":
			_ = json.Unmarshal([]byte(value), &settings.AutoStart)
		case "autoRetry":
			_ = json.Unmarshal([]byte(value), &settings.AutoRetry)
		case "shutdownOnComplete":
			_ = json.Unmarshal([]byte(value), &settings.ShutdownOnComplete)
		case "scheduleEnabled":
			_ = json.Unmarshal([]byte(value), &settings.ScheduleEnabled)
		case "scheduleStart":
			settings.ScheduleStart = value
		case "scheduleStop":
			settings.ScheduleStop = value
		case "githubToken":
			settings.GitHubToken = value
		case "notifyOnComplete":
			_ = json.Unmarshal([]byte(value), &settings.NotifyOnComplete)
		case "browserBridgeEnabled":
			_ = json.Unmarshal([]byte(value), &settings.BrowserBridgeEnabled)
		case "browserPort":
			_ = json.Unmarshal([]byte(value), &settings.BrowserPort)
		case "browserCaptureMode":
			settings.BrowserCaptureMode = value
		case "browserCaptureCategories":
			_ = json.Unmarshal([]byte(value), &settings.BrowserCaptureCategories)
		case "browserForwardCookies":
			_ = json.Unmarshal([]byte(value), &settings.BrowserForwardCookies)
		}
	}
	if settings.DownloadDir == "" {
		settings.DownloadDir = downloadDir
	}
	if strings.TrimSpace(settings.ProxyTestURL) == "" {
		settings.ProxyTestURL = types.DefaultProxyTestURL
	}
	return settings, rows.Err()
}

func (s *Store) SaveSettings(settings types.Settings) error {
	values := map[string]string{
		"downloadDir":              settings.DownloadDir,
		"maxConcurrent":            mustJSON(settings.MaxConcurrent),
		"connections":              mustJSON(settings.Connections),
		"proxy":                    settings.Proxy,
		"proxyDefaultId":           settings.ProxyDefaultID,
		"proxyBypass":              settings.ProxyBypass,
		"proxyTestUrl":             settings.ProxyTestURL,
		"proxyFallbackIds":         mustJSON(settings.ProxyFallbackIDs),
		"proxyAllowDirectFallback": mustJSON(settings.ProxyAllowDirectFallback),
		"clipboardWatch":           mustJSON(settings.ClipboardWatch),
		"autoAddClipboard":         mustJSON(settings.AutoAddClipboard),
		"autoStart":                mustJSON(settings.AutoStart),
		"autoRetry":                mustJSON(settings.AutoRetry),
		"shutdownOnComplete":       mustJSON(settings.ShutdownOnComplete),
		"scheduleEnabled":          mustJSON(settings.ScheduleEnabled),
		"scheduleStart":            settings.ScheduleStart,
		"scheduleStop":             settings.ScheduleStop,
		"githubToken":              settings.GitHubToken,
		"notifyOnComplete":         mustJSON(settings.NotifyOnComplete),
		"browserBridgeEnabled":     mustJSON(settings.BrowserBridgeEnabled),
		"browserPort":              mustJSON(settings.BrowserPort),
		"browserCaptureMode":       settings.BrowserCaptureMode,
		"browserCaptureCategories": mustJSON(settings.BrowserCaptureCategories),
		"browserForwardCookies":    mustJSON(settings.BrowserForwardCookies),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for key, value := range values {
		if _, err := tx.Exec(`INSERT INTO settings (key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func protectBrowserCookies(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return secure.Protect(value)
}

func protectSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if secure.IsProtected(value) {
		return value, nil
	}
	return secure.Protect(value)
}

func revealSecret(value string) string {
	if value == "" {
		return ""
	}
	if !secure.IsProtected(value) {
		return value
	}
	plain, err := secure.Unprotect(value)
	if err != nil {
		return ""
	}
	return plain
}

func revealBrowserCookies(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	plain, err := secure.Unprotect(value)
	if err != nil {
		return ""
	}
	return plain
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
