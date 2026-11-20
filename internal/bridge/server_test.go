package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cyronmanager/internal/types"
)

type fakeAdder struct {
	request   types.AddRequest
	calls     int
	duplicate bool
	err       error
	task      types.Task
}

func (f *fakeAdder) AddChecked(request types.AddRequest) (types.Task, bool, error) {
	f.calls++
	f.request = request
	if f.err != nil {
		return types.Task{}, false, f.err
	}
	if f.task.ID == "" {
		f.task = types.Task{ID: "task-1", URL: request.URL, Source: request.Source, Engine: request.Engine}
	}
	return f.task, f.duplicate, nil
}

type fakeLister struct {
	tasks []types.Task
	err   error
	limit int
}

func (f *fakeLister) ListTasks(limit int) ([]types.Task, error) {
	f.limit = limit
	return f.tasks, f.err
}

func newTestServer(adder Adder, lister TaskLister) *Server {
	return New(Config{
		Port:        DefaultPort,
		CaptureMode: "auto",
		Categories:  []string{types.CategoryVideo, types.CategoryAudio, types.CategoryArchive, types.CategoryProgram, types.CategoryOther},
		AppVersion:  "1.1.0",
	}, adder, lister)
}

func testRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Host = Address(DefaultPort)
	return request
}

func originRequest(method, path string, body io.Reader) *http.Request {
	request := testRequest(method, path, body)
	request.Header.Set("Origin", "chrome-extension://"+ExtensionID)
	return request
}

func jsonRequest(method, path, body string) *http.Request {
	request := originRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func serve(server *Server, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.handler().ServeHTTP(recorder, request)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response: %v: %s", err, recorder.Body.String())
	}
	return value
}

func TestHealthDoesNotRequireOrigin(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	recorder := serve(server, testRequest(http.MethodGet, "/v1/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	payload := decode[map[string]any](t, recorder)
	if payload["ok"] != true || payload["version"] != "1.1.0" {
		t.Fatalf("unexpected health payload: %v", payload)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("health response should not grant CORS without an origin")
	}
}

func TestProtectedEndpointsRequireExactOrigin(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/config", `{}`},
		{http.MethodPost, "/v1/capture/check", `{"url":"https://example.com/file.zip"}`},
		{http.MethodPost, "/v1/downloads", `{"url":"https://example.com/file.zip"}`},
		{http.MethodPost, "/v1/status", `{}`},
	}
	for _, item := range cases {
		for _, request := range []*http.Request{
			testRequest(item.method, item.path, bytes.NewBufferString(item.body)),
			originRequest(item.method, item.path, bytes.NewBufferString(item.body)),
		} {
			if request.Header.Get("Origin") == "chrome-extension://"+ExtensionID {
				request.Header.Set("Origin", "https://example.com")
			} else {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := serve(server, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("%s %s origin %q: status = %d, want 403", item.method, item.path, request.Header.Get("Origin"), recorder.Code)
			}
			if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("%s %s: disallowed origin received CORS", item.method, item.path)
			}
		}
	}
}

func TestHostMustMatchTheBridgeAddress(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	for _, host := range []string{"example.com", "127.0.0.1:9999", "localhost:17872"} {
		request := jsonRequest(http.MethodPost, "/v1/config", "{}")
		request.Host = host
		recorder := serve(server, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("host %q: status = %d, want 403", host, recorder.Code)
		}
	}
}

func TestPreflightAllowsOnlyTheExtension(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	request := originRequest(http.MethodOptions, "/v1/downloads", nil)
	recorder := serve(server, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "chrome-extension://"+ExtensionID {
		t.Fatalf("allow origin = %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
	if recorder.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatal("missing private network permission header")
	}
	if recorder.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Fatalf("allow methods = %q", recorder.Header().Get("Access-Control-Allow-Methods"))
	}

	request = testRequest(http.MethodOptions, "/v1/downloads", nil)
	request.Header.Set("Origin", "https://example.com")
	recorder = serve(server, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong origin preflight status = %d, want 403", recorder.Code)
	}
}

func TestConfigReturnsCaptureSettings(t *testing.T) {
	server := New(Config{
		Port:           DefaultPort,
		CaptureMode:    "ask",
		Categories:     []string{types.CategoryVideo, types.CategoryAudio},
		ForwardCookies: true,
		AppVersion:     "1.1.0",
	}, &fakeAdder{}, &fakeLister{})
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/config", "{}"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	payload := decode[map[string]any](t, recorder)
	if payload["captureMode"] != "ask" || payload["forwardCookies"] != true || payload["appVersion"] != "1.1.0" {
		t.Fatalf("unexpected config: %v", payload)
	}
	categories, ok := payload["categories"].([]any)
	if !ok || len(categories) != 2 || categories[0] != types.CategoryVideo || categories[1] != types.CategoryAudio {
		t.Fatalf("categories = %v", payload["categories"])
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "chrome-extension://"+ExtensionID {
		t.Fatal("missing exact CORS origin")
	}
}

func TestCaptureCheckRespectsModeAndCategories(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		categories []string
		body       string
		action     string
		reason     string
		category   string
	}{
		{"auto", "auto", nil, `{"url":"https://example.com/file.zip","mimeType":"application/zip","filename":"file.zip"}`, "download", "auto", types.CategoryArchive},
		{"ask", "ask", nil, `{"url":"https://example.com/file.zip","mimeType":"application/zip"}`, "ask", "ask", types.CategoryArchive},
		{"off", "off", nil, `{"url":"https://example.com/file.zip"}`, "skip", "capture_off", types.CategoryArchive},
		{"category disabled", "auto", []string{types.CategoryVideo}, `{"url":"https://example.com/file.zip","mimeType":"application/zip"}`, "skip", "category_disabled", types.CategoryArchive},
		{"video enabled", "auto", []string{types.CategoryVideo}, `{"url":"https://example.com/movie","mimeType":"video/mp4"}`, "download", "auto", types.CategoryVideo},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			server := New(Config{Port: DefaultPort, CaptureMode: item.mode, Categories: item.categories}, &fakeAdder{}, &fakeLister{})
			recorder := serve(server, jsonRequest(http.MethodPost, "/v1/capture/check", item.body))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			payload := decode[map[string]any](t, recorder)
			if payload["action"] != item.action || payload["reason"] != item.reason || payload["category"] != item.category {
				t.Fatalf("payload = %v", payload)
			}
		})
	}
}

