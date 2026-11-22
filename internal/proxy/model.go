package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cyronmanager/internal/types"
)

func NewID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func NormalizeProfile(input types.ProxyProfileInput) (types.ProxyProfile, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return types.ProxyProfile{}, errors.New("نام پروفایل را وارد کن")
	}
	if len([]rune(name)) > 128 {
		return types.ProxyProfile{}, errors.New("نام پروفایل بیش از حد طولانی است")
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	switch protocol {
	case "", "http":
		protocol = types.ProxyProtocolHTTP
	case "socks":
		protocol = types.ProxyProtocolSOCKS5
	case types.ProxyProtocolHTTPS, types.ProxyProtocolSOCKS5, types.ProxyProtocolSOCKS5H:
	default:
		return types.ProxyProfile{}, errors.New("پروتکل پروکسی پشتیبانی نمی‌شود")
	}
	host := normalizeHost(input.Host)
	if host == "" {
		return types.ProxyProfile{}, errors.New("آدرس پروکسی را وارد کن")
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return types.ProxyProfile{}, errors.New("آدرس پروکسی معتبر نیست")
	}
	if input.Port < 1 || input.Port > 65535 {
		return types.ProxyProfile{}, errors.New("پورت پروکسی باید بین ۱ تا ۶۵۵۳۵ باشد")
	}
	username := strings.TrimSpace(input.Username)
	if strings.ContainsAny(username, "\r\n") || strings.ContainsAny(input.Password, "\r\n") {
		return types.ProxyProfile{}, errors.New("نام کاربری یا رمز پروکسی معتبر نیست")
	}
	return types.ProxyProfile{
		ID:       strings.TrimSpace(input.ID),
		Name:     name,
		Protocol: protocol,
		Host:     host,
		Port:     input.Port,
		Username: username,
		Password: input.Password,
	}, nil
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	value = strings.TrimSuffix(value, ".")
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return strings.ToLower(value)
}

func ProfileView(profile types.ProxyProfile) types.ProxyProfileView {
	return types.ProxyProfileView{
		ID:          profile.ID,
		Name:        profile.Name,
		Protocol:    profile.Protocol,
		Host:        profile.Host,
		Port:        profile.Port,
		Username:    profile.Username,
		HasPassword: profile.Password != "",
		CreatedAt:   profile.CreatedAt,
		UpdatedAt:   profile.UpdatedAt,
		LoadError:   profile.LoadError,
	}
}

func ProfileURL(profile types.ProxyProfile) *url.URL {
	host := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	out := &url.URL{Scheme: profile.Protocol, Host: host}
	if profile.Username != "" {
		out.User = url.UserPassword(profile.Username, profile.Password)
	}
	return out
}

func ProfileLabel(profile types.ProxyProfile) string {
	host := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	if profile.Username != "" {
		host = profile.Username + "@" + host
	}
	return profile.Protocol + "://" + host
}

func ValidateTestURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return types.DefaultProxyTestURL, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("آدرس تست پروکسی معتبر نیست")
	}
	if parsed.User != nil {
		return "", errors.New("آدرس تست پروکسی نباید نام کاربری یا رمز داشته باشد")
	}
	return value, nil
}

func ParseLegacyProxy(value string) (types.ProxyProfileInput, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return types.ProxyProfileInput{}, errors.New("پروکسی خالی است")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return types.ProxyProfileInput{}, errors.New("پروکسی قدیمی معتبر نیست")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "socks":
		scheme = types.ProxyProtocolSOCKS5
	case "socks4":
		return types.ProxyProfileInput{}, errors.New("پروکسی SOCKS4 پشتیبانی نمی‌شود")
	case types.ProxyProtocolHTTP, types.ProxyProtocolHTTPS, types.ProxyProtocolSOCKS5, types.ProxyProtocolSOCKS5H:
	default:
		return types.ProxyProfileInput{}, errors.New("پروتکل پروکسی قدیمی پشتیبانی نمی‌شود")
	}
	port := 0
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return types.ProxyProfileInput{}, errors.New("پورت پروکسی قدیمی معتبر نیست")
		}
	}
	if port == 0 || parsed.Hostname() == "" {
		return types.ProxyProfileInput{}, errors.New("پروکسی قدیمی پورت یا آدرس معتبر ندارد")
	}
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	return types.ProxyProfileInput{
		Name:     "پروکسی قبلی",
		Protocol: scheme,
		Host:     parsed.Hostname(),
		Port:     port,
		Username: username,
		Password: password,
	}, nil
}
