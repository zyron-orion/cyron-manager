package proxy

import (
	"context"
	"strings"
	"testing"

	"cyronmanager/internal/types"
)

func TestProfileLabelHidesPassword(t *testing.T) {
	profile := types.ProxyProfile{
		Protocol: types.ProxyProtocolSOCKS5H, Host: "10.0.0.1", Port: 1080,
		Username: "user", Password: "hunter2",
	}
	label := ProfileLabel(profile)
	if strings.Contains(label, "hunter2") {
		t.Fatalf("label leaked the password: %s", label)
	}
	if label != "socks5h://user@10.0.0.1:1080" {
		t.Fatalf("label = %q", label)
	}
}

func TestTestProfileReportsFailureWithoutSecrets(t *testing.T) {
	profile := types.ProxyProfile{
		ID: "p1", Name: "closed", Protocol: types.ProxyProtocolHTTP,
		Host: "127.0.0.1", Port: 9, Username: "user", Password: "hunter2",
	}
	result := TestProfile(context.Background(), profile, "http://127.0.0.1:1")
	if result.OK {
		t.Fatal("expected failure against a closed port")
	}
	if result.Error == "" {
		t.Fatal("failure should carry an error message")
	}
	if strings.Contains(result.Error, "hunter2") || strings.Contains(result.ProfileTag, "hunter2") {
		t.Fatalf("result leaked the password: %+v", result)
	}
	if result.ErrorKind == "" {
		t.Fatal("failure should be classified")
	}
	if result.ProfileID != "p1" {
		t.Fatalf("profile id = %q", result.ProfileID)
	}
}

func TestTestPlanRejectsInvalidTestURL(t *testing.T) {
	plan := DirectPlan()
	result := TestPlan(context.Background(), plan, "p", "label", "ftp://example.com")
	if result.OK {
		t.Fatal("ftp test URL should be rejected")
	}
	if result.ErrorKind != "validation" {
		t.Fatalf("error kind = %q", result.ErrorKind)
	}
}

func TestParseExitIPHandlesCommonFormats(t *testing.T) {
	cases := map[string]string{
		`{"ip":"203.0.113.7"}`:               "203.0.113.7",
		`{"origin":"203.0.113.8, 10.0.0.1"}`: "203.0.113.8",
		"203.0.113.9\n":                      "203.0.113.9",
		"not an ip":                          "",
		"":                                   "",
	}
	for body, want := range cases {
		if got := parseExitIP([]byte(body)); got != want {
			t.Fatalf("parseExitIP(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestClassifyError(t *testing.T) {
	if ClassifyError(nil) != "" {
		t.Fatal("nil error should not be classified")
	}
	if got := ClassifyError(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("deadline = %q", got)
	}
}