func TestCaptureCheckRejectsBadRequests(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"invalid url", `{"url":"file:///tmp/a.zip"}`, http.StatusBadRequest},
		{"unknown field", `{"url":"https://example.com/a.zip","extra":true}`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			recorder := serve(server, jsonRequest(http.MethodPost, "/v1/capture/check", item.body))
			if recorder.Code != item.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, item.status, recorder.Body.String())
			}
		})
	}

	request := originRequest(http.MethodPost, "/v1/capture/check", bytes.NewBufferString(`{"url":"https://example.com/a.zip"}`))
	request.Header.Set("Content-Type", "text/plain")
	recorder := serve(server, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d, want 415", recorder.Code)
	}
}

func TestDownloadsForceBrowserPolicy(t *testing.T) {
	adder := &fakeAdder{}
	server := newTestServer(adder, &fakeLister{})
	body := `{"url":" https://example.com/movie.mkv ","engine":"HTTP","quality":"best","title":" movie ","source":"Evil","username":"user","password":"pass","cookies":"sid=secret","referrer":"javascript:bad","userAgent":"agent","mimeType":"video/x-matroska","filename":"../movie.mkv","allowDuplicate":true}`
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/downloads", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if adder.calls != 1 {
		t.Fatalf("adder calls = %d, want 1", adder.calls)
	}
	got := adder.request
	if got.URL != "https://example.com/movie.mkv" || got.Engine != "http" || got.Source != "Browser" || !got.Browser {
		t.Fatalf("unexpected policy fields: %+v", got)
	}
	if got.Username != "" || got.Password != "" || got.Cookies != "" || got.Referrer != "" || got.AllowDuplicate {
		t.Fatalf("unsafe fields survived: %+v", got)
	}
	if got.Title != "movie" || got.SuggestedName != "movie.mkv" || got.UserAgent != "agent" || got.MimeType != "video/x-matroska" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	result := decode[types.AddResult](t, recorder)
	if result.Task.ID != "task-1" || result.Duplicate {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDownloadsForwardCookiesWhenEnabled(t *testing.T) {
	adder := &fakeAdder{}
	server := New(Config{
		Port:           DefaultPort,
		CaptureMode:    "auto",
		ForwardCookies: true,
		AppVersion:     "1.1.0",
	}, adder, &fakeLister{})
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/downloads", `{"url":"https://example.com/a.zip","cookies":"sid=secret"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if adder.request.Cookies != "sid=secret" {
		t.Fatalf("cookies = %q", adder.request.Cookies)
	}
}

