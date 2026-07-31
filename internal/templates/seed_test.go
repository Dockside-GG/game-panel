package templates

import (
	"strings"
	"testing"
)

func TestBundledPalworldProtonKeepsLockedAppIDServerSide(t *testing.T) {
	t.Parallel()

	bundle, err := LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	var matched *TemplateEntry
	for index := range bundle.Templates {
		name := strings.ToLower(bundle.Templates[index].Name)
		if strings.Contains(name, "palworld") && strings.Contains(name, "proton") {
			matched = &bundle.Templates[index]
			break
		}
	}
	if matched == nil {
		t.Fatal("bundled Palworld Proton template was not found")
	}

	var foundAppID bool
	for _, variable := range matched.CanonicalDocument.Variables {
		if variable.Environment == "SRCDS_APPID" {
			foundAppID = true
			if variable.UserEditable || variable.DefaultValue == "" {
				t.Fatalf("SRCDS_APPID variable = %#v", variable)
			}
		}
		if (strings.Contains(variable.Environment, "SERVER_NAME") ||
			strings.Contains(variable.Environment, "SESSION_NAME")) &&
			(strings.Contains(strings.ToLower(variable.DefaultValue), "pterodactyl") ||
				strings.Contains(strings.ToLower(variable.DefaultValue), "pelican")) {
			t.Fatalf("server-name placeholder was not normalized: %#v", variable)
		}
	}
	if !foundAppID {
		t.Fatal("bundled Palworld Proton template has no SRCDS_APPID variable")
	}
	if len(matched.CanonicalDocument.NetworkPorts) == 0 {
		t.Fatal("bundled Palworld template has no network contract")
	}
	primary := matched.CanonicalDocument.NetworkPorts[0]
	if !primary.Primary {
		t.Fatalf("bundled compatibility template has no primary allocation: %#v", primary)
	}
}
