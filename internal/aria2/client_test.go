package aria2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type rpcCall struct {
	Method string
	Params []any
}

func newStubClient(t *testing.T, handler func(call rpcCall) (any, error)) (*Client, *[]rpcCall) {
	t.Helper()
	var mu sync.Mutex
	calls := make([]rpcCall, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		params := req.Params
		if len(params) > 0 {
			params = params[1:]
		}
		call := rpcCall{Method: req.Method, Params: params}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		result, err := handler(call)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": 1, "message": err.Error()},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return &Client{baseURL: server.URL, secret: "token", http: server.Client()}, &calls
}

func TestBuildArgsKeepsCredentialsOffTheCommandLine(t *testing.T) {
	opts := Options{
		DownloadDir:   "/downloads",
		Proxy:         "http://127.0.0.1:9000",
		ProxyUser:     "cyron",
		ProxyPassword: "s3cret",
		ProxyMethod:   "tunnel",
	}
	args := buildArgs(6800, "rpcsecret", "/tmp/aria2.log", 2, 8, opts)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "s3cret") || strings.Contains(joined, "--all-proxy-passwd") {
		t.Fatalf("proxy password leaked into args: %v", args)
	}
	if strings.Contains(joined, "--all-proxy-user") {
		t.Fatalf("proxy user leaked into args: %v", args)
	}
	if !strings.Contains(joined, "--all-proxy=http://127.0.0.1:9000") {
		t.Fatalf("missing proxy endpoint: %v", args)
	}
	if !strings.Contains(joined, "--proxy-method=tunnel") {
		t.Fatalf("missing proxy method: %v", args)
	}
}

func TestBuildArgsDefaultsProxyMethod(t *testing.T) {
	args := buildArgs(6800, "s", "/tmp/a.log", 1, 1, Options{DownloadDir: "/d", Proxy: "http://127.0.0.1:1"})
	if !strings.Contains(strings.Join(args, " "), "--proxy-method=tunnel") {
		t.Fatalf("args = %v", args)
	}
}

func TestBuildArgsOmitsProxyWhenDirect(t *testing.T) {
	args := buildArgs(6800, "s", "/tmp/a.log", 1, 1, Options{DownloadDir: "/d"})
	for _, arg := range args {
		if strings.HasPrefix(arg, "--all-proxy") || strings.HasPrefix(arg, "--proxy-method") {
			t.Fatalf("unexpected proxy arg %q", arg)
		}
	}
}

func TestProxyAuthUsesGlobalOptions(t *testing.T) {
	client, calls := newStubClient(t, func(rpcCall) (any, error) { return "OK", nil })
	if err := client.ProxyAuth("cyron", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	call := (*calls)[0]
	if call.Method != "aria2.changeGlobalOption" {
		t.Fatalf("method = %q", call.Method)
	}
	options, ok := call.Params[0].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v", call.Params)
	}
	if options["all-proxy-user"] != "cyron" || options["all-proxy-passwd"] != "s3cret" {
		t.Fatalf("options = %#v", options)
	}
}

func TestProxyAuthOmitsEmptyFields(t *testing.T) {
	client, calls := newStubClient(t, func(rpcCall) (any, error) { return "OK", nil })
	if err := client.ProxyAuth("", ""); err != nil {
		t.Fatal(err)
	}
	options := (*calls)[0].Params[0].(map[string]any)
	if len(options) != 0 {
		t.Fatalf("options = %#v, want empty", options)
	}
}

func TestRemoveFallsBackToResultRemoval(t *testing.T) {
	client, calls := newStubClient(t, func(call rpcCall) (any, error) {
		if call.Method == "aria2.remove" {
			return nil, errStubRemove
		}
		return "OK", nil
	})
	if err := client.Remove("g1"); err == nil {
		t.Fatal("expected the remove error to be returned")
	}
	if len(*calls) != 2 || (*calls)[1].Method != "aria2.removeDownloadResult" {
		t.Fatalf("calls = %#v", *calls)
	}
	if (*calls)[1].Params[0] != "g1" {
		t.Fatalf("fallback params = %#v", (*calls)[1].Params)
	}
}

func TestStopRefusesFurtherCalls(t *testing.T) {
	client, _ := newStubClient(t, func(rpcCall) (any, error) { return "OK", nil })
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	if err := client.SetConnections(8); err == nil {
		t.Fatal("expected a closed client error")
	}
}

func TestStopWithoutProcessIsSafeAndIdempotent(t *testing.T) {
	client, calls := newStubClient(t, func(rpcCall) (any, error) { return "OK", nil })
	client.Stop()
	client.Stop()
	if len(*calls) != 1 || (*calls)[0].Method != "aria2.shutdown" {
		t.Fatalf("calls = %#v", *calls)
	}
	client.mu.Lock()
	closed := client.closed
	client.mu.Unlock()
	if !closed {
		t.Fatal("client was not marked closed")
	}
}

var errStubRemove = &stubError{"cannot remove"}

type stubError struct{ message string }

func (e *stubError) Error() string { return e.message }
