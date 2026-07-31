package engine

import (
	"testing"
	"time"
)

func TestParseTimestampedLog(t *testing.T) {
	input := []byte("2026-07-30T03:00:01.123456789Z warning: low tick rate\ninvalid line\n")
	events := parseTimestampedLog(
		"11111111-1111-4111-8111-111111111111",
		"stderr",
		input,
	)
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].Stream != "stderr" || events[0].Message != "warning: low tick rate" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
	expected, _ := time.Parse(time.RFC3339Nano, "2026-07-30T03:00:01.123456789Z")
	if !events[0].ObservedAt.Equal(expected) {
		t.Fatalf("unexpected timestamp: %s", events[0].ObservedAt)
	}
}
