package proxy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/store"
	"cyronmanager/internal/types"
)

func newTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewManager(st), st
}

func rawQuery(t *testing.T, st *store.Store, query string, args ...any) string {
	t.Helper()
	var value string
	if err := st.RawDB().QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSaveProtectsPasswordAndHidesItFromViews(t *testing.T) {
	manager, st := newTestManager(t)
	view, err := manager.Save(types.ProxyProfileInput{
		Name: "work", Protocol: types.ProxyProtocolHTTP, Host: "10.1.1.1", Port: 8080,
		Username: "user", Password: "hunter2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !view.HasPassword {
		t.Fatal("view should report a stored password")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("password leaked into view JSON: %s", encoded)
	}
	var raw = rawQuery(t, st, `SELECT password_protected FROM proxy_profiles WHERE id=?`, view.ID)
	if !secure.IsProtected(raw) || strings.Contains(raw, "hunter2") {
		t.Fatalf("stored password = %q", raw)
	}
	profile, ok := manager.Get(view.ID)
	if !ok {
		t.Fatal("profile missing after save")
	}
	if profile.Password != "hunter2" {
		t.Fatalf("in-memory password = %q", profile.Password)
	}
}

func TestSaveKeepsPasswordWhenInputOmitsIt(t *testing.T) {
	manager, _ := newTestManager(t)
	view, err := manager.Save(types.ProxyProfileInput{
		Name: "work", Protocol: types.ProxyProtocolHTTP, Host: "10.1.1.1", Port: 8080, Password: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.Save(types.ProxyProfileInput{
		ID: view.ID, Name: "work renamed", Protocol: types.ProxyProtocolHTTP, Host: "10.1.1.1", Port: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasPassword {
		t.Fatal("password was dropped on update")
	}
	profile, _ := manager.Get(view.ID)
	if profile.Password != "first" {
		t.Fatalf("password = %q, want first", profile.Password)
	}
}

func TestReloadFlagsUndecryptablePassword(t *testing.T) {
	manager, st := newTestManager(t)
	if _, err := manager.Save(types.ProxyProfileInput{
		Name: "broken", Protocol: types.ProxyProtocolHTTP, Host: "10.1.1.1", Port: 8080, Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RawDB().Exec(`UPDATE proxy_profiles SET password_protected='dpapi:not-a-real-blob'`); err != nil {
		t.Fatal(err)
	}
	err := manager.Reload()
	if err == nil {
		t.Fatal("reload should report the undecryptable password")
	}
	list := manager.List()
	if len(list) != 1 {
		t.Fatalf("profiles = %d", len(list))
	}
	if list[0].LoadError == "" {
		t.Fatal("profile should carry a load error")
	}
	if list[0].HasPassword {
		t.Fatal("profile should not claim a usable password")
	}
}

func TestMigrateLegacyCreatesDefaultProfile(t *testing.T) {
	manager, _ := newTestManager(t)
	settings := types.Settings{Proxy: "socks5://user:pass@127.0.0.1:1080"}
	migrated, err := manager.MigrateLegacy(&settings)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected a migration")
	}
	if settings.Proxy != "" {
		t.Fatalf("legacy proxy not cleared: %q", settings.Proxy)
	}
	if settings.ProxyDefaultID == "" {
		t.Fatal("default profile id was not set")
	}
	profile, ok := manager.Get(settings.ProxyDefaultID)
	if !ok {
		t.Fatal("default profile is missing")
	}
	if profile.Protocol != types.ProxyProtocolSOCKS5 || profile.Host != "127.0.0.1" || profile.Port != 1080 {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.Username != "user" || profile.Password != "pass" {
		t.Fatalf("credentials = %q/%q", profile.Username, profile.Password)
	}
}

func TestMigrateLegacySkipsWhenProfilesExist(t *testing.T) {
	manager, _ := newTestManager(t)
	if _, err := manager.Save(types.ProxyProfileInput{
		Name: "existing", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 8080,
	}); err != nil {
		t.Fatal(err)
	}
	settings := types.Settings{Proxy: "http://127.0.0.1:9000"}
	migrated, err := manager.MigrateLegacy(&settings)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("migration should be skipped when profiles already exist")
	}
	if settings.Proxy != "http://127.0.0.1:9000" {
		t.Fatal("legacy value should be untouched when skipping")
	}
}

func TestMigrateLegacyReportsInvalidValue(t *testing.T) {
	manager, _ := newTestManager(t)
	settings := types.Settings{Proxy: "socks4://127.0.0.1:1080"}
	migrated, err := manager.MigrateLegacy(&settings)
	if err == nil {
		t.Fatal("expected an error for an unsupported legacy scheme")
	}
	if migrated {
		t.Fatal("invalid value must not migrate")
	}
	if settings.ProxyDefaultID != "" {
		t.Fatal("default id must stay empty")
	}
}

func TestResolveFailsClosedOnMissingProfile(t *testing.T) {
	manager, _ := newTestManager(t)
	if _, err := manager.Resolve(types.Task{ProxyMode: types.ProxyModeProfile, ProxyProfileID: "gone"}, types.Settings{}); err == nil {
		t.Fatal("missing profile must not resolve to a direct route")
	}
	if _, err := manager.Resolve(types.Task{ProxyMode: types.ProxyModeProfile}, types.Settings{}); err == nil {
		t.Fatal("empty profile id must not resolve to a direct route")
	}
}

func TestResolveInheritUsesDefaultAndFallbacks(t *testing.T) {
	manager, _ := newTestManager(t)
	primary, err := manager.Save(types.ProxyProfileInput{Name: "a", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := manager.Save(types.ProxyProfileInput{Name: "b", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.2", Port: 2})
	if err != nil {
		t.Fatal(err)
	}
	settings := types.Settings{
		ProxyDefaultID:   primary.ID,
		ProxyFallbackIDs: []string{fallback.ID, fallback.ID, "missing", primary.ID},
	}
	plan, err := manager.Resolve(types.Task{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Routes) != 2 {
		t.Fatalf("routes = %d, want 2 after de-duplication", len(plan.Routes))
	}
	if plan.DirectOnly() {
		t.Fatal("inherit with a default must not be direct-only")
	}

	settings.ProxyAllowDirectFallback = true
	plan, err = manager.Resolve(types.Task{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Routes[len(plan.Routes)-1].Direct {
		t.Fatal("direct fallback should be the last route")
	}
}

func TestResolveDirectIgnoresDefaultProfile(t *testing.T) {
	manager, _ := newTestManager(t)
	view, err := manager.Save(types.ProxyProfileInput{Name: "a", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.Resolve(types.Task{ProxyMode: types.ProxyModeDirect}, types.Settings{ProxyDefaultID: view.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DirectOnly() {
		t.Fatal("explicit direct must stay direct")
	}
}

func TestTestInputDoesNotPersist(t *testing.T) {
	manager, st := newTestManager(t)
	result, err := manager.TestInput(context.Background(), types.ProxyProfileInput{
		Name: "tmp", Protocol: types.ProxyProtocolHTTP, Host: "127.0.0.1", Port: 9,
	}, "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatal("connection to port 9 should not succeed")
	}
	profiles, err := st.ListProxyProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("TestInput persisted %d profile(s)", len(profiles))
	}
}

func TestTestInputRejectsUnsafeTestURL(t *testing.T) {
	manager, _ := newTestManager(t)
	result, err := manager.TestInput(context.Background(), types.ProxyProfileInput{
		Name: "tmp", Protocol: types.ProxyProtocolHTTP, Host: "127.0.0.1", Port: 9,
	}, "file:///etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.ErrorKind != "validation" {
		t.Fatalf("result = %+v", result)
	}
}

func TestListIsSortedByName(t *testing.T) {
	manager, _ := newTestManager(t)
	for _, name := range []string{"zeta", "Alpha", "middle"} {
		if _, err := manager.Save(types.ProxyProfileInput{
			Name: name, Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	list := manager.List()
	got := []string{list[0].Name, list[1].Name, list[2].Name}
	want := []string{"Alpha", "middle", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestFindByEndpointMatchesNormalizedEndpoint(t *testing.T) {
	manager, _ := newTestManager(t)
	saved, err := manager.Save(types.ProxyProfileInput{
		Name: "office", Protocol: types.ProxyProtocolHTTP, Host: "Proxy.Example.COM.", Port: 8080, Username: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := manager.FindByEndpoint("HTTP", "proxy.example.com", 8080, "user")
	if !ok || got.ID != saved.ID {
		t.Fatalf("FindByEndpoint = (%+v, %v), want id %q", got, ok, saved.ID)
	}
	if _, ok := manager.FindByEndpoint(types.ProxyProtocolHTTP, "proxy.example.com", 8081, "user"); ok {
		t.Fatal("FindByEndpoint matched a different port")
	}
	if _, ok := manager.FindByEndpoint(types.ProxyProtocolHTTP, "proxy.example.com", 8080, "other"); ok {
		t.Fatal("FindByEndpoint matched a different username")
	}
	if _, ok := manager.FindByEndpoint(types.ProxyProtocolSOCKS5, "proxy.example.com", 8080, "user"); ok {
		t.Fatal("FindByEndpoint matched a different protocol")
	}
}

func TestMigrateLegacyIsIdempotentWhenProfileExists(t *testing.T) {
	manager, _ := newTestManager(t)
	settings := types.Settings{Proxy: "http://127.0.0.1:9000"}
	if _, err := manager.MigrateLegacy(&settings); err != nil {
		t.Fatal(err)
	}
	before := len(manager.List())
	second := types.Settings{Proxy: "http://127.0.0.1:9000"}
	migrated, err := manager.MigrateLegacy(&second)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("second migration should be skipped when profiles exist")
	}
	if len(manager.List()) != before {
		t.Fatal("duplicate profile was created")
	}
}

func TestNewManagerLoadsExistingProfiles(t *testing.T) {
	manager, st := newTestManager(t)
	if _, err := manager.Save(types.ProxyProfileInput{
		Name: "persisted", Protocol: types.ProxyProtocolHTTP, Host: "10.0.0.1", Port: 1, Password: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := NewManager(st)
	list := reloaded.List()
	if len(list) != 1 || list[0].Name != "persisted" {
		t.Fatalf("list = %+v", list)
	}
	profile, _ := reloaded.Get(list[0].ID)
	if profile.Password != "pw" {
		t.Fatalf("password = %q", profile.Password)
	}
	_ = filepath.Separator
}
