package proxy

import (
	"net/url"
	"regexp"
	"strings"
)

var userInfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/\s:@]+):([^/\s@]+)@`)

func Redact(text string) string {
	if text == "" {
		return text
	}
	return userInfoRe.ReplaceAllString(text, "$1$2:***@")
}

func RedactURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return Redact(raw)
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		parsed.User = url.UserPassword(parsed.User.Username(), "***")
	}
	return parsed.String()
}
