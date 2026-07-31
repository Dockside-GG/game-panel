package store

import (
	"testing"

	"github.com/dockside-gg/game-panel/internal/templates"
)

func TestApplyTemplatePortEnvironmentIncludesUnpublishedListener(t *testing.T) {
	environment := map[string]string{"REST_PORT": "9000"}
	applyTemplatePortEnvironment(environment, []templates.NetworkPort{{
		Name: "REST API", ContainerPort: 8080, Protocol: "tcp",
		InternalOnly: true, Environment: "rest_port",
	}})

	if environment["REST_PORT"] != "8080" {
		t.Fatalf("REST_PORT = %q, want 8080", environment["REST_PORT"])
	}
}

func TestApplyTemplatePortEnvironmentSkipsInvalidOrUnnamedListener(t *testing.T) {
	environment := map[string]string{}
	applyTemplatePortEnvironment(environment, []templates.NetworkPort{
		{ContainerPort: 8080},
		{ContainerPort: 0, Environment: "ZERO_PORT"},
	})

	if len(environment) != 0 {
		t.Fatalf("environment = %#v, want empty", environment)
	}
}
