package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Bridge struct {
	listener net.Listener
	plan     *RoutePlan
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	username string
	password string
}

func StartBridge(plan *RoutePlan) (*Bridge, error) {
	if plan == nil {
		return nil, errors.New("proxy route is empty")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		_ = listener.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &Bridge{
		listener: listener,
		plan:     plan,
		ctx:      ctx,
		cancel:   cancel,
		conns:    make(map[net.Conn]struct{}),
		username: "cyron",
		password: hex.EncodeToString(token),
	}
	bridge.wg.Add(1)
	go bridge.serve()
	return bridge, nil
}

func (b *Bridge) URL() string {
	return "http://" + b.username + ":" + b.password + "@" + b.listener.Addr().String()
}

func (b *Bridge) Endpoint() string {
	return "http://" + b.listener.Addr().String()
}

func (b *Bridge) Auth() (string, string) {
	return b.username, b.password
}

func (b *Bridge) Address() string {
	return b.listener.Addr().String()
}

func (b *Bridge) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.cancel()
	_ = b.listener.Close()
	for conn := range b.conns {
		_ = conn.Close()
	}
	b.mu.Unlock()
	b.wg.Wait()
}

func (b *Bridge) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			select {
			case <-b.ctx.Done():
				return
			default:
				continue
			}
		}
		b.track(conn)
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer b.untrack(conn)
			b.handle(conn)
		}()
	}
}

func (b *Bridge) track(conn net.Conn) {
	b.mu.Lock()
	b.conns[conn] = struct{}{}
	b.mu.Unlock()
}

func (b *Bridge) untrack(conn net.Conn) {
	b.mu.Lock()
	delete(b.conns, conn)
	b.mu.Unlock()
}

func (b *Bridge) handle(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(client)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	if !b.authorized(req) {
		_ = writeProxyStatus(client, http.StatusProxyAuthRequired, "Proxy Authentication Required")
		return
	}
	target := req.Host
	if req.Method == http.MethodConnect {
		if target == "" {
			target = req.URL.Host
		}
		if target == "" {
			_ = writeProxyStatus(client, http.StatusBadRequest, "Bad Request")
			return
		}
		upstream, err := b.plan.DialContext(b.ctx, "tcp", target)
		if err != nil {
			_ = writeProxyStatus(client, http.StatusBadGateway, "Bad Gateway")
			return
		}
		defer upstream.Close()
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		_ = client.SetDeadline(time.Time{})
		pipe(client, upstream)
		return
	}
	if req.URL == nil || req.URL.Host == "" {
		_ = writeProxyStatus(client, http.StatusBadRequest, "Bad Request")
		return
	}
	target = req.URL.Host
	upstream, err := b.plan.DialContext(b.ctx, "tcp", target)
	if err != nil {
		_ = writeProxyStatus(client, http.StatusBadGateway, "Bad Gateway")
		return
	}
	defer upstream.Close()
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	if req.Host == "" {
		req.Host = target
	}
	if err := req.Write(upstream); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	pipe(client, upstream)
}

func (b *Bridge) authorized(req *http.Request) bool {
	value := strings.TrimSpace(req.Header.Get("Proxy-Authorization"))
	if value == "" {
		return false
	}
	const prefix = "Basic "
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value[len(prefix):]))
	if err != nil {
		return false
	}
	expected := b.username + ":" + b.password
	return subtleCompare(string(raw), expected)
}

func writeProxyStatus(conn net.Conn, code int, text string) error {
	status := http.StatusText(code)
	if status == "" {
		status = text
	}
	header := "HTTP/1.1 " + itoa(code) + " " + status + "\r\nContent-Length: 0\r\nConnection: close\r\n"
	if code == http.StatusProxyAuthRequired {
		header += "Proxy-Authenticate: Basic realm=\"CYRON\"\r\n"
	}
	header += "\r\n"
	_, err := io.WriteString(conn, header)
	return err
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		_ = a.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
