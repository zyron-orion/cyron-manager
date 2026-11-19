package bridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/types"
)

const Address = "127.0.0.1:17872"

type Adder interface {
	Add(types.AddRequest) (types.Task, error)
}

type Server struct {
	adder Adder
	token string

	mu      sync.Mutex
	server  *http.Server
	running bool
	err     string
}

func New(token string, adder Adder) *Server {
	return &Server{token: strings.TrimSpace(token), adder: adder}
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp4", Address)
	if err != nil {
		s.mu.Lock()
		s.err = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("browser bridge port %s is unavailable: %w", Address, err)
	}
	server := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	s.mu.Lock()
	s.server = server
	s.running = true
	s.err = ""
	s.mu.Unlock()
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.mu.Lock()
			s.running = false
			s.err = serveErr.Error()
			s.mu.Unlock()
		}
	}()
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.running = false
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Server) Error() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/downloads", s.downloads)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": "CYRON Manager"})
}

func (s *Server) downloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		writeError(w, http.StatusUnauthorized, "invalid browser bridge token")
		return
	}
	if s.adder == nil {
		writeError(w, http.StatusServiceUnavailable, "download manager is not ready")
		return
	}
	var request types.AddRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request body must contain one object")
		return
	}
	request.URL = strings.TrimSpace(request.URL)
	if request.URL == "" {
		writeError(w, http.StatusBadRequest, "link is empty")
		return
	}
	if strings.TrimSpace(request.Source) == "" {
		request.Source = "Browser"
	}
	if strings.TrimSpace(request.Engine) == "" {
		request.Engine = "auto"
	}
	task, err := s.adder.Add(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) authorized(value string) bool {
	if s.token == "" {
		return false
	}
	token, ok := strings.CutPrefix(strings.TrimSpace(value), "Bearer ")
	if !ok {
		return false
	}
	token = strings.TrimSpace(token)
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
