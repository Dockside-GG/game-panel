package sanitize

import (
	"regexp"
	"strings"
)

var (
	webhookURLPattern = regexp.MustCompile(
		`(?i)https://(?:canary\.|ptb\.)?(?:discord(?:app)?\.com)/api/webhooks/[^\s"'<>]+`,
	)
	bearerPattern     = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	assignmentPattern = regexp.MustCompile(
		`(?i)\b(password|passwd|token|secret|api[_-]?key|authorization)\b\s*[:=]\s*([^\s,;]+)`,
	)
	uriCredentialPattern = regexp.MustCompile(
		`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`,
	)
	sensitiveQueryPattern = regexp.MustCompile(
		`(?i)([?&](?:token|secret|key|signature|password)=)[^&#\s]+`,
	)
)

// Text removes credentials and bearer material from errors before they are
// logged, persisted, or returned to a browser. It intentionally operates on
// infrastructure errors only; game console output has a separate policy.
func Text(value string) string {
	value = webhookURLPattern.ReplaceAllString(value, "https://discord.com/api/webhooks/[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = assignmentPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = uriCredentialPattern.ReplaceAllString(value, "$1[REDACTED]@")
	value = sensitiveQueryPattern.ReplaceAllString(value, "$1[REDACTED]")
	return strings.TrimSpace(value)
}
