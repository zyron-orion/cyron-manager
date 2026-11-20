package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/types"
)

const (
	DefaultPort = 17872
	ExtensionID = "fapkmnilijgofgmeakjjeofaaohgdaoe"
	APIVersion  = 1
)

type Adder interface {
	AddChecked(types.AddRequest) (types.Task, bool, error)
}

type TaskLister interface {
	ListTasks(int) ([]types.Task, error)
}

type Config struct {
	Port           int
	CaptureMode    string
	Categories     []string
	ForwardCookies bool
	AppVersion     string
}

type Server struct {
	adder  Adder
	lister TaskLister
	config Config
	origin string
	host   string

	limiter rateLimiter

	mu      sync.Mutex
	server  *http.Server
	running bool
	err     string
}

type rateLimiter struct {
	mu     sync.Mutex
	window time.Time
	count  int
	limit  int
}

func (l *rateLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		l.limit = 120
	}
	if l.window.IsZero() || now.Sub(l.window) >= time.Minute {
		l.window = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

func New(config Config, adder Adder, lister TaskLister) *Server {
	if config.Port < 1 || config.Port > 65535 {
		config.Port = DefaultPort
	}
	config.CaptureMode = normalizeMode(config.CaptureMode)
	config.Categories = normalizeCategories(config.Categories)
	config.AppVersion = strings.TrimSpace(config.AppVersion)
	return &Server{
		adder:  adder,
		lister: lister,
		config: config,
		origin: "chrome-extension://" + ExtensionID,
		host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port)),
	}
}

func Address(port int) string {
	if port < 1 || port > 65535 {
		port = DefaultPort
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp4", s.host)
	if err != nil {
		s.mu.Lock()
		s.err = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("browser bridge port %s is unavailable: %w", s.host, err)
	}
	server := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	s.mu.Lock()
	s.server = server
	s.running = true
	s.err = ""
	s.mu.Unlock()
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.mu.Lock()
			s.running = false
			s.err = serveErr.Error()
			s.mu.Unlock()
		}
	}()
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.running = false
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Server) Error() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/config", s.configuration)
	mux.HandleFunc("/v1/capture/check", s.captureCheck)
	mux.HandleFunc("/v1/downloads", s.downloads)
	mux.HandleFunc("/v1/status", s.status)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == s.origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			if origin != s.origin {
				writeError(w, http.StatusForbidden, "origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.validHost(r.Host) {
			writeError(w, http.StatusForbidden, "host is not allowed")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) validHost(host string) bool {
	return strings.EqualFold(strings.TrimSpace(host), s.host)
}

func (s *Server) requireOrigin(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Origin")) != s.origin {
		writeError(w, http.StatusForbidden, "origin is not allowed")
		return false
	}
	return true
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     "CYRON Manager",
		"version": s.config.AppVersion,
		"api":     APIVersion,
	})
}

func (s *Server) configuration(w http.ResponseWriter, r *http.Request) {
	if !s.requireOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireJSON(w, r) {
		return
	}
	if err := decodeJSON(w, r, &struct{}{}); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"captureMode":    s.config.CaptureMode,
		"categories":     s.config.Categories,
		"forwardCookies": s.config.ForwardCookies,
		"appVersion":     s.config.AppVersion,
		"api":            APIVersion,
	})
}

type captureCheckRequest struct {
	URL        string `json:"url"`
	MimeType   string `json:"mimeType"`
	Filename   string `json:"filename"`
	TotalBytes int64  `json:"totalBytes"`
}

func (s *Server) captureCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireJSON(w, r) {
		return
	}
	if !s.limiter.allow(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var request captureCheckRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validHTTPURL(request.URL) {
		writeError(w, http.StatusBadRequest, "link is not a valid http or https URL")
		return
	}
	category := captureCategory(request.URL, request.MimeType, request.Filename)
	action := "download"
	reason := "auto"
	switch {
	case s.config.CaptureMode == "off":
		action = "skip"
		reason = "capture_off"
	case !s.categoryAllowed(category):
		action = "skip"
		reason = "category_disabled"
	case s.config.CaptureMode == "ask":
		action = "ask"
		reason = "ask"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action":   action,
		"reason":   reason,
		"category": category,
	})
}

