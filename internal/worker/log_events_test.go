package worker

import "testing"

func TestClassifyAndSanitizeConsoleLog(t *testing.T) {
	if got := classifyConsoleLog("stdout", "[ERROR] connection failed"); got != "error" {
		t.Fatalf("expected error, got %q", got)
	}
	if got := classifyConsoleLog("stderr", "plain diagnostic"); got != "" {
		t.Fatalf("expected routine stderr to be ignored, got %q", got)
	}
	sanitized := sanitizeConsoleLog("\x1b[31mfatal: password=super-secret token:abc123456789\x1b[0m")
	if sanitized != "fatal: password=[REDACTED] token=[REDACTED]" {
		t.Fatalf("unexpected sanitized output: %q", sanitized)
	}
}
