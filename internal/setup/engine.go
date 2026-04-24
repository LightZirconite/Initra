package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"
)

func Run(args []string, version string) error {
	opts, err := parseCLI(args)
	if err != nil {
		return err
	}
	opts.BaseURL = defaultBaseURL(opts.BaseURL)

	paths, err := resolvePaths(opts)
	if err != nil {
		return err
	}
	if err := ensureAppDirs(paths); err != nil {
		return err
	}
	logger, err := openLogger(paths.LogDir)
	if err != nil {
		return err
	}
	defer logger.Close()

	env, err := detectEnvironment()
	if err != nil {
		return err
	}
	logger.Println("detected environment", env.OS, env.Arch)
	printAppBanner(env, version)

	if opts.SelfUpdate {
		return runSelfUpdate(context.Background(), env, logger, opts.BaseURL)
	}

	if opts.CaptureFirefoxLayout {
		return captureFirefoxLayoutToRepo(env)
	}

	catalog, err := loadCatalog(paths.CatalogPath)
	if err != nil {
		return err
	}

	if opts.Diagnose {
		return printDiagnosis(catalog, env)
	}

	if opts.Resume {
		return resumeExecution(context.Background(), paths, env, logger, opts.BaseURL, !opts.NonInteractive)
	}

	profile, err := loadWorkingProfile(catalog, env, paths, opts)
	if err != nil {
		return err
	}
	if opts.ExportProfilePath != "" {
		if err := saveJSON(opts.ExportProfilePath, profile); err != nil {
			return fmt.Errorf("export profile: %w", err)
		}
		fmt.Printf("%s %s\n", termUI.green("Profile exported to"), opts.ExportProfilePath)
	}

	plan, err := buildPlan(catalog, env, profile, logger)
	if err != nil {
		return err
	}
	printPreflightChecks(runPreflightChecks(context.Background(), env, plan, logger, opts.BaseURL))
	printSelectionSummary(catalog, env, profile)
	printPlan(plan)
	if opts.DryRun {
		return nil
	}
	if !opts.NonInteractive {
		ok, err := confirmExecution()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return executePlan(context.Background(), plan, paths, env, logger, opts.BaseURL, !opts.NonInteractive)
}

func parseCLI(args []string) (CLIOptions, error) {
	var opts CLIOptions
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.StringVar(&opts.Preset, "preset", "generic", "preset to start from (generic|personal)")
	fs.StringVar(&opts.ProfilePath, "profile", "", "load a profile JSON file")
	fs.StringVar(&opts.ExportProfilePath, "export-profile", "", "export the resulting profile to JSON")
	fs.BoolVar(&opts.CaptureFirefoxLayout, "capture-firefox-layout", false, "capture the current machine's non-sensitive Firefox UI layout into the repository assets")
	fs.BoolVar(&opts.NonInteractive, "non-interactive", false, "run without prompts")
	fs.BoolVar(&opts.Resume, "resume", false, "resume from the last saved execution state")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print the plan without executing it")
	fs.BoolVar(&opts.SelfUpdate, "self-update", false, "update the current Initra binary")
	fs.BoolVar(&opts.Diagnose, "diagnose", false, "print machine diagnostics and compatibility info")
	fs.StringVar(&opts.BaseURL, "base-url", "", "release base URL")
	fs.StringVar(&opts.StatePath, "state-path", "", "override the execution state file path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func loadWorkingProfile(catalog Catalog, env Environment, paths Paths, opts CLIOptions) (UserProfile, error) {
	base := newProfile(opts.Preset)
	preset, err := mergePreset(catalog, opts.Preset)
	if err != nil {
		return base, err
	}
	for _, itemID := range preset.Selected {
		base.Selected[itemID] = true
		base.SelectionSource[itemID] = selectionPresetSelected
	}
	for key, value := range preset.Values {
		base.Inputs[key] = value
	}

	if opts.ProfilePath != "" {
		if err := loadJSON(opts.ProfilePath, &base); err != nil {
			return base, fmt.Errorf("load profile: %w", err)
		}
		if base.Selected == nil {
			base.Selected = map[string]bool{}
		}
		if base.SelectionSource == nil {
			base.SelectionSource = map[string]string{}
		}
		if base.Inputs == nil {
			base.Inputs = map[string]string{}
		}
		if base.Preset == "" {
			base.Preset = opts.Preset
		}
		for itemID, selected := range base.Selected {
			if !selected {
				if base.SelectionSource[itemID] == "" {
					base.SelectionSource[itemID] = selectionManualNo
				}
				continue
			}
			if base.SelectionSource[itemID] == "" {
				base.SelectionSource[itemID] = selectionManualYes
			}
		}
	}

	if opts.NonInteractive {
		return base, nil
	}
	return buildProfileInteractively(catalog, env, base)
}

func buildPlan(catalog Catalog, env Environment, profile UserProfile, logger *Logger) (Plan, error) {
	plan := Plan{
		Preset:      profile.Preset,
		Profile:     profile.clone(),
		GeneratedAt: time.Now(),
	}

	for _, item := range catalog.Items {
		if len(item.DependsOn) > 0 && !profileDependencySatisfied(item, profile) {
			continue
		}
		if !item.AutoApply && !profile.Selected[item.ID] {
			continue
		}
		step, warnings, err := resolveStep(item, env, profile, logger)
		plan.Warnings = append(plan.Warnings, warnings...)
		if err != nil {
			return plan, err
		}
		step.Phase = phaseForItem(item)
		if phaseNeedsRestore(step) {
			plan.NeedsRestore = runtime.GOOS == "windows"
		}
		plan.Steps = append(plan.Steps, step)
	}
	sortPlanByPhase(&plan)

	return plan, nil
}

func resolveStep(item Item, env Environment, profile UserProfile, logger *Logger) (ResolvedStep, []string, error) {
	step := ResolvedStep{Item: item, Inputs: map[string]string{}}
	warnings := append([]string{}, item.Notes...)
	step.SelectionState = selectionStateForItem(item, profile)
	for _, input := range item.Inputs {
		step.Inputs[input.ID] = resolveDefaultInput(input, profile, env)
		if profile.Inputs[input.ID] != "" {
			step.Inputs[input.ID] = profile.Inputs[input.ID]
		}
	}

	if !itemSupportedOn(item, env) {
		step.SkipReason = "unsupported on current platform"
		step.PlannedAction = stepActionSkip
		return step, append(warnings, fmt.Sprintf("%s is not supported on %s and will be skipped.", item.Name, env.OS)), nil
	}

	platformSpec, ok := item.Install[env.OS]
	if !ok {
		step.SkipReason = "no install method for current platform"
		step.PlannedAction = stepActionSkip
		return step, append(warnings, fmt.Sprintf("%s has no install method on %s and will be skipped.", item.Name, env.OS)), nil
	}

	method, methodWarnings := selectMethod(platformSpec.Methods, env)
	warnings = append(warnings, methodWarnings...)
	if method == nil {
		step.SkipReason = "no compatible install method"
		step.PlannedAction = stepActionSkip
		return step, append(warnings, fmt.Sprintf("%s has no compatible install method on this machine and will be skipped.", item.Name)), nil
	}

	step.Method = *method
	action, err := resolvePlannedAction(item, *method, env, logger)
	if err != nil {
		logger.Println("step action detection failed", item.ID, err)
	}
	step.PlannedAction = action
	switch action {
	case stepActionAlreadyPresent, stepActionAlreadyUpToDate:
		step.AlreadyPresent = true
	}
	warnings = append(warnings, extraItemWarnings(item, env, step.Inputs)...)
	step.RequiresReboot = method.Reboot
	step.EstimatedAction = describeResolvedAction(step)
	return step, warnings, nil
}

func profileDependencySatisfied(item Item, profile UserProfile) bool {
	if len(item.DependsOn) == 0 {
		return true
	}
	for _, dep := range item.DependsOn {
		if profile.Selected[dep] {
			return true
		}
	}
	return false
}

func extraItemWarnings(item Item, env Environment, inputs map[string]string) []string {
	warnings := []string{}
	switch item.ID {
	case "office":
		if strings.Contains(strings.ToLower(env.Windows.ProductName), "windows 10") {
			warnings = append(warnings, "Microsoft notes that Windows 10 reached end of support on October 14, 2025; Microsoft 365 Apps support can be limited on that OS.")
		}
		if env.Windows.IsLTSC || env.Windows.IsIoT {
			warnings = append(warnings, "Windows LTSC/IoT combinations can be technically installable for Office but may not be officially supported in every scenario.")
		}
		if lang := strings.TrimSpace(inputs["office_language"]); lang != "" {
			warnings = append(warnings, fmt.Sprintf("Office will be requested with language %s.", lang))
		}
	case "mesh-agent":
		if url := strings.TrimSpace(inputs["mesh_url"]); url != "" {
			warnings = append(warnings, "Remote Support Agent uses the configured Mesh URL: "+url)
		}
	case "onedrive":
		if env.OS == "linux" {
			warnings = append(warnings, "Linux OneDrive uses the community abraunegg/onedrive path instead of an official Microsoft desktop client.")
		}
	case "superwhisper":
		if env.OS == "linux" {
			warnings = append(warnings, "Linux dictation uses OpenWhispr as the allowlisted alternative because SuperWhisper has no official Linux desktop app.")
		}
	case "theme-dark":
		if env.OS == "linux" {
			warnings = append(warnings, "Linux dark theme is automated for GNOME and KDE Plasma sessions when their native tooling is available.")
		}
	case "auto-refresh-rate":
		if env.OS == "linux" {
			warnings = append(warnings, "Linux refresh-rate optimization currently targets X11 sessions with xrandr.")
		}
	case "consumer-cleanup":
		warnings = append(warnings, "Consumer cleanup removes a conservative allowlist of bundled Microsoft apps, but some image-protected packages can remain.")
	case "feature-rdp":
		warnings = append(warnings, "Remote Desktop is enabled without changing NLA. Initra does not weaken that protection automatically.")
	case "emoji-font-pack":
		warnings = append(warnings, "The Windows 10 emoji font pack requires a reboot before it is fully applied.")
	case "vencord":
		warnings = append(warnings, "Vencord modifies Discord in a way that can violate Discord's terms of service.")
	case "fastfetch":
		warnings = append(warnings, "Fastfetch is a terminal info command. It is installed for convenience but does not replace the default terminal app.")
	case "localsend":
		warnings = append(warnings, "LocalSend can need local firewall or private-network permission changes before nearby devices become visible.")
	case "everything-toolbar":
		warnings = append(warnings, "EverythingToolbar installs Everything automatically because the toolbar depends on the Everything search indexer.")
	case "simplewall":
		warnings = append(warnings, "simplewall can interrupt app connectivity or remote support if rules are tightened aggressively.")
	case "winutil-shortcut":
		warnings = append(warnings, "WinUtil is not bundled. The desktop shortcut downloads the official script only when the user launches it.")
	}
	return warnings
}

func selectMethod(methods []Method, env Environment) (*Method, []string) {
	warnings := []string{}
	for _, method := range methods {
		if methodCompatible(method, env) {
			selected := method
			return &selected, warnings
		}
	}
	if env.OS == "linux" && len(methods) > 0 {
		warnings = append(warnings, "No Linux method matched the detected distro/package managers.")
	}
	return nil, warnings
}

func methodCompatible(method Method, env Environment) bool {
	for _, requirement := range method.Requires {
		switch requirement {
		case "winget":
			if !env.HasWinget {
				return false
			}
		case "no-winget":
			if env.HasWinget {
				return false
			}
		case "apt":
			if !contains(env.PackageManagers, "apt") {
				return false
			}
		case "dnf":
			if !contains(env.PackageManagers, "dnf") {
				return false
			}
		case "pacman":
			if !contains(env.PackageManagers, "pacman") {
				return false
			}
		case "windows":
			if env.OS != "windows" {
				return false
			}
		case "linux":
			if env.OS != "linux" {
				return false
			}
		case "iot":
			if !env.Windows.IsIoT {
				return false
			}
		case "not-iot":
			if env.Windows.IsIoT {
				return false
			}
		case "flatpak":
			if !env.Capabilities["flatpak"] {
				return false
			}
		case "windows10":
			if env.OS != "windows" || !isWindows10(env) {
				return false
			}
		case "windows11":
			if env.OS != "windows" || !isWindows11(env) {
				return false
			}
		case "not-ltsc":
			if env.Windows.IsLTSC {
				return false
			}
		}
	}
	return true
}

func selectionStateForItem(item Item, profile UserProfile) string {
	if item.AutoApply {
		return selectionAutoApply
	}
	if state := strings.TrimSpace(profile.SelectionSource[item.ID]); state != "" {
		return state
	}
	if profile.Selected[item.ID] {
		return selectionManualYes
	}
	return selectionManualNo
}

func resolvePlannedAction(item Item, method Method, env Environment, logger *Logger) (string, error) {
	switch {
	case method.Type == "winget" && env.OS == "windows":
		state, err := detectWingetPackageState(item, method.Package, env, logger)
		if err != nil {
			return stepActionInstall, err
		}
		switch state {
		case stepActionUpgrade:
			return stepActionUpgrade, nil
		case stepActionAlreadyUpToDate:
			return stepActionAlreadyUpToDate, nil
		case stepActionAlreadyPresent:
			return stepActionAlreadyPresent, nil
		default:
			return stepActionInstall, nil
		}
	default:
		installed, err := detectItemInstalled(item, env)
		if err != nil {
			return stepActionInstall, err
		}
		if installed {
			if env.OS == "linux" {
				return stepActionAlreadyPresent, nil
			}
			return stepActionAlreadyPresent, nil
		}
	}
	return stepActionInstall, nil
}

func detectItemInstalled(item Item, env Environment) (bool, error) {
	spec, ok := item.Detect[env.OS]
	if !ok || len(spec.Any) == 0 {
		return false, nil
	}
	for _, command := range spec.Any {
		ok, err := runDetectionCommand(command, env)
		if err == nil && ok {
			return true, nil
		}
	}
	return false, nil
}

func runDetectionCommand(command string, env Environment) (bool, error) {
	var cmd *exec.Cmd
	if env.OS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("/bin/sh", "-lc", command)
	}
	if err := cmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func detectWingetPackageState(item Item, packageID string, env Environment, logger *Logger) (string, error) {
	if strings.TrimSpace(packageID) == "" {
		return stepActionInstall, nil
	}
	listDetected, listOutput, listErr := runWingetQuery("list", packageID)
	if listErr == nil && listDetected {
		upgradeAvailable, upgradeErr := wingetUpgradeAvailable(packageID)
		if upgradeErr != nil {
			logger.Println("winget upgrade detection failed", packageID, upgradeErr)
			return stepActionAlreadyPresent, nil
		}
		if upgradeAvailable {
			return stepActionUpgrade, nil
		}
		return stepActionAlreadyUpToDate, nil
	}
	if listErr != nil {
		logger.Println("winget list detection failed", packageID, listErr)
	}
	if registryDetected, regErr := detectWindowsInstalledViaRegistry(item, packageID); regErr == nil && registryDetected {
		return stepActionAlreadyPresent, nil
	} else if regErr != nil {
		logger.Println("registry install detection failed", packageID, regErr)
	}
	if parseWingetQueryDetected(packageID, listOutput) {
		return stepActionAlreadyPresent, nil
	}
	return stepActionInstall, nil
}

func runWingetQuery(command, packageID string) (bool, string, error) {
	output, err := runOutput("winget", command, "--id", packageID, "-e", "--disable-interactivity")
	if err != nil {
		return false, output, err
	}
	return parseWingetQueryDetected(packageID, output), output, nil
}

func wingetUpgradeAvailable(packageID string) (bool, error) {
	output, err := runOutput("winget", "upgrade", "--id", packageID, "-e", "--disable-interactivity")
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "no installed package found") {
			return false, nil
		}
		return false, err
	}
	return parseWingetUpgradeAvailable(packageID, output), nil
}