func TestDownloadsReturnDuplicateAndErrors(t *testing.T) {
	adder := &fakeAdder{duplicate: true}
	server := newTestServer(adder, &fakeLister{})
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/downloads", `{"url":"https://example.com/a.zip"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	result := decode[types.AddResult](t, recorder)
	if !result.Duplicate {
		t.Fatal("duplicate flag was not returned")
	}

	adder = &fakeAdder{err: errors.New("duplicate rejected")}
	server = newTestServer(adder, &fakeLister{})
	recorder = serve(server, jsonRequest(http.MethodPost, "/v1/downloads", `{"url":"https://example.com/a.zip"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("adder error status = %d, want 400", recorder.Code)
	}

	server = newTestServer(nil, &fakeLister{})
	recorder = serve(server, jsonRequest(http.MethodPost, "/v1/downloads", `{"url":"https://example.com/a.zip"}`))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil adder status = %d, want 503", recorder.Code)
	}
}

func TestDownloadsRejectInvalidBodies(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	for _, body := range []string{
		`{"url":"bad"}`,
		`{"url":"https://example.com/a.zip","extra":true}`,
		`{"url":""}`,
	} {
		recorder := serve(server, jsonRequest(http.MethodPost, "/v1/downloads", body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, recorder.Code)
		}
	}
}

func TestStatusReturnsSafeTaskFields(t *testing.T) {
	lister := &fakeLister{tasks: []types.Task{{
		ID: "task-1", Title: "movie.mkv", Status: types.StatusActive,
		Progress: 42.5, Downloaded: 1024, Total: 2048, Speed: 512, ETA: 2,
		Category: types.CategoryVideo, Cookies: "manual=secret", BrowserCookies: "sid=secret",
	}}}
	server := newTestServer(&fakeAdder{}, lister)
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/status", "{}"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if lister.limit != 30 {
		t.Fatalf("list limit = %d, want 30", lister.limit)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret")) || bytes.Contains(recorder.Body.Bytes(), []byte("cookies")) {
		t.Fatalf("status leaked cookies: %s", recorder.Body.String())
	}
	payload := decode[struct {
		Tasks []types.BrowserTaskStatus `json:"tasks"`
	}](t, recorder)
	if len(payload.Tasks) != 1 || payload.Tasks[0].ID != "task-1" || payload.Tasks[0].Progress != 42.5 {
		t.Fatalf("unexpected status payload: %+v", payload)
	}
}

func TestConfigAndStatusRequirePost(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{})
	for _, path := range []string{"/v1/config", "/v1/status"} {
		recorder := serve(server, originRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s status = %d, want 405", path, recorder.Code)
		}
	}
}

func TestStatusHandlesListerFailures(t *testing.T) {
	server := newTestServer(&fakeAdder{}, &fakeLister{err: errors.New("database unavailable")})
	recorder := serve(server, jsonRequest(http.MethodPost, "/v1/status", "{}"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}

	server = newTestServer(&fakeAdder{}, nil)
	recorder = serve(server, jsonRequest(http.MethodPost, "/v1/status", "{}"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil lister status = %d, want 503", recorder.Code)
	}
}

func TestRateLimiterWindow(t *testing.T) {
	limiter := rateLimiter{limit: 2}
	now := time.Unix(1000, 0)
	if !limiter.allow(now) || !limiter.allow(now) {
		t.Fatal("first two requests should pass")
	}
	if limiter.allow(now) {
		t.Fatal("third request should be limited")
	}
	if !limiter.allow(now.Add(time.Minute)) {
		t.Fatal("request after the window should pass")
	}
}

func TestAddressFallsBackToDefaultPort(t *testing.T) {
	if Address(0) != "127.0.0.1:17872" {
		t.Fatalf("address = %q", Address(0))
	}
	if Address(70000) != "127.0.0.1:17872" {
		t.Fatalf("address = %q", Address(70000))
	}
}
