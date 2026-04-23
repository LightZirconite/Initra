package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	appName                   = "Initra"
	appSlug                   = "initra"
	catalogRelativePath       = "catalog/catalog.yaml"
	firefoxLayoutRelativePath = "assets/firefox/layout/ui-layout.json"
	defaultReleaseBaseURL     = "https://git.justw.tf/LightZirconite/setup-win/raw/branch/main"
	defaultLatestManifest     = defaultReleaseBaseURL + "/releases/latest.json"
	stateFileName             = "state.json"
	defaultProfileFileName    = "profile.json"
	restorePointDescription   = "Initra pre-change checkpoint"
)

type CLIOptions struct {
	Preset               string
	ProfilePath          string
	ExportProfilePath    string
	CaptureFirefoxLayout bool
	NonInteractive       bool
	Resume               bool
	DryRun               bool
	SelfUpdate           bool
	Diagnose             bool
	BaseURL              string
	StatePath            string
}

type Paths struct {
	BaseDir         string
	LogDir          string
	StatePath       string
	DefaultProfile  string
	CatalogPath     string
	ResumeAutostart string
}

type Catalog struct {
	Version    int                 `yaml:"version"`
	Categories []Category          `yaml:"categories"`
	Presets    map[string]Preset   `yaml:"presets"`
	Items      []Item              `yaml:"items"`
	itemIndex  map[string]int      `yaml:"-"`
	catIndex   map[string]Category `yaml:"-"`
}

type Category struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Platforms   []string `yaml:"platforms"`
}

type Preset struct {
	Description string            `yaml:"description"`
	Extends     string            `yaml:"extends"`
	Selected    []string          `yaml:"selected"`
	Values      map[string]string `yaml:"values"`
}

type Item struct {
	ID            string                 `yaml:"id"`
	Name          string                 `yaml:"name"`
	Category      string                 `yaml:"category"`
	Description   string                 `yaml:"description"`
	Platforms     []string               `yaml:"platforms"`
	AutoApply     bool                   `yaml:"auto_apply"`
	DependsOn     []string               `yaml:"depends_on"`
	RequiresAdmin bool                   `yaml:"requires_admin"`
	Notes         []string               `yaml:"notes"`
	Inputs        []InputSpec            `yaml:"inputs"`
	Detect        map[string]DetectSpec  `yaml:"detect"`
	Install       map[string]InstallSpec `yaml:"install"`
}

type InputSpec struct {
	ID          string   `yaml:"id"`
	Label       string   `yaml:"label"`
	Prompt      string   `yaml:"prompt"`
	Type        string   `yaml:"type"`
	Default     string   `yaml:"default"`
	Options     []string `yaml:"options"`
	Description string   `yaml:"description"`
}

type DetectSpec struct {
	Any []string `yaml:"any"`
}

type InstallSpec struct {
	Methods []Method `yaml:"methods"`
}

type Method struct {
	ID          string   `yaml:"id" json:"id"`
	Type        string   `yaml:"type" json:"type"`
	Package     string   `yaml:"package" json:"package,omitempty"`
	Packages    []string `yaml:"packages" json:"packages,omitempty"`
	Repo        []string `yaml:"repo" json:"repo,omitempty"`
	Commands    []string `yaml:"commands" json:"commands,omitempty"`
	URL         string   `yaml:"url" json:"url,omitempty"`
	FileName    string   `yaml:"filename" json:"filename,omitempty"`
	Arguments   []string `yaml:"arguments" json:"arguments,omitempty"`
	Requires    []string `yaml:"requires" json:"requires,omitempty"`
	Reboot      bool     `yaml:"reboot" json:"reboot,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Action      string   `yaml:"action" json:"action,omitempty"`
	Interaction string   `yaml:"interaction" json:"interaction,omitempty"`
}

type UserProfile struct {
	Version         int               `json:"version"`
	Preset          string            `json:"preset"`
	Selected        map[string]bool   `json:"selected"`
	SelectionSource map[string]string `json:"selection_source,omitempty"`
	Inputs          map[string]string `json:"inputs"`
}

type Environment struct {
	OS              string            `json:"os"`
	Arch            string            `json:"arch"`
	Hostname        string            `json:"hostname"`
	UserName        string            `json:"user_name"`
	HomeDir         string            `json:"home_dir"`
	TempDir         string            `json:"temp_dir"`
	DocumentsDir    string            `json:"documents_dir"`
	IsAdmin         bool              `json:"is_admin"`
	HasSudo         bool              `json:"has_sudo"`
	HasWinget       bool              `json:"has_winget"`
	WingetVersion   string            `json:"winget_version"`
	DistroID        string            `json:"distro_id"`
	DistroName      string            `json:"distro_name"`
	DistroLike      []string          `json:"distro_like"`
	DesktopSession  string            `json:"desktop_session"`
	SessionType     string            `json:"session_type"`
	PackageManagers []string          `json:"package_managers"`
	Capabilities    map[string]bool   `json:"capabilities"`
	Windows         WindowsInfo       `json:"windows"`
	Raw             map[string]string `json:"raw"`
}

type WindowsInfo struct {
	ProductName  string `json:"product_name"`
	DisplayVer   string `json:"display_version"`
	EditionID    string `json:"edition_id"`
	ReleaseID    string `json:"release_id"`
	CurrentBuild int    `json:"current_build"`
	IsLTSC       bool   `json:"is_ltsc"`
	IsIoT        bool   `json:"is_iot"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	GPUVendor    string `json:"gpu_vendor"`
	CPUVendor    string `json:"cpu_vendor"`
}

