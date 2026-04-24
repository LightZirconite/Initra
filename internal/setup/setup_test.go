package setup

import (
	"encoding/xml"
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
	if !agent.ContinueOnError {
		t.Fatalf("expected initra-agent failures to be non-fatal")
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
	simplewall, ok := catalog.itemByID("simplewall")
	if !ok {
		t.Fatalf("expected simplewall item in catalog")
	}
	if !simplewall.AutoApply || !simplewall.RequiresAdmin {
		t.Fatalf("expected simplewall to be mandatory auto_apply admin item")
	}
	if simplewall.Install["windows"].Methods[0].Type != "builtin" || simplewall.Install["windows"].Methods[0].Action != "simplewall" {
		t.Fatalf("expected simplewall to use the builtin configuration flow")
	}
	firstRun, ok := catalog.itemByID("first-run-apps")
	if !ok {
		t.Fatalf("expected first-run-apps item in catalog")
	}
	if !firstRun.AutoApply {
		t.Fatalf("expected first-run-apps to be auto_apply")
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
			{Item: Item{ID: "simplewall"}, Phase: phaseFinal},
			{Item: Item{ID: "first-run-apps"}, Phase: phaseFirstRun},
		},
	}
	sortPlanByPhase(&plan)
	got := []string{plan.Steps[0].Item.ID, plan.Steps[1].Item.ID, plan.Steps[2].Item.ID, plan.Steps[3].Item.ID, plan.Steps[4].Item.ID}
	want := []string{"windows-update", "spotify", "theme-dark", "first-run-apps", "simplewall"}
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

func TestPhaseForFinalSteps(t *testing.T) {
	if got := phaseForItem(Item{ID: "first-run-apps"}); got != phaseFirstRun {
		t.Fatalf("unexpected phase for first-run-apps: got %s want %s", got, phaseFirstRun)
	}
	if got := phaseForItem(Item{ID: "simplewall"}); got != phaseFinal {
		t.Fatalf("unexpected phase for simplewall: got %s want %s", got, phaseFinal)
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

func TestGitAuthCatalogItemFollowsGit(t *testing.T) {
	catalog, err := loadCatalog(filepath.Join("..", "..", "catalog", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gitIdx, ok := catalog.itemIndex["git"]
	if !ok {
		t.Fatalf("expected git item in catalog")
	}
	authIdx, ok := catalog.itemIndex["git-auth"]
	if !ok {
		t.Fatalf("expected git-auth item in catalog")
	}
	if authIdx != gitIdx+1 {
		t.Fatalf("expected git-auth immediately after git, got git=%d git-auth=%d", gitIdx, authIdx)
	}
	auth := catalog.Items[authIdx]
	if len(auth.Inputs) == 0 || auth.Inputs[len(auth.Inputs)-1].Type != "password" {
		t.Fatalf("expected git-auth to request a password-style token input")
	}
}

func TestBundledSimplewallProfileIsValid(t *testing.T) {
	profilePath := filepath.Join("..", "..", "app", "profile.xml")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var profile simplewallProfile
	if err := xml.Unmarshal(data, &profile); err != nil {
		t.Fatalf("simplewall profile XML is invalid: %v", err)
	}
	if strings.Contains(strings.ToLower(string(data)), `c:\users\light`) || strings.Contains(strings.ToLower(string(data)), `app-1.`) {
		t.Fatalf("simplewall profile should not contain user-specific app paths or app versions")
	}
	required := []string{
		"System",
		"%systemroot%\\system32\\svchost.exe",
		"%systemroot%\\system32\\WindowsPowerShell\\v1.0\\powershell.exe",
		"WinDefend",
		"%programfiles%\\simplewall\\simplewall.exe",
		"%programfiles%\\Initra Agent\\initra-agent.exe",
	}
	for _, want := range required {
		found := false
		for _, item := range profile.Apps.Items {
			if strings.EqualFold(item.Path, want) && item.IsEnabled == "true" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected enabled simplewall profile entry %q", want)
		}
	}
}

func TestAugmentSimplewallProfileAddsCurrentSetupBinary(t *testing.T) {
	source := filepath.Join("..", "..", "app", "profile.xml")
	tempDir := t.TempDir()
	programFiles := filepath.Join(tempDir, "Program Files")
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("SystemRoot", filepath.Join(tempDir, "Windows"))
	target := filepath.Join(tempDir, "profile.xml")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := augmentSimplewallProfile(target, Environment{OS: "windows"}); err != nil {
		t.Fatalf("augmentSimplewallProfile() error = %v", err)
	}
	augmented, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() augmented error = %v", err)
	}
	if current := currentBinaryPath(); current != "" && !strings.Contains(strings.ToLower(string(augmented)), strings.ToLower(current)) {
		t.Fatalf("expected augmented simplewall profile to include current setup binary %q", current)
	}
	if !strings.Contains(strings.ToLower(string(augmented)), strings.ToLower(programFiles+`\Initra Agent\initra-agent.exe`)) {
		t.Fatalf("expected augmented simplewall profile to include Initra Agent")
	}
	if !strings.Contains(strings.ToLower(string(augmented)), strings.ToLower(programFiles+`\simplewall\simplewall.exe`)) {
		t.Fatalf("expected augmented simplewall profile to include expanded simplewall path")
	}
	if !strings.Contains(strings.ToLower(string(augmented)), strings.ToLower(os.Getenv("SystemRoot")+`\system32\WindowsPowerShell\v1.0\powershell.exe`)) {
		t.Fatalf("expected augmented simplewall profile to include expanded PowerShell path")
	}
}

func TestExpandWindowsPercentEnv(t *testing.T) {
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("LOCALAPPDATA", `C:\Users\Setup\AppData\Local`)

	tests := map[string]string{
		`%programfiles%\Initra Agent\initra-agent.exe`:     `C:\Program Files\Initra Agent\initra-agent.exe`,
		`%LOCALAPPDATA%\Discord\Update.exe`:                `C:\Users\Setup\AppData\Local\Discord\Update.exe`,
		`%unknown_variable%\Tool\tool.exe`:                 `%unknown_variable%\Tool\tool.exe`,
		`System`:                                           `System`,
		`%programfiles%\simplewall\%unknown_variable%.exe`: `C:\Program Files\simplewall\%unknown_variable%.exe`,
	}
	for input, want := range tests {
		if got := expandWindowsPercentEnv(input); got != want {
			t.Fatalf("expandWindowsPercentEnv(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildPlanDoesNotWriteSimplewallProfile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("APPDATA", tempDir)
	catalog := Catalog{
		Categories: []Category{{ID: "tweaks", Name: "Tweaks"}},
		Items: []Item{
			{
				ID:        "simplewall",
				Name:      "simplewall",
				Category:  "tweaks",
				Platforms: []string{"windows"},
				AutoApply: true,
				Install: map[string]InstallSpec{
					"windows": {Methods: []Method{{Type: "builtin", Action: "simplewall"}}},
				},
			},
		},
	}
	catalog.index()
	if _, err := buildPlan(catalog, Environment{OS: "windows"}, newProfile("generic"), &Logger{}); err != nil {
		t.Fatalf("buildPlan() error = %v", err)
	}
	profilePath := filepath.Join(tempDir, "Henry++", "simplewall", "profile.xml")
	if _, err := os.Stat(profilePath); err == nil {
		t.Fatalf("buildPlan created simplewall profile at %s", profilePath)
	} else if !isMissing(err) {
		t.Fatalf("stat simplewall profile: %v", err)
	}
}

func TestNormalizeGitCredentialHost(t *testing.T) {
	tests := map[string]string{
		"git.justw.tf":                         "git.justw.tf",
		"https://git.justw.tf/Light/setup-win": "git.justw.tf",
		"http://gitea.example.com:3000/org/x":  "gitea.example.com:3000",
		"gitea.example.com/org/repo":           "gitea.example.com",
	}
	for input, want := range tests {
		if got := normalizeGitCredentialHost(input); got != want {
			t.Fatalf("normalizeGitCredentialHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFinalCompletedStatusWarnsOnFailedNonFatalStep(t *testing.T) {
	report := SessionReport{
		StepResults: []StepResult{
			{ItemID: "initra-agent", Outcome: stepOutcomeFailed},
			{ItemID: "firefox", Outcome: stepOutcomeInstalled},
		},
	}
	if got := finalCompletedStatus(report); got != "completed_with_warnings" {
		t.Fatalf("unexpected final status: got %s", got)
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

func TestDecodeJSONBytesAllowsUTF8BOM(t *testing.T) {
	var payload struct {
		Version string `json:"version"`
	}
	if err := decodeJSONBytes([]byte("\ufeff{\"version\":\"ok\"}"), &payload); err != nil {
		t.Fatalf("decodeJSONBytes() error = %v", err)
	}
	if payload.Version != "ok" {
		t.Fatalf("unexpected version: %s", payload.Version)
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
