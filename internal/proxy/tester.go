package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cyronmanager/internal/types"
)

func TestProfile(ctx context.Context, profile types.ProxyProfile, testURL string) types.ProxyTestResult {
	plan := &RoutePlan{Routes: []Route{{Profile: profile}}, Timeout: 20 * time.Second}
	result := TestPlan(ctx, plan, profile.ID, ProfileLabel(profile), testURL)
	result.ProfileID = profile.ID
	result.ProfileTag = ProfileLabel(profile)
	return result
}

func TestPlan(ctx context.Context, plan *RoutePlan, profileID, label, testURL string) types.ProxyTestResult {
	testURL, err := ValidateTestURL(testURL)
	result := types.ProxyTestResult{
		TestedAt:   time.Now().UnixMilli(),
		TestURL:    testURL,
		ProfileID:  profileID,
		ProfileTag: label,
	}
	if err != nil {
		result.Error = err.Error()
		result.ErrorKind = "validation"
		return result
	}
	parsed, err := url.Parse(testURL)
	if err != nil || parsed.Host == "" {
		result.Error = "آدرس تست پروکسی معتبر نیست"
		result.ErrorKind = "validation"
		return result
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           plan.DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		result.Error = err.Error()
		result.ErrorKind = "validation"
		return result
	}
	req.Header.Set("User-Agent", "CYRON-Manager/1.1")
	req.Header.Set("Accept", "text/plain, application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		result.LatencyMS = time.Since(start).Milliseconds()
		result.Error = cleanProxyError(err)
		result.ErrorKind = ClassifyError(err)
		return result
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	result.LatencyMS = time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("تست پروکسی با HTTP %d پاسخ داد", resp.StatusCode)
		if resp.StatusCode == http.StatusProxyAuthRequired {
			result.ErrorKind = "auth"
		} else {
			result.ErrorKind = "http"
		}
		return result
	}
	result.OK = true
	result.ExitIP = parseExitIP(body)
	return result
}

func parseExitIP(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload struct {
		IP     string `json:"ip"`
		Origin string `json:"origin"`
	}
	if json.Unmarshal(body, &payload) == nil {
		for _, value := range []string{payload.IP, payload.Origin} {
			for _, part := range strings.Split(value, ",") {
				if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
					return ip.String()
				}
			}
		}
	}
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == ','
	}) {
		if ip := net.ParseIP(strings.TrimSpace(field)); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return "tls"
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return "tls"
	}
	var recordErr x509.CertificateInvalidError
	if errors.As(err, &recordErr) {
		return "tls"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "407"), strings.Contains(lower, "authentication"), strings.Contains(lower, "auth failed"):
		return "auth"
	case strings.Contains(lower, "tls"), strings.Contains(lower, "certificate"), strings.Contains(lower, "handshake"):
		return "tls"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"):
		return "timeout"
	case strings.Contains(lower, "dns"), strings.Contains(lower, "lookup"):
		return "dns"
	case strings.Contains(lower, "refused"), strings.Contains(lower, "unreachable"), strings.Contains(lower, "reset"):
		return "connection"
	default:
		return "connection"
	}
}

func cleanProxyError(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 400 {
		message = message[:400]
	}
	return message
}
