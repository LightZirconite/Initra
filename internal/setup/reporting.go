package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type sessionCounters struct {
	Installed       int
	Updated         int
	AlreadyUpToDate int
	Skipped         int
	Failed          int
}

func newSessionReport(plan Plan, paths Paths, logger *Logger, startedAt time.Time) (SessionReport, string) {
	reportPath := filepath.Join(paths.BaseDir, "reports", startedAt.Format("20060102-150405")+".json")
	report := SessionReport{
		Version:    1,
		Status:     "running",
		StartedAt:  startedAt,
		LogPath:    logger.Path(),
		ReportPath: reportPath,
		Profile:    redactProfileForReport(plan.Profile),
		Plan:       redactPlanForReport(plan),
		Warnings:   uniqueStrings(plan.Warnings),
	}
	return report, reportPath
}

func saveSessionReport(path string, report *SessionReport) error {
	if report == nil || path == "" {
		return nil
	}
	report.ReportPath = path
	if !report.FinishedAt.IsZero() {
		report.Duration = report.FinishedAt.Sub(report.StartedAt).Round(time.Second).String()
	}
	return saveJSON(path, report)
}

func redactPlanForReport(plan Plan) Plan {
	redacted := plan
	redacted.Profile = redactProfileForReport(plan.Profile)
	for idx := range redacted.Steps {
		redacted.Steps[idx].Inputs = redactStringMapForReport(redacted.Steps[idx].Inputs)
	}
	return redacted
}

func redactProfileForReport(profile UserProfile) UserProfile {
	redacted := profile.clone()
	redacted.Inputs = redactStringMapForReport(profile.Inputs)
	return redacted
}

