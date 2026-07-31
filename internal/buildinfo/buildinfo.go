package buildinfo

import "strings"

// Values are replaced through -ldflags for published container images. Source
// builds intentionally retain the development defaults.
var (
	Version  = "dev"
	Revision = "unknown"
	BuiltAt  = "unknown"
)

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	BuiltAt  string `json:"built_at"`
}

func Current() Info {
	return Info{
		Version:  valueOrDefault(Version, "dev"),
		Revision: valueOrDefault(Revision, "unknown"),
		BuiltAt:  valueOrDefault(BuiltAt, "unknown"),
	}
}

func valueOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
