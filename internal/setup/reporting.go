package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultDiscordErrorWebhookURL = "https://discord.com/api/webhooks/1504175665388195891/wJq_-OwYnTV1lnpOIRIDEKOdZPo8oD1_GPH-Be4NBoTq79WgWquDpW9XY0uTLR2KjU9O"
	maxDiscordUploadBytes         = 8 * 1024 * 1024
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
	fmt.Println("Technician override: hold Ctrl+Alt+F12 for 5 seconds to release kiosk mode.")
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

func notifySessionError(ctx context.Context, env Environment, report SessionReport, logger *Logger) {
	webhookURL := strings.TrimSpace(os.Getenv("INITRA_ERROR_WEBHOOK_URL"))
	if webhookURL == "" {
		webhookURL = strings.TrimSpace(os.Getenv("SETUPCTL_ERROR_WEBHOOK_URL"))
	}
	if webhookURL == "" {
		webhookURL = defaultDiscordErrorWebhookURL
	}
	if err := sendDiscordErrorWebhook(ctx, webhookURL, env, report); err != nil && logger != nil {
		logger.Println("discord error webhook failed", err)
	}
}

func sendDiscordErrorWebhook(ctx context.Context, webhookURL string, env Environment, report SessionReport) error {
	if strings.TrimSpace(webhookURL) == "" {
		return nil
	}
	failed := lastFailedStep(report)
	title := "Initra setup error"
	if report.Status == "completed_with_warnings" {
		title = "Initra setup warning"
	}
	description := strings.TrimSpace(report.Error)
	if description == "" && failed.Error != "" {
		description = failed.Error
	}
	if description == "" {
		description = "A setup step failed."
	}
	description = sanitizeWebhookText(description, 900)

	fields := []discordWebhookField{
		{Name: "Status", Value: nonEmpty(report.Status, "unknown"), Inline: true},
		{Name: "Machine", Value: sanitizeWebhookText(machineLabel(env), 256), Inline: true},
		{Name: "OS", Value: sanitizeWebhookText(osLabel(env), 256), Inline: true},
	}
	if failed.ItemName != "" {
		fields = append(fields, discordWebhookField{Name: "Step", Value: sanitizeWebhookText(failed.ItemName+" ("+failed.ItemID+")", 256), Inline: false})
	}
	if report.ReportPath != "" {
		fields = append(fields, discordWebhookField{Name: "Report path", Value: sanitizeWebhookText(report.ReportPath, 256), Inline: false})
	}
	if report.LogPath != "" {
		fields = append(fields, discordWebhookField{Name: "Log path", Value: sanitizeWebhookText(report.LogPath, 256), Inline: false})
	}
	files := discordWebhookFiles(report)
	if len(files) > 0 {
		fields = append(fields, discordWebhookField{Name: "Attached files", Value: sanitizeWebhookText(discordAttachmentSummary(files), 512), Inline: false})
	}

	payload := discordWebhookPayload{
		Username: "Initra",
		Content:  "Initra a detecte une erreur pendant une installation. Rapport et logs joints quand disponibles.",
		Embeds: []discordWebhookEmbed{
			{
				Title:       title,
				Description: description,
				Color:       discordColorForReport(report),
				Fields:      fields,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reqBody := bytes.NewReader(body)
	contentType := "application/json"
	if len(files) > 0 {
		var multipartBody bytes.Buffer
		writer := multipart.NewWriter(&multipartBody)
		if err := writer.WriteField("payload_json", string(body)); err != nil {
			return err
		}
		for idx, file := range files {
			part, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", idx), file.Name)
			if err != nil {
				return err
			}
			if _, err := part.Write(file.Data); err != nil {
				return err
			}
		}
		if err := writer.Close(); err != nil {
			return err
		}
		reqBody = bytes.NewReader(multipartBody.Bytes())
		contentType = writer.FormDataContentType()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned %s", resp.Status)
	}
	return nil
}

type discordWebhookFile struct {
	Name string
	Data []byte
}

type discordWebhookPayload struct {
	Username string                `json:"username,omitempty"`
	Content  string                `json:"content,omitempty"`
	Embeds   []discordWebhookEmbed `json:"embeds,omitempty"`
}

type discordWebhookEmbed struct {
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description,omitempty"`
	Color       int                   `json:"color,omitempty"`
	Fields      []discordWebhookField `json:"fields,omitempty"`
	Timestamp   string                `json:"timestamp,omitempty"`
}

type discordWebhookField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

func lastFailedStep(report SessionReport) StepResult {
	for idx := len(report.StepResults) - 1; idx >= 0; idx-- {
		if report.StepResults[idx].Outcome == stepOutcomeFailed {
			return report.StepResults[idx]
		}
	}
	return StepResult{}
}

func discordColorForReport(report SessionReport) int {
	if report.Status == "completed_with_warnings" {
		return 0xf59e0b
	}
	return 0xef4444
}

func discordWebhookFiles(report SessionReport) []discordWebhookFile {
	files := []discordWebhookFile{}
	if file, ok := readDiscordAttachment(report.ReportPath, "initra-report.json"); ok {
		files = append(files, file)
	}
	if file, ok := readDiscordAttachment(report.LogPath, "initra.log"); ok {
		files = append(files, file)
	}
	return files
}

func readDiscordAttachment(path, fallbackName string) (discordWebhookFile, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return discordWebhookFile{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return discordWebhookFile{}, false
	}
	name := filepath.Base(path)
	if strings.TrimSpace(name) == "" || name == "." || name == string(filepath.Separator) {
		name = fallbackName
	}
	if len(data) <= maxDiscordUploadBytes {
		return discordWebhookFile{Name: name, Data: data}, true
	}
	tail := data[len(data)-maxDiscordUploadBytes:]
	header := []byte(fmt.Sprintf("Initra log was %d bytes; Discord upload limit kept the last %d bytes.\n\n", len(data), len(tail)))
	return discordWebhookFile{Name: "tail-" + name, Data: append(header, tail...)}, true
}

func discordAttachmentSummary(files []discordWebhookFile) string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, fmt.Sprintf("%s (%d bytes)", file.Name, len(file.Data)))
	}
	return strings.Join(names, ", ")
}

func machineLabel(env Environment) string {
	parts := []string{}
	if env.Hostname != "" {
		parts = append(parts, env.Hostname)
	}
	if env.UserName != "" {
		parts = append(parts, env.UserName)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " / ")
}

func osLabel(env Environment) string {
	if env.OS == "windows" {
		label := strings.TrimSpace(strings.Join([]string{env.Windows.ProductName, env.Windows.DisplayVer}, " "))
		if label != "" {
			return label
		}
	}
	if env.DistroName != "" {
		return env.DistroName
	}
	return strings.TrimSpace(env.OS + "/" + env.Arch)
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func sanitizeWebhookText(value string, limit int) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\x00", "")
	for _, marker := range []string{"discord.com/api/webhooks/", "discordapp.com/api/webhooks/"} {
		lower := strings.ToLower(value)
		for {
			idx := strings.Index(lower, marker)
			if idx < 0 {
				break
			}
			end := idx
			for end < len(value) && !strings.ContainsRune(" \r\n\t\"'<>`", rune(value[end])) {
				end++
			}
			value = value[:idx] + "[discord-webhook-redacted]" + value[end:]
			lower = strings.ToLower(value)
		}
	}
	if limit > 0 && len(value) > limit {
		return value[:limit-3] + "..."
	}
	if value == "" {
		return "unknown"
	}
	return value
}
