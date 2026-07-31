package library

import (
	"embed"
	"fmt"
)

//go:embed generated/catalog.json
var content embed.FS

func Catalog() ([]byte, error) {
	value, err := content.ReadFile("generated/catalog.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded template catalog: %w", err)
	}
	return value, nil
}
