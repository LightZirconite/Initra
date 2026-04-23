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
		Profile:    plan.Profile.clone(),
		Plan:       plan,
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

func printFinalSessionScreen(report SessionReport, interactive bool) {
	if interactive {
		fmt.Print(strings.Repeat("\n", 8))
	}
	counters := summarizeSessionReport(report)
	title := "Initra session finished"
	if report.Status == "error" {
		title = "Initra session failed"
	} else if report.Status == "partial" {
		title = "Initra session finished with pending work"
	}
	line := strings.Repeat("=", len(title)+8)
	fmt.Println(line)
	fmt.Printf("=== %s ===\n", title)
	fmt.Println(line)
	fmt.Printf("Status: %s\n", report.Status)
	fmt.Printf("Installed: %d | Updated: %d | Already up to date: %d | Skipped: %d | Failed: %d\n", counters.Installed, counters.Updated, counters.AlreadyUpToDate, counters.Skipped, counters.Failed)
	if report.LogPath != "" {
		fmt.Printf("Log: %s\n", report.LogPath)
	}
	if report.ReportPath != "" {
		fmt.Printf("Report: %s\n", report.ReportPath)
	}
	if report.Error != "" {
		fmt.Printf("Error: %s\n", report.Error)
	}
	if len(report.Warnings) > 0 {
		fmt.Println("Important notes:")
		for _, warning := range report.Warnings {
			fmt.Printf("  - %s\n", warning)
		}
	}
	if interactive {
		fmt.Println()
		fmt.Println("Press Enter to close this summary.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}
