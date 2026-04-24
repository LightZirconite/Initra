package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if _, ok := catalog.itemByID("sleep-policy"); !ok {
		t.Fatalf("expected sleep-policy item in catalog")
	}
	agent, ok := catalog.itemByID("initra-agent")
	if !ok {
		t.Fatalf("expected initra-agent item in catalog")
	}
	if !agent.AutoApply || !agent.RequiresAdmin {
		t.Fatalf("expected initra-agent to be mandatory auto_apply admin item")
	}
	fastfetch, ok := catalog.itemByID("fastfetch")
	if !ok {
		t.Fatalf("expected fastfetch item in catalog")
	}
	if !fastfetch.AutoApply {
		t.Fatalf("expected fastfetch to be auto_apply")
	}
	if _, ok := catalog.itemByID("everything-toolbar"); !ok {
		t.Fatalf("expected everything-toolbar item in catalog")
	}
	if _, ok := catalog.itemByID("everything"); ok {
		t.Fatalf("did not expect legacy everything item to remain in catalog")
	}
	if _, ok := catalog.itemByID("localsend"); !ok {
		t.Fatalf("expected localsend item in catalog")
	}
	if _, ok := catalog.itemByID("noisetorch"); !ok {
		t.Fatalf("expected noisetorch item in catalog")
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
			{Item: Item{ID: "windows-update"}, Phase: phaseMaintenance},
			{Item: Item{ID: "spotify"}, Phase: phaseApplications},
			{Item: Item{ID: "theme-dark"}, Phase: phasePostUpdate},
		},
	}
	sortPlanByPhase(&plan)
	got := []string{plan.Steps[0].Item.ID, plan.Steps[1].Item.ID, plan.Steps[2].Item.ID}
	want := []string{"windows-update", "spotify", "theme-dark"}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("unexpected order: got %v want %v", got, want)
		}
	}
}

func TestPhaseForSleepPolicy(t *testing.T) {
	if got := phaseForItem(Item{ID: "sleep-policy"}); got != phasePostUpdate {
		t.Fatalf("unexpected phase for sleep-policy: got %s want %s", got, phasePostUpdate)
	}
}

func TestPhaseForInitraAgent(t *testing.T) {
	if got := phaseForItem(Item{ID: "initra-agent"}); got != phaseMaintenance {
		t.Fatalf("unexpected phase for initra-agent: got %s want %s", got, phaseMaintenance)
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

func TestDefaultSelectionForItemReflectsProfile(t *testing.T) {
	profile := newProfile("generic")
	profile.Selected["firefox"] = true
	profile.SelectionSource["firefox"] = selectionPresetSelected

	if !defaultSelectionForItem(Item{ID: "firefox"}, profile) {
		t.Fatalf("expected preset-selected item to default to yes")
	}
	if !defaultSelectionForItem(Item{ID: "proton-vpn"}, profile) {
		t.Fatalf("expected non-selected item to default to yes for a consistent prompt flow")
	}
}

func TestBuildPlanDoesNotIncludeUnselectedProtonVPN(t *testing.T) {
	catalog := Catalog{
		Categories: []Category{{ID: "media", Name: "Media"}},
		Items: []Item{
			{
				ID:          "proton-vpn",
				Name:        "Proton VPN",
				Category:    "media",
				Platforms:   []string{"windows"},
				Description: "VPN client",
				Install: map[string]InstallSpec{
					"windows": {Methods: []Method{{Type: "winget", Package: "Proton.ProtonVPN"}}},
				},
			},
		},
	}
	catalog.index()
	env := Environment{OS: "windows"}
	profile := newProfile("generic")

	plan, err := buildPlan(catalog, env, profile, &Logger{})
	if err != nil {
		t.Fatalf("buildPlan() error = %v", err)
	}
	if len(plan.Steps) != 0 {
		t.Fatalf("expected no steps for unselected proton-vpn, got %d", len(plan.Steps))
	}
}

func TestParseWingetQueryDetected(t *testing.T) {
	output := `
Name          Id                 Version Available Source
---------------------------------------------------------
Proton VPN    Proton.ProtonVPN   3.6.0             winget
`
	if !parseWingetQueryDetected("Proton.ProtonVPN", output) {
		t.Fatalf("expected winget list output to detect package")
	}
	if parseWingetQueryDetected("Proton.ProtonVPN", "No installed package found matching input criteria.") {
		t.Fatalf("expected missing package output to return false")
	}
}

func TestParseWingetUpgradeAvailable(t *testing.T) {
	output := `
Name          Id                 Version Available Source
---------------------------------------------------------
Proton VPN    Proton.ProtonVPN   3.6.0   3.7.0     winget
`
	if !parseWingetUpgradeAvailable("Proton.ProtonVPN", output) {
		t.Fatalf("expected upgrade output to detect available update")
	}
	if parseWingetUpgradeAvailable("Proton.ProtonVPN", "No available upgrade found.") {
		t.Fatalf("expected no-upgrade output to return false")
	}
}

func TestDescribeResolvedAction(t *testing.T) {
	step := ResolvedStep{
		Method:        Method{Type: "winget", Package: "Proton.ProtonVPN"},
		PlannedAction: stepActionUpgrade,
	}
	if got := describeResolvedAction(step); got != "winget upgrade Proton.ProtonVPN" {
		t.Fatalf("unexpected resolved action: %s", got)
	}
}

func TestSessionReportSerialization(t *testing.T) {
	report := SessionReport{
		Version:    1,
		Status:     "success",
		StartedAt:  time.Now().Add(-2 * time.Minute),
		FinishedAt: time.Now(),
		Profile:    newProfile("generic"),
		Plan:       Plan{Preset: "generic"},
		StepResults: []StepResult{
			{ItemID: "firefox", ItemName: "Firefox", Outcome: stepOutcomeInstalled},
		},
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := saveSessionReport(path, &report); err != nil {
		t.Fatalf("saveSessionReport() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"status": "success"`) {
		t.Fatalf("expected serialized report status, got %s", text)
	}
	if !strings.Contains(text, `"item_id": "firefox"`) {
		t.Fatalf("expected serialized step result, got %s", text)
	}
}

func TestParseWindowsProcessIDs(t *testing.T) {
	got := parseWindowsProcessIDs("123\r\n  44\nbad\n123\n7")
	want := []int{7, 44, 123}
	if len(got) != len(want) {
		t.Fatalf("unexpected ids: got %v want %v", got, want)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("unexpected ids: got %v want %v", got, want)
		}
	}
}

func TestWindowsOnlyAndVersionSpecificVisibility(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "catalog", "catalog.yaml"))
	if err != nil {
		t.Fatalf("loadCatalog() error = %v", err)
	}

	nilesoft, ok := catalog.itemByID("nilesoft-shell")
	if !ok {
		t.Fatalf("expected nilesoft-shell item in catalog")
	}
	if itemVisibleOn(nilesoft, Environment{OS: "linux"}) {
		t.Fatalf("did not expect Windows-only nilesoft-shell to be visible on Linux")
	}
	if !itemVisibleOn(nilesoft, Environment{OS: "windows", Windows: WindowsInfo{ProductName: "Windows 10 Pro"}}) {
		t.Fatalf("expected nilesoft-shell to be visible on Windows 10")
	}
	if itemVisibleOn(nilesoft, Environment{OS: "windows", Windows: WindowsInfo{ProductName: "Windows 11 Pro"}}) {
		t.Fatalf("did not expect nilesoft-shell to be visible on Windows 11")
	}
}
