package store

import (
	"testing"
	"time"
)

func TestRuntimeServerStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		observed string
		desired  string
		previous string
		want     string
	}{
		{name: "running", observed: "running", desired: "running", previous: "starting", want: "running"},
		{name: "graceful stop remains transitional", observed: "running", desired: "stopped", previous: "stopping", want: "stopping"},
		{name: "docker restarting", observed: "restarting", desired: "running", previous: "running", want: "restarting"},
		{name: "unexpected exit is stopped", observed: "exited", desired: "running", previous: "running", want: "stopped"},
		{name: "requested exit is stopped", observed: "exited", desired: "stopped", previous: "stopping", want: "stopped"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := runtimeServerStatus(test.observed, test.desired, test.previous); got != test.want {
				t.Fatalf("runtimeServerStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRecoveryDelayIsBounded(t *testing.T) {
	t.Parallel()
	if got := recoveryDelay(1); got != 5*time.Second {
		t.Fatalf("recoveryDelay(1) = %s", got)
	}
	if got := recoveryDelay(5); got != 2*time.Minute {
		t.Fatalf("recoveryDelay(5) = %s", got)
	}
	if got := recoveryDelay(99); got != 2*time.Minute {
		t.Fatalf("recoveryDelay(99) = %s", got)
	}
}
