package consolelog

import "testing"

func TestClassifyDoesNotEquateStderrWithFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stream, message, want string
	}{
		{"stderr", "routine application diagnostic", "notice"},
		{"stderr", "the word error is game-owned text", "notice"},
		{"stdout", "fatal-looking game-owned text", "info"},
		{"system", "panel-generated lifecycle message", "info"},
	}
	for _, test := range tests {
		if got := Classify(test.stream, test.message); got != test.want {
			t.Errorf("Classify(%q, %q) = %q, want %q", test.stream, test.message, got, test.want)
		}
	}
}
