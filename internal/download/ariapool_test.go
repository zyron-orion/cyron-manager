package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyronmanager/internal/aria2"
	"cyronmanager/internal/proxy"
	"cyronmanager/internal/store"
	"cyronmanager/internal/tools"
	"cyronmanager/internal/types"
)

type fakeAria struct {
	mu      sync.Mutex
	gids    map[string]bool
	added   []string
	stopped int32
}

func newFakeAria() *fakeAria {
	return &fakeAria{gids: make(map[string]bool)}
}

func (f *fakeAria) AddURI(uris []string, _ map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gid := "gid-" + uris[0]
	f.gids[gid] = true
	f.added = append(f.added, uris[0])
	return gid, nil
}

func (f *fakeAria) AddTorrent(string, map[string]string) (string, error) {
	return "", errors.New("not supported")
}

func (f *fakeAria) TellStatus(gid string) (aria2.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.gids[gid] {
		return aria2.Status{}, errors.New("gid not found")
	}
	return aria2.Status{GID: gid, Status: "active"}, nil
}

func (f *fakeAria) Pause(string) error   { return nil }
func (f *fakeAria) Unpause(string) error { return nil }
func (f *fakeAria) Remove(gid string) error {
	f.mu.Lock()
	delete(f.gids, gid)
	f.mu.Unlock()
	return nil
}
func (f *fakeAria) RemoveDownloadResult(string) error { return nil }
func (f *fakeAria) SetConnections(int) error          { return nil }
func (f *fakeAria) SetMaxConcurrent(int) error        { return nil }
func (f *fakeAria) Stop()                             { atomic.AddInt32(&f.stopped, 1) }

func newPoolTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "aria2c.exe"), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := types.Settings{DownloadDir: filepath.Join(dir, "downloads"), MaxConcurrent: 2, Connections: 4}
	m := New(context.Background(), st, tools.New(dir), nil, settings, dir, func(string, any) {})
	t.Cleanup(m.Close)
	return m
}

func directTask(id string) types.Task {
	return types.Task{ID: id, URL: "https://example.com/" + id, Engine: "aria2", Kind: "file", ProxyMode: types.ProxyModeDirect}
}

func TestEnsureAriaForReusesClientForSameRoute(t *testing.T) {
	m := newPoolTestManager(t)
	var starts int32
	fake := newFakeAria()
	m.ariaStart = func(context.Context, string, aria2.Options) (ariaEngine, error) {
		atomic.AddInt32(&starts, 1)
		return fake, nil
	}

	first, err := m.ensureAriaFor(directTask("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.ensureAriaFor(directTask("b"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("expected the same engine for the same route")
	}
	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("engine started %d times, want 1", got)
	}
	m.ariaMu.Lock()
	keyA, keyB := m.ariaTask["a"], m.ariaTask["b"]
	m.ariaMu.Unlock()
	if keyA == "" || keyA != keyB {
		t.Fatalf("task keys = %q/%q, want equal non-empty", keyA, keyB)
	}
}

