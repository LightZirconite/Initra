package setup

import (
	"context"
	"fmt"
)

type preflightCheck struct {
	Name    string
	Status  string
	Details string
}

func runPreflightChecks(ctx context.Context, env Environment, plan Plan, logger *Logger, baseURL string) []preflightCheck {
	checks := []preflightCheck{}
	manifestURL := defaultBaseURL(baseURL) + "/releases/latest.json"
	if checkHTTPReachable(ctx, manifestURL) {
		checks = append(checks, preflightCheck{Name: "Network", Status: "ok", Details: "Release endpoint reachable."})
	} else {
		checks = append(checks, preflightCheck{Name: "Network", Status: "warn", Details: "Release endpoint is not reachable right now. Initra will wait before execution."})
	}

	needsPrivilege := false
	for _, step := range plan.Steps {
		if step.Item.RequiresAdmin && stepShouldRun(step) {
			needsPrivilege = true
			break
		}
	}
	switch {
	case env.OS == "windows" && needsPrivilege && !env.IsAdmin:
		checks = append(checks, preflightCheck{Name: "Privileges", Status: "warn", Details: "Administrative steps are selected. The bootstrapper must stay elevated."})
	case env.OS == "linux" && needsPrivilege && !env.IsAdmin && !env.HasSudo:
		checks = append(checks, preflightCheck{Name: "Privileges", Status: "error", Details: "Administrative steps are selected but sudo is unavailable."})
	case needsPrivilege:
		checks = append(checks, preflightCheck{Name: "Privileges", Status: "ok", Details: "Privilege requirements are satisfied."})
	default:
		checks = append(checks, preflightCheck{Name: "Privileges", Status: "ok", Details: "No privileged step selected."})
	}

	if env.OS == "windows" {
		pending, err := windowsRebootPending(ctx, logger)
		if err != nil {
			checks = append(checks, preflightCheck{Name: "Pending reboot", Status: "warn", Details: "Could not confirm whether Windows is waiting for a reboot."})
		} else if pending {
			checks = append(checks, preflightCheck{Name: "Pending reboot", Status: "warn", Details: "Windows already reports a pending reboot before setup starts."})
		} else {
			checks = append(checks, preflightCheck{Name: "Pending reboot", Status: "ok", Details: "No pending reboot detected."})
		}
	}

	if env.OS == "windows" && selectedNeedsWinget(plan) {
		details := "WinGet is already available."
		status := "ok"
		if !env.HasWinget {
			status = "warn"
			details = "WinGet is missing and will be bootstrapped before package installs."
		}
		checks = append(checks, preflightCheck{Name: "WinGet", Status: status, Details: details})
	}

	return checks
}

func printPreflightChecks(checks []preflightCheck) {
	if len(checks) == 0 {
		return
	}
	fmt.Println()
	printSection("Preflight Checks")
	for _, check := range checks {
		fmt.Printf("  %s %s: %s\n", colorizeBullet("-"), termUI.bold(check.Name), formatPreflightStatus(check.Status, check.Details))
	}
	fmt.Println()
}

func formatPreflightStatus(status, details string) string {
	switch status {
	case "ok":
		return termUI.green(details)
	case "warn":
		return termUI.yellow(details)
	case "error":
		return termUI.red(details)
	default:
		return details
	}
}
