package store

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cyronmanager/internal/types"
)

func TestCountPendingLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	task := &types.Task{ID: "t1", URL: "https://example.com/file.zip", Title: "file.zip", Dir: dir, Kind: "http", Status: types.StatusQueued}
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	count, err := s.CountPending()
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pending after queueing, got %d", count)
	}

	ok, err := s.MarkActive("t1", "gid-1", 100)
	if err != nil || !ok {
		t.Fatalf("mark active: ok=%v err=%v", ok, err)
	}

	count, err = s.CountPending()
	if err != nil {
		t.Fatalf("count pending after start: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pending while active, got %d", count)
	}

	ok, err = s.CompleteTask("t1", filepath.Join(dir, "file.zip"), 10, 10, 123)
	if err != nil || !ok {
		t.Fatalf("complete task: ok=%v err=%v", ok, err)
	}

	count, err = s.CountPending()
	if err != nil {
		t.Fatalf("count pending after completion: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 pending after completion, got %d", count)
	}
}

func TestCompletedTaskTimestampsAndStats(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	task := &types.Task{
		ID: "done-1", URL: "https://example.com/file.bin", Title: "file.bin", Dir: dir,
		Engine: "http", Kind: "file", Status: types.StatusQueued, CreatedAt: now - 20000,
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkActive(task.ID, "gid", now-10000); err != nil || !ok {
		t.Fatalf("mark active: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CompleteTask(task.ID, filepath.Join(dir, "file.bin"), 1000, 1000, now); err != nil || !ok {
		t.Fatalf("complete: ok=%v err=%v", ok, err)
	}
	loaded, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StartedAt != now-10000 || loaded.CompletedAt != now {
		t.Fatalf("timestamps = %d/%d, want %d/%d", loaded.StartedAt, loaded.CompletedAt, now-10000, now)
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.DailyBytes != 1000 {
		t.Fatalf("dailyBytes = %d, want 1000", stats.DailyBytes)
	}
	if stats.AvgSpeed != 100 {
		t.Fatalf("avgSpeed = %d, want 100 B/s", stats.AvgSpeed)
	}
}

func TestMigrationBackfillsCompletedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	task := &types.Task{
		ID: "old-done", URL: "https://example.com/old.bin", Title: "old.bin", Dir: dir,
		Engine: "http", Kind: "file", Status: types.StatusCompleted, CreatedAt: 10, UpdatedAt: 4242,
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	loaded, err := s2.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompletedAt != 4242 {
		t.Fatalf("completedAt = %d, want backfilled 4242", loaded.CompletedAt)
	}
}

func TestListTasksWithoutLimitReturnsAll(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 205; i++ {
		task := &types.Task{
			ID: "task-" + strconv.Itoa(i), URL: "https://example.com/file.bin", Title: "file.bin",
			Dir: dir, Engine: "http", Kind: "file", Status: types.StatusCompleted,
			CreatedAt: int64(i + 1), UpdatedAt: int64(i + 1),
		}
		if err := s.SaveTask(task); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListTasks(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 205 {
		t.Fatalf("ListTasks(0) returned %d, want 205", len(all))
	}
	limited, err := s.ListTasks(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 50 {
		t.Fatalf("ListTasks(50) returned %d, want 50", len(limited))
	}
}