func TestEnsureAriaForStartsOnceUnderConcurrency(t *testing.T) {
	m := newPoolTestManager(t)
	var starts int32
	m.ariaStart = func(context.Context, string, aria2.Options) (ariaEngine, error) {
		atomic.AddInt32(&starts, 1)
		time.Sleep(20 * time.Millisecond)
		return newFakeAria(), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := m.ensureAriaFor(directTask(string(rune('a' + i)))); err != nil {
				t.Errorf("ensure: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("engine started %d times, want 1", got)
	}
	if got := len(m.ariaSnapshot()); got != 1 {
		t.Fatalf("pool size = %d, want 1", got)
	}
}

func TestEnsureAriaForClosesBridgeAndFailsWhenStartFails(t *testing.T) {
	m := newPoolTestManager(t)
	m.ariaStart = func(context.Context, string, aria2.Options) (ariaEngine, error) {
		return nil, errors.New("boom")
	}
	if _, err := m.ensureAriaFor(directTask("a")); err == nil {
		t.Fatal("expected start failure")
	}
	m.ariaMu.Lock()
	clients, bridges := len(m.ariaClients), len(m.ariaBridges)
	m.ariaMu.Unlock()
	if clients != 0 || bridges != 0 {
		t.Fatalf("pool after failure = %d clients, %d bridges; want 0/0", clients, bridges)
	}
}

func TestAriaClientForTaskRecoversMappingByGID(t *testing.T) {
	m := newPoolTestManager(t)
	other := newFakeAria()
	owner := newFakeAria()
	gid, err := owner.AddURI([]string{"https://example.com/file"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.ariaMu.Lock()
	m.ariaClients["k1"] = other
	m.ariaClients["k2"] = owner
	m.ariaMu.Unlock()

	task := types.Task{ID: "t", GID: gid, Engine: "aria2"}
	if got := m.ariaClientForTask(task); got != owner {
		t.Fatal("did not locate the engine that owns the gid")
	}
	m.ariaMu.Lock()
	mapped := m.ariaTask["t"]
	m.ariaMu.Unlock()
	if mapped != "k2" {
		t.Fatalf("mapping = %q, want k2", mapped)
	}

	m.forgetAriaTask("t")
	if got := m.ariaClientForTask(types.Task{ID: "t", GID: "missing"}); got != nil {
		t.Fatal("expected nil for an unknown gid")
	}
}

func TestReapIdleAriaStopsOnlyIdleExpiredClients(t *testing.T) {
	m := newPoolTestManager(t)
	busy, idle := newFakeAria(), newFakeAria()
	m.ariaMu.Lock()
	m.ariaClients["busy"] = busy
	m.ariaClients["idle"] = idle
	m.ariaTask["t"] = "busy"
	m.ariaMu.Unlock()

	start := time.Now()
	m.reapIdleAria(start)
	if atomic.LoadInt32(&idle.stopped) != 0 {
		t.Fatal("idle client stopped before the ttl expired")
	}
	m.reapIdleAria(start.Add(defaultAriaIdleTTL / 2))
	if atomic.LoadInt32(&idle.stopped) != 0 {
		t.Fatal("idle client stopped before the ttl expired")
	}
	m.reapIdleAria(start.Add(defaultAriaIdleTTL + time.Second))
	if atomic.LoadInt32(&idle.stopped) != 1 {
		t.Fatal("idle client was not stopped after the ttl")
	}
	if atomic.LoadInt32(&busy.stopped) != 0 {
		t.Fatal("busy client was stopped")
	}
	m.ariaMu.Lock()
	_, idleKept := m.ariaClients["idle"]
	_, busyKept := m.ariaClients["busy"]
	m.ariaMu.Unlock()
	if idleKept || !busyKept {
		t.Fatalf("pool after reap: idle=%v busy=%v", idleKept, busyKept)
	}
}

func TestEnsureAriaForProxyRouteStartsBridge(t *testing.T) {
	m := newPoolTestManager(t)
	profile := types.ProxyProfile{
		ID: "p1", Name: "test", Protocol: types.ProxyProtocolHTTP,
		Host: "127.0.0.1", Port: 9, CreatedAt: 1,
	}
	if err := m.store.SaveProxyProfile(&profile); err != nil {
		t.Fatal(err)
	}
	m.proxy = proxy.NewManager(m.store)

	var got aria2.Options
	m.ariaStart = func(_ context.Context, _ string, opts aria2.Options) (ariaEngine, error) {
		got = opts
		return newFakeAria(), nil
	}
	task := directTask("proxied")
	task.ProxyMode = types.ProxyModeProfile
	task.ProxyProfileID = "p1"
	if _, err := m.ensureAriaFor(task); err != nil {
		t.Fatal(err)
	}
	if got.Proxy == "" || got.ProxyUser == "" || got.ProxyPassword == "" {
		t.Fatalf("options = %+v, want a bridge endpoint and credentials", got)
	}
	if strings.Contains(got.Proxy, "@") {
		t.Fatal("bridge endpoint must not embed credentials")
	}
	if got.ProxyMethod != "tunnel" {
		t.Fatalf("proxy method = %q", got.ProxyMethod)
	}
	m.ariaMu.Lock()
	bridges := len(m.ariaBridges)
	key := m.ariaTask["proxied"]
	m.ariaMu.Unlock()
	if bridges != 1 || key == "" || key == "direct" {
		t.Fatalf("bridges = %d, key = %q", bridges, key)
	}
	m.closeAria()
	m.ariaMu.Lock()
	defer m.ariaMu.Unlock()
	if len(m.ariaBridges) != 0 {
		t.Fatal("bridge survived close")
	}
}

func TestFallbackToHTTPForgetsPoolMapping(t *testing.T) {
	m := newPoolTestManager(t)
	task := directTask("fallback")
	task.Status = types.StatusActive
	if err := m.store.SaveTask(&task); err != nil {
		t.Fatal(err)
	}
	engine := newFakeAria()
	m.ariaMu.Lock()
	m.ariaClients["k"] = engine
	m.ariaTask[task.ID] = "k"
	m.ariaMu.Unlock()

	if !m.fallbackToHTTP(task) {
		t.Fatal("fallback was rejected")
	}
	m.ariaMu.Lock()
	_, mapped := m.ariaTask[task.ID]
	m.ariaMu.Unlock()
	if mapped {
		t.Fatal("aria2 pool mapping survived the fallback")
	}
	current, err := m.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Engine != "http" || current.GID != "" {
		t.Fatalf("task after fallback = engine %q gid %q", current.Engine, current.GID)
	}
}

func TestCloseAriaStopsEverything(t *testing.T) {
	m := newPoolTestManager(t)
	a, b := newFakeAria(), newFakeAria()
	m.ariaMu.Lock()
	m.ariaClients["a"] = a
	m.ariaClients["b"] = b
	m.ariaTask["t"] = "a"
	m.ariaMu.Unlock()

	m.closeAria()
	if atomic.LoadInt32(&a.stopped) != 1 || atomic.LoadInt32(&b.stopped) != 1 {
		t.Fatal("close did not stop every engine")
	}
	m.ariaMu.Lock()
	defer m.ariaMu.Unlock()
	if len(m.ariaClients) != 0 || len(m.ariaBridges) != 0 || len(m.ariaTask) != 0 || len(m.ariaIdle) != 0 {
		t.Fatal("pool maps were not cleared")
	}
}

func TestAriaKeyDistinguishesRoutesWithoutLeakingPassword(t *testing.T) {
	direct := ariaKey(nil)
	if direct != "direct" {
		t.Fatalf("direct key = %q", direct)
	}
	plan := func(host, password string) *proxy.RoutePlan {
		return &proxy.RoutePlan{Routes: []proxy.Route{{Profile: types.ProxyProfile{
			ID: "p1", Protocol: types.ProxyProtocolHTTP, Host: host, Port: 8080,
			Username: "user", Password: password,
		}}}}
	}
	keyA := ariaKey(plan("127.0.0.1", "secret-one"))
	keyB := ariaKey(plan("127.0.0.1", "secret-two"))
	keyC := ariaKey(plan("127.0.0.2", "secret-one"))
	if keyA == "" || keyA == "direct" {
		t.Fatalf("unexpected key %q", keyA)
	}
	if keyA == keyB {
		t.Fatal("keys ignore the password")
	}
	if keyA == keyC {
		t.Fatal("keys ignore the host")
	}
	for _, key := range []string{keyA, keyB, keyC} {
		if len(key) > 64 || strings.Contains(key, "secret") {
			t.Fatalf("key %q leaks credentials or is too long", key)
		}
	}
}
