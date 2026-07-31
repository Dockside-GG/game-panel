package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dockside-gg/game-panel/internal/engineclient"
)

func TestValidatePanelUpdateRequest(t *testing.T) {
	t.Parallel()
	valid := engineclient.PanelUpdateRequest{
		CurrentVersion: "0.1.0-alpha.1",
		TargetVersion:  "0.1.0-alpha.2",
		ReleaseURL:     "https://github.com/Dockside-GG/game-panel/releases/tag/v0.1.0-alpha.2",
		ArchiveURL:     "https://github.com/Dockside-GG/game-panel/releases/download/v0.1.0-alpha.2/dockside-game-panel-0.1.0-alpha.2.zip",
		ChecksumsURL:   "https://github.com/Dockside-GG/game-panel/releases/download/v0.1.0-alpha.2/SHA256SUMS",
	}
	if err := validatePanelUpdateRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*engineclient.PanelUpdateRequest)
	}{
		{"untrusted host", func(input *engineclient.PanelUpdateRequest) {
			input.ArchiveURL = strings.Replace(input.ArchiveURL, "github.com", "example.com", 1)
		}},
		{"mismatched archive", func(input *engineclient.PanelUpdateRequest) {
			input.ArchiveURL = strings.Replace(input.ArchiveURL, "alpha.2.zip", "alpha.3.zip", 1)
		}},
		{"same version", func(input *engineclient.PanelUpdateRequest) { input.TargetVersion = input.CurrentVersion }},
		{"development version", func(input *engineclient.PanelUpdateRequest) { input.CurrentVersion = "dev" }},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if err := validatePanelUpdateRequest(input); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
}

func TestComposeFileBasenames(t *testing.T) {
	t.Parallel()
	got := composeFileBasenames("C:\\dockside\\compose.yml,/srv/dockside/compose.public.yml,/tmp/not-compose.txt")
	if strings.Join(got, ",") != "compose.yml,compose.public.yml" {
		t.Fatalf("unexpected compose files: %#v", got)
	}
}

func TestEnvironmentVersionAndSnapshotArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	environment := filepath.Join(root, ".env")
	if err := os.WriteFile(environment, []byte("NAME=dockside\nDOCKSIDE_VERSION=0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setEnvironmentVersion(environment, "0.2.0-alpha.1"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(environment)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "DOCKSIDE_VERSION=") != 1 || !strings.Contains(string(content), "DOCKSIDE_VERSION=0.2.0-alpha.1") {
		t.Fatalf("unexpected .env content: %q", content)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "secrets", "token"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "panel.tar.gz")
	if err := archiveDirectory(source, archive, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "restored")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGzip(archive, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(destination, "secrets", "token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "private" {
		t.Fatalf("unexpected restored content: %q", restored)
	}
}
