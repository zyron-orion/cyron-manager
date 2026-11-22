package download

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cyronmanager/internal/proxy"
	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

func startConnectProxy(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				req, err := http.ReadRequest(reader)
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				upstream, err := net.DialTimeout("tcp", req.Host, 5*time.Second)
				if err != nil {
					_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
					return
				}
				defer upstream.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}()
		}
	}()
	return listener.Addr().String(), &count
}

func newProxyHTTPTestManager(t *testing.T, profileID string) (*Manager, *store.Store, types.Settings) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pm := proxy.NewManager(st)
	settings := types.Settings{DownloadDir: filepath.Join(dir, "downloads"), MaxConcurrent: 2, Connections: 4}
	m := New(context.Background(), st, tools.New(dir), pm, settings, dir, func(string, any) {})
	t.Cleanup(m.Close)
	_ = profileID
	return m, st, settings
}

func waitTaskStatus(t *testing.T, st *store.Store, id, status string, timeout time.Duration) types.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := st.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == status {
			return task
		}
		if task.Status == types.StatusError && status != types.StatusError {
			t.Fatalf("task failed: %s", task.Error)
		}
		time.Sleep(40 * time.Millisecond)
	}
	task, _ := st.GetTask(id)
	t.Fatalf("task %s status = %s, want %s", id, task.Status, status)
	return types.Task{}
}

func TestHTTPDownloadUsesTaskProxy(t *testing.T) {
	payload := bytes.Repeat([]byte("proxy-data-"), 8000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "proxy.bin", time.Unix(0, 0), bytes.NewReader(payload))
	}))
	defer server.Close()

	proxyAddr, count := startConnectProxy(t)
	host, port, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	var portNumber int
	if _, err := fmt.Sscanf(port, "%d", &portNumber); err != nil {
		t.Fatal(err)
	}

	m, st, settings := newProxyHTTPTestManager(t, "")
	view, err := m.proxy.Save(types.ProxyProfileInput{
		Name:     "local-http",
		Protocol: types.ProxyProtocolHTTP,
		Host:     host,
		Port:     portNumber,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = settings

	task, err := m.Add(types.AddRequest{
		URL:            server.URL + "/proxy.bin",
		Engine:         "http",
		ProxyMode:      types.ProxyModeProfile,
		ProxyProfileID: view.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitTaskStatus(t, st, task.ID, types.StatusCompleted, 10*time.Second)
	if done.Downloaded != int64(len(payload)) {
		t.Fatalf("downloaded = %d, want %d", done.Downloaded, len(payload))
	}
	if count.Load() < 2 {
		t.Fatalf("proxy connection count = %d, want at least 2", count.Load())
	}
	data, err := os.ReadFile(done.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("downloaded payload mismatch")
	}
}

func TestHTTPDirectIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	payload := bytes.Repeat([]byte("direct-data-"), 2000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	m, st, _ := newProxyHTTPTestManager(t, "")
	task, err := m.Add(types.AddRequest{URL: server.URL + "/direct.bin", Engine: "http", ProxyMode: types.ProxyModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	done := waitTaskStatus(t, st, task.ID, types.StatusCompleted, 10*time.Second)
	data, err := os.ReadFile(done.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("direct download was altered or proxied")
	}
}

func TestHTTPMissingProxyProfileFailsWithoutDirectFallback(t *testing.T) {
	m, _, _ := newProxyHTTPTestManager(t, "")
	_, _, err := m.AddChecked(types.AddRequest{
		URL:            "http://example.invalid/file.bin",
		Engine:         "http",
		ProxyMode:      types.ProxyModeProfile,
		ProxyProfileID: "missing-profile",
	})
	if err == nil {
		t.Fatal("expected missing proxy profile error")
	}
}

func TestHTTPDirectRoutePlanDoesNotUseEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	route := proxy.DirectPlan()
	m := &Manager{}
	client := m.newHTTPClient(route)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport type")
	}
	if transport.Proxy != nil {
		t.Fatal("direct transport must not use ProxyFromEnvironment")
	}
	if transport.DialContext == nil {
		t.Fatal("direct transport must use route dialer")
	}
}
