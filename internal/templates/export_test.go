package templates

import (
	"encoding/json"
	"testing"
)

func TestExportDocksideRoundTrip(t *testing.T) {
	source := json.RawMessage(`{
		"name": "Export Test",
		"author": "Dockside",
		"description": "Round trip",
		"docker_images": {"Default": "alpine:3.22"},
		"startup": "./server --port {{SERVER_PORT}}",
		"config": {"stop": "quit"},
		"dockside": {
			"network_ports": [{
				"name": "Game", "purpose": "Game traffic",
				"container_port": 25565, "protocol": "udp",
				"primary": true, "required": true, "published": true,
				"environment": "SERVER_PORT"
			}],
			"backup_defaults": {"include_paths": ["world"], "retention_days": 7}
		},
		"variables": [{
			"name": "Port", "env_variable": "SERVER_PORT",
			"default_value": "25565", "user_viewable": true,
			"user_editable": true, "rules": "required|integer"
		}]
	}`)
	entry, err := Normalize("dockside", "Games", "", source)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(entry.CanonicalDocument)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := ExportDockside(canonical)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Normalize("dockside", "Games", "", exported)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.CanonicalDocument.StartupCommand != entry.CanonicalDocument.StartupCommand {
		t.Fatalf("startup changed: %q", roundTrip.CanonicalDocument.StartupCommand)
	}
	if got := roundTrip.CanonicalDocument.NetworkPorts; len(got) != 1 || got[0].Protocol != "udp" {
		t.Fatalf("network contract changed: %#v", got)
	}
}