func parseWingetQueryDetected(packageID, output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "no installed package found matching input criteria") {
		return false
	}
	if strings.Contains(lower, strings.ToLower(packageID)) {
		return true
	}
	return false
}

func parseWingetUpgradeAvailable(packageID, output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "no available upgrade found") {
		return false
	}
	if strings.Contains(lower, "no installed package found matching input criteria") {
		return false
	}
	return strings.Contains(lower, strings.ToLower(packageID))
}

func detectWindowsInstalledViaRegistry(item Item, packageID string) (bool, error) {
	tokens := wingetRegistrySearchTokens(item, packageID)
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$tokens = @(%s)
$paths = @(
  'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*'
)
$found = $false
foreach ($path in $paths) {
  Get-ItemProperty -Path $path -ErrorAction SilentlyContinue | ForEach-Object {
    $blob = @($_.DisplayName, $_.Publisher, $_.UninstallString, $_.DisplayIcon) -join ' '
    $blob = $blob.ToLowerInvariant()
    foreach ($token in $tokens) {
      if ($token -and $blob.Contains($token)) {
        $found = $true
        break
      }
    }
    if ($found) { break }
  }
  if ($found) { break }
}
if ($found) { 'true' } else { 'false' }
`, quotedPowerShellArray(tokens))
	output, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(output), "true"), nil
}

func wingetRegistrySearchTokens(item Item, packageID string) []string {
	normalized := strings.ToLower(strings.TrimSpace(packageID))
	lastSegment := normalized
	if idx := strings.LastIndex(normalized, "."); idx >= 0 && idx+1 < len(normalized) {
		lastSegment = normalized[idx+1:]
	}
	nameToken := strings.ToLower(strings.TrimSpace(item.Name))
	tokens := uniqueStrings([]string{
		strings.ReplaceAll(normalized, ".", " "),
		lastSegment,
		strings.ReplaceAll(lastSegment, "-", " "),
		nameToken,
	})
	return tokens
}

func quotedPowerShellArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, "'"+strings.ReplaceAll(value, "'", "''")+"'")
	}
	if len(quoted) == 0 {
		return "''"
	}
	return strings.Join(quoted, ", ")
}

func describeMethod(method Method) string {
	switch method.Type {
	case "winget":
		return "winget install " + method.Package
	case "apt", "dnf", "pacman", "flatpak":
		return method.Type + " install " + strings.Join(method.Packages, " ")
	case "direct":
		return "download and execute"
	case "builtin":
		return "builtin action: " + method.Action
	case "shell":
		return "shell commands"
	default:
		return method.Type
	}
}

func describeResolvedAction(step ResolvedStep) string {
	switch step.PlannedAction {
	case stepActionInstall:
		return describeMethod(step.Method)
	case stepActionUpgrade:
		if step.Method.Type == "winget" {
			return "winget upgrade " + step.Method.Package
		}
		return "upgrade"
	case stepActionAlreadyUpToDate:
		return stepActionAlreadyUpToDate
	case stepActionAlreadyPresent:
		return stepActionAlreadyPresent
	case stepActionSkip:
		return stepActionSkip
	default:
		return describeMethod(step.Method)
	}
}

func printSelectionSummary(catalog Catalog, env Environment, profile UserProfile) {
	fmt.Println()
	printSection("Selection Summary")
	for _, category := range catalog.Categories {
		hasItems := false
		for _, item := range catalog.Items {
			if item.Category != category.ID || !itemVisibleOn(item, env) || (len(item.DependsOn) > 0 && !profileDependencySatisfied(item, profile)) {
				continue
			}
			if !hasItems {
				fmt.Printf("%s\n", formatCategoryTitle(category.Name))
				hasItems = true
			}
			fmt.Printf("  %s %s: %s\n", colorizeBullet("-"), item.Name, formatStatusLabel(selectionStateForItem(item, profile)))
		}
	}
	fmt.Println()
}

func printPlan(plan Plan) {
	fmt.Println()
	printSection("Execution Plan")
	fmt.Printf("%s %s\n", termUI.dim("Preset:"), termUI.bold(plan.Preset))
	alreadyPresent := 0
	alreadyUpToDate := 0
	skipped := 0
	runnable := 0
	for _, step := range plan.Steps {
		switch {
		case step.PlannedAction == stepActionAlreadyUpToDate:
			alreadyUpToDate++
		case step.AlreadyPresent:
			alreadyPresent++
		case step.SkipReason != "":
			skipped++
		default:
			runnable++
		}
	}
	fmt.Printf("%s %d to run, %d already present, %d already up to date, %d skipped\n", termUI.bold("Summary:"), runnable, alreadyPresent, alreadyUpToDate, skipped)
	if len(plan.Warnings) > 0 {
		fmt.Println(termUI.yellow(termUI.bold("Warnings:")))
		for _, warning := range uniqueStrings(plan.Warnings) {
			fmt.Printf("  %s %s\n", colorizeBullet("-"), warning)
		}
	}
	fmt.Println(termUI.bold("Steps:"))
	currentPhase := ""
	for _, step := range plan.Steps {
		if step.Phase != currentPhase {
			currentPhase = step.Phase
			fmt.Printf("  %s\n", formatCategoryTitle(phaseDisplayName(step.Phase)))
		}
		status := step.EstimatedAction
		if step.SkipReason != "" {
			status = "skip: " + step.SkipReason
		}
		fmt.Printf("  %s %s: %s %s\n", colorizeBullet("-"), step.Item.Name, formatPlanStatus(step, status), termUI.dim("("+step.SelectionState+")"))
	}
	fmt.Println()
}

func stepShouldRun(step ResolvedStep) bool {
	if step.SkipReason != "" {
		return false
	}
	switch step.PlannedAction {
	case stepActionInstall, stepActionUpgrade:
		return true
	default:
		return false
	}
}

func executePlan(ctx context.Context, plan Plan, paths Paths, env Environment, logger *Logger, baseURL string, interactive bool) error {
	startedAt := time.Now()
	report, reportPath := newSessionReport(plan, paths, logger, startedAt)
	state := RunState{
		Version:    2,
		StartedAt:  startedAt,
		UpdatedAt:  startedAt,
		Plan:       plan,
		NextStep:   0,
		BinaryPath: currentBinaryPath(),
		BaseURL:    baseURL,
		Attempts:   map[string]int{},
		ReportPath: reportPath,
	}
	if err := saveJSON(paths.StatePath, state); err != nil {
		return err
	}
	if err := setupResumeHook(paths, logger); err != nil {
		return err
	}
	if err := saveSessionReport(reportPath, &report); err != nil {
		return err
	}
	if err := waitForNetwork(ctx, logger, baseURL); err != nil {
		report.Status = "error"
		report.Error = err.Error()
		report.FinishedAt = time.Now()
		_ = saveSessionReport(reportPath, &report)
		return err
	}
	stopHostedSession := func() {}
	if interactive {
		_ = prepareHostedWindowsSession(ctx, logger)
		stopHostedSession = startHostedSessionController(logger)
		printKioskInstallScreen(env, false)
	}
	defer stopHostedSession()

	if env.OS == "windows" && selectedNeedsWinget(plan) {
		if err := ensureWinget(ctx, env, logger); err != nil {
			report.Status = "error"
			report.Error = err.Error()
			report.FinishedAt = time.Now()
			_ = saveSessionReport(reportPath, &report)
			printFinalSessionScreen(report, interactive)
			return err
		}
	}

	restoreCreated := false
	totalRunnable := 0
	for _, step := range plan.Steps {
		if stepShouldRun(step) {
			totalRunnable++
		}
	}
	currentRunnable := 0
	currentPhase := ""
	for idx := range plan.Steps {
		step := plan.Steps[idx]
		if !stepShouldRun(step) {
			recordStaticStepResult(&report, step)
			state.NextStep = idx + 1
			state.Completed = append(state.Completed, step.Item.ID)
			state.UpdatedAt = time.Now()
			if err := saveJSON(paths.StatePath, state); err != nil {
				return err
			}
			if err := saveSessionReport(reportPath, &report); err != nil {
				return err
			}
			continue
		}
		if plan.NeedsRestore && !restoreCreated && env.OS == "windows" && step.Item.RequiresAdmin {
			_ = createRestorePoint(logger)
			restoreCreated = true
		}

		if step.Phase != currentPhase {
			currentPhase = step.Phase
			fmt.Printf("\n%s\n", formatCategoryTitle(phaseDisplayName(step.Phase)))
		}
		currentRunnable++
		fmt.Printf("%s [%d/%d] %s\n", termUI.cyan("==>"), currentRunnable, totalRunnable, termUI.bold(step.Item.Name))
		stepKey := stepStateKey(step)
		state.Attempts[stepKey]++
		startedStep := time.Now()
		if err := executeStep(ctx, step, env, logger, baseURL); err != nil {
			recordExecutedStepResult(&report, step, startedStep, err)
			report.Status = "error"
			report.Error = fmt.Sprintf("%s: %v", step.Item.Name, err)
			report.FinishedAt = time.Now()
			_ = saveSessionReport(reportPath, &report)
			printFinalSessionScreen(report, interactive)
			return fmt.Errorf("%s: %w", step.Item.Name, err)
		}
		recordExecutedStepResult(&report, step, startedStep, nil)

		if env.OS == "windows" && isMaintenanceLoopStep(step) {
			rebootPending, err := windowsRebootPending(ctx, logger)
			if err == nil && rebootPending && state.Attempts[stepKey] < 4 {
				state.PendingReboot = true
				state.NextStep = idx
				state.UpdatedAt = time.Now()
				report.Status = "partial"
				report.PendingReboot = true
				report.FinishedAt = time.Now()
				if err := saveSessionReport(reportPath, &report); err != nil {
					return err
				}
				if err := persistRebootState(paths, logger, &state, "Windows requested another update reboot cycle. Initra will resume automatically."); err != nil {
					return err
				}
				return nil
			}
		}

		if step.RequiresReboot {
			state.PendingReboot = true
			state.NextStep = idx + 1
			state.UpdatedAt = time.Now()
			report.Status = "partial"
			report.PendingReboot = true
			report.FinishedAt = time.Now()
			if err := saveSessionReport(reportPath, &report); err != nil {
				return err
			}
			if err := persistRebootState(paths, logger, &state, fmt.Sprintf("%s requires a reboot. Initra will resume automatically.", step.Item.Name)); err != nil {
				return err
			}
			return nil
		}
		state.NextStep = idx + 1
		state.Completed = append(state.Completed, step.Item.ID)
		state.PendingReboot = false
		state.UpdatedAt = time.Now()
		if err := saveJSON(paths.StatePath, state); err != nil {
			return err
		}
		if err := saveSessionReport(reportPath, &report); err != nil {
			return err
		}
	}

	_ = os.Remove(paths.StatePath)
	_ = removeResumeHook(paths)
	report.Status = "success"
	report.FinishedAt = time.Now()
	if err := saveSessionReport(reportPath, &report); err != nil {
		return err
	}
	printFinalSessionScreen(report, interactive)
	return nil
}

func resumeExecution(ctx context.Context, paths Paths, env Environment, logger *Logger, baseURL string, interactive bool) error {
	var state RunState
	if err := loadJSON(paths.StatePath, &state); err != nil {
		if isMissing(err) {
			return errors.New("no saved execution state found")
		}
		return err
	}
	state.BaseURL = defaultBaseURL(state.BaseURL)
	if state.BaseURL == "" {
		state.BaseURL = baseURL
	}
	if state.Attempts == nil {
		state.Attempts = map[string]int{}
	}
	if err := waitForNetwork(ctx, logger, state.BaseURL); err != nil {
		return err
	}
	if err := setupResumeHook(paths, logger); err != nil {
		return err
	}
	report := SessionReport{}
	if state.ReportPath != "" {
		_ = loadJSON(state.ReportPath, &report)
	}
	if report.ReportPath == "" {
		report, state.ReportPath = newSessionReport(state.Plan, paths, logger, state.StartedAt)
	}
	stopHostedSession := func() {}
	if interactive {
		_ = prepareHostedWindowsSession(ctx, logger)
		stopHostedSession = startHostedSessionController(logger)
		printKioskInstallScreen(env, true)
	}
	defer stopHostedSession()
	currentPhase := ""
	for idx := state.NextStep; idx < len(state.Plan.Steps); idx++ {
		step := state.Plan.Steps[idx]
		if !stepShouldRun(step) {
			recordStaticStepResult(&report, step)
			state.NextStep = idx + 1
			state.Completed = append(state.Completed, step.Item.ID)
			state.UpdatedAt = time.Now()
			if err := saveJSON(paths.StatePath, state); err != nil {
				return err
			}
			if err := saveSessionReport(state.ReportPath, &report); err != nil {
				return err
			}
			continue
		}
		if step.Phase != currentPhase {
			currentPhase = step.Phase
			fmt.Printf("\n%s\n", formatCategoryTitle(phaseDisplayName(step.Phase)))
		}
		fmt.Printf("%s [resume %d/%d] %s\n", termUI.cyan("==>"), idx+1, len(state.Plan.Steps), termUI.bold(step.Item.Name))
		stepKey := stepStateKey(step)
		state.Attempts[stepKey]++
		startedStep := time.Now()
		if err := executeStep(ctx, step, env, logger, state.BaseURL); err != nil {
			recordExecutedStepResult(&report, step, startedStep, err)
			report.Status = "error"
			report.Error = fmt.Sprintf("resume %s: %v", step.Item.Name, err)
			report.FinishedAt = time.Now()
			_ = saveSessionReport(state.ReportPath, &report)
			printFinalSessionScreen(report, interactive)
			return fmt.Errorf("resume %s: %w", step.Item.Name, err)
		}
		recordExecutedStepResult(&report, step, startedStep, nil)

		if env.OS == "windows" && isMaintenanceLoopStep(step) {
			rebootPending, err := windowsRebootPending(ctx, logger)
			if err == nil && rebootPending && state.Attempts[stepKey] < 4 {
				state.PendingReboot = true
				state.NextStep = idx
				state.UpdatedAt = time.Now()
				report.Status = "partial"
				report.PendingReboot = true
				report.FinishedAt = time.Now()
				if err := saveSessionReport(state.ReportPath, &report); err != nil {
					return err
				}
				if err := persistRebootState(paths, logger, &state, "Windows requested another update reboot cycle. Initra will resume automatically."); err != nil {
					return err
				}
				return nil
			}
		}

		if step.RequiresReboot {
			state.PendingReboot = true
			state.NextStep = idx + 1
			state.UpdatedAt = time.Now()
			report.Status = "partial"
			report.PendingReboot = true
			report.FinishedAt = time.Now()
			if err := saveSessionReport(state.ReportPath, &report); err != nil {
				return err
			}
			if err := persistRebootState(paths, logger, &state, fmt.Sprintf("%s requires a reboot. Initra will resume automatically.", step.Item.Name)); err != nil {
				return err
			}
			return nil
		}
		state.NextStep = idx + 1
		state.PendingReboot = false
		state.UpdatedAt = time.Now()
		if err := saveJSON(paths.StatePath, state); err != nil {
			return err
		}
		if err := saveSessionReport(state.ReportPath, &report); err != nil {
			return err
		}
	}
	_ = os.Remove(paths.StatePath)
	_ = removeResumeHook(paths)
	report.Status = "success"
	report.FinishedAt = time.Now()
	if err := saveSessionReport(state.ReportPath, &report); err != nil {
		return err
	}
	printFinalSessionScreen(report, interactive)
	return nil
}

func executeStep(ctx context.Context, step ResolvedStep, env Environment, logger *Logger, baseURL string) error {
	run := func() error {
		switch step.Method.Type {
		case "winget":
			return runWingetAction(ctx, env, logger, step.Method.Package, step.PlannedAction)
		case "apt":
			return runLinuxPackages(ctx, env, logger, "apt", step.Method)
		case "dnf":
			return runLinuxPackages(ctx, env, logger, "dnf", step.Method)
		case "pacman":
			return runLinuxPackages(ctx, env, logger, "pacman", step.Method)
		case "flatpak":
			return runLinuxPackages(ctx, env, logger, "flatpak", step.Method)
		case "direct":
			return runDirectInstall(ctx, env, logger, step.Method, step.Inputs)
		case "shell":
			return runShellCommands(ctx, env, logger, step.Method.Commands, step.Inputs)
		case "builtin":
			return runBuiltin(ctx, env, logger, step, baseURL)
		default:
			return fmt.Errorf("unsupported method type %q", step.Method.Type)
		}
	}
	var err error
	if env.OS == "windows" && stepInteraction(step) == methodInteractionHelper {
		err = withWindowsFocusRelaxed(ctx, logger, run)
	} else {
		err = run()
	}
	if err != nil {
		return err
	}
	if env.OS == "windows" && step.Item.ID == "proton-vpn" {
		_ = stopProtonVPNProcesses(ctx, logger)
		_ = runWindowsStartupCleanup(ctx, env, logger)
	}
	return nil
}

func stepInteraction(step ResolvedStep) string {
	if strings.TrimSpace(step.Method.Interaction) == "" {
		return methodInteractionUnattended
	}
	return step.Method.Interaction
}

func runWingetAction(ctx context.Context, env Environment, logger *Logger, id, action string) error {
	command := "install"
	if action == stepActionUpgrade {
		command = "upgrade"
	}
	args := []string{command, "--id", id, "-e", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
	return runProcess(ctx, env, logger, "winget", args...)
}

func selectedNeedsWinget(plan Plan) bool {
	for _, step := range plan.Steps {
		if step.Method.Type == "winget" || step.Item.ID == "windows-inbox-apps" {
			return true
		}
	}
	return false
}

func runLinuxPackages(ctx context.Context, env Environment, logger *Logger, manager string, method Method) error {
	if env.OS != "linux" {
		return errors.New("linux package manager used on non-linux host")
	}
	if len(method.Repo) > 0 {
		if err := runShellCommands(ctx, env, logger, method.Repo, nil); err != nil {
			return err
		}
	}
	switch manager {
	case "apt":
		if err := runProcess(ctx, env, logger, "apt-get", "update"); err != nil {
			return err
		}
		args := append([]string{"install", "-y"}, method.Packages...)
		return runProcess(ctx, env, logger, "apt-get", args...)
	case "dnf":
		args := append([]string{"install", "-y"}, method.Packages...)
		return runProcess(ctx, env, logger, "dnf", args...)
	case "pacman":
		args := append([]string{"-Sy", "--noconfirm"}, method.Packages...)
		return runProcess(ctx, env, logger, "pacman", args...)
	case "flatpak":
		if !env.Capabilities["flatpak"] {
			if contains(env.PackageManagers, "apt") {
				if err := runProcess(ctx, env, logger, "apt-get", "update"); err != nil {
					return err
				}
				if err := runProcess(ctx, env, logger, "apt-get", "install", "-y", "flatpak"); err != nil {
					return err
				}
			} else if contains(env.PackageManagers, "dnf") {
				if err := runProcess(ctx, env, logger, "dnf", "install", "-y", "flatpak"); err != nil {
					return err
				}
			} else if contains(env.PackageManagers, "pacman") {
				if err := runProcess(ctx, env, logger, "pacman", "-Sy", "--noconfirm", "flatpak"); err != nil {
					return err
				}
			}
		}
		_ = runProcess(ctx, env, logger, "flatpak", "remote-add", "--if-not-exists", "flathub", "https://flathub.org/repo/flathub.flatpakrepo")
		args := append([]string{"install", "-y", "flathub"}, method.Packages...)
		return runProcess(ctx, env, logger, "flatpak", args...)
	}
	return nil
}

func runDirectInstall(ctx context.Context, env Environment, logger *Logger, method Method, inputs map[string]string) error {
	url, err := renderTemplate(method.URL, env, inputs)
	if err != nil {
		return err
	}
	fileName := method.FileName
	if fileName == "" {
		fileName = filepath.Base(strings.Split(url, "?")[0])
	}
	target := filepath.Join(env.TempDir, fileName)
	if err := downloadFile(ctx, url, target); err != nil {
		return err
	}
	logger.Println("downloaded", url, "to", target)

	if env.OS == "windows" {
		args := make([]string, 0, len(method.Arguments)+1)
		args = append(args, target)
		for _, arg := range method.Arguments {
			rendered, err := renderTemplate(arg, env, inputs)
			if err != nil {
				return err
			}
			args = append(args, rendered)
		}
		return runProcess(ctx, env, logger, target, args[1:]...)
	}

	switch {
	case strings.HasSuffix(target, ".deb"):
		return runProcess(ctx, env, logger, "apt-get", "install", "-y", target)
	case strings.HasSuffix(target, ".rpm"):
		return runProcess(ctx, env, logger, "dnf", "install", "-y", target)
	case strings.HasSuffix(strings.ToLower(target), ".appimage"):
		finalPath := filepath.Join(env.HomeDir, ".local", "bin", filepath.Base(target))
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
			return err
		}
		if err := os.Rename(target, finalPath); err != nil {
			return err
		}
		if err := os.Chmod(finalPath, 0o755); err != nil {
			return err
		}
		return nil
	default:
		return runProcess(ctx, env, logger, "bash", target)
	}
}

func runShellCommands(ctx context.Context, env Environment, logger *Logger, commands []string, inputs map[string]string) error {
	for _, command := range commands {
		rendered, err := renderTemplate(command, env, inputs)
		if err != nil {
			return err
		}
		if env.OS == "windows" {
			if err := runProcess(ctx, env, logger, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", rendered); err != nil {
				return err
			}
			continue
		}
		if err := runProcess(ctx, env, logger, "/bin/sh", "-lc", rendered); err != nil {
			return err
		}
	}
	return nil
}

func runWindowsPowerShellScript(ctx context.Context, logger *Logger, script string) error {
	args := []string{
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
	}
	return runProcess(ctx, Environment{OS: "windows"}, logger, "powershell", args...)
}

func renderTemplate(value string, env Environment, inputs map[string]string) (string, error) {
	ctx := map[string]any{
		"env": map[string]string{
			"os":               env.OS,
			"arch":             env.Arch,
			"home_dir":         env.HomeDir,
			"temp_dir":         env.TempDir,
			"documents_dir":    env.DocumentsDir,
			"winget_available": fmt.Sprintf("%v", env.HasWinget),
			"windows_is_ltsc":  fmt.Sprintf("%v", env.Windows.IsLTSC),
			"windows_is_iot":   fmt.Sprintf("%v", env.Windows.IsIoT),
			"windows_build":    fmt.Sprintf("%d", env.Windows.CurrentBuild),
			"windows_product":  env.Windows.ProductName,
			"distro_id":        env.DistroID,
			"distro_name":      env.DistroName,
		},
		"inputs": inputs,
	}
	tmpl, err := template.New("value").Parse(value)
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	if err := tmpl.Execute(&builder, ctx); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func runBuiltin(ctx context.Context, env Environment, logger *Logger, step ResolvedStep, baseURL string) error {
	switch step.Method.Action {
	case "auto_refresh_rate":
		return runAutoRefreshRate(ctx, env, logger)
	case "firefox_layout":
		return applyBundledFirefoxLayout(ctx, env, logger, baseURL)
	case "office":
		return runDirectInstall(ctx, env, logger, Method{
			URL:      "https://c2rsetup.officeapps.live.com/c2r/download.aspx?ProductreleaseID=O365ProPlusRetail&platform=x64&language={{index .inputs \"office_language\"}}&version=O16GA",
			FileName: "OfficeSetup.exe",
		}, step.Inputs)
	case "mesh_agent":
		return runDirectInstall(ctx, env, logger, Method{
			URL:      "{{index .inputs \"mesh_url\"}}",
			FileName: "mesh-agent.exe",
		}, step.Inputs)
	case "fastfetch":
		return installFastfetch(ctx, env, logger)
	case "openwhispr_linux":
		return installOpenWhispr(ctx, env, logger)
	case "noisetorch_linux":
		return installNoiseTorch(ctx, env, logger)
	case "onedrive_linux":
		return installOneDriveLinux(ctx, env, logger)
	case "spicetify_marketplace":
		return installSpicetifyMarketplace(ctx, env, logger, baseURL)
	case "vencord":
		return installVencord(ctx, env, logger, baseURL)
	case "stoat":
		return installStoat(ctx, env, logger)
	case "everything_toolbar":
		return installEverythingToolbar(ctx, env, logger)
	case "windows_update":
		return runWindowsMaintenance(ctx, env, logger)
	case "driver_refresh":
		return runDriverRefresh(ctx, env, logger)
	case "windows_inbox_apps":
		return restoreWindowsInboxApps(ctx, env, logger)
	case "defender_exclude":
		return configureDefenderExclusion(ctx, env, logger, step.Inputs["defender_exclude_path"])
	case "theme_dark":
		return runDarkTheme(ctx, env, logger)
	case "sleep_policy":
		return configureSleepPolicy(ctx, env, logger)
	case "firefox_default":
		return setFirefoxDefault(ctx, env, logger)
	case "time_sync":
		return syncParisTime(ctx, env, logger)
	case "dualboot_utc":
		return enableDualBootUTC(ctx, env, logger)
	case "emoji_font_pack":
		return installWindows10EmojiFont(ctx, env, logger, baseURL)
	case "wallpaper":
		return applyWallpaper(ctx, env, logger, baseURL)
	case "firefox_policies":
		return applyFirefoxPolicies(ctx, env, logger)
	case "windows_default_apps":
		return applyWindowsDefaultApps(ctx, env, logger)
	case "windows_taskbar_cleanup":
		return applyWindowsTaskbarCleanup(ctx, env, logger)
	case "windows_startup_cleanup":
		return runWindowsStartupCleanup(ctx, env, logger)
	case "consumer_cleanup":
		return runConsumerCleanup(ctx, env, logger)
	case "tweak_privacy":
		return runPrivacyTweaks(ctx, env, logger)
	case "tweak_performance":
		return runPerformanceTweaks(ctx, env, logger)
	case "tweak_ux":
		return runUXTweaks(ctx, env, logger)
	case "tweak_gaming":
		return runGamingTweaks(ctx, env, logger)
	case "tweak_security":
		return runSecurityTweaks(ctx, env, logger)
	case "feature_wsl":
		return enableFeatureWSL(ctx, env, logger)
	case "remove_edge":
		return removeEdge(ctx, env, logger)
	case "install_cherax":
		return installCherax(ctx, env, logger)
	case "install_undetek":
		return installUndetek(ctx, env, logger)
	case "open_undetek_plus":
		return openUndetekPlusLink(ctx, env, logger)
	case "mas_activation":
		return runMASActivation(ctx, env, logger)
	case "winutil_shortcut":
		return createWinUtilDesktopShortcut(ctx, env, logger)
	case "feature_hyperv":
		return runShellCommands(ctx, env, logger, []string{`Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -All -NoRestart`}, nil)
	case "feature_sandbox":
		return runShellCommands(ctx, env, logger, []string{`Enable-WindowsOptionalFeature -Online -FeatureName Containers-DisposableClientVM -All -NoRestart`}, nil)
	case "feature_openssh":
		return runShellCommands(ctx, env, logger, []string{
			`Add-WindowsCapability -Online -Name OpenSSH.Client~~~~0.0.1.0`,
			`Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0`,
		}, nil)
	case "feature_rdp":
		return enableRemoteDesktop(ctx, env, logger)
	case "feature_developer_mode":
		return runShellCommands(ctx, env, logger, []string{`reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock" /t REG_DWORD /f /v "AllowDevelopmentWithoutDevLicense" /d 1`}, nil)
	default:
		return fmt.Errorf("unknown builtin action %q", step.Method.Action)
	}
}

