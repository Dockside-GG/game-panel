package store

import "testing"

func TestMatchesTemplateStopCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		actual   string
		want     bool
	}{
		{name: "exact", template: "shutdown", actual: "shutdown", want: true},
		{name: "case and whitespace", template: " Quit ", actual: "quit now", want: true},
		{name: "template arguments", template: "save-and-exit true", actual: "save-and-exit false", want: true},
		{name: "different command", template: "shutdown", actual: "restart", want: false},
		{name: "empty template", template: "", actual: "shutdown", want: false},
		{name: "empty command", template: "shutdown", actual: "", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesTemplateStopCommand(test.template, test.actual); got != test.want {
				t.Fatalf("matchesTemplateStopCommand(%q, %q) = %t, want %t", test.template, test.actual, got, test.want)
			}
		})
	}
}
