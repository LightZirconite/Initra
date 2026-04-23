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
	preset, err := mergePreset(catalog, "personal")
	if err != nil {
		t.Fatalf("mergePreset() error = %v", err)
	}
	if !contains(preset.Selected, "mesh-agent") {
		t.Fatalf("expected personal preset to include mesh-agent")
	}
	if preset.Values["mesh_url"] == "" {
		t.Fatalf("expected personal preset mesh_url")
	}

	alias, err := mergePreset(catalog, "light")
	if err != nil {
		t.Fatalf("mergePreset(light) error = %v", err)
	}
	if !contains(alias.Selected, "mesh-agent") {
		t.Fatalf("expected light alias preset to include mesh-agent")
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

func TestSortPlanByPhase(t *testing.T) {
	plan := Plan{
		Steps: []ResolvedStep{
			{Item: Item{ID: "theme-dark"}, Phase: phasePostUpdate},
			{Item: Item{ID: "windows-update"}, Phase: phaseMaintenance},
			{Item: Item{ID: "spotify"}, Phase: phaseApplications},
		},
	}
	sortPlanByPhase(&plan)
	got := []string{plan.Steps[0].Item.ID, plan.Steps[1].Item.ID, plan.Steps[2].Item.ID}
	want := []string{"spotify", "windows-update", "theme-dark"}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("unexpected order: got %v want %v", got, want)
		}
	}
}

func TestUnwrapVencordSettings(t *testing.T) {
	raw := []byte(`{"settings":{"foo":true,"bar":{"baz":"qux"}}}`)
	unwrapped, err := unwrapVencordSettings(raw)
	if err != nil {
		t.Fatalf("unwrapVencordSettings() error = %v", err)
	}
	text := string(unwrapped)
	if !strings.Contains(text, `"foo": true`) {
		t.Fatalf("expected foo setting in unwrapped payload, got %s", text)
	}
	if strings.Contains(text, `"settings"`) {
		t.Fatalf("expected wrapper key to be removed, got %s", text)
	}
}

func TestProfileDependencySatisfied(t *testing.T) {
	item := Item{ID: "spicetify-marketplace", DependsOn: []string{"spotify"}}
	profile := newProfile("generic")
	if profileDependencySatisfied(item, profile) {
		t.Fatalf("expected dependency to be unsatisfied when spotify is not selected")
	}
	profile.Selected["spotify"] = true
	if !profileDependencySatisfied(item, profile) {
		t.Fatalf("expected dependency to be satisfied when spotify is selected")
	}
}
