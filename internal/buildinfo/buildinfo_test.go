package buildinfo

import "testing"

func TestCurrentNormalizesEmptyLinkerValues(t *testing.T) {
	previousVersion, previousRevision, previousBuiltAt := Version, Revision, BuiltAt
	t.Cleanup(func() {
		Version, Revision, BuiltAt = previousVersion, previousRevision, previousBuiltAt
	})
	Version, Revision, BuiltAt = "  ", "", "\t"

	info := Current()
	if info.Version != "dev" || info.Revision != "unknown" || info.BuiltAt != "unknown" {
		t.Fatalf("unexpected normalized build info: %#v", info)
	}
}
