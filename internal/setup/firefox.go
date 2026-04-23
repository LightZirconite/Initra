package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	firefoxManagedBlockStart = "// Initra Firefox Layout Start"
	firefoxManagedBlockEnd   = "// Initra Firefox Layout End"
)

var firefoxUserPrefPattern = regexp.MustCompile(`^user_pref\("([^"]+)",\s*(.+)\);$`)

type firefoxLayoutBundle struct {
	Version       int               `json:"version"`
	CapturedAt    string            `json:"captured_at"`
	SourceProfile string            `json:"source_profile"`
	Notes         []string          `json:"notes,omitempty"`
	StringPrefs   map[string]string `json:"string_prefs,omitempty"`
	BoolPrefs     map[string]bool   `json:"bool_prefs,omitempty"`
}

func captureFirefoxLayoutToRepo(env Environment) error {
	profile, err := defaultFirefoxProfilePath(env)
	if err != nil {
		return err
	}
	bundle, err := extractFirefoxLayout(profile)
	if err != nil {
		return err
	}

	layoutPath := filepath.Join(mustAbs("."), firefoxLayoutRelativePath)
	if err := saveJSON(layoutPath, bundle); err != nil {
		return err
	}

	userJSPath := filepath.Join(filepath.Dir(layoutPath), "user.js")
	if err := os.MkdirAll(filepath.Dir(userJSPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(userJSPath, []byte(renderFirefoxLayoutUserJS(bundle)), 0o644); err != nil {
		return err
	}

	fmt.Printf("Captured non-sensitive Firefox UI layout from %s\n", profile)
	fmt.Printf("Saved layout asset to %s\n", layoutPath)
	fmt.Printf("Saved user.js template to %s\n", userJSPath)
	return nil
}

func applyBundledFirefoxLayout(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	bundle, cleanup, err := loadBundledFirefoxLayout(ctx, env, baseURL)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	profiles, err := firefoxProfilePaths(env)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles, err = ensureFirefoxProfileExists(ctx, env, logger)
		if err != nil {
			return err
		}
	}
	if len(profiles) == 0 {
		fmt.Println("Firefox could not create a profile automatically. Launch Firefox once, then try the layout action again.")
		return nil
	}

	block := renderFirefoxLayoutUserJS(bundle)
	for _, profile := range profiles {
		userJSPath := filepath.Join(profile, "user.js")
		existing, err := os.ReadFile(userJSPath)
		if err != nil && !isMissing(err) {
			return err
		}
		updated := mergeManagedFirefoxBlock(string(existing), block)
		if err := os.WriteFile(userJSPath, []byte(updated), 0o644); err != nil {
			return err
		}
		logger.Println("applied Firefox layout to", userJSPath)
		fmt.Printf("Applied Initra Firefox layout to %s\n", profile)
	}

	fmt.Println("Firefox should be restarted before the new layout is visible.")
	return nil
}

func loadBundledFirefoxLayout(ctx context.Context, env Environment, baseURL string) (firefoxLayoutBundle, func(), error) {
	path, cleanup, err := resolveAssetPath(ctx, env, baseURL, firefoxLayoutRelativePath)
	if err != nil {
		return firefoxLayoutBundle{}, nil, err
	}
	var bundle firefoxLayoutBundle
	if err := loadJSON(path, &bundle); err != nil {
		if cleanup != nil {
			cleanup()
		}
		return firefoxLayoutBundle{}, nil, err
	}
	return bundle, cleanup, nil
}

func extractFirefoxLayout(profile string) (firefoxLayoutBundle, error) {
	prefsPath := filepath.Join(profile, "prefs.js")
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		return firefoxLayoutBundle{}, err
	}

	stringPrefs := map[string]string{}
	boolPrefs := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		matches := firefoxUserPrefPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(matches) != 3 {
			continue
		}
		name := matches[1]
		rawValue := strings.TrimSpace(matches[2])
		switch name {
		case "browser.uiCustomization.state", "browser.toolbars.bookmarks.visibility":
			value, err := strconv.Unquote(rawValue)
			if err != nil {
				return firefoxLayoutBundle{}, fmt.Errorf("parse Firefox pref %s: %w", name, err)
			}
			stringPrefs[name] = value
		case "sidebar.revamp":
			boolValue, err := strconv.ParseBool(rawValue)
			if err != nil {
				return firefoxLayoutBundle{}, fmt.Errorf("parse Firefox pref %s: %w", name, err)
			}
			boolPrefs[name] = boolValue
		}
	}

	if stringPrefs["browser.uiCustomization.state"] == "" {
		return firefoxLayoutBundle{}, errors.New("browser.uiCustomization.state was not found in the Firefox profile")
	}

	bundle := firefoxLayoutBundle{
		Version:       1,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProfile: filepath.Base(profile),
		Notes: []string{
			"Only non-sensitive Firefox UI layout preferences are stored here.",
			"Bookmarks, browsing history, cookies, saved logins, and key databases are intentionally excluded.",
			"xulstore.json is intentionally not redistributed because window sizes and screen positions are machine-specific.",
		},
		StringPrefs: stringPrefs,
		BoolPrefs:   boolPrefs,
	}
	return bundle, nil
}

