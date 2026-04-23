package setup

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCatalog(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "catalog", "catalog.yaml"))
	if err != nil {
		t.Fatalf("loadCatalog() error = %v", err)
	}
	if len(catalog.Items) < 10 {
		t.Fatalf("expected many catalog items, got %d", len(catalog.Items))
	}
	if _, ok := catalog.itemByID("office"); !ok {
		t.Fatalf("expected office item in catalog")
	}
	autoRefresh, ok := catalog.itemByID("auto-refresh-rate")
	if !ok {
		t.Fatalf("expected auto-refresh-rate item in catalog")
	}
	if !autoRefresh.AutoApply {
		t.Fatalf("expected auto-refresh-rate to be auto_apply")
	}
}

func TestMergePreset(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "catalog", "catalog.yaml"))
	if err != nil {
		t.Fatalf("loadCatalog() error = %v", err)
	}
	preset, err := mergePreset(catalog, "light")
	if err != nil {
		t.Fatalf("mergePreset() error = %v", err)
	}
	if !contains(preset.Selected, "mesh-agent") {
		t.Fatalf("expected light preset to include mesh-agent")
	}
	if preset.Values["mesh_url"] == "" {
		t.Fatalf("expected light preset mesh_url")
	}
}

func TestMergeManagedFirefoxBlock(t *testing.T) {
	block := renderFirefoxLayoutUserJS(firefoxLayoutBundle{
		StringPrefs: map[string]string{
			"browser.toolbars.bookmarks.visibility": "always",
		},
	})

	merged := mergeManagedFirefoxBlock("user_pref(\"foo\", true);\n", block)
	if !containsString(merged, firefoxManagedBlockStart) {
		t.Fatalf("expected managed block to be appended")
	}

	replaced := mergeManagedFirefoxBlock(merged, block)
	if strings.Count(replaced, firefoxManagedBlockStart) != 1 {
		t.Fatalf("expected managed block to be replaced in place")
	}
}

func containsString(value, fragment string) bool {
	return strings.Contains(value, fragment)
}