func isWindows10(env Environment) bool {
	return strings.Contains(strings.ToLower(env.Windows.ProductName), "windows 10")
}

func isWindows11(env Environment) bool {
	return strings.Contains(strings.ToLower(env.Windows.ProductName), "windows 11")
}

func runAutoRefreshRate(ctx context.Context, env Environment, logger *Logger) error {
	switch env.OS {
	case "windows":
		return runAutoRefreshRateWindows(ctx, logger)
	case "linux":
		return runAutoRefreshRateLinux(ctx, env, logger)
	default:
		fmt.Println("Refresh-rate optimization is not implemented on this platform.")
		return nil
	}
}

func runAutoRefreshRateWindows(ctx context.Context, logger *Logger) error {
	script := `
$signature = @"
using System;
using System.Runtime.InteropServices;
public static class DisplayTweaks {
  public const int ENUM_CURRENT_SETTINGS = -1;
  public const int DM_DISPLAYFREQUENCY = 0x400000;
  public const int DM_PELSWIDTH = 0x80000;
  public const int DM_PELSHEIGHT = 0x100000;
  public const int DM_DISPLAYFLAGS = 0x200000;
  public const int DM_DISPLAYORIENTATION = 0x80;
  public const int CDS_UPDATEREGISTRY = 0x01;
  public const int DISPLAY_DEVICE_ATTACHED_TO_DESKTOP = 0x1;
  public const int DISP_CHANGE_SUCCESSFUL = 0;
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Ansi)]
  public struct DISPLAY_DEVICE {
    public int cb;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string DeviceName;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceString;
    public int StateFlags;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceID;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceKey;
  }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Ansi)]
  public struct DEVMODE {
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string dmDeviceName;
    public ushort dmSpecVersion;
    public ushort dmDriverVersion;
    public ushort dmSize;
    public ushort dmDriverExtra;
    public uint dmFields;
    public int dmPositionX;
    public int dmPositionY;
    public uint dmDisplayOrientation;
    public uint dmDisplayFixedOutput;
    public short dmColor;
    public short dmDuplex;
    public short dmYResolution;
    public short dmTTOption;
    public short dmCollate;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string dmFormName;
    public ushort dmLogPixels;
    public uint dmBitsPerPel;
    public uint dmPelsWidth;
    public uint dmPelsHeight;
    public uint dmDisplayFlags;
    public uint dmDisplayFrequency;
    public uint dmICMMethod;
    public uint dmICMIntent;
    public uint dmMediaType;
    public uint dmDitherType;
    public uint dmReserved1;
    public uint dmReserved2;
    public uint dmPanningWidth;
    public uint dmPanningHeight;
  }
  [DllImport("user32.dll", CharSet = CharSet.Ansi)]
  public static extern bool EnumDisplayDevices(string lpDevice, uint iDevNum, ref DISPLAY_DEVICE lpDisplayDevice, uint dwFlags);
  [DllImport("user32.dll", CharSet = CharSet.Ansi)]
  public static extern bool EnumDisplaySettings(string deviceName, int modeNum, ref DEVMODE devMode);
  [DllImport("user32.dll", CharSet = CharSet.Ansi)]
  public static extern int ChangeDisplaySettingsEx(string lpszDeviceName, ref DEVMODE lpDevMode, IntPtr hwnd, uint dwflags, IntPtr lParam);
}
"@
Add-Type -TypeDefinition $signature -ErrorAction Stop
$updated = 0
$skipped = 0
for ($i = 0; $i -lt 16; $i++) {
  $device = New-Object DisplayTweaks+DISPLAY_DEVICE
  $device.cb = [Runtime.InteropServices.Marshal]::SizeOf([type][DisplayTweaks+DISPLAY_DEVICE])
  if (-not [DisplayTweaks]::EnumDisplayDevices($null, [uint32]$i, [ref]$device, 0)) { break }
  if (($device.StateFlags -band [DisplayTweaks]::DISPLAY_DEVICE_ATTACHED_TO_DESKTOP) -eq 0) { continue }
  $current = New-Object DisplayTweaks+DEVMODE
  $current.dmSize = [Runtime.InteropServices.Marshal]::SizeOf([type][DisplayTweaks+DEVMODE])
  if (-not [DisplayTweaks]::EnumDisplaySettings($device.DeviceName, [DisplayTweaks]::ENUM_CURRENT_SETTINGS, [ref]$current)) { continue }
  $best = $current
  $bestRate = [int]$current.dmDisplayFrequency
  for ($modeIndex = 0; $modeIndex -lt 256; $modeIndex++) {
    $candidate = New-Object DisplayTweaks+DEVMODE
    $candidate.dmSize = [Runtime.InteropServices.Marshal]::SizeOf([type][DisplayTweaks+DEVMODE])
    if (-not [DisplayTweaks]::EnumDisplaySettings($device.DeviceName, $modeIndex, [ref]$candidate)) { break }
    if ($candidate.dmPelsWidth -ne $current.dmPelsWidth) { continue }
    if ($candidate.dmPelsHeight -ne $current.dmPelsHeight) { continue }
    if ($candidate.dmBitsPerPel -ne $current.dmBitsPerPel) { continue }
    if ($candidate.dmDisplayOrientation -ne $current.dmDisplayOrientation) { continue }
    $rate = [int]$candidate.dmDisplayFrequency
    if ($rate -gt $bestRate -and $rate -lt 1000) {
      $best = $candidate
      $bestRate = $rate
    }
  }
  if ($bestRate -le [int]$current.dmDisplayFrequency) {
    Write-Host ("No higher refresh rate found for {0} ({1} Hz)." -f $device.DeviceName, $current.dmDisplayFrequency)
    $skipped++
    continue
  }
  $best.dmFields = $best.dmFields -bor [DisplayTweaks]::DM_DISPLAYFREQUENCY -bor [DisplayTweaks]::DM_PELSWIDTH -bor [DisplayTweaks]::DM_PELSHEIGHT -bor [DisplayTweaks]::DM_DISPLAYFLAGS -bor [DisplayTweaks]::DM_DISPLAYORIENTATION
  $result = [DisplayTweaks]::ChangeDisplaySettingsEx($device.DeviceName, [ref]$best, [IntPtr]::Zero, [DisplayTweaks]::CDS_UPDATEREGISTRY, [IntPtr]::Zero)
  if ($result -eq [DisplayTweaks]::DISP_CHANGE_SUCCESSFUL) {
    Write-Host ("Applied {0} Hz on {1}." -f $bestRate, $device.DeviceName)
    $updated++
  } else {
    Write-Host ("Unable to change refresh rate for {0} (result {1})." -f $device.DeviceName, $result)
  }
}
Write-Host ("Refresh-rate optimization finished. Updated: {0}; unchanged: {1}." -f $updated, $skipped)
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func runAutoRefreshRateLinux(ctx context.Context, env Environment, logger *Logger) error {
	if env.SessionType == "wayland" {
		fmt.Println("Skipping refresh-rate optimization on Linux: Wayland sessions are not handled yet.")
		return nil
	}
	if !env.Capabilities["xrandr"] || os.Getenv("DISPLAY") == "" {
		fmt.Println("Skipping refresh-rate optimization on Linux: xrandr or DISPLAY is unavailable.")
		return nil
	}

	output, err := runOutput("xrandr", "--query")
	if err != nil {
		return err
	}

	type displayMode struct {
		name        string
		mode        string
		currentRate float64
		bestRate    float64
	}

	var currentOutput string
	var changes []displayMode
	lines := strings.Split(output, "\n")
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, " connected") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				currentOutput = fields[0]
			}
			continue
		}
		if currentOutput == "" || trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, "   ") && !strings.HasPrefix(line, "\t") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || !strings.Contains(fields[0], "x") {
			continue
		}
		modeName := fields[0]
		rates := parseXRandrRates(fields[1:])
		if len(rates) == 0 {
			continue
		}
		modeHasCurrent := false
		currentRate := 0.0
		bestRate := 0.0
		for _, rate := range rates {
			if rate.Value > bestRate {
				bestRate = rate.Value
			}
			if rate.Current {
				modeHasCurrent = true
				currentRate = rate.Value
			}
		}
		if modeHasCurrent {
			if bestRate > currentRate {
				changes = append(changes, displayMode{
					name:        currentOutput,
					mode:        modeName,
					currentRate: currentRate,
					bestRate:    bestRate,
				})
			}
			continue
		}
	}

	if len(changes) == 0 {
		fmt.Println("No higher refresh rate was found on Linux displays.")
		return nil
	}

	for _, change := range changes {
		rateArg := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", change.bestRate), "0"), ".")
		fmt.Printf("Applying %s Hz on %s (%s).\n", rateArg, change.name, change.mode)
		if err := runProcess(ctx, env, logger, "xrandr", "--output", change.name, "--mode", change.mode, "--rate", rateArg); err != nil {
			return err
		}
	}
	return nil
}

type xrandrRate struct {
	Value   float64
	Current bool
}

func parseXRandrRates(tokens []string) []xrandrRate {
	rates := make([]xrandrRate, 0, len(tokens))
	for _, token := range tokens {
		clean := strings.TrimSpace(token)
		if clean == "" {
			continue
		}
		current := strings.Contains(clean, "*")
		clean = strings.TrimRight(clean, "+*i")
		value, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			continue
		}
		rates = append(rates, xrandrRate{Value: value, Current: current})
	}
	return rates
}

func runDarkTheme(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		return runShellCommands(ctx, env, logger, []string{
			`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize" /v AppsUseLightTheme /t REG_DWORD /d 0 /f`,
			`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize" /v SystemUsesLightTheme /t REG_DWORD /d 0 /f`,
		}, nil)
	}

	if env.Capabilities["gsettings"] && strings.Contains(strings.ToLower(env.DesktopSession), "gnome") {
		if err := runProcess(ctx, env, logger, "gsettings", "set", "org.gnome.desktop.interface", "color-scheme", "prefer-dark"); err == nil {
			_ = runProcess(ctx, env, logger, "gsettings", "set", "org.gnome.desktop.interface", "gtk-theme", "Adwaita-dark")
			return nil
		}
	}
	if strings.Contains(strings.ToLower(env.DesktopSession), "kde") || strings.Contains(strings.ToLower(env.DesktopSession), "plasma") {
		switch {
		case env.Capabilities["plasma-apply-lookandfeel"]:
			return runProcess(ctx, env, logger, "plasma-apply-lookandfeel", "-a", "org.kde.breezedark.desktop")
		case env.Capabilities["lookandfeeltool"]:
			return runProcess(ctx, env, logger, "lookandfeeltool", "-a", "org.kde.breezedark.desktop")
		}
	}
	fmt.Println("Dark theme was skipped on Linux because only GNOME and KDE Plasma desktops are handled automatically right now.")
	return nil
}

