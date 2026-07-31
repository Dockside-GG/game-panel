package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/dockside-gg/game-panel/internal/templates"
)

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

func TestRCONCommandEnvironmentUsesDeclaredShellTransport(t *testing.T) {
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

func TestResolveRESTRequestSelectsRouteAndRendersArguments(t *testing.T) {
	t.Parallel()
	spec := &templates.RESTCommandTransport{
		Port: 8212,
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
		BasicAuth: &templates.RESTBasicAuth{
			Username:            "admin",
			PasswordEnvironment: "ADMIN_PASSWORD",
		},
		Routes: []templates.RESTCommandRoute{{
			Command:      "announce",
			Aliases:      []string{"broadcast"},
			Usage:        "announce <message>",
			MinArgs:      1,
			Method:       "POST",
			Path:         "/v1/api/announce",
			BodyTemplate: `{"message":{{ARGS_JSON}}}`,
		}},
	}
	request, err := resolveRESTRequest(
		spec,
		"broadcast Server restart in five minutes",
		map[string]string{"ADMIN_PASSWORD": "secret-value"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.method != "POST" || request.path != "/v1/api/announce" {
		t.Fatalf("request target = %s %s", request.method, request.path)
	}
	if request.body != `{"message":"Server restart in five minutes"}` {
		t.Fatalf("request body = %q", request.body)
	}
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret-value"))
	if request.headers["Authorization"] != expectedAuth {
		t.Fatalf("authorization header = %q", request.headers["Authorization"])
	}
}

func TestResolveRESTRequestRendersPositionalArguments(t *testing.T) {
	t.Parallel()
	spec := &templates.RESTCommandTransport{
		Port: 8212,
		Routes: []templates.RESTCommandRoute{{
			Command:      "shutdown",
			Usage:        "shutdown <seconds> [message]",
			MinArgs:      1,
			Method:       "POST",
			Path:         "/v1/api/shutdown",
			BodyTemplate: `{"waittime":{{ARG1_INT}},"message":{{ARGS_AFTER_1_JSON}}}`,
		}},
	}
	request, err := resolveRESTRequest(
		spec,
		"shutdown 30 Server restarting",
		map[string]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.body != `{"waittime":30,"message":"Server restarting"}` {
		t.Fatalf("request body = %q", request.body)
	}
}

func TestConsoleWriterEmitsGameLinesIndependently(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	writer := &lineFrameWriter{
		stream: "stderr",
		phase:  "runtime",
		encoder: &synchronizedFrameEncoder{
			writer: &output,
		},
	}
	if _, err := writer.Write([]byte("detail follows:\nwith error:\n  application-owned detail\n")); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	want := []string{"detail follows:", "with error:", "  application-owned detail"}
	for index, expected := range want {
		var frame consoleFrame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatalf("decode frame %d: %v", index, err)
		}
		if frame.Message != expected {
			t.Fatalf("frame %d message = %q, want %q", index, frame.Message, expected)
		}
	}
	var unexpected consoleFrame
	if err := decoder.Decode(&unexpected); err != io.EOF {
		t.Fatalf("console writer emitted an unexpected frame: %#v (decode error: %v)", unexpected, err)
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	if got := stripANSI("\x1b[31merror\x1b[0m"); got != "error" {
		t.Fatalf("stripANSI() = %q", got)
	}
}