func renderFirefoxLayoutUserJS(bundle firefoxLayoutBundle) string {
	lines := []string{
		firefoxManagedBlockStart,
		"// Generated by Initra. Safe UI layout only.",
	}
	for _, key := range sortedKeys(bundle.StringPrefs) {
		lines = append(lines, fmt.Sprintf("user_pref(%q, %s);", key, strconv.Quote(bundle.StringPrefs[key])))
	}
	for _, key := range sortedKeys(bundle.BoolPrefs) {
		lines = append(lines, fmt.Sprintf("user_pref(%q, %t);", key, bundle.BoolPrefs[key]))
	}
	lines = append(lines, firefoxManagedBlockEnd)
	return strings.Join(lines, "\n") + "\n"
}

func mergeManagedFirefoxBlock(existing, block string) string {
	existing = strings.ReplaceAll(existing, "\r\n", "\n")
	start := strings.Index(existing, firefoxManagedBlockStart)
	end := strings.Index(existing, firefoxManagedBlockEnd)
	switch {
	case start >= 0 && end >= start:
		end += len(firefoxManagedBlockEnd)
		updated := strings.TrimRight(existing[:start], "\n")
		if updated != "" {
			updated += "\n\n"
		}
		updated += block
		tail := strings.TrimLeft(existing[end:], "\n")
		if tail != "" {
			updated += "\n" + tail
			if !strings.HasSuffix(updated, "\n") {
				updated += "\n"
			}
		}
		return updated
	default:
		existing = strings.TrimRight(existing, "\n")
		if existing == "" {
			return block
		}
		return existing + "\n\n" + block
	}
}

func firefoxProfilePaths(env Environment) ([]string, error) {
	root, err := firefoxRootDir(env)
	if err != nil {
		return nil, err
	}
	profilesIni := filepath.Join(root, "profiles.ini")
	data, err := os.ReadFile(profilesIni)
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, err
	}

	type profileSection struct {
		name string
		keys map[string]string
	}
	var sections []profileSection
	current := profileSection{keys: map[string]string{}}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current.name != "" {
				sections = append(sections, current)
			}
			current = profileSection{
				name: strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"),
				keys: map[string]string{},
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		current.keys[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if current.name != "" {
		sections = append(sections, current)
	}

	var profiles []string
	for _, section := range sections {
		if !strings.HasPrefix(section.name, "Profile") {
			continue
		}
		path := strings.TrimSpace(section.keys["Path"])
		if path == "" {
			continue
		}
		isRelative := strings.TrimSpace(section.keys["IsRelative"]) != "0"
		if isRelative {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		if _, err := os.Stat(path); err == nil {
			profiles = append(profiles, path)
		}
	}
	return uniqueStrings(profiles), nil
}

func defaultFirefoxProfilePath(env Environment) (string, error) {
	root, err := firefoxRootDir(env)
	if err != nil {
		return "", err
	}
	profilesIni := filepath.Join(root, "profiles.ini")
	data, err := os.ReadFile(profilesIni)
	if err != nil {
		return "", err
	}

	var currentSection string
	currentKeys := map[string]string{}
	resolveCurrent := func() (string, bool) {
		if !strings.HasPrefix(currentSection, "Profile") {
			return "", false
		}
		if strings.TrimSpace(currentKeys["Default"]) != "1" {
			return "", false
		}
		path := strings.TrimSpace(currentKeys["Path"])
		if path == "" {
			return "", false
		}
		if strings.TrimSpace(currentKeys["IsRelative"]) != "0" {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		return path, true
	}

	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if path, ok := resolveCurrent(); ok {
				return path, nil
			}
			currentSection = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			currentKeys = map[string]string{}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			currentKeys[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if path, ok := resolveCurrent(); ok {
		return path, nil
	}

	profiles, err := firefoxProfilePaths(env)
	if err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", errors.New("no Firefox profile was found")
	}
	return profiles[0], nil
}

func firefoxRootDir(env Environment) (string, error) {
	switch env.OS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appData, "Mozilla", "Firefox"), nil
	case "linux":
		if env.HomeDir == "" {
			return "", errors.New("home directory is not available")
		}
		return filepath.Join(env.HomeDir, ".mozilla", "firefox"), nil
	default:
		return "", fmt.Errorf("Firefox profile discovery is not implemented on %s", env.OS)
	}
}

func ensureFirefoxProfileExists(ctx context.Context, env Environment, logger *Logger) ([]string, error) {
	fmt.Println("No Firefox profile was found yet. Initra will launch Firefox once to create it.")
	switch env.OS {
	case "windows":
		firefoxPath := findFirefoxBinaryWindows()
		if firefoxPath == "" {
			return nil, nil
		}
		cmd := exec.CommandContext(ctx, firefoxPath, "about:blank")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		logger.Println("started Firefox to create a profile")
	case "linux":
		if !commandExists("firefox") {
			return nil, nil
		}
		cmd := exec.CommandContext(ctx, "firefox", "about:blank")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		logger.Println("started Firefox to create a profile")
	default:
		return nil, nil
	}

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		profiles, err := firefoxProfilePaths(env)
		if err == nil && len(profiles) > 0 {
			fmt.Println("Firefox profile detected. Continuing with the bundled layout.")
			return profiles, nil
		}
		time.Sleep(2 * time.Second)
	}
	return firefoxProfilePaths(env)
}