func configureSleepPolicy(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		for _, args := range [][]string{
			{"powercfg", "/change", "standby-timeout-ac", "0"},
			{"powercfg", "/change", "hibernate-timeout-ac", "0"},
			{"powercfg", "/change", "standby-timeout-dc", "10"},
			{"powercfg", "/change", "hibernate-timeout-dc", "0"},
		} {
			if err := runProcess(ctx, env, logger, args[0], args[1:]...); err != nil {
				return err
			}
		}
		return nil
	}

	if env.OS != "linux" {
		return nil
	}

	desktop := strings.ToLower(env.DesktopSession)
	switch {
	case strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma"):
		writer := "kwriteconfig6"
		configFile := "powerdevilrc"
		if !env.Capabilities["kwriteconfig6"] {
			if env.Capabilities["kwriteconfig5"] {
				writer = "kwriteconfig5"
				configFile = "powermanagementprofilesrc"
			} else {
				fmt.Println("Sleep policy was skipped on KDE because kwriteconfig6 or kwriteconfig5 is unavailable.")
				return nil
			}
		}
		configPath := filepath.Join(env.HomeDir, ".config", configFile)
		for _, update := range []struct {
			profile string
			key     string
			value   string
		}{
			{profile: "AC", key: "AutoSuspendAction", value: "0"},
			{profile: "AC", key: "AutoSuspendIdleTimeoutSec", value: "0"},
			{profile: "Battery", key: "AutoSuspendAction", value: "1"},
			{profile: "Battery", key: "AutoSuspendIdleTimeoutSec", value: "600"},
			{profile: "LowBattery", key: "AutoSuspendAction", value: "1"},
			{profile: "LowBattery", key: "AutoSuspendIdleTimeoutSec", value: "600"},
		} {
			if err := runProcess(ctx, env, logger, writer, "--notify", "--file", configPath, "--group", update.profile, "--group", "SuspendAndShutdown", "--type", "int", "--key", update.key, update.value); err != nil {
				return err
			}
		}
		return nil
	default:
		fmt.Println("Sleep policy was skipped on Linux because only KDE Plasma desktops are handled automatically right now.")
		return nil
	}
}

