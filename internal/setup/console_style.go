package setup

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

type consoleStyler struct {
	enabled bool
}

var termUI = newConsoleStyler()

func newConsoleStyler() consoleStyler {
	enabled := os.Getenv("NO_COLOR") == ""
	if enabled && runtime.GOOS == "windows" {
		enabled = enableWindowsVirtualTerminal()
	}
	return consoleStyler{enabled: enabled}
}

func (c consoleStyler) paint(style, text string) string {
	if !c.enabled || strings.TrimSpace(text) == "" {
		return text
	}
	return style + text + "\x1b[0m"
}

func (c consoleStyler) bold(text string) string    { return c.paint("\x1b[1m", text) }
func (c consoleStyler) dim(text string) string     { return c.paint("\x1b[2m", text) }
func (c consoleStyler) cyan(text string) string    { return c.paint("\x1b[36m", text) }
func (c consoleStyler) blue(text string) string    { return c.paint("\x1b[94m", text) }
func (c consoleStyler) green(text string) string   { return c.paint("\x1b[32m", text) }
func (c consoleStyler) yellow(text string) string  { return c.paint("\x1b[33m", text) }
func (c consoleStyler) red(text string) string     { return c.paint("\x1b[31m", text) }
func (c consoleStyler) magenta(text string) string { return c.paint("\x1b[35m", text) }

func printAppBanner(env Environment, version string) {
	art := []string{
		"  ___ _   _ ___ _____ ____      _",
		" |_ _| \\ | |_ _|_   _|  _ \\    / \\",
		"  | ||  \\| || |  | | | |_) |  / _ \\",
		"  | || |\\  || |  | | |  _ <  / ___ \\",
		" |___|_| \\_|___| |_| |_| \\_\\/_/   \\_\\",
	}
	fmt.Println()
	for _, line := range art {
		fmt.Println(termUI.cyan(termUI.bold(line)))
	}
	subtitle := "Workstation bootstrapper"
	if version != "" && version != "dev" {
		subtitle += "  |  version " + version
	}
	fmt.Println(termUI.dim(subtitle))
	target := fmt.Sprintf("%s/%s", env.OS, env.Arch)
	if env.OS == "windows" && env.Windows.ProductName != "" {
		target += "  |  " + env.Windows.ProductName
	} else if env.DistroName != "" {
		target += "  |  " + env.DistroName
	}
	fmt.Println(termUI.dim(target))
	fmt.Println()
}

func printSection(title string) {
	fmt.Println(termUI.blue(termUI.bold(title)))
	fmt.Println(termUI.dim(strings.Repeat("-", len(title))))
}

func formatCategoryTitle(title string) string {
	return termUI.bold(termUI.cyan("[" + title + "]"))
}

func formatStatusLabel(label string) string {
	switch label {
	case selectionPresetSelected, selectionManualYes, stepActionInstall, stepOutcomeInstalled:
		return termUI.green(label)
	case selectionAutoApply, stepActionUpgrade, stepOutcomeUpdated:
		return termUI.cyan(label)
	case selectionManualNo, stepActionSkip, stepOutcomeSkipped:
		return termUI.dim(label)
	case stepActionAlreadyUpToDate, stepActionAlreadyPresent:
		return termUI.yellow(label)
	case stepOutcomeFailed:
		return termUI.red(label)
	default:
		return label
	}
}

func formatPlanStatus(step ResolvedStep, raw string) string {
	if step.SkipReason != "" {
		return termUI.dim(raw)
	}
	switch step.PlannedAction {
	case stepActionInstall:
		return termUI.green(raw)
	case stepActionUpgrade:
		return termUI.cyan(raw)
	case stepActionAlreadyPresent, stepActionAlreadyUpToDate:
		return termUI.yellow(raw)
	default:
		return raw
	}
}

func formatPrompt(prompt string) string {
	return termUI.bold(prompt)
}

func formatBadge(badge string) string {
	if strings.Contains(strings.ToLower(badge), "admin") {
		return termUI.magenta(badge)
	}
	if strings.Contains(strings.ToLower(badge), "auto") || strings.Contains(strings.ToLower(badge), "system") {
		return termUI.cyan(badge)
	}
	return termUI.dim(badge)
}

func colorizeBullet(text string) string {
	return termUI.dim(text)
}
