package media

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cyronmanager/internal/proxy"
)

func TestAppendProxyConfig(t *testing.T) {
	base := []string{"--no-colors"}
	if got := appendProxyConfig(base, ""); len(got) != len(base) {
		t.Fatalf("empty config changed args: %v", got)
	}
	if got := appendProxyConfig(base, "   "); len(got) != len(base) {
		t.Fatalf("whitespace config changed args: %v", got)
	}
	want := []string{"--no-colors", "--config-locations", "/tmp/x.conf"}
	got := appendProxyConfig(base, " /tmp/x.conf ")
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestWriteProxyConfigIsPrivateAndCleanable(t *testing.T) {
	dir := t.TempDir()
	path, cleanup, err := writeProxyConfig(dir, "task-1", "http://cyron:secret@127.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("no config path returned")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows only maps the read-only bit onto file modes and relies on ACLs
	// instead, so the owner-only assertion is only meaningful on Unix.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions = %v, want owner-only", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--proxy http://cyron:secret@127.0.0.1:9000") {
		t.Fatalf("config content = %q", data)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config file was not removed")
	}
}

func TestWriteProxyConfigSkipsEmptyProxy(t *testing.T) {
	dir := t.TempDir()
	path, cleanup, err := writeProxyConfig(dir, "task-2", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if path != "" {
		t.Fatalf("path = %q, want empty for a direct task", path)
	}
	if _, err := os.Stat(filepath.Join(dir, "proxycfg")); !os.IsNotExist(err) {
		t.Fatal("proxy config directory was created for a direct task")
	}
}

func TestWriteProxyConfigSanitizesTaskID(t *testing.T) {
	dir := t.TempDir()
	path, cleanup, err := writeProxyConfig(dir, "../../evil/../id", "http://127.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if strings.Contains(path, "..") {
		t.Fatalf("path escaped the config directory: %s", path)
	}
	if filepath.Dir(path) != filepath.Join(dir, "proxycfg") {
		t.Fatalf("path = %s", path)
	}
}

func TestCleanProxyEnvRemovesAllCases(t *testing.T) {
	env := []string{
		"PATH=/bin",
		"HTTP_PROXY=http://one",
		"http_proxy=http://two",
		"HTTPS_PROXY=http://three",
		"ALL_PROXY=socks5://four",
		"NO_PROXY=localhost",
		"KEEP=value",
	}
	got := proxy.CleanEnv(env)
	want := []string{"PATH=/bin", "KEEP=value"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("clean env = %v, want %v", got, want)
	}
}