func setFirefoxDefault(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		firefoxPath := findFirefoxBinaryWindows()
		if firefoxPath == "" {
			fmt.Println("Firefox is not installed, so default-browser setup was skipped.")
			return nil
		}
		if err := runProcess(ctx, env, logger, firefoxPath, "-silent", "-setDefaultBrowser"); err == nil {
			return nil
		}
		fmt.Println("Firefox could not set itself as default automatically. Opening Default Apps settings instead.")
		return runProcess(ctx, env, logger, "cmd", "/c", "start", "", "ms-settings:defaultapps")
	}

	if !commandExists("firefox") {
		fmt.Println("Firefox is not installed, so default-browser setup was skipped.")
		return nil
	}
	if env.Capabilities["xdg-settings"] {
		if err := runProcess(ctx, env, logger, "xdg-settings", "set", "default-web-browser", "firefox.desktop"); err == nil {
			return nil
		}
	}
	return runShellCommands(ctx, env, logger, []string{
		`xdg-mime default firefox.desktop x-scheme-handler/http`,
		`xdg-mime default firefox.desktop x-scheme-handler/https`,
		`xdg-mime default firefox.desktop text/html`,
	}, nil)
}

func findFirefoxBinaryWindows() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Mozilla Firefox", "firefox.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Mozilla Firefox", "firefox.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Mozilla Firefox", "firefox.exe"),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func syncParisTime(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		script := `
$ErrorActionPreference = 'Stop'
tzutil /s "Romance Standard Time"
try { Start-Service W32Time -ErrorAction SilentlyContinue } catch {}
try { w32tm /resync /force | Out-Host } catch { Write-Host 'Time resync returned a non-blocking error.' }
`
		return runWindowsPowerShellScript(ctx, logger, script)
	}

	if !env.Capabilities["timedatectl"] {
		fmt.Println("Skipping timezone sync on Linux because timedatectl is unavailable.")
		return nil
	}
	if err := runProcess(ctx, env, logger, "timedatectl", "set-timezone", "Europe/Paris"); err != nil {
		return err
	}
	return runProcess(ctx, env, logger, "timedatectl", "set-ntp", "true")
}

func enableDualBootUTC(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	script := `
$ErrorActionPreference = 'Stop'
reg add "HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation" /v RealTimeIsUniversal /t REG_DWORD /d 1 /f | Out-Null
try { Start-Service W32Time -ErrorAction SilentlyContinue } catch {}
try { w32tm /resync /force | Out-Host } catch { Write-Host 'UTC clock fix applied. Time resync returned a non-blocking error.' }
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func installWindows10EmojiFont(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	if env.OS != "windows" {
		return nil
	}
	if !isWindows10(env) {
		fmt.Println("Skipping emoji font pack: this option only targets Windows 10.")
		return nil
	}

	fontPath, cleanup, err := resolveAssetPath(ctx, env, baseURL, filepath.Join("app", "pack-emoji.ttf"))
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$fontSource = '%s'
$fontFile = Split-Path $fontSource -Leaf
$fontTarget = [System.IO.Path]::Combine($env:WINDIR, 'Fonts', $fontFile)
Add-Type -AssemblyName System.Drawing
$fonts = New-Object System.Drawing.Text.PrivateFontCollection
$fonts.AddFontFile($fontSource)
$family = $fonts.Families[0].Name
Copy-Item -LiteralPath $fontSource -Destination $fontTarget -Force
New-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts' -Name ($family + ' (TrueType)') -PropertyType String -Value $fontFile -Force | Out-Null
Add-Type -Namespace Win32 -Name FontApi -MemberDefinition @"
[System.Runtime.InteropServices.DllImport("gdi32.dll", CharSet=System.Runtime.InteropServices.CharSet.Auto)]
public static extern int AddFontResource(string lpFileName);
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError=true)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, IntPtr lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
"@
[void][Win32.FontApi]::AddFontResource($fontTarget)
$result = [UIntPtr]::Zero
[void][Win32.FontApi]::SendMessageTimeout([IntPtr]0xffff, 0x001D, [UIntPtr]::Zero, [IntPtr]::Zero, 0x0002, 1000, [ref]$result)
Write-Host ('Installed font family: ' + $family)
Write-Host 'Reboot required before the emoji pack is fully applied.'
`, filepath.Clean(fontPath))
	return runWindowsPowerShellScript(ctx, logger, script)
}

func resolveAssetPath(ctx context.Context, env Environment, baseURL, relPath string) (string, func(), error) {
	candidates := []string{
		filepath.Join(mustAbs("."), relPath),
		filepath.Join(mustAbs("."), "releases", relPath),
	}
	execPath, _ := os.Executable()
	if execPath != "" {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, relPath),
			filepath.Join(execDir, "..", relPath),
			filepath.Join(execDir, "..", "..", relPath),
			filepath.Join(execDir, "releases", relPath),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil, nil
		}
	}

	target := filepath.Join(env.TempDir, filepath.Base(relPath))
	remote := strings.TrimRight(baseURL, "/") + "/releases/" + filepath.ToSlash(relPath)
	if err := downloadFile(ctx, remote, target); err != nil {
		return "", nil, err
	}
	return target, func() { _ = os.Remove(target) }, nil
}

func runConsumerCleanup(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	script := `
$ErrorActionPreference = 'Continue'
$patterns = @(
  'Clipchamp.Clipchamp',
  'Microsoft.BingNews',
  'Microsoft.BingWeather',
  'Microsoft.GamingApp',
  'Microsoft.GetHelp',
  'Microsoft.Getstarted',
  'Microsoft.MicrosoftOfficeHub',
  'Microsoft.MicrosoftSolitaireCollection',
  'Microsoft.People',
  'Microsoft.PowerAutomateDesktop',
  'Microsoft.Teams',
  'Microsoft.Todos',
  'Microsoft.WindowsFeedbackHub',
  'Microsoft.Xbox.TCUI',
  'Microsoft.XboxApp',
  'Microsoft.XboxGameOverlay',
  'Microsoft.XboxGamingOverlay',
  'Microsoft.XboxIdentityProvider',
  'Microsoft.XboxSpeechToTextOverlay',
  'MicrosoftCorporationII.QuickAssist',
  'Microsoft.OutlookForWindows'
)
foreach ($pattern in $patterns) {
  Get-AppxPackage -AllUsers -Name $pattern -ErrorAction SilentlyContinue | ForEach-Object {
    try { Remove-AppxPackage -Package $_.PackageFullName -AllUsers -ErrorAction SilentlyContinue } catch {}
  }
  Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -like $pattern -or $_.PackageName -like ($pattern + '*') } | ForEach-Object {
    try { Remove-AppxProvisionedPackage -Online -PackageName $_.PackageName -ErrorAction SilentlyContinue | Out-Null } catch {}
  }
}
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager" /v SilentInstalledAppsEnabled /t REG_DWORD /d 0 /f | Out-Null
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager" /v SubscribedContent-338388Enabled /t REG_DWORD /d 0 /f | Out-Null
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager" /v SystemPaneSuggestionsEnabled /t REG_DWORD /d 0 /f | Out-Null
Write-Host 'Consumer app cleanup finished. Some built-in packages may remain if the current image protects them.'
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func enableRemoteDesktop(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	script := `
$ErrorActionPreference = 'Stop'
reg add "HKLM\SYSTEM\CurrentControlSet\Control\Terminal Server" /v fDenyTSConnections /t REG_DWORD /d 0 /f | Out-Null
reg add "HKLM\SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp" /v UserAuthentication /t REG_DWORD /d 0 /f | Out-Null
try { Enable-NetFirewallRule -DisplayGroup "Remote Desktop" -ErrorAction SilentlyContinue | Out-Null } catch {}
cmd /c 'netsh advfirewall firewall set rule group="remote desktop" new enable=Yes' | Out-Null
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func printDiagnosis(catalog Catalog, env Environment) error {
	data, _ := json.MarshalIndent(env, "", "  ")
	fmt.Println(string(data))
	fmt.Println()
	fmt.Println("Visible catalog items:")
	for _, item := range catalog.visibleItemsFor(env) {
		fmt.Printf("  - %s (%s)\n", item.Name, item.ID)
	}
	return nil
}

func openLogger(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(logDir, time.Now().Format("20060102-150405")+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: file, path: path}, nil
}

func (l *Logger) Println(values ...any) {
	if l == nil || l.file == nil {
		return
	}
	fmt.Fprintln(l.file, append([]any{time.Now().Format(time.RFC3339)}, values...)...)
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func runProcess(ctx context.Context, env Environment, logger *Logger, name string, args ...string) error {
	logger.Println("run", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = io.MultiWriter(os.Stdout, logger.file)
	cmd.Stderr = io.MultiWriter(os.Stderr, logger.file)
	if env.OS == "linux" && !env.IsAdmin && requiresPrivilege(name, args...) && env.HasSudo && name != "sudo" {
		fullArgs := append([]string{name}, args...)
		cmd = exec.CommandContext(ctx, "sudo", fullArgs...)
		cmd.Stdout = io.MultiWriter(os.Stdout, logger.file)
		cmd.Stderr = io.MultiWriter(os.Stderr, logger.file)
	}
	return cmd.Run()
}

func requiresPrivilege(name string, args ...string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	switch name {
	case "apt-get", "dnf", "pacman", "flatpak":
		return true
	case "timedatectl":
		return len(args) > 0 && (args[0] == "set-timezone" || args[0] == "set-ntp")
	case "/bin/sh":
		joined := strings.Join(args, " ")
		return strings.Contains(joined, "apt-get") || strings.Contains(joined, "dnf ") || strings.Contains(joined, "pacman ") || strings.Contains(joined, "fwupdmgr ") || strings.Contains(joined, "timedatectl ")
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func currentBinaryPath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

func createRestorePoint(logger *Logger) error {
	logger.Println("creating restore point")
	_, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf(`try { Checkpoint-Computer -Description '%s' -RestorePointType 'MODIFY_SETTINGS' | Out-Null; 'ok' } catch { $_ | Out-String }`, restorePointDescription))
	return err
}

func ensureWinget(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" || env.HasWinget {
		return nil
	}
	logger.Println("bootstrapping winget")
	if !env.Windows.IsIoT {
		_ = runProcess(ctx, env, logger, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Add-AppxPackage -RegisterByFamilyName -MainPackage Microsoft.DesktopAppInstaller_8wekyb3d8bbwe`)
		if commandExists("winget") {
			return nil
		}
		_ = runProcess(ctx, env, logger, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `$progressPreference = 'silentlyContinue'; Install-PackageProvider -Name NuGet -Force | Out-Null; Install-Module -Name Microsoft.WinGet.Client -Force -Repository PSGallery | Out-Null; Repair-WinGetPackageManager -AllUsers`)
		if commandExists("winget") {
			return nil
		}
	}
	return installWingetForIoT(ctx, env, logger)
}

