package secure

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestProtectRoundTrip(t *testing.T) {
	value := "p@ssw0rd with spaces & symbols ✓"
	protected, err := Protect(value)
	if err != nil {
		t.Fatal(err)
	}
	if protected == value || strings.Contains(protected, value) {
		t.Fatalf("value was not transformed: %q", protected)
	}
	if !IsProtected(protected) {
		t.Fatalf("IsProtected(%q) = false", protected)
	}
	plain, err := Unprotect(protected)
	if err != nil {
		t.Fatal(err)
	}
	if plain != value {
		t.Fatalf("round trip = %q, want %q", plain, value)
	}
}

func TestProtectIsNonDeterministic(t *testing.T) {
	first, err := Protect("same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Protect("same")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two encryptions of the same value are identical; nonce is not random")
	}
}

func TestProtectEmptyValue(t *testing.T) {
	protected, err := Protect("")
	if err != nil {
		t.Fatal(err)
	}
	if protected != "" {
		t.Fatalf("empty value produced %q", protected)
	}
	plain, err := Unprotect("")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestUnprotectRejectsTamperedCiphertext(t *testing.T) {
	protected, err := Protect("tamper-me")
	if err != nil {
		t.Fatal(err)
	}
	prefix, payload, ok := strings.Cut(protected, ":")
	if !ok {
		t.Fatalf("unexpected format: %q", protected)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF
	tampered := prefix + ":" + base64.StdEncoding.EncodeToString(raw)
	if _, err := Unprotect(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestUnprotectRejectsUnprotectedValue(t *testing.T) {
	if _, err := Unprotect("plain-text"); err == nil {
		t.Fatal("plain value was accepted as protected")
	}
}

func TestUnprotectRejectsTruncatedPayload(t *testing.T) {
	if _, err := Unprotect("aes:" + base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Fatal("truncated payload was accepted")
	}
}

func TestIsProtected(t *testing.T) {
	if IsProtected("") {
		t.Fatal("empty value reported as protected")
	}
	if IsProtected("plain") {
		t.Fatal("plain value reported as protected")
	}
	if !IsProtected("dpapi:abc") || !IsProtected("aes:abc") {
		t.Fatal("known prefixes not recognised")
	}
}
