package engine

import "testing"

func TestSafeArchiveName(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]string{
		"./world/level.dat": "world/level.dat",
		"config.yml":        "config.yml",
	} {
		got, err := safeArchiveName(input)
		if err != nil || got != expected {
			t.Fatalf("safeArchiveName(%q) = %q, %v; want %q", input, got, err, expected)
		}
	}
	for _, input := range []string{"../../etc/passwd", "/etc/passwd"} {
		if _, err := safeArchiveName(input); err == nil {
			t.Fatalf("safeArchiveName(%q) accepted an unsafe path", input)
		}
	}
}

func TestBackupPathSelected(t *testing.T) {
	t.Parallel()
	if !backupPathSelected("world/level.dat", []string{"world/"}, []string{"*.log"}) {
		t.Fatal("included world file was not selected")
	}
	if backupPathSelected("latest.log", nil, []string{"*.log"}) {
		t.Fatal("excluded log file was selected")
	}
	if backupPathSelected("cache/item.bin", []string{"world/"}, nil) {
		t.Fatal("file outside include paths was selected")
	}
}

func TestValidateBackupFilters(t *testing.T) {
	t.Parallel()
	if err := validateBackupFilters([]string{"world/"}, []string{"*.log"}); err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"/etc", "../secret", ""} {
		if err := validateBackupFilters([]string{rule}, nil); err == nil {
			t.Fatalf("unsafe filter %q was accepted", rule)
		}
	}
}
