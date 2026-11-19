package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProxy(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isErr bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"http://1.2.3.4:8080", "http://1.2.3.4:8080", false},
		{"https://proxy.corp:3128", "https://proxy.corp:3128", false},
		{"SOCKS5://host:1080", "SOCKS5://host:1080", false},
		{"socks4://host:1080", "socks4://host:1080", false},
		{" 1.2.3.4:8080 ", "http://1.2.3.4:8080", false},
		{"localhost:9050", "http://localhost:9050", false},
		{"ftp://host:21", "", true},
		{"noport", "", true},
	}
	for _, c := range cases {
		got, err := normalizeProxy(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("normalizeProxy(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeProxy(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("normalizeProxy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseClock(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"00:00", 0, true},
		{"07:30", 450, true},
		{"23:59", 1439, true},
		{" 08:15 ", 495, true},
		{"24:00", 0, false},
		{"07:60", 0, false},
		{"7:30", 0, false},
		{"0730", 0, false},
		{"ab:cd", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseClock(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseClock(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestExtractExtension(t *testing.T) {
	dir := t.TempDir()
	target, err := extractExtension(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(target) != "browser-extension" {
		t.Fatalf("target = %q", target)
	}
	data, err := os.ReadFile(filepath.Join(target, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"manifest_version": 3`) {
		t.Fatalf("unexpected manifest: %s", data)
	}
}
