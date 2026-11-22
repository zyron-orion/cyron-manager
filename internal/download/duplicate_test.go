package download

import (
	"path/filepath"
	"testing"

	"cyronmanager/internal/store"
	"cyronmanager/internal/types"
)

func TestDropDuplicateInterruptedKeepsMostProgress(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := int64(1000)
	oldTask := types.Task{
		ID: "old", URL: "https://example.com/file.mkv", Engine: "aria2", Kind: "file",
		Quality: "best", Status: types.StatusQueued, CreatedAt: now, UpdatedAt: now,
	}
	newTask := types.Task{
		ID: "new", URL: oldTask.URL, Engine: "aria2", Kind: "file",
		Quality: "best", Status: types.StatusQueued, Downloaded: 4096, Progress: 10,
		CreatedAt: now + 1, UpdatedAt: now + 1,
	}
	if err := st.SaveTask(&oldTask); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(&newTask); err != nil {
		t.Fatal(err)
	}

	m := &Manager{store: st}
	kept := m.dropDuplicateInterrupted([]types.Task{oldTask, newTask})
	if len(kept) != 1 || kept[0].ID != "new" {
		t.Fatalf("kept = %+v, want new task", kept)
	}
	if _, err := st.GetTask("old"); err == nil {
		t.Fatal("old duplicate was not deleted")
	}
	if _, err := st.GetTask("new"); err != nil {
		t.Fatalf("new task missing: %v", err)
	}
}

func TestAddReturnsExistingQueuedDuplicate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := int64(1000)
	existing := types.Task{
		ID: "existing", URL: "https://example.com/file.zip", Title: "file.zip",
		Engine: "http", Kind: "file", Quality: "best", Status: types.StatusQueued,
		Dir: filepath.Join(dir, "downloads"), CreatedAt: now, UpdatedAt: now,
	}
	if err := st.SaveTask(&existing); err != nil {
		t.Fatal(err)
	}
	m := New(t.Context(), st, nil, nil, types.Settings{DownloadDir: existing.Dir, MaxConcurrent: 2}, dir, nil)
	defer m.Close()
	task, err := m.Add(types.AddRequest{URL: existing.URL, Engine: "http", Title: "new title"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != existing.ID {
		t.Fatalf("Add returned %q, want existing %q", task.ID, existing.ID)
	}
}

func TestDirectFilesPreferNativeHTTP(t *testing.T) {
	engine, kind, _, _, err := classify("https://dl33.onlinedigi.top/serial/a/file.mkv?md5=x&expires=1", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine != "http" || kind != "file" {
		t.Fatalf("classify = %q/%q, want http/file", engine, kind)
	}
	engine, _, _, _, err = classify("https://dl33.onlinedigi.top/serial/a/file.mkv", "aria2")
	if err != nil {
		t.Fatal(err)
	}
	if engine != "aria2" {
		t.Fatalf("explicit aria2 classified as %q", engine)
	}
	engine, _, _, _, err = classify("https://other.example/download/file.iso", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine != "http" {
		t.Fatalf("generic direct file classified as %q, want http", engine)
	}
}

func TestAddCheckedAllowDuplicateCreatesNewTask(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	existing := types.Task{
		ID: "existing", URL: "https://example.com/file.zip", Title: "file.zip",
		Engine: "http", Kind: "file", Quality: "best", Status: types.StatusQueued,
		Dir: filepath.Join(dir, "downloads"), CreatedAt: 1000, UpdatedAt: 1000,
	}
	if err := st.SaveTask(&existing); err != nil {
		t.Fatal(err)
	}
	m := New(t.Context(), st, nil, nil, types.Settings{DownloadDir: existing.Dir, MaxConcurrent: 1}, dir, nil)
	defer m.Close()
	task, duplicate, err := m.AddChecked(types.AddRequest{URL: existing.URL, Engine: "http", Title: "copy", AllowDuplicate: true})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("AllowDuplicate still reported a duplicate")
	}
	if task.ID == existing.ID {
		t.Fatal("AllowDuplicate reused the existing task")
	}
	if err := m.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
}
