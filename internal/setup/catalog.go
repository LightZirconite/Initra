package setup

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.embedded.yaml
var embeddedCatalog []byte

func loadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if len(embeddedCatalog) == 0 {
			return Catalog{}, fmt.Errorf("read catalog %s: %w", path, err)
		}
		data = embeddedCatalog
	}

	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	catalog.index()
	return catalog, nil
}
