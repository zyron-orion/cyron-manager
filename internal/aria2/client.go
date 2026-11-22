package aria2

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/proxy"
)

type Options struct {
	DownloadDir   string
	MaxConcurrent int
	Connections   int
	Proxy         string
	ProxyUser     string
	ProxyPassword string
	ProxyMethod   string
	DataDir       string
}

type Client struct {
	baseURL string
	secret  string
	http    *http.Client
	cmd     *exec.Cmd
	logFile *os.File
	waitCh  chan error
	mu      sync.Mutex
	closed  bool
}

type Status struct {
	GID             string `json:"gid"`
	Status          string `json:"status"`
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	ErrorMessage    string `json:"errorMessage"`
	Dir             string `json:"dir"`
	Files           []struct {
		Path string `json:"path"`
	} `json:"files"`
	Connections string `json:"connections"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func Start(ctx context.Context, bin string, opts Options) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("aria2c not found: %w", err)
	}
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(opts.DataDir, "aria2.session"))
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	secretBytes := make([]byte, 24)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	secret := hex.EncodeToString(secretBytes)
	logPath := filepath.Join(opts.DataDir, "aria2.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}
	connections := opts.Connections
	if connections < 1 {
		connections = 8
	}
	args := buildArgs(port, secret, logPath, maxConcurrent, connections, opts)

	cmd := exec.Command(bin, args...)
	cmd.Dir = opts.DownloadDir
	cmd.Env = proxy.CleanEnv(os.Environ())
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	hideConsole(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}

	c := &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port),
		secret:  secret,
		http:    &http.Client{Timeout: 8 * time.Second},
		cmd:     cmd,
		logFile: logFile,
		waitCh:  make(chan error, 1),
	}
	go func() {
		c.waitCh <- cmd.Wait()
	}()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-c.waitCh:
			tail := readTail(logPath, 4096)
			logFile.Close()
			return nil, fmt.Errorf("aria2 exited during startup: %v %s", err, tail)
		case <-ctx.Done():
			c.Stop()
			return nil, ctx.Err()
		default:
		}
		if _, err := c.call("aria2.getVersion"); err == nil {
			if opts.Proxy != "" && (opts.ProxyUser != "" || opts.ProxyPassword != "") {
				optionErr := c.ProxyAuth(opts.ProxyUser, opts.ProxyPassword)
				if optionErr != nil {
					c.Stop()
					return nil, fmt.Errorf("aria2 proxy credentials rejected: %w", optionErr)
				}
			}
			return c, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.Stop()
	return nil, fmt.Errorf("aria2 did not become ready; see %s", logPath)
}

func buildArgs(port int, secret, logPath string, maxConcurrent, connections int, opts Options) []string {
	args := []string{
		"--no-conf=true",
		"--enable-rpc=true",
		fmt.Sprintf("--rpc-listen-port=%d", port),
		"--rpc-listen-all=false",
		"--rpc-allow-origin-all=false",
		"--rpc-secret=" + secret,
		"--dir=" + opts.DownloadDir,
		fmt.Sprintf("--max-concurrent-downloads=%d", maxConcurrent),
		fmt.Sprintf("--max-connection-per-server=%d", connections),
		fmt.Sprintf("--split=%d", connections),
		"--min-split-size=1M",
		"--max-overall-download-limit=0",
		"--max-download-limit=0",
		"--continue=true",
		"--file-allocation=none",
		"--auto-file-renaming=true",
		"--allow-overwrite=false",
		"--max-tries=5",
		"--retry-wait=3",
		"--timeout=30",
		"--connect-timeout=15",
		"--disk-cache=32M",
		"--async-dns=true",
		"--seed-time=0",
		"--bt-stop-timeout=0",
		"--summary-interval=0",
		"--console-log-level=warn",
		"--log=" + logPath,
		"--log-level=warn",
	}
	if opts.Proxy != "" {
		args = append(args, "--all-proxy="+opts.Proxy)
		method := strings.TrimSpace(opts.ProxyMethod)
		if method == "" {
			method = "tunnel"
		}
		args = append(args, "--proxy-method="+method)
	}
	return args
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (c *Client) call(method string, params ...any) (json.RawMessage, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("aria2 client is closed")
	}
	if len(params) == 0 {
		params = []any{}
	}
	allParams := append([]any{"token:" + c.secret}, params...)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: "dm", Method: method, Params: allParams})
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Post(c.baseURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var out rpcResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("aria2: %s", out.Error.Message)
	}
	return out.Result, nil
}

func (c *Client) AddURI(uris []string, options map[string]string) (string, error) {
	if len(uris) == 0 {
		return "", errors.New("empty uri")
	}
	raw, err := c.call("aria2.addUri", uris, options)
	if err != nil {
		return "", err
	}
	var gid string
	if err := json.Unmarshal(raw, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func (c *Client) AddTorrent(torrentBase64 string, options map[string]string) (string, error) {
	if torrentBase64 == "" {
		return "", errors.New("empty torrent")
	}
	raw, err := c.call("aria2.addTorrent", torrentBase64, options)
	if err != nil {
		return "", err
	}
	var gid string
	if err := json.Unmarshal(raw, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func (c *Client) TellStatus(gid string) (Status, error) {
	raw, err := c.call("aria2.tellStatus", gid, []string{
		"gid", "status", "totalLength", "completedLength", "downloadSpeed",
		"errorMessage", "dir", "files", "connections",
	})
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, err
	}
	return st, nil
}

func (c *Client) Pause(gid string) error {
	_, err := c.call("aria2.pause", gid)
	return err
}

func (c *Client) Unpause(gid string) error {
	_, err := c.call("aria2.unpause", gid)
	return err
}

func (c *Client) Remove(gid string) error {
	_, err := c.call("aria2.remove", gid)
	if err != nil {
		_, _ = c.call("aria2.removeDownloadResult", gid)
	}
	return err
}

func (c *Client) RemoveDownloadResult(gid string) error {
	_, err := c.call("aria2.removeDownloadResult", gid)
	return err
}

func (c *Client) ChangeGlobalOption(options map[string]string) error {
	_, err := c.call("aria2.changeGlobalOption", options)
	return err
}

func (c *Client) ProxyAuth(user, password string) error {
	options := map[string]string{}
	if user != "" {
		options["all-proxy-user"] = user
	}
	if password != "" {
		options["all-proxy-passwd"] = password
	}
	return c.ChangeGlobalOption(options)
}

func (c *Client) SetConnections(connections int) error {
	return c.ChangeGlobalOption(map[string]string{
		"max-connection-per-server": strconv.Itoa(connections),
		"split":                     strconv.Itoa(connections),
	})
}

func (c *Client) SetMaxConcurrent(n int) error {
	return c.ChangeGlobalOption(map[string]string{"max-concurrent-downloads": strconv.Itoa(n)})
}

func (c *Client) Stop() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	if c.http != nil {
		_, _ = c.call("aria2.shutdown")
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	if c.waitCh != nil {
		select {
		case <-c.waitCh:
		case <-time.After(2500 * time.Millisecond):
			if c.cmd != nil && c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
			select {
			case <-c.waitCh:
			case <-time.After(time.Second):
			}
		}
	}
	if c.logFile != nil {
		c.logFile.Close()
	}
}

func readTail(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - max
	if offset < 0 {
		offset = 0
	}
	_, _ = f.Seek(offset, 0)
	b, _ := io.ReadAll(io.LimitReader(f, max))
	return strings.TrimSpace(string(b))
}

func ParseInt(value string) int64 {
	n, _ := strconv.ParseInt(value, 10, 64)
	return n
}
