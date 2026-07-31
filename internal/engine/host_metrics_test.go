package engine

import "testing"

func TestHostCPUPercent(t *testing.T) {
	t.Parallel()

	percent, ok := hostCPUPercent(
		procSample{total: 1_000, idle: 600},
		procSample{total: 1_200, idle: 650},
	)
	if !ok || percent != 75 {
		t.Fatalf("hostCPUPercent() = %v, %v", percent, ok)
	}
}

func TestHostCPUPercentRejectsInvalidCounters(t *testing.T) {
	t.Parallel()

	if _, ok := hostCPUPercent(
		procSample{total: 1_000, idle: 600},
		procSample{total: 900, idle: 500},
	); ok {
		t.Fatal("hostCPUPercent() accepted counters that moved backwards")
	}
}
