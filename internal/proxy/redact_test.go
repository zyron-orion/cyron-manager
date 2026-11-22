package proxy

import (
	"strings"
	"testing"
)

func TestRedactMasksProxyPassword(t *testing.T) {
	cases := map[string]string{
		"http://cyron:s3cret@127.0.0.1:9000":             "http://cyron:***@127.0.0.1:9000",
		"connect failed via socks5h://user:pw@host:1080": "connect failed via socks5h://user:***@host:1080",
		"no credentials here":                            "no credentials here",
		"":                                               "",
	}
	for input, want := range cases {
		if got := Redact(input); got != want {
			t.Fatalf("Redact(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRedactKeepsNonProxyText(t *testing.T) {
	input := "HTTP 502 from 127.0.0.1:9000 while reading https://example.com/file.zip"
	if got := Redact(input); got != input {
		t.Fatalf("Redact changed text without credentials: %q", got)
	}
}

func TestRedactURL(t *testing.T) {
	if got := RedactURL("http://user:pw@127.0.0.1:9000"); strings.Contains(got, "pw") {
		t.Fatalf("RedactURL leaked: %q", got)
	}
	if got := RedactURL("http://127.0.0.1:9000"); got != "http://127.0.0.1:9000" {
		t.Fatalf("RedactURL = %q", got)
	}
}
