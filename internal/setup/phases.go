package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	phaseApplications = "applications"
	phaseMaintenance  = "maintenance"
	phasePostUpdate   = "post-update"
	phaseFirstRun     = "first-run"
)

func phaseForItem(item Item) string {
	switch item.ID {
	case "windows-update", "driver-refresh", "windows-inbox-apps", "initra-agent", "consumer-cleanup":
		return phaseMaintenance
	case "first-run-apps":
		return phaseFirstRun
	case "auto-refresh-rate",
		"theme-dark",
		"sleep-policy",
		"firefox-default",
		"time-sync-paris",
		"dualboot-utc",
		"emoji-font-pack",
		"wallpaper",
		"firefox-policies",
		"windows-default-apps",
		"windows-taskbar-cleanup",
		"windows-startup-cleanup":
		return phasePostUpdate
	default:
		return phaseApplications
	}
}

func phaseWeight(name string) int {
	switch name {
	case phaseMaintenance:
		return 0
	case phaseApplications:
		return 1
	case phasePostUpdate:
		return 2
	case phaseFirstRun:
		return 3
	default:
		return 99
	}
}

func phaseDisplayName(name string) string {
	switch name {
	case phaseMaintenance:
		return "System Preparation & Updates"
	case phaseApplications:
		return "Applications"
	case phasePostUpdate:
		return "Post-Update Personalization"
	case phaseFirstRun:
		return "Application First Runs"
	default:
		return strings.Title(name)
	}
}

func sortPlanByPhase(plan *Plan) {
	sort.SliceStable(plan.Steps, func(i, j int) bool {
		return phaseWeight(plan.Steps[i].Phase) < phaseWeight(plan.Steps[j].Phase)
	})
}

func stepStateKey(step ResolvedStep) string {
	if step.Item.ID == "" {
		return step.Method.Action
	}
	return step.Item.ID
}

func phaseNeedsRestore(step ResolvedStep) bool {
	if step.Item.RequiresAdmin && (strings.HasPrefix(step.Item.ID, "tweak-") || strings.HasPrefix(step.Item.ID, "feature-") || step.Item.ID == "defender-exclude") {
		return true
	}
	return step.Phase == phaseMaintenance || step.Phase == phasePostUpdate
}

func isMaintenanceLoopStep(step ResolvedStep) bool {
	return step.Item.ID == "windows-update" || step.Item.ID == "driver-refresh"
}

func waitForNetwork(ctx context.Context, logger *Logger, baseURL string) error {
	targets := []string{
		strings.TrimRight(defaultBaseURL(baseURL), "/") + "/releases/latest.json",
		"https://www.msftconnecttest.com/connecttest.txt",
	}

	for attempt := 1; attempt <= 24; attempt++ {
		for _, target := range targets {
			ok := checkHTTPReachable(ctx, target)
			logger.Println("network-check", target, ok)
			if ok {
				if attempt > 1 {
					fmt.Println("Network connectivity is available again. Resuming.")
				}
				return nil
			}
		}
		if attempt == 1 {
			fmt.Println("Waiting for network connectivity before continuing...")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return errors.New("network did not become reachable in time")
}

func checkHTTPReachable(ctx context.Context, target string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		return resp.StatusCode < 500
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err = client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func nextPendingPhase(steps []ResolvedStep, start int) string {
	for idx := start; idx < len(steps); idx++ {
		step := steps[idx]
		if step.AlreadyPresent || step.SkipReason != "" {
			continue
		}
		return step.Phase
	}
	return ""
}