type Plan struct {
	Preset       string         `json:"preset"`
	Warnings     []string       `json:"warnings"`
	Steps        []ResolvedStep `json:"steps"`
	Profile      UserProfile    `json:"profile"`
	GeneratedAt  time.Time      `json:"generated_at"`
	NeedsRestore bool           `json:"needs_restore"`
}

type ResolvedStep struct {
	Item            Item              `json:"item"`
	Phase           string            `json:"phase"`
	Method          Method            `json:"method"`
	Inputs          map[string]string `json:"inputs"`
	SelectionState  string            `json:"selection_state,omitempty"`
	PlannedAction   string            `json:"planned_action,omitempty"`
	SkipReason      string            `json:"skip_reason,omitempty"`
	AlreadyPresent  bool              `json:"already_present"`
	RequiresReboot  bool              `json:"requires_reboot"`
	EstimatedAction string            `json:"estimated_action"`
}

type RunState struct {
	Version       int            `json:"version"`
	StartedAt     time.Time      `json:"started_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Plan          Plan           `json:"plan"`
	NextStep      int            `json:"next_step"`
	Completed     []string       `json:"completed"`
	Warnings      []string       `json:"warnings"`
	PendingReboot bool           `json:"pending_reboot"`
	BinaryPath    string         `json:"binary_path"`
	BaseURL       string         `json:"base_url"`
	Attempts      map[string]int `json:"attempts,omitempty"`
	ReportPath    string         `json:"report_path,omitempty"`
}

type Logger struct {
	file *os.File
	path string
}

type SessionReport struct {
	Version       int          `json:"version"`
	Status        string       `json:"status"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	Duration      string       `json:"duration"`
	LogPath       string       `json:"log_path,omitempty"`
	ReportPath    string       `json:"report_path,omitempty"`
	Profile       UserProfile  `json:"profile"`
	Plan          Plan         `json:"plan"`
	StepResults   []StepResult `json:"step_results"`
	Warnings      []string     `json:"warnings,omitempty"`
	Error         string       `json:"error,omitempty"`
	PendingReboot bool         `json:"pending_reboot,omitempty"`
}