func installWingetForIoT(ctx context.Context, env Environment, logger *Logger) error {
	tempDir := filepath.Join(env.TempDir, appSlug+"-winget")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}

	release, err := fetchGitHubRelease(ctx, "microsoft", "winget-cli")
	if err != nil {
		return err
	}
	var msixURL, licenseURL string
	for _, asset := range release.Assets {
		switch {
		case strings.HasSuffix(asset.Name, ".msixbundle") && msixURL == "":
			msixURL = asset.BrowserDownloadURL
		case strings.Contains(strings.ToLower(asset.Name), "license") && strings.HasSuffix(strings.ToLower(asset.Name), ".xml") && licenseURL == "":
			licenseURL = asset.BrowserDownloadURL
		}
	}
	if msixURL == "" || licenseURL == "" {
		return errors.New("could not resolve WinGet release assets")
	}

	vclibsPath := filepath.Join(tempDir, "Microsoft.VCLibs.x64.14.00.Desktop.appx")
	if err := downloadFile(ctx, "https://aka.ms/Microsoft.VCLibs.x64.14.00.Desktop.appx", vclibsPath); err != nil {
		return err
	}

	uiPath, err := fetchLatestUIXamlAppx(ctx, tempDir)
	if err != nil {
		return err
	}
	msixPath := filepath.Join(tempDir, "winget.msixbundle")
	licensePath := filepath.Join(tempDir, "License1.xml")
	if err := downloadFile(ctx, msixURL, msixPath); err != nil {
		return err
	}
	if err := downloadFile(ctx, licenseURL, licensePath); err != nil {
		return err
	}

	script := fmt.Sprintf(`
Add-AppxPackage -Path '%s'
Add-AppxPackage -Path '%s'
Add-AppxPackage -Path '%s'
Add-AppxProvisionedPackage -Online -PackagePath '%s' -LicensePath '%s'
`, vclibsPath, uiPath, msixPath, msixPath, licensePath)
	if err := runProcess(ctx, env, logger, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script); err != nil {
		return err
	}
	return nil
}

