package engine

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestSafeRelativePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		allowRoot bool
		want      string
		wantError bool
	}{
		{name: "root listing", input: "", allowRoot: true, want: "."},
		{name: "normal", input: "config/server.yml", want: "config/server.yml"},
		{name: "windows separator", input: `config\server.yml`, want: "config/server.yml"},
		{name: "cleaned", input: "config/../server.yml", want: "server.yml"},
		{name: "absolute", input: "/etc/passwd", wantError: true},
		{name: "escape", input: "../../etc/passwd", wantError: true},
		{name: "root mutation", input: ".", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := safeRelativePath(test.input, test.allowRoot)
			if test.wantError {
				if err == nil {
					t.Fatalf("safeRelativePath(%q) unexpectedly succeeded with %q", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("safeRelativePath(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("safeRelativePath(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseFileEntries(t *testing.T) {
	t.Parallel()
	name := "server config.yml"
	line := strings.Join([]string{
		base64.StdEncoding.EncodeToString([]byte(name)),
		"file",
		"42",
		"1700000000",
	}, "\t")
	entries, err := parseFileEntries("config", line+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != name ||
		entries[0].Path != "config/"+name || entries[0].Size != 42 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestExtractRegularFileLimit(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("hello")
	if err := writer.WriteHeader(&tar.Header{
		Name: "server.properties", Mode: 0o640, Size: int64(len(content)), ModTime: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractRegularFile(&archive, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content = %q, want %q", got, content)
	}
}
