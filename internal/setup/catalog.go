package setup

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func loadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog %s: %w", path, err)
	}

	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	catalog.index()
	return catalog, nil
}
