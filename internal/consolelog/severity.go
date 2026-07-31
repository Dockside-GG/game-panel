package consolelog

import "strings"

// Classify separates a process stream from the meaning of a message. Many game
// servers write routine startup diagnostics to stderr, so stderr alone is never
// treated as an error.
func Classify(stream, message string) string {
	value := strings.ToLower(strings.TrimSpace(message))
	if value == "" {
		return "info"
	}
	for _, fragment := range []string{
		"loaded '", "loaded successfully", "initialized successfully",
		"server started", "server is ready", "running dedicated server",
		"installation completed",
	} {
		if strings.Contains(value, fragment) {
			return "info"
		}
	}
	for _, fragment := range []string{
		"first tried local 'steamclient.so'", "steamclient.so: cannot open shared object file",
	} {
		if strings.Contains(value, fragment) {
			return "notice"
		}
	}
	for _, fragment := range []string{
		"segmentation fault", "fatal:", "[fatal]", "panic:", "core dumped",
		"unhandled exception", "out of memory", "oom killed",
	} {
		if strings.Contains(value, fragment) {
			return "fatal"
		}
	}
	for _, fragment := range []string{
		"traceback", "exception:", " error:", "[error]", "failed to start",
		"startup failure", "permission denied", "address already in use",
	} {
		if strings.Contains(value, fragment) {
			return "error"
		}
	}
	for _, fragment := range []string{
		"warning", "warn:", "[warn]", "deprecated", "low tick", "overloaded",
		"[s_api fail]", "dlopen failed", "connection refused",
	} {
		if strings.Contains(value, fragment) {
			return "warning"
		}
	}
	if stream == "stderr" {
		return "notice"
	}
	return "info"
}
