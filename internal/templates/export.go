package templates

import (
	"encoding/json"
	"fmt"
)

type docksideExportVariable struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Environment  string `json:"env_variable"`
	DefaultValue string `json:"default_value"`
	UserViewable bool   `json:"user_viewable"`
	UserEditable bool   `json:"user_editable"`
	Rules        string `json:"rules,omitempty"`
	FieldType    string `json:"field_type,omitempty"`
	Secret       bool   `json:"secret"`
}

func ExportDockside(canonicalDocument json.RawMessage) (json.RawMessage, error) {
	var canonical CanonicalTemplate
	if err := json.Unmarshal(canonicalDocument, &canonical); err != nil {
		return nil, fmt.Errorf("decode canonical template for export: %w", err)
	}
	variables := make([]docksideExportVariable, 0, len(canonical.Variables))
	for _, variable := range canonical.Variables {
		variables = append(variables, docksideExportVariable{
			Name:         variable.Name,
			Description:  variable.Description,
			Environment:  variable.Environment,
			DefaultValue: variable.DefaultValue,
			UserViewable: variable.UserViewable,
			UserEditable: variable.UserEditable,
			Rules:        variable.Rules,
			FieldType:    variable.FieldType,
			Secret:       variable.Secret,
		})
	}
	document := map[string]any{
		"_comment":      "Dockside.GG game server template",
		"meta":          map[string]any{"version": "DOCKSIDE_v1"},
		"name":          canonical.Name,
		"description":   canonical.Description,
		"docker_images": canonical.Images,
		"startup":       canonical.StartupCommand,
		"config": map[string]any{
			"stop": canonical.StopCommand,
		},
		"scripts": map[string]any{
			"installation": map[string]any{
				"script":     canonical.InstallScript,
				"container":  canonical.InstallContainer,
				"entrypoint": canonical.InstallEntrypoint,
			},
		},
		"variables":     variables,
		"file_denylist": canonical.FileDenylist,
		"features":      canonical.Features,
		"dockside": map[string]any{
			"network_ports":     canonical.NetworkPorts,
			"command_transport": canonical.CommandTransport,
			"backup_defaults":   canonical.BackupDefaults,
			"resource_defaults": canonical.ResourceDefaults,
		},
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Dockside template export: %w", err)
	}
	return encoded, nil
}
