package httpapi

import (
	"strings"
	"testing"

	"github.com/dockside-gg/game-panel/internal/templates"
)

func TestValidateTemplateVariablesKeepsLockedDefaults(t *testing.T) {
	t.Parallel()

	definitions := []templates.Variable{
		{
			Name:         "Steam application ID",
			Environment:  "SRCDS_APPID",
			DefaultValue: "2394010",
			UserEditable: false,
			Rules:        "required|integer",
		},
		{
			Name:         "Server name",
			Environment:  "SERVER_NAME",
			DefaultValue: "A DOCKSIDE.GG Panel Server",
			UserEditable: true,
			Rules:        "required|string",
		},
	}

	stored, err := validateTemplateVariables(definitions, map[string]string{
		"SERVER_NAME": "Community Palworld",
	})
	if err != nil {
		t.Fatalf("validateTemplateVariables() error = %v", err)
	}
	if len(stored) != 2 || stored[0].ValueText == nil || *stored[0].ValueText != "2394010" {
		t.Fatalf("stored variables = %#v", stored)
	}
}

func TestValidateTemplateVariablesAcceptsIdenticalLockedDefaultFromStaleClient(t *testing.T) {
	t.Parallel()

	definitions := []templates.Variable{{
		Environment: "SRCDS_APPID", DefaultValue: "2394010", UserEditable: false,
	}}
	if _, err := validateTemplateVariables(definitions, map[string]string{
		"SRCDS_APPID": "2394010",
	}); err != nil {
		t.Fatalf("validateTemplateVariables() error = %v", err)
	}
}

func TestValidateTemplateVariablesRejectsLockedOverride(t *testing.T) {
	t.Parallel()

	definitions := []templates.Variable{{
		Environment: "SRCDS_APPID", DefaultValue: "2394010", UserEditable: false,
	}}
	_, err := validateTemplateVariables(definitions, map[string]string{
		"SRCDS_APPID": "123",
	})
	if err == nil || !strings.Contains(err.Error(), "not user editable") {
		t.Fatalf("validateTemplateVariables() error = %v", err)
	}
}
