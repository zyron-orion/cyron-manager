package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

func newResumeTestTask(t *testing.T, serverURL string) (*Manager, *store.Store, types.Task, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	settings := types.Settings{DownloadDir: filepath.Join(dir, "downloads"), MaxConcurrent: 2, Connections: 4}
	m := New(context.Background(), st, tools.New(dir), nil, settings, dir, func(string, any) {})
	t.Cleanup(m.Close)
	now := time.Now().UnixMilli()
	task := types.Task{
		ID:          randomID(),
		URL:         serverURL + "/resume.bin",
		Title:       "resume.bin",
		Engine:      "http",
		Kind:        "file",
		Status:      types.StatusQueued,
		Category:    "other",
		Connections: 4,
		Dir:         settings.DownloadDir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := st.SaveTask(&task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(settings.DownloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	finalPath := filepath.Join(settings.DownloadDir, "resume.bin")
	return m, st, task, finalPath
}

func TestHTTPResumeUsesIfRange(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789"), 600)
	var mu sync.Mutex
	seenRange := ""
	seenIfRange := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenRange = r.Header.Get("Range")
		seenIfRange = r.Header.Get("If-Range")
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "resume.bin", time.Unix(0, 0), bytes.NewReader(payload))
	}))
	defer server.Close()

	m, st, task, finalPath := newResumeTestTask(t, server.URL)
	partPath := finalPath + ".part"
	if err := os.WriteFile(partPath, payload[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeHTTPPartMeta(partPath+".meta", httpPartMeta{ETag: `"v1"`, Total: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}

	m.startHTTPTask(task)

	mu.Lock()
	gotRange := seenRange
	gotIfRange := seenIfRange
	mu.Unlock()
	if gotRange != "bytes=100-" || gotIfRange != `"v1"` {
		t.Fatalf("unexpected resume headers: range=%q if-range=%q", gotRange, gotIfRange)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("resumed file differs: got %d bytes, want %d", len(data), len(payload))
	}
	current, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != types.StatusCompleted {
		t.Fatalf("status=%s error=%s", current.Status, current.Error)
	}
	if _, err := os.Stat(partPath + ".meta"); !os.IsNotExist(err) {
		t.Fatalf("meta file should be removed, err=%v", err)
	}
}

func TestHTTPResumeRestartsWhenSourceChanges(t *testing.T) {
	payload := bytes.Repeat([]byte("B"), 4000)
	var mu sync.Mutex
	seenIfRange := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenIfRange = r.Header.Get("If-Range")
		mu.Unlock()
		w.Header().Set("ETag", `"v2"`)
		http.ServeContent(w, r, "resume.bin", time.Unix(0, 0), bytes.NewReader(payload))
	}))
	defer server.Close()

	m, _, task, finalPath := newResumeTestTask(t, server.URL)
	partPath := finalPath + ".part"
	if err := os.WriteFile(partPath, bytes.Repeat([]byte("A"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeHTTPPartMeta(partPath+".meta", httpPartMeta{ETag: `"v1"`, Total: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}

	m.startHTTPTask(task)

	mu.Lock()
	gotIfRange := seenIfRange
	mu.Unlock()
	if gotIfRange != `"v1"` {
		t.Fatalf("If-Range=%q, want %q", gotIfRange, `"v1"`)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("file was not replaced after source change: got %d bytes", len(data))
	}
}

func TestHTTPResumeWithoutMetaRestarts(t *testing.T) {
	payload := bytes.Repeat([]byte("C"), 3000)
	var mu sync.Mutex
	seenRange := "unset"
	seenIfRange := "unset"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenRange = r.Header.Get("Range")
		seenIfRange = r.Header.Get("If-Range")
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "resume.bin", time.Unix(0, 0), bytes.NewReader(payload))
	}))
	defer server.Close()

	m, _, task, finalPath := newResumeTestTask(t, server.URL)
	partPath := finalPath + ".part"
	if err := os.WriteFile(partPath, bytes.Repeat([]byte("A"), 100), 0o644); err != nil {
		t.Fatal(err)
	}

	m.startHTTPTask(task)

	mu.Lock()
	gotRange := seenRange
	gotIfRange := seenIfRange
	mu.Unlock()
	if gotRange != "" || gotIfRange != "" {
		t.Fatalf("expected a fresh request, got range=%q if-range=%q", gotRange, gotIfRange)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("file differs after restart: got %d bytes", len(data))
	}
}

func TestHTTP416RestartsFromZero(t *testing.T) {
	payload := bytes.Repeat([]byte("D"), 2500)
	var mu sync.Mutex
	requests := 0
	secondRange := "unset"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		count := requests
		if count == 2 {
			secondRange = r.Header.Get("Range")
		}
		mu.Unlock()
		if count == 1 {
			w.Header().Set("Content-Range", "bytes */2500")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "resume.bin", time.Unix(0, 0), bytes.NewReader(payload))
	}))
	defer server.Close()

	m, _, task, finalPath := newResumeTestTask(t, server.URL)
	partPath := finalPath + ".part"
	if err := os.WriteFile(partPath, bytes.Repeat([]byte("A"), 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeHTTPPartMeta(partPath+".meta", httpPartMeta{ETag: `"v1"`, Total: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}

	m.startHTTPTask(task)

	mu.Lock()
	total := requests
	mu.Unlock()
	if total != 2 || secondRange != "" {
		t.Fatalf("requests=%d second range=%q", total, secondRange)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("file differs after 416 restart: got %d bytes", len(data))
	}
}
