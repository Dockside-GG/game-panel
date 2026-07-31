package identity

import (
	"encoding/base64"
	"regexp"
	"testing"
)

func TestNewUUIDReturnsRFC4122Version4(t *testing.T) {
	t.Parallel()

	value, err := NewUUID()
	if err != nil {
		t.Fatalf("NewUUID() error = %v", err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(value) {
		t.Fatalf("NewUUID() = %q, want RFC 4122 version 4 UUID", value)
	}
}

func TestTokenHasRequestedEntropyAndIsUnique(t *testing.T) {
	t.Parallel()

	first, err := Token(32)
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	second, err := Token(32)
	if err != nil {
		t.Fatalf("Token() second error = %v", err)
	}
	if first == second {
		t.Fatal("Token() returned the same random token twice")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("Token() returned invalid base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("Token() decoded length = %d, want 32", len(decoded))
	}
}

func TestTokenRejectsShortLength(t *testing.T) {
	t.Parallel()

	if _, err := Token(15); err == nil {
		t.Fatal("Token() accepted fewer than 16 bytes")
	}
}
