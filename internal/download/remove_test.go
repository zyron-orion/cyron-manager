package download

import (
	"os"
	"path/filepath"
	"testing"

	"cyronmanager/internal/store"
	"cyronmanager/internal/types"
)

func TestRemoveWithFileDeletesTaskFilesAndSidecars(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dataDir, "movie.mkv")
	paths := []string{target, target + ".aria2", target + ".part", target + ".part.meta", target + ".part.multi", target + ".part.multi.json"}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task := types.Task{
		ID: "remove-1", URL: "https://example.com/movie.mkv", Title: "movie.mkv",
		Engine: "http", Kind: "file", Status: types.StatusCompleted, Dir: dataDir, FilePath: target,
	}
	if err := st.SaveTask(&task); err != nil {
		t.Fatal(err)
	}
	m := New(t.Context(), st, nil, types.Settings{DownloadDir: dataDir, MaxConcurrent: 1}, dir, nil)
	defer m.Close()
	if err := m.RemoveWithFile(task.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
	if _, err := st.GetTask(task.ID); err == nil {
		t.Fatal("task was not removed")
	}
}

func TestRemoveWithFileNeverDeletesOutsideTaskDir(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "keep.bin")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := types.Task{
		ID: "remove-2", URL: "https://example.com/file.bin", Title: "file.bin",
		Engine: "http", Kind: "file", Status: types.StatusCompleted, Dir: dataDir, FilePath: outside,
	}
	if err := st.SaveTask(&task); err != nil {
		t.Fatal(err)
	}
	m := New(t.Context(), st, nil, types.Settings{DownloadDir: dataDir, MaxConcurrent: 1}, dir, nil)
	defer m.Close()
	if err := m.RemoveWithFile(task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was deleted: %v", err)
	}
}
