package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/types"
)

func TestSettingsBrowserDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	settings, err := s.GetSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.NotifyOnComplete {
		t.Fatal("notifyOnComplete should default to true")
	}
	if !settings.BrowserBridgeEnabled {
		t.Fatal("browserBridgeEnabled should default to true")
	}
	if settings.BrowserPort != DefaultBrowserPort {
		t.Fatalf("browserPort = %d, want %d", settings.BrowserPort, DefaultBrowserPort)
	}
	if settings.BrowserCaptureMode != "auto" {
		t.Fatalf("browserCaptureMode = %q, want auto", settings.BrowserCaptureMode)
	}
	if len(settings.BrowserCaptureCategories) != 5 {
		t.Fatalf("categories = %v, want all five", settings.BrowserCaptureCategories)
	}
	if settings.BrowserForwardCookies {
		t.Fatal("browserForwardCookies should default to false")
	}

	settings.NotifyOnComplete = false
	settings.BrowserBridgeEnabled = false
	settings.BrowserPort = 19000
	settings.BrowserCaptureMode = "ask"
	settings.BrowserCaptureCategories = []string{types.CategoryVideo, types.CategoryAudio}
	settings.BrowserForwardCookies = true
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NotifyOnComplete {
		t.Fatal("notifyOnComplete did not persist")
	}
	if loaded.BrowserBridgeEnabled {
		t.Fatal("browserBridgeEnabled did not persist")
	}
	if loaded.BrowserPort != 19000 {
		t.Fatalf("browserPort = %d, want 19000", loaded.BrowserPort)
	}
	if loaded.BrowserCaptureMode != "ask" {
		t.Fatalf("browserCaptureMode = %q, want ask", loaded.BrowserCaptureMode)
	}
	if len(loaded.BrowserCaptureCategories) != 2 || loaded.BrowserCaptureCategories[0] != types.CategoryVideo || loaded.BrowserCaptureCategories[1] != types.CategoryAudio {
		t.Fatalf("categories = %v", loaded.BrowserCaptureCategories)
	}
	if !loaded.BrowserForwardCookies {
		t.Fatal("browserForwardCookies did not persist")
	}
}

func TestTaskCategoryPriorityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &types.Task{
		ID: "task-1", URL: "https://example.com/movie.mkv", Title: "movie.mkv",
		Dir: dir, Kind: "file", Status: types.StatusQueued,
		Category: types.CategoryVideo, Priority: 7,
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Category != types.CategoryVideo || loaded.Priority != 7 {
		t.Fatalf("loaded category/priority = %q/%d", loaded.Category, loaded.Priority)
	}

	task.Category = types.CategoryArchive
	task.Priority = 2
	if err := s.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Category != types.CategoryArchive || loaded.Priority != 2 {
		t.Fatalf("updated category/priority = %q/%d", loaded.Category, loaded.Priority)
	}

	tasks, err := s.ListTasks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Category != types.CategoryArchive || tasks[0].Priority != 2 {
		t.Fatalf("unexpected list: %+v", tasks)
	}
}

func TestBrowserCookiesAreProtectedAndCleared(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &types.Task{
		ID: "browser-1", URL: "https://example.com/movie.mkv", Title: "movie.mkv",
		Dir: dir, Kind: "file", Status: types.StatusActive, Category: types.CategoryVideo,
		BrowserCookies: "sid=secret; theme=dark",
		Referrer:       "https://example.com/watch",
		UserAgent:      "Mozilla/5.0",
		MimeType:       "video/x-matroska",
		SuggestedName:  "movie.mkv",
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT browser_cookies FROM tasks WHERE id=?`, task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "" || strings.Contains(raw, "sid=secret") {
		t.Fatalf("browser cookies were not protected: %q", raw)
	}
	if !strings.HasPrefix(raw, secure.Prefix) {
		t.Fatalf("browser cookies prefix = %q, want %q", raw, secure.Prefix)
	}

	loaded, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BrowserCookies != task.BrowserCookies {
		t.Fatalf("browserCookies = %q", loaded.BrowserCookies)
	}
	if loaded.Referrer != task.Referrer || loaded.UserAgent != task.UserAgent || loaded.MimeType != task.MimeType || loaded.SuggestedName != task.SuggestedName {
		t.Fatalf("browser metadata did not round trip: %+v", loaded)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "browserCookies") || strings.Contains(string(encoded), "sid=secret") {
		t.Fatalf("browser cookies leaked into JSON: %s", encoded)
	}

	ok, err := s.CompleteTask(task.ID, filepath.Join(dir, "movie.mkv"), 100, 100, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("complete task returned false")
	}
	loaded, err = s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BrowserCookies != "" {
		t.Fatalf("browser cookies were not cleared: %q", loaded.BrowserCookies)
	}
	if err := s.db.QueryRow(`SELECT browser_cookies FROM tasks WHERE id=?`, task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Fatalf("stored browser cookies = %q, want empty", raw)
	}
}