func fetchLatestUIXamlAppx(ctx context.Context, tempDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.nuget.org/v3-flatcontainer/microsoft.ui.xaml/index.json", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("nuget index returned %s", resp.Status)
	}
	var payload struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	version := ""
	for i := len(payload.Versions) - 1; i >= 0; i-- {
		if strings.HasPrefix(payload.Versions[i], "2.8.") {
			version = payload.Versions[i]
			break
		}
	}
	if version == "" {
		return "", errors.New("no Microsoft.UI.Xaml 2.8 package found")
	}

	nupkgURL := fmt.Sprintf("https://www.nuget.org/api/v2/package/Microsoft.UI.Xaml/%s", version)
	nupkgPath := filepath.Join(tempDir, "Microsoft.UI.Xaml."+version+".nupkg")
	if err := downloadFile(ctx, nupkgURL, nupkgPath); err != nil {
		return "", err
	}
	zipPath := strings.TrimSuffix(nupkgPath, ".nupkg") + ".zip"
	if err := os.Rename(nupkgPath, zipPath); err != nil {
		return "", err
	}
	if err := unzipSingle(zipPath, filepath.Join("tools", "AppX", "x64", "Release", "Microsoft.UI.Xaml.2.8.appx"), filepath.Join(tempDir, "Microsoft.UI.Xaml.2.8.appx")); err != nil {
		return "", err
	}
	return filepath.Join(tempDir, "Microsoft.UI.Xaml.2.8.appx"), nil
}

func setupResumeHook(paths Paths, logger *Logger) error {
	binary := currentBinaryPath()
	if binary == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		taskName := appName + " Resume"
		taskRun := fmt.Sprintf(`"%s" --resume --state-path "%s"`, binary, paths.StatePath)
		logger.Println("installing windows resume task", taskName)
		return exec.Command("schtasks", "/create", "/f", "/sc", "ONLOGON", "/rl", "HIGHEST", "/tn", taskName, "/tr", taskRun).Run()
	}
	if paths.ResumeAutostart == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(paths.ResumeAutostart), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s Resume\nExec=%q --resume --state-path %q\nX-GNOME-Autostart-enabled=true\n", appName, binary, paths.StatePath)
	return os.WriteFile(paths.ResumeAutostart, []byte(content), 0o644)
}

func removeResumeHook(paths Paths) error {
	if runtime.GOOS == "windows" {
		taskName := appName + " Resume"
		return exec.Command("schtasks", "/delete", "/f", "/tn", taskName).Run()
	}
	if paths.ResumeAutostart == "" {
		return nil
	}
	_ = os.Remove(paths.ResumeAutostart)
	return nil
}

func downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %s returned %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fetchGitHubRelease(ctx context.Context, owner, repo string) (githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo), nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return githubRelease{}, fmt.Errorf("github latest release returned %s", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func ensureWingetPackage(ctx context.Context, env Environment, logger *Logger, packageID string) error {
	action, err := detectWingetPackageState(Item{Name: packageID}, packageID, env, logger)
	if err != nil {
		logger.Println("winget package state detection failed", packageID, err)
	}
	switch action {
	case stepActionAlreadyPresent, stepActionAlreadyUpToDate:
		return nil
	case stepActionUpgrade:
		return runWingetAction(ctx, env, logger, packageID, stepActionUpgrade)
	default:
		return runWingetAction(ctx, env, logger, packageID, stepActionInstall)
	}
}

func findGitHubReleaseAsset(release githubRelease, parts ...string) *githubReleaseAsset {
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		match := true
		for _, part := range parts {
			if !strings.Contains(name, strings.ToLower(part)) {
				match = false
				break
			}
		}
		if match {
			selected := asset
			return &selected
		}
	}
	return nil
}

func installFastfetch(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		return ensureWingetPackage(ctx, env, logger, "Fastfetch-cli.Fastfetch")
	}
	if env.OS != "linux" {
		return nil
	}

	release, err := fetchGitHubRelease(ctx, "fastfetch-cli", "fastfetch")
	if err != nil {
		return err
	}

	arch := "amd64"
	switch env.Arch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "aarch64"
	default:
		return fmt.Errorf("fastfetch is not wired for linux arch %q yet", env.Arch)
	}

	if contains(env.PackageManagers, "apt") {
		if asset := findGitHubReleaseAsset(release, "linux-"+arch, ".deb"); asset != nil {
			return runDirectInstall(ctx, env, logger, Method{
				URL:      asset.BrowserDownloadURL,
				FileName: asset.Name,
			}, nil)
		}
	}
	if contains(env.PackageManagers, "dnf") {
		if asset := findGitHubReleaseAsset(release, "linux-"+arch, ".rpm"); asset != nil {
			return runDirectInstall(ctx, env, logger, Method{
				URL:      asset.BrowserDownloadURL,
				FileName: asset.Name,
			}, nil)
		}
	}

	asset := findGitHubReleaseAsset(release, "linux-"+arch, ".tar.gz")
	if asset == nil {
		return errors.New("could not find a compatible fastfetch Linux asset")
	}
	target := filepath.Join(env.TempDir, asset.Name)
	if err := downloadFile(ctx, asset.BrowserDownloadURL, target); err != nil {
		return err
	}
	extractDir := filepath.Join(env.TempDir, "initra-fastfetch")
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := runProcess(ctx, env, logger, "tar", "-xzf", target, "-C", extractDir); err != nil {
		return err
	}
	binaryPath, err := findFileRecursive(extractDir, "fastfetch")
	if err != nil {
		return err
	}
	finalDir := filepath.Join(env.HomeDir, ".local", "bin")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return err
	}
	finalPath := filepath.Join(finalDir, "fastfetch")
	if err := copyFile(binaryPath, finalPath, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(finalPath, 0o755); err != nil {
		return err
	}
	fmt.Println("Installed fastfetch into ~/.local/bin using the official release artifact.")
	return nil
}

func installNoiseTorch(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "linux" {
		return nil
	}
	if env.Arch != "amd64" {
		return fmt.Errorf("noisetorch automation currently targets linux amd64 only, got %q", env.Arch)
	}

	release, err := fetchGitHubRelease(ctx, "noisetorch", "NoiseTorch")
	if err != nil {
		return err
	}
	asset := findGitHubReleaseAsset(release, "x64", ".tgz")
	if asset == nil {
		return errors.New("could not find a compatible NoiseTorch release asset")
	}
	target := filepath.Join(env.TempDir, asset.Name)
	if err := downloadFile(ctx, asset.BrowserDownloadURL, target); err != nil {
		return err
	}
	if err := runProcess(ctx, env, logger, "tar", "-C", env.HomeDir, "-h", "-xzf", target); err != nil {
		return err
	}
	noisetorchPath := filepath.Join(env.HomeDir, ".local", "bin", "noisetorch")
	if _, err := os.Stat(noisetorchPath); err != nil {
		return fmt.Errorf("noisetorch binary was not found after extraction: %w", err)
	}
	if commandExists("setcap") {
		if err := runProcess(ctx, env, logger, "setcap", "CAP_SYS_RESOURCE=+ep", noisetorchPath); err != nil {
			logger.Println("setcap for noisetorch failed", err)
		}
	}
	fmt.Println("Installed NoiseTorch into ~/.local/bin. Select the filtered microphone in your voice apps afterwards.")
	return nil
}

func installEverythingToolbar(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	if err := ensureWingetPackage(ctx, env, logger, "voidtools.Everything"); err != nil {
		return err
	}

	toolbarPackage := "srwi.EverythingToolbar.Launcher"
	if isWindows10(env) {
		toolbarPackage = "srwi.EverythingToolbar.Deskband"
	}
	if err := ensureWingetPackage(ctx, env, logger, toolbarPackage); err != nil {
		return err
	}

	if isWindows10(env) {
		fmt.Println("EverythingToolbar was installed. On Windows 10 you may still need to enable it from the taskbar Toolbars menu.")
	} else {
		fmt.Println("EverythingToolbar was installed. If the setup assistant does not appear automatically, launch it from Start.")
	}
	return nil
}

func createWinUtilDesktopShortcut(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	_ = ctx
	_ = logger

	desktopDir := firstExisting(
		filepath.Join(env.HomeDir, "Desktop"),
		filepath.Join(env.HomeDir, "OneDrive", "Desktop"),
	)
	if desktopDir == "" {
		desktopDir = filepath.Join(env.HomeDir, "Desktop")
	}
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		return err
	}

	powershellPath := filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershellPath); err != nil {
		powershellPath = "powershell.exe"
	}
	iconPath := currentBinaryPath()
	if iconPath == "" {
		iconPath = powershellPath
	}
	shortcutPath := filepath.Join(desktopDir, "WinUtil - App Installer.lnk")
	arguments := `-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command "Start-Process powershell -Verb RunAs -WindowStyle Hidden -ArgumentList '-NoProfile -ExecutionPolicy Bypass -Command ""irm https://christitus.com/win | iex""'"`
	if err := createWindowsShortcutEx(shortcutPath, powershellPath, env.HomeDir, arguments, iconPath); err != nil {
		return err
	}
	fmt.Printf("Created %s on the desktop.\n", filepath.Base(shortcutPath))
	return nil
}

func installOpenWhispr(ctx context.Context, env Environment, logger *Logger) error {
	release, err := fetchGitHubRelease(ctx, "OpenWhispr", "openwhispr")
	if err != nil {
		return err
	}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		switch {
		case strings.Contains(name, "linux") && strings.Contains(name, "appimage"):
			return runDirectInstall(ctx, env, logger, Method{
				URL:      asset.BrowserDownloadURL,
				FileName: asset.Name,
			}, nil)
		case contains(env.PackageManagers, "apt") && strings.Contains(name, "linux-amd64.deb"):
			return runDirectInstall(ctx, env, logger, Method{
				URL:      asset.BrowserDownloadURL,
				FileName: asset.Name,
			}, nil)
		}
	}
	return errors.New("could not find a supported OpenWhispr Linux asset")
}

func installOneDriveLinux(ctx context.Context, env Environment, logger *Logger) error {
	switch {
	case contains(env.PackageManagers, "apt"):
		return runProcess(ctx, env, logger, "apt-get", "install", "-y", "onedrive")
	case contains(env.PackageManagers, "dnf"):
		return runProcess(ctx, env, logger, "dnf", "install", "-y", "onedrive")
	default:
		return errors.New("onedrive Linux helper is currently implemented for apt/dnf systems only")
	}
}

func runWindowsMaintenance(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "linux" {
		var updateErr error
		if contains(env.PackageManagers, "apt") {
			if err := runProcess(ctx, env, logger, "apt-get", "update"); err != nil {
				return err
			}
			if err := runProcess(ctx, env, logger, "apt-get", "upgrade", "-y"); err != nil {
				updateErr = err
			}
		} else if contains(env.PackageManagers, "dnf") {
			if err := runProcess(ctx, env, logger, "dnf", "upgrade", "-y", "--refresh"); err != nil {
				updateErr = err
			}
		} else if contains(env.PackageManagers, "pacman") {
			if err := runProcess(ctx, env, logger, "pacman", "-Syu", "--noconfirm"); err != nil {
				updateErr = err
			}
		}
		if updateErr != nil {
			return updateErr
		}
		if env.Capabilities["flatpak"] {
			if err := runProcess(ctx, env, logger, "flatpak", "update", "-y"); err != nil {
				return err
			}
		}
		if env.Capabilities["fwupdmgr"] {
			_ = runProcess(ctx, env, logger, "fwupdmgr", "refresh", "--force")
			_ = runProcess(ctx, env, logger, "fwupdmgr", "get-updates")
		}
		return nil
	}

	var updateErr error
	pswuAvailable := false
	if err := ensurePSWindowsUpdate(ctx, logger); err == nil {
		pswuAvailable = true
		script := `
$ErrorActionPreference = 'Stop'
Import-Module PSWindowsUpdate -Force
try { Add-WUServiceManager -MicrosoftUpdate -Confirm:$false -ErrorAction SilentlyContinue | Out-Null } catch {}
$pending = Get-WindowsUpdate -MicrosoftUpdate -AcceptAll -IgnoreReboot -IgnoreUserInput -ErrorAction SilentlyContinue
if ($pending) {
  $pending | Select-Object Title, KB, Size | Format-Table -AutoSize | Out-String | Write-Host
  Install-WindowsUpdate -MicrosoftUpdate -AcceptAll -IgnoreReboot -IgnoreUserInput -Verbose -ErrorAction Stop
} else {
  Write-Host 'No pending Windows/Microsoft updates detected by PSWindowsUpdate.'
}
Write-Host ''
Write-Host 'Recent Windows Update history:'
Get-WUHistory | Select-Object -First 10 Date, Title, Result | Format-Table -AutoSize | Out-String | Write-Host
`
		if err := runWindowsPowerShellScript(ctx, logger, script); err != nil {
			logger.Println("pswindowsupdate maintenance flow failed, falling back to builtin scan")
			updateErr = err
		}
	} else {
		updateErr = err
	}

	if !pswuAvailable || updateErr != nil {
		script := `
try { Start-Process "ms-settings:windowsupdate" } catch {}
try { UsoClient StartInteractiveScan } catch {}
try { UsoClient StartScan } catch {}
try { UsoClient StartDownload } catch {}
try { UsoClient StartInstall } catch {}
`
		if err := runWindowsPowerShellScript(ctx, logger, script); err != nil {
			return err
		}
	}
	if commandExists("winget") {
		if err := runProcess(ctx, env, logger, "winget", "upgrade", "--all", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"); err != nil {
			logger.Println("winget upgrade all failed", err)
		}
	}
	return nil
}

