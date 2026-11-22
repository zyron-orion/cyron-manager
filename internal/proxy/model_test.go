package proxy

import (
	"strings"
	"testing"

	"cyronmanager/internal/types"
)

func TestValidateTestURLRejectsEmbeddedCredentials(t *testing.T) {
	if _, err := ValidateTestURL("https://user:pass@example.com/ip"); err == nil {
		t.Fatal("credentials in the test URL must be rejected")
	}
	got, err := ValidateTestURL(" https://example.com/ip ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/ip" {
		t.Fatalf("url = %q", got)
	}
	if _, err := ValidateTestURL("file:///etc/passwd"); err == nil {
		t.Fatal("non-http scheme must be rejected")
	}
}

func TestProfileStringDoesNotLeakPassword(t *testing.T) {
	profile := types.ProxyProfile{
		ID: "p1", Name: "work", Protocol: types.ProxyProtocolHTTP,
		Host: "10.0.0.1", Port: 8080, Username: "user", Password: "hunter2",
	}
	text := profile.String()
	if strings.Contains(text, "hunter2") {
		t.Fatalf("password leaked: %s", text)
	}
	if !strings.Contains(text, "hasPassword=true") {
		t.Fatalf("string = %s", text)
	}
}

func TestParseBypassHandlesCommonForms(t *testing.T) {
	bypass := ParseBypass("*.example.com, .internal.test, 10.0.0.0/8, 192.168.1.5, , localhost")
	cases := map[string]bool{
		"example.com":      true,
		"api.example.com":  true,
		"internal.test":    true,
		"a.internal.test":  true,
		"10.1.2.3":         true,
		"192.168.1.5":      true,
		"localhost":        true,
		"localhost:8080":   true,
		"192.168.1.6":      false,
		"example.org":      false,
		"notinternal.test": false,
	}
	for host, want := range cases {
		if got := bypass.Match(host); got != want {
			t.Fatalf("Match(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestBypassStringRoundTrips(t *testing.T) {
	raw := "example.com\n10.0.0.0/8\n192.168.1.5"
	want := "example.com, 10.0.0.0/8, 192.168.1.5"
	text := ParseBypass(raw).String()
	if text != want {
		t.Fatalf("String() = %q, want %q", text, want)
	}
	// The Settings field is a single-line input, so the canonical form must
	// survive a round trip through it without losing separators.
	if again := ParseBypass(text).String(); again != want {
		t.Fatalf("round trip = %q, want %q", again, want)
	}
}

func TestDirectOnlyDetection(t *testing.T) {
	if !DirectPlan().DirectOnly() {
		t.Fatal("direct plan must be direct-only")
	}
	plan := &RoutePlan{Routes: []Route{{Profile: types.ProxyProfile{ID: "a"}}}}
	if plan.DirectOnly() {
		t.Fatal("proxy plan reported direct-only")
	}
	withFallback := &RoutePlan{Routes: []Route{{Profile: types.ProxyProfile{ID: "a"}}, {Direct: true}}}
	if withFallback.DirectOnly() {
		t.Fatal("plan with a proxy route reported direct-only")
	}
}

func TestCleanEnvStripsAllProxyVariants(t *testing.T) {
	env := []string{
		"PATH=/bin", "HTTP_PROXY=a", "http_proxy=b", "Https_Proxy=c",
		"ALL_PROXY=d", "all_proxy=e", "NO_PROXY=f", "no_proxy=g", "KEEP=h",
	}
	got := CleanEnv(env)
	if len(got) != 2 || got[0] != "PATH=/bin" || got[1] != "KEEP=h" {
		t.Fatalf("env = %v", got)
	}
}
