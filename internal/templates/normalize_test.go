package templates

import (
	"encoding/json"
	"testing"
)

func TestNormalizeSupportsPterodactylV2(t *testing.T) {
	t.Parallel()

	entry, err := Normalize("pterodactyl", "Minecraft", "https://example.invalid/paper.json", json.RawMessage(`{
		"name": "Paper",
		"author": "dockside@example.com",
		"description": "Minecraft server",
		"docker_images": {
			"Java 21": "ghcr.io/pelican-eggs/yolks:java_21"
		},
		"startup": "java -jar {{SERVER_JARFILE}}",
		"config": {"stop": "stop"},
		"scripts": {
			"installation": {
				"container": "ghcr.io/pelican-eggs/installers:alpine",
				"entrypoint": "ash",
				"script": "touch server.jar"
			}
		},
		"variables": [{
			"name": "Jar file",
			"env_variable": "SERVER_JARFILE",
			"default_value": "server.jar",
			"user_viewable": true,
			"user_editable": true,
			"rules": "required|string"
		}]
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if entry.CanonicalDocument.StartupCommand != "java -jar {{SERVER_JARFILE}}" {
		t.Fatalf("StartupCommand = %q", entry.CanonicalDocument.StartupCommand)
	}
	if entry.CanonicalDocument.StopCommand != "stop" {
		t.Fatalf("StopCommand = %q", entry.CanonicalDocument.StopCommand)
	}
	if len(entry.CanonicalDocument.Variables) != 1 ||
		entry.CanonicalDocument.Variables[0].Rules != "required|string" {
		t.Fatalf("Variables = %#v", entry.CanonicalDocument.Variables)
	}
}

func TestNormalizeSupportsPelicanV3Variants(t *testing.T) {
	t.Parallel()

	entry, err := Normalize("pelican", "Games", "https://example.invalid/game.yaml", json.RawMessage(`{
		"name": "Example",
		"docker_images": ["ghcr.io/example/game:latest"],
		"startup_commands": {"Default": "./server {{TOKEN}}"},
		"file_denylist": {},
		"config": {"stop": "^C"},
		"variables": [{
			"name": "Access token",
			"env_variable": "TOKEN",
			"default_value": "",
			"user_viewable": true,
			"user_editable": true,
			"rules": ["required", "string"]
		}]
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if entry.CanonicalDocument.StartupCommand != "./server {{TOKEN}}" {
		t.Fatalf("StartupCommand = %q", entry.CanonicalDocument.StartupCommand)
	}
	variable := entry.CanonicalDocument.Variables[0]
	if variable.Rules != "required|string" || !variable.Secret {
		t.Fatalf("Variable = %#v", variable)
	}
}

func TestNormalizeBrandsKnownUpstreamServerNamePlaceholder(t *testing.T) {
	t.Parallel()

	entry, err := Normalize("pterodactyl", "Games", "", json.RawMessage(`{
		"name": "Palworld Proton",
		"docker_images": {"Proton": "example.invalid/palworld:latest"},
		"startup": "./PalServer.sh",
		"variables": [{
			"name": "Server Name",
			"env_variable": "SERVER_NAME",
			"default_value": "A Pterodactyl hosted Palworld Server",
			"user_viewable": true,
			"user_editable": true,
			"rules": "required|string"
		}, {
			"name": "Steam App ID",
			"env_variable": "SRCDS_APPID",
			"default_value": "2394010",
			"user_viewable": false,
			"user_editable": false,
			"rules": "required|integer"
		}]
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got := entry.CanonicalDocument.Variables[0].DefaultValue; got != "A DOCKSIDE.GG Panel Server" {
		t.Fatalf("server-name default = %q", got)
	}
	if got := entry.CanonicalDocument.Variables[1].DefaultValue; got != "2394010" {
		t.Fatalf("locked application ID = %q", got)
	}
	if len(entry.CompatibilityReport.Warnings) != 1 {
		t.Fatalf("warnings = %#v", entry.CompatibilityReport.Warnings)
	}
}

func TestNormalizePreservesTemplateDefinedStartupBehavior(t *testing.T) {
	t.Parallel()

	const startup = "(while read cmd; do rcon -s -a \"localhost:$RCON_PORT\" -p \"$ADMIN_PASSWORD\" \"$cmd\";done) < /dev/stdin & ./PalServer"
	entry, err := Normalize("pterodactyl", "Games", "", json.RawMessage(`{
		"name": "Palworld Proton",
		"docker_images": {"Proton": "example.invalid/palworld:latest"},
		"startup": "(while read cmd; do rcon -s -a \"localhost:$RCON_PORT\" -p \"$ADMIN_PASSWORD\" \"$cmd\";done) < /dev/stdin & ./PalServer",
		"variables": []
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if entry.CanonicalDocument.StartupCommand != startup {
		t.Fatalf("startup command was changed: %q", entry.CanonicalDocument.StartupCommand)
	}
}

func TestNormalizeSeparatesAdvertisedAndContainerPorts(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("pterodactyl", "Games", "", json.RawMessage(`{
		"name": "Example",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server -port={{SERVER_PORT}} -publicport={{SERVER_PORT}}",
		"variables": []
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := entry.CanonicalDocument.StartupCommand; got !=
		"./server -port={{SERVER_PORT}} -publicport={{SERVER_PUBLIC_PORT}}" {
		t.Fatalf("startup command = %q", got)
	}
}

func TestNormalizeDoesNotRewriteDescriptionsOrUnrelatedDefaults(t *testing.T) {
	t.Parallel()

	entry, err := Normalize("pelican", "Games", "", json.RawMessage(`{
		"name": "Example",
		"description": "A Pelican hosted server definition.",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server",
		"variables": [{
			"name": "Welcome message",
			"env_variable": "MOTD",
			"default_value": "A Pelican hosted server",
			"user_viewable": true,
			"user_editable": true
		}]
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if entry.Description != "A Pelican hosted server definition." {
		t.Fatalf("description = %q", entry.Description)
	}
	if got := entry.CanonicalDocument.Variables[0].DefaultValue; got != "A Pelican hosted server" {
		t.Fatalf("MOTD default = %q", got)
	}
}

func TestNormalizeUsesExplicitGenericNetworkContract(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("dockside", "Games", "", json.RawMessage(`{
		"name": "Example UDP Game",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server --port {{GAME_PORT}} --query {{QUERY_PORT}}",
		"dockside": {"network_ports": [
			{"name":"Game","purpose":"Primary traffic","container_port":7777,"protocol":"udp","primary":true,"required":true,"published":true,"environment":"GAME_PORT"},
			{"name":"Query","purpose":"Server query","container_port":27015,"protocol":"udp","primary":false,"required":false,"published":true,"environment":"QUERY_PORT"},
			{"name":"RCON","purpose":"Remote console","container_port":27020,"protocol":"tcp","primary":false,"required":false,"published":false,"environment":"RCON_PORT"}
		]}
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got := len(entry.CanonicalDocument.NetworkPorts); got != 3 {
		t.Fatalf("network port count = %d", got)
	}
	if entry.CanonicalDocument.NetworkPorts[2].Published {
		t.Fatal("internal RCON allocation was unexpectedly published")
	}
}

func TestNormalizeDoesNotInferNetworkingFromAStorefrontName(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("pterodactyl", "Games", "", json.RawMessage(`{
		"name": "Storefront-hosted example",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server --distribution-port {{STEAM_PORT}}",
		"variables": [
			{"name":"Distribution port","env_variable":"STEAM_PORT","default_value":"27015","user_viewable":true,"user_editable":true}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var distribution *NetworkPort
	for index := range entry.CanonicalDocument.NetworkPorts {
		if entry.CanonicalDocument.NetworkPorts[index].Environment == "STEAM_PORT" {
			distribution = &entry.CanonicalDocument.NetworkPorts[index]
			break
		}
	}
	if distribution == nil {
		t.Fatal("generic port variable was not retained for provisioning")
	}
	if distribution.Protocol != "" || distribution.Primary {
		t.Fatalf("storefront name influenced networking: %#v", distribution)
	}
}

func TestNormalizeSupportsInternalOnlyNetworkPort(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("dockside", "Games", "", json.RawMessage(`{
		"name": "Internal REST Game",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server",
		"dockside": {"network_ports": [
			{"name":"Game","purpose":"Primary traffic","container_port":7777,"protocol":"udp","primary":true,"required":true,"published":true,"environment":"SERVER_PORT"},
			{"name":"REST","purpose":"Local command API","container_port":8080,"protocol":"tcp","primary":false,"required":false,"published":false,"internal_only":true,"environment":"REST_PORT"}
		]}
	}`))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	port := entry.CanonicalDocument.NetworkPorts[1]
	if !port.InternalOnly || port.Published || port.Required {
		t.Fatalf("internal REST port = %#v", port)
	}
}

func TestNormalizeRejectsPublishedInternalOnlyNetworkPort(t *testing.T) {
	t.Parallel()
	_, err := Normalize("dockside", "Games", "", json.RawMessage(`{
		"name": "Invalid Internal Port",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server",
		"dockside": {"network_ports": [
			{"name":"Game","purpose":"Primary traffic","container_port":7777,"protocol":"udp","primary":true,"required":true,"published":true,"environment":"SERVER_PORT"},
			{"name":"REST","purpose":"Local command API","container_port":8080,"protocol":"tcp","primary":false,"required":false,"published":true,"internal_only":true,"environment":"REST_PORT"}
		]}
	}`))
	if err == nil {
		t.Fatal("Normalize() unexpectedly accepted an externally published internal-only port")
	}
}

func TestNormalizeDocksideRESTAndBackupDefaults(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("dockside", "Games", "", json.RawMessage(`{
		"name": "REST Game",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server",
		"dockside": {
			"network_ports": [
				{"name":"Game","purpose":"Primary traffic","container_port":7777,"protocol":"udp","primary":true,"required":true,"published":true,"environment":"SERVER_PORT"}
			],
			"command_transport": {
				"type": "http_rest",
				"rest": {
					"method": "POST",
					"port_environment": "REST_PORT",
					"path": "/command",
					"body_template": "{\"command\":{{COMMAND_JSON}}}",
					"headers": {"Authorization":"Bearer {{ENV:REST_TOKEN}}"}
				}
			},
			"backup_defaults": {
				"include_paths": ["save/"],
				"exclude_globs": ["logs/"],
				"retention_days": 14
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if entry.CanonicalDocument.CommandTransport.Type != "http_rest" {
		t.Fatalf("transport = %#v", entry.CanonicalDocument.CommandTransport)
	}
	if entry.CanonicalDocument.CommandTransport.REST.TimeoutSeconds != 10 {
		t.Fatalf("REST timeout = %d", entry.CanonicalDocument.CommandTransport.REST.TimeoutSeconds)
	}
	if entry.CanonicalDocument.BackupDefaults.RetentionDays == nil ||
		*entry.CanonicalDocument.BackupDefaults.RetentionDays != 14 {
		t.Fatalf("backup defaults = %#v", entry.CanonicalDocument.BackupDefaults)
	}
}

func TestNormalizeDocksideRESTCommandRoutes(t *testing.T) {
	t.Parallel()
	entry, err := Normalize("dockside", "Games", "", json.RawMessage(`{
		"name": "Routed REST Game",
		"docker_images": {"Default": "example.invalid/game:latest"},
		"startup": "./server",
		"dockside": {
			"network_ports": [
				{"name":"Game","purpose":"Primary traffic","container_port":7777,"protocol":"udp","primary":true,"required":true,"published":true,"environment":"SERVER_PORT"}
			],
			"command_transport": {
				"type": "http_rest",
				"rest": {
					"port": 8212,
					"headers": {"Content-Type":"application/json"},
					"basic_auth": {
						"username": "admin",
						"password_environment": "admin_password"
					},
					"routes": [
						{"command":"info","aliases":["serverinfo"],"method":"GET","path":"/v1/api/info"},
						{"command":"announce","usage":"announce <message>","min_args":1,"method":"POST","path":"/v1/api/announce","body_template":"{\"message\":{{ARGS_JSON}}}"}
					]
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	rest := entry.CanonicalDocument.CommandTransport.REST
	if rest == nil || len(rest.Routes) != 2 {
		t.Fatalf("REST routes = %#v", rest)
	}
	if rest.BasicAuth == nil || rest.BasicAuth.PasswordEnvironment != "ADMIN_PASSWORD" {
		t.Fatalf("REST basic auth = %#v", rest.BasicAuth)
	}
	if rest.Method != "" || rest.Path != "" {
		t.Fatalf("routed transport retained a static request: %#v", rest)
	}
}
