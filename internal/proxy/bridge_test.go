package proxy

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bridgeAuth(b *Bridge) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(b.username+":"+b.password))
}

func TestBridgeRequiresAuthentication(t *testing.T) {
	bridge, err := StartBridge(DirectPlan())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	conn, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusProxyAuthRequired)
	}
}

func TestBridgeConnectTunnelsBytes(t *testing.T) {
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	bridge, err := StartBridge(DirectPlan())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	conn, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT "+echo.Addr().String()+" HTTP/1.1\r\nHost: "+echo.Addr().String()+"\r\nProxy-Authorization: "+bridgeAuth(bridge)+"\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(conn, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
}

func TestBridgeForwardsHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("upstream received proxy credentials")
		}
		_, _ = io.WriteString(w, "bridge-ok")
	}))
	defer server.Close()

	bridge, err := StartBridge(DirectPlan())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	conn, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET "+server.URL+"/file HTTP/1.1\r\nHost: example\r\nProxy-Authorization: "+bridgeAuth(bridge)+"\r\nProxy-Connection: keep-alive\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "bridge-ok" {
		t.Fatalf("status = %d body = %q", resp.StatusCode, body)
	}
}

func TestBridgeCloseClosesActiveTunnel(t *testing.T) {
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	bridge, err := StartBridge(DirectPlan())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "CONNECT "+echo.Addr().String()+" HTTP/1.1\r\nHost: "+echo.Addr().String()+"\r\nProxy-Authorization: "+bridgeAuth(bridge)+"\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	bridge.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected tunnel to close")
	}
	_ = conn.Close()
}

func TestBridgeRejectsNonBasicAuth(t *testing.T) {
	bridge, err := StartBridge(DirectPlan())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	conn, err := net.Dial("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Bearer token\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407", resp.StatusCode)
	}
	if !strings.Contains(resp.Status, "Proxy") {
		t.Fatalf("unexpected status %q", resp.Status)
	}
}