func (s *Server) downloads(w http.ResponseWriter, r *http.Request) {
	if !s.requireOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireJSON(w, r) {
		return
	}
	if !s.limiter.allow(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	if s.adder == nil {
		writeError(w, http.StatusServiceUnavailable, "download manager is not ready")
		return
	}
	var request types.AddRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	request.URL = strings.TrimSpace(request.URL)
	if !validHTTPURL(request.URL) {
		writeError(w, http.StatusBadRequest, "link is not a valid http or https URL")
		return
	}
	request.Engine = normalizeEngine(request.Engine)
	request.Category = normalizeCategory(request.Category)
	request.Source = "Browser"
	request.Browser = true
	request.AllowDuplicate = false
	request.Username = ""
	request.Password = ""
	request.Title = limitString(request.Title, 512)
	request.Referrer = limitString(request.Referrer, 2048)
	if !validHTTPURL(request.Referrer) {
		request.Referrer = ""
	}
	request.UserAgent = limitString(request.UserAgent, 1024)
	request.MimeType = limitString(request.MimeType, 255)
	request.SuggestedName = limitString(safeName(request.SuggestedName), 255)
	if !s.config.ForwardCookies {
		request.Cookies = ""
	} else {
		request.Cookies = limitString(strings.TrimSpace(request.Cookies), 64*1024)
	}
	task, duplicate, err := s.adder.AddChecked(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.AddResult{Task: task, Duplicate: duplicate})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !s.requireOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireJSON(w, r) {
		return
	}
	if err := decodeJSON(w, r, &struct{}{}); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if s.lister == nil {
		writeError(w, http.StatusServiceUnavailable, "download list is not ready")
		return
	}
	tasks, err := s.lister.ListTasks(30)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read downloads")
		return
	}
	out := make([]types.BrowserTaskStatus, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, types.BrowserTaskStatus{
			ID:         task.ID,
			Title:      task.Title,
			Status:     task.Status,
			Progress:   task.Progress,
			Downloaded: task.Downloaded,
			Total:      task.Total,
			Speed:      task.Speed,
			ETA:        task.ETA,
			Category:   task.Category,
			Error:      task.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) requireJSON(w http.ResponseWriter, r *http.Request) bool {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return false
	}
	return true
}

func (s *Server) categoryAllowed(category string) bool {
	for _, allowed := range s.config.Categories {
		if allowed == category {
			return true
		}
	}
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one object")
	}
	return nil
}

func validHTTPURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 8192 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func normalizeMode(value string) string {
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

func normalizeEngine(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "http", "aria2", "ytdlp":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func normalizeCategory(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case types.CategoryVideo, types.CategoryAudio, types.CategoryArchive, types.CategoryProgram, types.CategoryOther:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeCategories(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, 5)
	for _, value := range values {
		category := normalizeCategory(value)
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	if len(out) == 0 {
		for _, category := range []string{types.CategoryVideo, types.CategoryAudio, types.CategoryArchive, types.CategoryProgram, types.CategoryOther} {
			out = append(out, category)
		}
	}
	return out
}

func captureCategory(rawURL, mimeType, filename string) string {
	name := strings.ToLower(strings.TrimSpace(filename))
	if name == "" {
		if parsed, err := url.Parse(rawURL); err == nil {
			name = path.Base(parsed.Path)
		}
	}
	extension := strings.TrimPrefix(path.Ext(strings.ReplaceAll(name, "\\", "/")), ".")
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if index := strings.IndexByte(mimeType, ';'); index >= 0 {
		mimeType = strings.TrimSpace(mimeType[:index])
	}
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return types.CategoryVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return types.CategoryAudio
	case mimeType == "application/zip",
		mimeType == "application/x-rar-compressed",
		mimeType == "application/vnd.rar",
		mimeType == "application/x-7z-compressed",
		mimeType == "application/x-tar",
		mimeType == "application/gzip",
		mimeType == "application/x-bzip2",
		mimeType == "application/x-xz":
		return types.CategoryArchive
	case mimeType == "application/vnd.microsoft.portable-executable",
		mimeType == "application/x-msdownload",
		mimeType == "application/x-msi",
		mimeType == "application/vnd.android.package-archive",
		mimeType == "application/x-deb",
		mimeType == "application/x-rpm":
		return types.CategoryProgram
	}
	switch extension {
	case "mp4", "mkv", "webm", "avi", "mov", "m4v", "flv", "ts", "mpeg", "mpg":
		return types.CategoryVideo
	case "mp3", "m4a", "flac", "wav", "ogg", "opus", "aac":
		return types.CategoryAudio
	case "zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso", "dmg":
		return types.CategoryArchive
	case "exe", "msi", "apk", "deb", "rpm", "appimage", "pkg":
		return types.CategoryProgram
	}
	return types.CategoryOther
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	value = path.Base(strings.ReplaceAll(value, "\\", "/"))
	if value == "." || value == ".." || value == "/" {
		return ""
	}
	return value
}

func limitString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