func runDriverRefresh(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "linux" {
		if env.Capabilities["fwupdmgr"] {
			_ = runProcess(ctx, env, logger, "fwupdmgr", "refresh", "--force")
			return runProcess(ctx, env, logger, "fwupdmgr", "update", "-y")
		}
		return nil
	}

	if err := ensurePSWindowsUpdate(ctx, logger); err == nil {
		script := `
$ErrorActionPreference = 'Stop'
Import-Module PSWindowsUpdate -Force
try { Add-WUServiceManager -MicrosoftUpdate -Confirm:$false -ErrorAction SilentlyContinue | Out-Null } catch {}
$driverUpdates = Get-WindowsUpdate -MicrosoftUpdate -UpdateType Driver -AcceptAll -IgnoreReboot -IgnoreUserInput -ErrorAction SilentlyContinue
if ($driverUpdates) {
  $driverUpdates | Select-Object Title, DriverModel, DriverVerDate | Format-Table -AutoSize | Out-String | Write-Host
  Install-WindowsUpdate -MicrosoftUpdate -UpdateType Driver -AcceptAll -IgnoreReboot -IgnoreUserInput -Verbose -ErrorAction Stop
} else {
  Write-Host 'No driver updates detected through Microsoft Update.'
}
`
		if err := runWindowsPowerShellScript(ctx, logger, script); err != nil {
			logger.Println("pswindowsupdate driver flow failed, continuing with OEM tools", err)
		}
	}

	switch {
	case strings.Contains(strings.ToLower(env.Windows.Manufacturer), "dell"):
		_ = runWingetAction(ctx, env, logger, "Dell.CommandUpdate.Universal", stepActionInstall)
	case strings.Contains(strings.ToLower(env.Windows.Manufacturer), "lenovo"):
		_ = runWingetAction(ctx, env, logger, "Lenovo.SystemUpdate", stepActionInstall)
	case strings.Contains(strings.ToLower(env.Windows.Manufacturer), "hp"):
		_ = runWingetAction(ctx, env, logger, "HPInc.HPSupportAssistant", stepActionInstall)
	}
	if strings.Contains(strings.ToLower(env.Windows.CPUVendor), "intel") || strings.Contains(strings.ToLower(env.Windows.GPUVendor), "intel") {
		_ = runWingetAction(ctx, env, logger, "Intel.IntelDriverAndSupportAssistant", stepActionInstall)
	}
	if strings.Contains(strings.ToLower(env.Windows.GPUVendor), "amd") {
		_ = runWingetAction(ctx, env, logger, "AMD.AMDSoftwareCloudEdition", stepActionInstall)
	}
	_ = maybeInstallSteamDeckLCDDrivers(ctx, env, logger)
	_ = runProcess(ctx, env, logger, "pnputil", "/scan-devices")
	if commandExists("winget") {
		_ = runProcess(ctx, env, logger, "winget", "upgrade", "--all", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
	}
	return nil
}

func ensurePSWindowsUpdate(ctx context.Context, logger *Logger) error {
	script := `
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if (-not (Get-PackageProvider -ListAvailable -Name NuGet -ErrorAction SilentlyContinue)) {
  Install-PackageProvider -Name NuGet -Force -Scope AllUsers | Out-Null
}
if (-not (Get-Module -ListAvailable -Name PSWindowsUpdate)) {
  Install-Module -Name PSWindowsUpdate -Force -AllowClobber -Scope AllUsers -Repository PSGallery
}
Import-Module PSWindowsUpdate -Force
Get-Command Install-WindowsUpdate | Out-Null
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func restoreWindowsInboxApps(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	packages := []string{
		"Windows Camera",
		"Microsoft Photos",
		"Windows Media Player",
		"Notepad",
		"Microsoft Store",
	}
	for _, pkg := range packages {
		_ = runProcess(ctx, env, logger, "winget", "install", "--name", pkg, "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
	}
	return nil
}

func configureDefenderExclusion(ctx context.Context, env Environment, logger *Logger, path string) error {
	if env.OS != "windows" {
		return nil
	}
	if path == "" {
		path = filepath.Join(env.DocumentsDir, "Excluded")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return runProcess(ctx, env, logger, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", fmt.Sprintf(`Add-MpPreference -ExclusionPath '%s'`, path))
}

func removeEdge(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	excludedDir := filepath.Join(env.DocumentsDir, "Excluded")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		return err
	}

	exePath := filepath.Join(excludedDir, "Remove-Edge.exe")
	url := "https://github.com/ShadowWhisperer/Remove-MS-Edge/releases/latest/download/Remove-Edge.exe"
	if err := downloadFile(ctx, url, exePath); err != nil {
		return err
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'SilentlyContinue'
Stop-Process -Name "smartscreen" -Force
Start-Process -FilePath '%s' -Wait -Verb RunAs
`, exePath)
	return runWindowsPowerShellScript(ctx, logger, script)
}

func installCherax(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	excludedDir := filepath.Join(env.DocumentsDir, "Excluded")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		return err
	}

	exePath := filepath.Join(excludedDir, "CheraxLoader.exe")
	url := "https://cherax.menu/cdn/files/CheraxLoader.exe"
	return downloadFile(ctx, url, exePath)
}

func installUndetek(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	excludedDir := filepath.Join(env.DocumentsDir, "Excluded")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		return err
	}

	zipPath := filepath.Join(excludedDir, "undetek.zip")
	url := "https://undetek.com/download/download.php"
	if err := downloadFile(ctx, url, zipPath); err != nil {
		return err
	}

	destPath := filepath.Join(excludedDir, "Undetek")
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Expand-Archive -Force -Path '%s' -DestinationPath '%s'
`, zipPath, destPath)
	if err := runWindowsPowerShellScript(ctx, logger, script); err != nil {
		return err
	}
	return os.Remove(zipPath)
}

func openUndetekPlusLink(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	url := "https://undetek.com/my-account/downloads/"
	return runProcess(ctx, env, logger, "cmd", "/c", "start", "", url)
}

func runMASActivation(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	// Démarrer MAS via PowerShell en élevant les privilèges ou simplement dans une nouvelle fenêtre console invisible pour la commande hôte, mais laissant MAS s'afficher si nécessaire
	script := "Start-Process powershell -ArgumentList \"-NoProfile -ExecutionPolicy Bypass -Command `\"irm https://get.activated.win | iex`\"\" -WindowStyle Hidden -Verb RunAs"
	return runWindowsPowerShellScript(ctx, logger, script)
}

func runPrivacyTweaks(ctx context.Context, env Environment, logger *Logger) error {
	return runShellCommands(ctx, env, logger, []string{
		`reg add "HKLM\SOFTWARE\Policies\Microsoft\Windows\DataCollection" /v AllowTelemetry /t REG_DWORD /d 1 /f`,
		`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\AdvertisingInfo" /v Enabled /t REG_DWORD /d 0 /f`,
		`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Privacy" /v TailoredExperiencesWithDiagnosticDataEnabled /t REG_DWORD /d 0 /f`,
	}, nil)
}

func runPerformanceTweaks(ctx context.Context, env Environment, logger *Logger) error {
	return runShellCommands(ctx, env, logger, []string{
		`powercfg /setactive SCHEME_MAX`,
		`reg add "HKCU\Control Panel\Desktop" /v MenuShowDelay /t REG_SZ /d 0 /f`,
	}, nil)
}

func runUXTweaks(ctx context.Context, env Environment, logger *Logger) error {
	return runShellCommands(ctx, env, logger, []string{
		`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v HideFileExt /t REG_DWORD /d 0 /f`,
		`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 1 /f`,
	}, nil)
}

func runGamingTweaks(ctx context.Context, env Environment, logger *Logger) error {
	return runShellCommands(ctx, env, logger, []string{
		`reg add "HKCU\Software\Microsoft\GameBar" /v AutoGameModeEnabled /t REG_DWORD /d 1 /f`,
		`reg add "HKCU\Software\Microsoft\GameBar" /v AllowAutoGameMode /t REG_DWORD /d 1 /f`,
	}, nil)
}

func runSecurityTweaks(ctx context.Context, env Environment, logger *Logger) error {
	return runShellCommands(ctx, env, logger, []string{
		`Set-MpPreference -PUAProtection Enabled`,
		`reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer" /v SmartScreenEnabled /t REG_SZ /d Off /f`,
		`reg add "HKLM\SOFTWARE\Policies\Microsoft\Windows\System" /v EnableSmartScreen /t REG_DWORD /d 0 /f`,
	}, nil)
}

func enableFeatureWSL(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	return runProcess(ctx, env, logger, "wsl", "--install", "-d", "Debian")
}

func unzipSingle(zipPath, containedPath, target string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf(`Add-Type -AssemblyName System.IO.Compression.FileSystem; $zip=[System.IO.Compression.ZipFile]::OpenRead('%s'); $entry=$zip.Entries | Where-Object { $_.FullName -eq '%s' }; if (-not $entry) { throw 'entry not found' }; [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, '%s', $true); $zip.Dispose()`, zipPath, containedPath, target))
	return cmd.Run()
}

func runSelfUpdate(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	manifestURL := strings.TrimRight(baseURL, "/") + "/releases/latest.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("self-update manifest returned %s", resp.Status)
	}
	var manifest manifestResponse
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return err
	}
	artifactKey := env.OS + "-" + env.Arch
	url := manifest.Artifacts[artifactKey]
	if url == "" {
		return fmt.Errorf("manifest does not contain artifact %s", artifactKey)
	}
	current := currentBinaryPath()
	if current == "" {
		return errors.New("could not determine current executable path")
	}
	temp := current + ".new"
	catalogPath := filepath.Join(filepath.Dir(current), "catalog", "catalog.yaml")
	if err := downloadFile(ctx, url, temp); err != nil {
		return err
	}
	if err := downloadFile(ctx, strings.TrimRight(baseURL, "/")+"/releases/catalog/catalog.yaml", catalogPath); err != nil {
		return err
	}
	if sum := manifest.Sha256[artifactKey]; sum != "" {
		got, err := sha256File(temp)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, sum) {
			return fmt.Errorf("sha256 mismatch for downloaded artifact")
		}
	}
	if runtime.GOOS == "windows" {
		fmt.Printf("Downloaded updated binary to %s. Replace the running Initra binary after exit.\n", temp)
		return nil
	}
	if err := os.Rename(temp, current); err != nil {
		return err
	}
	fmt.Printf("Updated %s to %s\n", current, manifest.Version)
	return nil
}
