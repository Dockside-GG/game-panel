package consolelog

// Classify describes only which process stream produced a frame. It deliberately
// does not interpret game-specific text: the panel core remains neutral to every
// game, installer, and distribution platform.
func Classify(stream, _ string) string {
	if stream == "stderr" {
		return "notice"
	}
	return "info"
}