type StepResult struct {
	ItemID         string    `json:"item_id"`
	ItemName       string    `json:"item_name"`
	Phase          string    `json:"phase"`
	SelectionState string    `json:"selection_state,omitempty"`
	PlannedAction  string    `json:"planned_action,omitempty"`
	Outcome        string    `json:"outcome"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	Error          string    `json:"error,omitempty"`
}

const (
	selectionAutoApply      = "auto apply"
	selectionPresetSelected = "preset selected"
	selectionManualYes      = "manual yes"
	selectionManualNo       = "manual no"

	stepActionInstall         = "install"
	stepActionUpgrade         = "upgrade"
	stepActionAlreadyUpToDate = "already up to date"
	stepActionAlreadyPresent  = "already present"
	stepActionSkip            = "skip"

	stepOutcomeInstalled       = "installed"
	stepOutcomeUpdated         = "updated"
	stepOutcomeAlreadyUpToDate = "already up to date"
	stepOutcomeSkipped         = "skipped"
	stepOutcomeFailed          = "failed"

	methodInteractionUnattended = "unattended"
	methodInteractionHelper     = "helper_window"
)

type manifestResponse struct {
	Version    string            `json:"version"`
	Published  string            `json:"published"`
	Notes      string            `json:"notes"`
	Artifacts  map[string]string `json:"artifacts"`
	Sha256     map[string]string `json:"sha256"`
	RequireMin string            `json:"requireMinVersion"`
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func defaultBaseURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return defaultReleaseBaseURL
	}
	return strings.TrimRight(value, "/")
}

func resolvePaths(opts CLIOptions) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}

	var baseDir string
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		baseDir = filepath.Join(localAppData, appName)
	} else {
		stateHome := os.Getenv("XDG_STATE_HOME")
		if stateHome == "" {
			stateHome = filepath.Join(home, ".local", "state")
		}
		baseDir = filepath.Join(stateHome, appSlug)
	}

	logDir := filepath.Join(baseDir, "logs")
	statePath := opts.StatePath
	if statePath == "" {
		statePath = filepath.Join(baseDir, stateFileName)
	}
	defaultProfile := filepath.Join(baseDir, defaultProfileFileName)

	execPath, _ := os.Executable()
	candidates := []string{
		filepath.Join(mustAbs("."), catalogRelativePath),
		filepath.Join(filepath.Dir(execPath), catalogRelativePath),
		filepath.Join(filepath.Dir(execPath), "..", catalogRelativePath),
		filepath.Join(filepath.Dir(execPath), "..", "..", catalogRelativePath),
	}

	var catalogPath string
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			catalogPath = candidate
			break
		}
	}
	if catalogPath == "" {
		catalogPath = filepath.Join(mustAbs("."), catalogRelativePath)
	}

	resumeAutostart := filepath.Join(home, ".config", "autostart", appSlug+"-resume.desktop")
	if runtime.GOOS == "windows" {
		resumeAutostart = ""
	}

	return Paths{
		BaseDir:         baseDir,
		LogDir:          logDir,
		StatePath:       statePath,
		DefaultProfile:  defaultProfile,
		CatalogPath:     catalogPath,
		ResumeAutostart: resumeAutostart,
	}, nil
}

func ensureAppDirs(paths Paths) error {
	for _, dir := range []string{paths.BaseDir, paths.LogDir, filepath.Dir(paths.StatePath)} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func newProfile(preset string) UserProfile {
	return UserProfile{
		Version:         1,
		Preset:          preset,
		Selected:        map[string]bool{},
		SelectionSource: map[string]string{},
		Inputs:          map[string]string{},
	}
}

func (p UserProfile) clone() UserProfile {
	dup := newProfile(p.Preset)
	dup.Version = p.Version
	for key, value := range p.Selected {
		dup.Selected[key] = value
	}
	for key, value := range p.SelectionSource {
		dup.SelectionSource[key] = value
	}
	for key, value := range p.Inputs {
		dup.Inputs[key] = value
	}
	return dup
}

func (c *Catalog) index() {
	c.itemIndex = map[string]int{}
	c.catIndex = map[string]Category{}
	for idx, item := range c.Items {
		c.itemIndex[item.ID] = idx
	}
	for _, cat := range c.Categories {
		c.catIndex[cat.ID] = cat
	}
}

func (c Catalog) itemByID(id string) (Item, bool) {
	if idx, ok := c.itemIndex[id]; ok {
		return c.Items[idx], true
	}
	return Item{}, false
}

func (c Catalog) categoryByID(id string) (Category, bool) {
	cat, ok := c.catIndex[id]
	return cat, ok
}

func (c Catalog) visibleItemsFor(env Environment) []Item {
	items := make([]Item, 0, len(c.Items))
	for _, item := range c.Items {
		if itemVisibleOn(item, env) {
			items = append(items, item)
		}
	}
	return items
}

func itemSupportedOn(item Item, env Environment) bool {
	if len(item.Platforms) == 0 {
		return true
	}
	for _, platform := range item.Platforms {
		if strings.EqualFold(platform, env.OS) {
			return true
		}
	}
	return false
}

func itemVisibleOn(item Item, env Environment) bool {
	if !itemSupportedOn(item, env) {
		return false
	}
	spec, ok := item.Install[env.OS]
	if !ok {
		return false
	}
	if len(spec.Methods) == 0 {
		return true
	}
	for _, method := range spec.Methods {
		if methodCompatible(method, env) {
			return true
		}
	}
	return false
}

func mergePreset(c Catalog, name string) (Preset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "generic"
	}
	preset, ok := c.Presets[name]
	if !ok {
		return Preset{}, fmt.Errorf("unknown preset %q", name)
	}

	if preset.Extends == "" {
		return Preset{
			Description: preset.Description,
			Extends:     preset.Extends,
			Selected:    append([]string(nil), preset.Selected...),
			Values:      cloneStringMap(preset.Values),
		}, nil
	}

	parent, err := mergePreset(c, preset.Extends)
	if err != nil {
		return Preset{}, err
	}
	selected := append([]string{}, parent.Selected...)
	selected = append(selected, preset.Selected...)
	values := cloneStringMap(parent.Values)
	for key, value := range preset.Values {
		values[key] = value
	}
	return Preset{
		Description: preset.Description,
		Extends:     preset.Extends,
		Selected:    uniqueStrings(selected),
		Values:      values,
	}, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return string(keys[i]) < string(keys[j]) })
	return keys
}

func saveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func isMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
