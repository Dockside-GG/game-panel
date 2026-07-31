package secure

import (
	"strings"
	"testing"
)

func TestBoxRoundTripAndAssociatedData(t *testing.T) {
	t.Parallel()

	box, err := NewBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewBox() error = %v", err)
	}

	sealed, err := box.Seal("discord-client-secret", []byte("installation:one"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if strings.Contains(sealed, "discord-client-secret") {
		t.Fatal("Seal() returned plaintext")
	}

	opened, err := box.Open(sealed, []byte("installation:one"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened != "discord-client-secret" {
		t.Fatalf("Open() = %q, want original plaintext", opened)
	}

	if _, err := box.Open(sealed, []byte("installation:two")); err == nil {
		t.Fatal("Open() succeeded with different associated data")
	}
}

func TestBoxRejectsInvalidKeyAndCiphertext(t *testing.T) {
	t.Parallel()

	if _, err := NewBox([]byte("too-short")); err == nil {
		t.Fatal("NewBox() accepted an invalid key")
	}

	box, err := NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewBox() error = %v", err)
	}
	if _, err := box.Open("not-valid-base64!", nil); err == nil {
		t.Fatal("Open() accepted invalid base64")
	}
}

func TestHashIsDeterministicAndDoesNotReturnInput(t *testing.T) {
	t.Parallel()

	first := Hash("single-use-token")
	second := Hash("single-use-token")
	if first != second {
		t.Fatal("Hash() is not deterministic")
	}
	if first == "single-use-token" || len(first) != 64 {
		t.Fatalf("Hash() returned unexpected value %q", first)
	}
}