func redactStringMapForReport(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if isSensitiveReportKey(key) {
			redacted[key] = "[redacted]"
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func isSensitiveReportKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"password", "passwd", "token", "secret", "apikey", "api_key", "access_key", "private_key"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func recordStaticStepResult(report *SessionReport, step ResolvedStep) {
	if report == nil {
		return
	}
	outcome := stepOutcomeSkipped
	switch step.PlannedAction {
	case stepActionAlreadyPresent, stepActionAlreadyUpToDate:
		outcome = stepOutcomeAlreadyUpToDate
	case stepActionSkip:
		outcome = stepOutcomeSkipped
	}
	now := time.Now()
	report.StepResults = append(report.StepResults, StepResult{
		ItemID:         step.Item.ID,
		ItemName:       step.Item.Name,
		Phase:          step.Phase,
		SelectionState: step.SelectionState,
		PlannedAction:  step.PlannedAction,
		Outcome:        outcome,
		StartedAt:      now,
		FinishedAt:     now,
	})
}

func recordExecutedStepResult(report *SessionReport, step ResolvedStep, startedAt time.Time, err error) {
	if report == nil {
		return
	}
	outcome := stepOutcomeInstalled
	switch step.PlannedAction {
	case stepActionUpgrade:
		outcome = stepOutcomeUpdated
	case stepActionAlreadyPresent, stepActionAlreadyUpToDate:
		outcome = stepOutcomeAlreadyUpToDate
	case stepActionSkip:
		outcome = stepOutcomeSkipped
	}
	result := StepResult{
		ItemID:         step.Item.ID,
		ItemName:       step.Item.Name,
		Phase:          step.Phase,
		SelectionState: step.SelectionState,
		PlannedAction:  step.PlannedAction,
		Outcome:        outcome,
		StartedAt:      startedAt,
		FinishedAt:     time.Now(),
	}
	if err != nil {
		result.Outcome = stepOutcomeFailed
		result.Error = err.Error()
	}
	report.StepResults = append(report.StepResults, result)
}

func summarizeSessionReport(report SessionReport) sessionCounters {
	counters := sessionCounters{}
	for _, step := range report.StepResults {
		switch step.Outcome {
		case stepOutcomeInstalled:
			counters.Installed++
		case stepOutcomeUpdated:
			counters.Updated++
		case stepOutcomeAlreadyUpToDate:
			counters.AlreadyUpToDate++
		case stepOutcomeFailed:
			counters.Failed++
		default:
			counters.Skipped++
		}
	}
	return counters
}

func printKioskInstallScreen(env Environment, resumed bool) {
	fmt.Print("\x1b[2J\x1b[H")
	title := "Initra setup is running"
	if resumed {
		title = "Initra setup has resumed"
	}
	fmt.Println(termUI.blue(termUI.bold(strings.Repeat("=", len(title)+8))))
	fmt.Println(termUI.blue(termUI.bold("=== " + title + " ===")))
	fmt.Println(termUI.blue(termUI.bold(strings.Repeat("=", len(title)+8))))
	fmt.Println()
	fmt.Println(termUI.yellow(termUI.bold("Configuration is in progress. Do not turn off or restart this computer.")))
	fmt.Println("The workstation can become temporarily unresponsive while system updates, drivers, and applications are installed.")
	fmt.Println("This screen will stay locked until setup finishes or a managed reboot is required.")
	fmt.Println("Technician override: hold Esc for 5 seconds to release kiosk mode.")
	fmt.Println()
	fmt.Printf("%s %s/%s\n", termUI.dim("Target:"), env.OS, env.Arch)
	if env.OS == "windows" && env.Windows.ProductName != "" {
		fmt.Printf("%s %s\n", termUI.dim("Windows:"), env.Windows.ProductName)
	} else if env.DistroName != "" {
		fmt.Printf("%s %s\n", termUI.dim("Distro:"), env.DistroName)
	}
	fmt.Println()
}

func printFinalSessionScreen(report SessionReport, interactive bool) {
	if interactive {
		setHostedSessionFinalInputMode(true)
		defer setHostedSessionFinalInputMode(false)
		fmt.Print("\x1b[2J\x1b[H")
	}
	counters := summarizeSessionReport(report)
	title := "Initra session finished"
	if report.Status == "error" {
		title = "Initra session failed"
		if interactive {
			setHostedSessionTopmostEnabled(false)
			setHostedSessionFinalInputMode(false)
		}
	} else if report.Status == "partial" {
		title = "Initra session finished with pending work"
	} else if report.Status == "completed_with_warnings" {
		title = "Initra session finished with warnings"
	}
	line := strings.Repeat("=", len(title)+8)
	fmt.Println(termUI.blue(line))
	fmt.Printf("%s\n", termUI.blue(termUI.bold("=== "+title+" ===")))
	fmt.Println(termUI.blue(line))
	fmt.Printf("%s %s\n", termUI.dim("Status:"), formatFinalStatus(report.Status))
	fmt.Printf("%s %d | %s %d | %s %d | %s %d | %s %d\n",
		termUI.green("Installed:"), counters.Installed,
		termUI.cyan("Updated:"), counters.Updated,
		termUI.yellow("Already up to date:"), counters.AlreadyUpToDate,
		termUI.dim("Skipped:"), counters.Skipped,
		termUI.red("Failed:"), counters.Failed,
	)
	if report.LogPath != "" {
		fmt.Printf("%s %s\n", termUI.dim("Log:"), report.LogPath)
	}
	if report.ReportPath != "" {
		fmt.Printf("%s %s\n", termUI.dim("Report:"), report.ReportPath)
	}
	if report.Error != "" {
		fmt.Printf("%s %s\n", termUI.red("Error:"), report.Error)
	}
	if len(report.Warnings) > 0 {
		fmt.Println(termUI.yellow(termUI.bold("Important notes:")))
		for _, warning := range report.Warnings {
			fmt.Printf("  %s %s\n", colorizeBullet("-"), warning)
		}
	}
	if interactive {
		fmt.Println()
		fmt.Println(termUI.dim("Press Enter to close this summary."))
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func formatFinalStatus(status string) string {
	switch status {
	case "success":
		return termUI.green(status)
	case "partial":
		return termUI.yellow(status)
	case "completed_with_warnings":
		return termUI.yellow(status)
	case "error":
		return termUI.red(status)
	default:
		return status
	}
}

func finalCompletedStatus(report SessionReport) string {
	for _, result := range report.StepResults {
		if result.Outcome == stepOutcomeFailed {
			return "completed_with_warnings"
		}
	}
	return "success"
}
