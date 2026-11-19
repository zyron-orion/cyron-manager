package store

import (
	"path/filepath"
	"testing"

	"cyronmanager/internal/types"
)

func TestSettingsBridgeDefaultsAndRoundTrip(t *testing.T) {
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

	settings.NotifyOnComplete = false
	settings.BrowserBridgeEnabled = false
	settings.BrowserToken = "token-1"
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
	if loaded.BrowserToken != "token-1" {
		t.Fatalf("browserToken = %q, want token-1", loaded.BrowserToken)
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
