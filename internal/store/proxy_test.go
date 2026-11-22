package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/types"
)

func TestProxyProfilePasswordIsProtectedAtRest(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	profile := &types.ProxyProfile{
		ID: "p1", Name: "office", Protocol: types.ProxyProtocolSOCKS5H,
		Host: "proxy.example.com", Port: 1080, Username: "user",
		Password: "top-secret",
	}
	if err := s.SaveProxyProfile(profile); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT password_protected FROM proxy_profiles WHERE id=?`, profile.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "top-secret") {
		t.Fatalf("password stored in clear text: %q", raw)
	}
	if !secure.IsProtected(raw) {
		t.Fatalf("password is not protected: %q", raw)
	}
	loaded, err := s.GetProxyProfile(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !secure.IsProtected(loaded.Password) {
		t.Fatalf("store should hand back the protected value, got %q", loaded.Password)
	}
	plain, err := secure.Unprotect(loaded.Password)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "top-secret" {
		t.Fatalf("plaintext = %q", plain)
	}
}

func TestProxyProfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	profiles := []*types.ProxyProfile{
		{ID: "a", Name: "Alpha", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 8080},
		{ID: "b", Name: "Beta", Protocol: types.ProxyProtocolSOCKS5, Host: "10.0.0.2", Port: 1080, Username: "u", Password: "p"},
	}
	for _, profile := range profiles {
		if err := s.SaveProxyProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListProxyProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("profiles = %d, want 2", len(list))
	}
	if list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("order = %q,%q", list[0].ID, list[1].ID)
	}
	if err := s.DeleteProxyProfile("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProxyProfile("a"); err == nil {
		t.Fatal("deleted profile is still readable")
	}
}

func TestSaveProxyProfileKeepsProtectedValue(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	profile := &types.ProxyProfile{ID: "p", Name: "n", Protocol: types.ProxyProtocolHTTP, Host: "h", Port: 1, Password: "once"}
	if err := s.SaveProxyProfile(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetProxyProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProxyProfile(&loaded); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT password_protected FROM proxy_profiles WHERE id=?`, "p").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != loaded.Password {
		t.Fatal("re-saving an already protected password re-encrypted it")
	}
	plain, err := secure.Unprotect(stored)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "once" {
		t.Fatalf("password = %q", plain)
	}
}

func TestTaskCredentialsAreProtectedAtRest(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &types.Task{
		ID: "t1", URL: "https://example.com/file.zip", Title: "file.zip",
		Dir: dir, Engine: "http", Kind: "file", Status: types.StatusQueued,
		Username: "alice", Password: "task-pass", Cookies: "session=abc",
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	var password, cookies string
	if err := s.db.QueryRow(`SELECT password,cookies FROM tasks WHERE id=?`, task.ID).Scan(&password, &cookies); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(password, "task-pass") || !secure.IsProtected(password) {
		t.Fatalf("task password at rest = %q", password)
	}
	if strings.Contains(cookies, "session=abc") || !secure.IsProtected(cookies) {
		t.Fatalf("task cookies at rest = %q", cookies)
	}

	loaded, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Password != "task-pass" || loaded.Cookies != "session=abc" {
		t.Fatalf("loaded secrets = %q/%q", loaded.Password, loaded.Cookies)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "task-pass") || strings.Contains(string(encoded), "session=abc") {
		t.Fatalf("secrets leaked into JSON: %s", encoded)
	}
}

func TestCompleteTaskClearsCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &types.Task{
		ID: "t2", URL: "https://example.com/a.bin", Title: "a.bin", Dir: dir,
		Engine: "http", Kind: "file", Status: types.StatusQueued,
		Username: "bob", Password: "pw", Cookies: "c=1", BrowserCookies: "b=2",
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkActive(task.ID, "", 10); err != nil || !ok {
		t.Fatalf("mark active: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CompleteTask(task.ID, filepath.Join(dir, "a.bin"), 10, 10, 20); err != nil || !ok {
		t.Fatalf("complete: ok=%v err=%v", ok, err)
	}
	var password, cookies, browserCookies string
	if err := s.db.QueryRow(`SELECT password,cookies,browser_cookies FROM tasks WHERE id=?`, task.ID).
		Scan(&password, &cookies, &browserCookies); err != nil {
		t.Fatal(err)
	}
	if password != "" || cookies != "" || browserCookies != "" {
		t.Fatalf("credentials survived completion: %q/%q/%q", password, cookies, browserCookies)
	}
}

func TestMigrationEncryptsLegacyPlainSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO tasks (id,url,title,engine,kind,status,password,cookies,created_at,updated_at)
		VALUES ('legacy','https://example.com/x','x','http','file','completed','old-pass','old=cookie',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var password, cookies string
	if err := s2.db.QueryRow(`SELECT password,cookies FROM tasks WHERE id='legacy'`).Scan(&password, &cookies); err != nil {
		t.Fatal(err)
	}
	if !secure.IsProtected(password) || !secure.IsProtected(cookies) {
		t.Fatalf("legacy secrets were not encrypted: %q/%q", password, cookies)
	}
	plain, err := secure.Unprotect(password)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "old-pass" {
		t.Fatalf("legacy password = %q", plain)
	}
}
