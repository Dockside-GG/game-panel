package engine

import "testing"

func TestValidTail(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"20", "250", "5000"} {
		if !validTail(value) {
			t.Fatalf("validTail(%q) = false", value)
		}
	}
	for _, value := range []string{"", "0", "19", "5001", "-1", "all"} {
		if validTail(value) {
			t.Fatalf("validTail(%q) = true", value)
		}
	}
}

func TestRCONCommandEnvironmentUsesDeclaredPalworldTransport(t *testing.T) {
	t.Parallel()
	result, ok := rconCommandEnvironment([]string{
		`STARTUP=(while read cmd; do rcon -s -a "localhost:$RCON_PORT" "$cmd"; done)`,
		"RCON_PORT=25575",
		"ADMIN_PASSWORD=not-logged",
	})
	if !ok {
		t.Fatal("rconCommandEnvironment() did not recognize the declared transport")
	}
	if result.port != "25575" || result.password != "not-logged" {
		t.Fatalf("rcon environment = %#v", result)
	}
}

func TestRCONCommandEnvironmentRejectsUnsafePort(t *testing.T) {
	t.Parallel()
	if _, ok := rconCommandEnvironment([]string{
		"STARTUP=rcon -s -a localhost:$RCON_PORT",
		"RCON_PORT=25575;bad",
		"ADMIN_PASSWORD=secret",
	}); ok {
		t.Fatal("rconCommandEnvironment() accepted a non-numeric port")
	}
}

func TestConsoleLineGroupingJoinsDiagnosticFragments(t *testing.T) {
	t.Parallel()
	if !consoleLineContinues("dlopen failed trying to load:", "steamclient.so") {
		t.Fatal("loader filename was not grouped with its diagnostic")
	}
	if !consoleLineContinues("steamclient.so", "with error:") {
		t.Fatal("with error marker was not grouped with its diagnostic")
	}
	if !consoleLineContinues("with error:", "file not found") {
		t.Fatal("error explanation was not grouped with its marker")
	}
	if consoleLineContinues("file not found", "[S_API] Loaded fallback") {
		t.Fatal("independent log entry was incorrectly grouped")
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	if got := stripANSI("\x1b[31merror\x1b[0m"); got != "error" {
		t.Fatalf("stripANSI() = %q", got)
	}
}
