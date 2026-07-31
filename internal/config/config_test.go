package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppAcceptsLoopbackAndSecretFiles(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	tempDir := t.TempDir()
	writeTestSecret(t, filepath.Join(tempDir, "database"), "postgres://dockside:test@db/dockside")
	writeTestSecret(t, filepath.Join(tempDir, "encryption"), base64.RawURLEncoding.EncodeToString(key))
	writeTestSecret(t, filepath.Join(tempDir, "session"), string(key))
	writeTestSecret(t, filepath.Join(tempDir, "discord"), "discord-secret")
	writeTestSecret(t, filepath.Join(tempDir, "engine"), "engine-secret")
	writeTestSecret(t, filepath.Join(tempDir, "bootstrap"), "bootstrap-secret")

	t.Setenv("DOCKSIDE_INSTANCE_ID", "test-installation")
	t.Setenv("DOCKSIDE_PUBLIC_URL", "http://127.0.0.1:8080/")
	t.Setenv("DOCKSIDE_DISCORD_CLIENT_ID", "discord-client-id")
	t.Setenv("DOCKSIDE_DATABASE_URL_FILE", filepath.Join(tempDir, "database"))
	t.Setenv("DOCKSIDE_ENCRYPTION_KEY_FILE", filepath.Join(tempDir, "encryption"))
	t.Setenv("DOCKSIDE_SESSION_KEY_FILE", filepath.Join(tempDir, "session"))
	t.Setenv("DOCKSIDE_DISCORD_CLIENT_SECRET_FILE", filepath.Join(tempDir, "discord"))
	t.Setenv("DOCKSIDE_ENGINE_TOKEN_FILE", filepath.Join(tempDir, "engine"))
	t.Setenv("DOCKSIDE_BOOTSTRAP_TOKEN_FILE", filepath.Join(tempDir, "bootstrap"))

	cfg, err := Load("app")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.PublicURL.String(); got != "http://127.0.0.1:8080" {
		t.Fatalf("PublicURL = %q, want normalized loopback URL", got)
	}
	if string(cfg.EncryptionKey) != string(key) || string(cfg.SessionKey) != string(key) {
		t.Fatal("Load() did not decode the configured keys")
	}
}

func TestLoadAppRejectsInsecureExternalURL(t *testing.T) {
	t.Setenv("DOCKSIDE_INSTANCE_ID", "test-installation")
	t.Setenv("DOCKSIDE_PUBLIC_URL", "http://panel.example.com")
	t.Setenv("DOCKSIDE_DISCORD_CLIENT_ID", "discord-client-id")

	if _, err := Load("app"); err == nil {
		t.Fatal("Load() accepted HTTP for an external host")
	}
}

func TestReadKeyRejectsWrongLength(t *testing.T) {
	t.Setenv("TEST_KEY", "short-key")
	if _, err := readKey("TEST_KEY", "TEST_KEY_FILE", true); err == nil {
		t.Fatal("readKey() accepted an invalid key")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false", host)
		}
	}
	if isLoopbackHost("panel.example.com") {
		t.Error("isLoopbackHost() accepted an external host")
	}
}

func writeTestSecret(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write secret %s: %v", path, err)
	}
}
