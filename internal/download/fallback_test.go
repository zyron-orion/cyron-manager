package download

import (
	"context"
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

func TestAriaTLSFailureFallsBackToHTTP(t *testing.T) {
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 239)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "65536")
		_, _ = w.Write(payload)
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

	task := types.Task{
		ID: "fallback", URL: server.URL + "/episode.mkv", Title: "episode.mkv",
		Engine: "aria2", Kind: "file", Status: types.StatusActive,
		Dir: settings.DownloadDir, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(),
	}
	if err := st.SaveTask(&task); err != nil {
		t.Fatal(err)
	}
	m.fail(task, "SSL/TLS handshake failure: Error: The revocation function was unable to check revocation because the revocation server was offline.")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := st.GetTask(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == types.StatusCompleted {
			if current.Engine != "http" {
				t.Fatalf("engine = %q, want http", current.Engine)
			}
			data, readErr := os.ReadFile(current.FilePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(data) != len(payload) {
				t.Fatalf("downloaded %d bytes, want %d", len(data), len(payload))
			}
			return
		}
		if current.Status == types.StatusError {
			t.Fatalf("fallback failed: %s", current.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("fallback download did not finish")
}
