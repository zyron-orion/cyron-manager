package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cyronmanager/internal/types"
)

type Route struct {
	Direct  bool
	Profile types.ProxyProfile
}

type RoutePlan struct {
	Routes      []Route
	Bypass      *Bypass
	ApplyBypass bool
	Healthy     func(string) bool
	Timeout     time.Duration
}

func DirectPlan() *RoutePlan {
	return &RoutePlan{Routes: []Route{{Direct: true}}, Timeout: 20 * time.Second}
}

func (p *RoutePlan) DirectOnly() bool {
	return p == nil || len(p.Routes) == 0 || (len(p.Routes) == 1 && p.Routes[0].Direct)
}

func (p *RoutePlan) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if p == nil || len(p.Routes) == 0 {
		return nil, errors.New("no proxy route is available")
	}
	if network == "" {
		network = "tcp"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if p.ApplyBypass && p.Bypass != nil && p.Bypass.Match(host) {
		return p.dialDirect(ctx, network, address)
	}
	routes := p.routesForAttempt()
	var lastErr error
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, dialErr := p.dialRoute(ctx, network, address, route)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("proxy connection failed")
	}
	return nil, fmt.Errorf("proxy connection failed: %w", lastErr)
}

func (p *RoutePlan) routesForAttempt() []Route {
	if p.Healthy == nil {
		return p.Routes
	}
	healthy := make([]Route, 0, len(p.Routes))
	fallback := make([]Route, 0, len(p.Routes))
	for _, route := range p.Routes {
		if route.Direct || p.Healthy(route.Profile.ID) {
			healthy = append(healthy, route)
		} else {
			fallback = append(fallback, route)
		}
	}
	if len(healthy) > 0 {
		return append(healthy, fallback...)
	}
	return p.Routes
}

func (p *RoutePlan) dialRoute(ctx context.Context, network, address string, route Route) (net.Conn, error) {
	if route.Direct {
		return p.dialDirect(ctx, network, address)
	}
	switch route.Profile.Protocol {
	case types.ProxyProtocolHTTP, types.ProxyProtocolHTTPS:
		return p.dialHTTPProxy(ctx, network, address, route.Profile)
	case types.ProxyProtocolSOCKS5, types.ProxyProtocolSOCKS5H:
		return p.dialSOCKS5(ctx, network, address, route.Profile)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol %q", route.Profile.Protocol)
	}
}

func (p *RoutePlan) dialDirect(ctx context.Context, network, address string) (net.Conn, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return dialer.DialContext(ctx, network, address)
}

func (p *RoutePlan) dialHTTPProxy(ctx context.Context, network, address string, profile types.ProxyProfile) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	conn, err := p.dialDirect(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}
	if profile.Protocol == types.ProxyProtocolHTTPS {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: profile.Host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("proxy TLS handshake: %w", err)
		}
		conn = tlsConn
	}
	var builder strings.Builder
	builder.WriteString("CONNECT ")
	builder.WriteString(address)
	builder.WriteString(" HTTP/1.1\r\nHost: ")
	builder.WriteString(address)
	builder.WriteString("\r\n")
	if profile.Username != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(profile.Username + ":" + profile.Password))
		builder.WriteString("Proxy-Authorization: Basic ")
		builder.WriteString(auth)
		builder.WriteString("\r\n")
	}
	builder.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
	if _, err := io.WriteString(conn, builder.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write proxy request: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read proxy response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
	}
	_ = resp.Body.Close()
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

func (p *RoutePlan) dialSOCKS5(ctx context.Context, network, address string, profile types.ProxyProfile) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	conn, err := p.dialDirect(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid target address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		_ = conn.Close()
		return nil, errors.New("invalid target port")
	}
	if profile.Username != "" {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
			_ = conn.Close()
			return nil, err
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if reply[0] != 0x05 {
		_ = conn.Close()
		return nil, errors.New("invalid SOCKS version")
	}
	if reply[1] == 0x02 {
		if profile.Username == "" {
			_ = conn.Close()
			return nil, errors.New("SOCKS proxy requires authentication")
		}
		user := []byte(profile.Username)
		pass := []byte(profile.Password)
		if len(user) > 255 || len(pass) > 255 {
			_ = conn.Close()
			return nil, errors.New("SOCKS credentials are too long")
		}
		auth := append([]byte{0x01, byte(len(user))}, user...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, pass...)
		if _, err := conn.Write(auth); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if reply[1] != 0x00 {
			_ = conn.Close()
			return nil, errors.New("SOCKS authentication failed")
		}
	} else if reply[1] != 0x00 {
		_ = conn.Close()
		return nil, errors.New("SOCKS proxy rejected authentication method")
	}
	targetHost := host
	if profile.Protocol == types.ProxyProtocolSOCKS5 {
		if net.ParseIP(host) == nil {
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				_ = conn.Close()
				return nil, fmt.Errorf("local DNS lookup failed: %w", err)
			}
			targetHost = ips[0].IP.String()
		}
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(targetHost); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(targetHost) > 255 {
			_ = conn.Close()
			return nil, errors.New("target hostname is too long")
		}
		request = append(request, 0x03, byte(len(targetHost)))
		request = append(request, []byte(targetHost)...)
	}
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		_ = conn.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if head[0] != 0x05 {
		_ = conn.Close()
		return nil, errors.New("invalid SOCKS reply")
	}
	if head[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("SOCKS connection failed: %s", socksError(head[1]))
	}
	switch head[3] {
	case 0x01:
		_, err = io.CopyN(io.Discard, conn, 4)
	case 0x03:
		length := make([]byte, 1)
		if _, err = io.ReadFull(conn, length); err == nil {
			_, err = io.CopyN(io.Discard, conn, int64(length[0]))
		}
	case 0x04:
		_, err = io.CopyN(io.Discard, conn, 16)
	default:
		err = errors.New("invalid SOCKS address type")
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func socksError(code byte) string {
	switch code {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return "unknown error"
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
