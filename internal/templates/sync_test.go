package templates

import (
	"encoding/json"
	"testing"
)

func TestValidateCatalogNormalizesDefinitions(t *testing.T) {
	source := json.RawMessage(`{
		"name": "Catalog Test",
		"docker_images": {"Default": "alpine:3.22"},
		"startup": "./server",
		"variables": []
	}`)
	entry, err := Normalize("dockside", "Games", "", source)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		FormatVersion:  BundleFormatVersion,
		CatalogVersion: "1.2.3",
		GeneratedAt:    "2026-07-30T20:00:00Z",
		Templates:      []TemplateEntry{entry},
	}
	result, err := validateCatalog(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Slug != entry.Slug {
		t.Fatalf("unexpected normalized catalog result: %#v", result)
	}
}

func TestValidateCatalogRejectsInvalidVersionAndDuplicateSlug(t *testing.T) {
	source := json.RawMessage(`{
		"name": "Catalog Test",
		"docker_images": {"Default": "alpine:3.22"},
		"startup": "./server",
		"variables": []
	}`)
	entry, err := Normalize("dockside", "Games", "", source)
	if err != nil {
		t.Fatal(err)
	}
	for name, bundle := range map[string]Bundle{
		"invalid version": {
			FormatVersion: BundleFormatVersion, CatalogVersion: "next",
			Templates: []TemplateEntry{entry},
		},
		"duplicate slug": {
			FormatVersion: BundleFormatVersion, CatalogVersion: "1.0.0",
			Templates: []TemplateEntry{entry, entry},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateCatalog(bundle); err == nil {
				t.Fatal("expected catalog validation error")
			}
		})
	}
}

func TestValidateCatalogAcceptsDigestSuffixedCollisionSlug(t *testing.T) {
	source := json.RawMessage(`{
		"name": "Duplicate Name",
		"docker_images": {"Default": "alpine:3.22"},
		"startup": "./server",
		"variables": []
	}`)
	entry, err := Normalize("dockside", "Games", "", source)
	if err != nil {
		t.Fatal(err)
	}
	entry.Slug += "-" + entry.SourceDigest[:10]
	result, err := validateCatalog(Bundle{
		FormatVersion: BundleFormatVersion, CatalogVersion: "1.0.0",
		Templates: []TemplateEntry{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result[0].Slug != entry.Slug {
		t.Fatalf("collision slug changed to %q", result[0].Slug)
	}
}

func TestValidateCatalogRejectsCompatibilityDefinitions(t *testing.T) {
	source := json.RawMessage(`{
		"name": "Remote Compatibility Definition",
		"docker_images": {"Default": "alpine:3.22"},
		"startup": "./server",
		"variables": []
	}`)
	entry, err := Normalize("pterodactyl", "Games", "", source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateCatalog(Bundle{
		FormatVersion: BundleFormatVersion, CatalogVersion: "1.0.0",
		GeneratedAt: "2026-07-30T20:00:00Z",
		Templates:   []TemplateEntry{entry},
	})
	if err == nil {
		t.Fatal("expected remote compatibility definition to be rejected")
	}
}
