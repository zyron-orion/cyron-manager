package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

func TestHTTPTaskDownloadsChunkedArchive(t *testing.T) {
	payload := make([]byte, 300*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		flusher, _ := w.(http.Flusher)
		for offset := 0; offset < len(payload); offset += 64 * 1024 {
			end := offset + 64*1024
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[offset:end])
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	settings := types.Settings{DownloadDir: filepath.Join(dir, "downloads"), MaxConcurrent: 2, Connections: 4}
	m := New(context.Background(), st, tools.New(dir), nil, settings, dir, func(string, any) {})
	defer m.Close()

	task, err := m.Add(types.AddRequest{URL: server.URL + "/archive/repo.zip", Engine: "http", Title: "repo", Source: "GitHub"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := st.GetTask(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == types.StatusCompleted {
			data, readErr := os.ReadFile(current.FilePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(data) != len(payload) {
				t.Fatalf("downloaded %d bytes, want %d", len(data), len(payload))
			}
			if current.FilePath != filepath.Join(settings.DownloadDir, "repo.zip") {
				t.Fatalf("unexpected path %s", current.FilePath)
			}
			return
		}
		if current.Status == types.StatusError {
			t.Fatalf("download failed: %s", current.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("download did not finish: %+v", task))
}
