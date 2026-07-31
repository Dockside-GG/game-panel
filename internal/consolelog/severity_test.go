package consolelog

import "testing"

func TestClassifyDoesNotEquateStderrWithFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stream, message, want string
	}{
		{"stderr", "steamclient.so: cannot open shared object file: No such file", "notice"},
		{"stderr", "[S_API] SteamAPI_Init(): Loaded '/home/container/.steam/sdk64/steamclient.so' OK.", "info"},
		{"stderr", "[S_API FAIL] Tried to access Steam interface before initialization", "warning"},
		{"stdout", "FATAL: address already in use", "fatal"},
		{"stdout", "Running dedicated server on :8211", "info"},
	}
	for _, test := range tests {
		if got := Classify(test.stream, test.message); got != test.want {
			t.Errorf("Classify(%q, %q) = %q, want %q", test.stream, test.message, got, test.want)
		}
	}
}
