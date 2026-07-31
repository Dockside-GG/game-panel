package sanitize

import (
	"strings"
	"testing"
)

func TestTextRedactsInfrastructureSecrets(t *testing.T) {
	input := strings.Join([]string{
		`Post "https://discord.com/api/webhooks/123456/very-secret-token": DNS failed`,
		`Bearer abc.def.ghi`,
		`password=hunter2`,
		`postgres://dockside:database-secret@postgres/dockside`,
		`https://example.com/callback?token=callback-secret`,
	}, " ")
	got := Text(input)
	for _, secret := range []string{
		"very-secret-token", "abc.def.ghi", "hunter2", "database-secret", "callback-secret",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized value still contains %q: %s", secret, got)
		}
	}
}
