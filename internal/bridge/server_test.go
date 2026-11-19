package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cyronmanager/internal/types"
)

type fakeAdder struct {
	request types.AddRequest
	err     error
}

func (f *fakeAdder) Add(request types.AddRequest) (types.Task, error) {
	f.request = request
	if f.err != nil {
		return types.Task{}, f.err
	}
	return types.Task{ID: "task-1", URL: request.URL, Source: request.Source, Engine: request.Engine}, nil
}

func TestHealthDoesNotRequireToken(t *testing.T) {
	server := New("secret", &fakeAdder{})
	recorder := httptest.NewRecorder()
	server.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestDownloadsRequireBearerToken(t *testing.T) {
	server := New("secret", &fakeAdder{})
	for _, authorization := range []string{"", "Bearer wrong", "secret"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/downloads", bytes.NewBufferString(`{"url":"https://example.com/file.zip"}`))
		request.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		server.handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status = %d, want 401", authorization, recorder.Code)
		}
	}
}

func TestDownloadsAddsTask(t *testing.T) {
	adder := &fakeAdder{}
	server := New("secret", adder)
	body, err := json.Marshal(types.AddRequest{URL: "https://example.com/movie.mkv", Engine: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/downloads", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	server.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if adder.request.Source != "Browser" || adder.request.Engine != "auto" {
		t.Fatalf("unexpected request: %+v", adder.request)
	}
	var task types.Task
	if err := json.Unmarshal(recorder.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "task-1" {
		t.Fatalf("task id = %q", task.ID)
	}
}

func TestDownloadsRejectUnknownFields(t *testing.T) {
	server := New("secret", &fakeAdder{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/downloads", bytes.NewBufferString(`{"url":"https://example.com/file.zip","extra":true}`))
	request.Header.Set("Authorization", "Bearer secret")
	server.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestDownloadsReturnAdderError(t *testing.T) {
	server := New("secret", &fakeAdder{err: errors.New("bad link")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/downloads", bytes.NewBufferString(`{"url":"bad"}`))
	request.Header.Set("Authorization", "Bearer secret")
	server.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
