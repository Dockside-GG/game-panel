package store

import "testing"

func TestScheduleNextRunDisabledHasNoNextRun(t *testing.T) {
	t.Parallel()
	next, err := scheduleNextRun("0 4 * * *", "UTC", false)
	if err != nil {
		t.Fatalf("scheduleNextRun() error = %v", err)
	}
	if next != nil {
		t.Fatalf("scheduleNextRun() = %v, want nil", next)
	}
}

func TestScheduleNextRunEnabledCalculatesFutureRun(t *testing.T) {
	t.Parallel()
	next, err := scheduleNextRun("0 4 * * *", "UTC", true)
	if err != nil {
		t.Fatalf("scheduleNextRun() error = %v", err)
	}
	if next == nil {
		t.Fatal("scheduleNextRun() = nil")
	}
}
