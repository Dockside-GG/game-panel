package worker

import (
	"strings"
	"testing"
	"time"
)

func TestScheduledBackupNameUsesScheduleTimezone(t *testing.T) {
	t.Parallel()
	planned := time.Date(2026, time.July, 31, 18, 45, 7, 0, time.UTC)
	got := scheduledBackupName("Nightly world", planned, "America/Chicago")
	if got != "Nightly world - 2026-07-31_13-45-07_CDT" {
		t.Fatalf("scheduledBackupName() = %q", got)
	}
}

func TestScheduledBackupNameIsLimitedAndFallsBackToUTC(t *testing.T) {
	t.Parallel()
	planned := time.Date(2026, time.July, 31, 18, 45, 7, 0, time.UTC)
	got := scheduledBackupName(strings.Repeat("server-", 30), planned, "invalid/timezone")
	if len([]rune(got)) > 120 {
		t.Fatalf("scheduledBackupName() length = %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "2026-07-31_18-45-07_UTC") {
		t.Fatalf("scheduledBackupName() = %q", got)
	}
}
