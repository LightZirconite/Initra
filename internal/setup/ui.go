package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func buildProfileInteractively(catalog Catalog, env Environment, base UserProfile) (UserProfile, error) {
	reader := bufio.NewReader(os.Stdin)
	profile := base.clone()

	printSection("Interactive Setup Profile")
	fmt.Printf("%s %s\n", termUI.dim("Preset:"), termUI.bold(profile.Preset))
	fmt.Printf("%s %s/%s", termUI.dim("Target:"), env.OS, env.Arch)
	if env.OS == "windows" {
		fmt.Printf(" %s %s", termUI.dim("|"), env.Windows.ProductName)
	} else if env.DistroName != "" {
		fmt.Printf(" %s %s", termUI.dim("|"), env.DistroName)
	}
	fmt.Println()
	fmt.Println(termUI.dim("Tip: press Enter to accept, or type n to skip an item."))
	fmt.Println()

	for _, category := range catalog.Categories {
		if !categoryHasVisibleItems(catalog, category.ID, env, profile) {
			continue
		}
		fmt.Printf("%s\n", formatCategoryTitle(category.Name))
		fmt.Printf("  %s\n", termUI.dim(category.Description))
		fmt.Println()
		manualIndex := 0
		for _, item := range catalog.Items {
			if item.Category != category.ID || !itemVisibleInProfile(catalog, item, env, profile) {
				continue
			}
			badges := itemBadges(item, env)
			if item.AutoApply {
				if len(badges) > 0 {
					fmt.Printf("  - %s  %s\n", item.Name, joinStyledBadges(badges))
				} else {
					fmt.Printf("  - %s\n", item.Name)
				}
			} else if len(badges) > 0 {
				manualIndex++
				fmt.Printf("  %s. %s  %s\n", strconv.Itoa(manualIndex), item.Name, joinStyledBadges(badges))
			} else {
				manualIndex++
				fmt.Printf("  %s. %s\n", strconv.Itoa(manualIndex), item.Name)
			}
			if description := strings.TrimSpace(item.Description); description != "" {
				fmt.Printf("     %s\n", description)
			}
			fmt.Printf("     %s %s\n", termUI.dim("Selection:"), formatStatusLabel(selectionStateForItem(item, profile)))
			if len(item.Notes) > 0 {
				for _, note := range item.Notes {
					note = strings.TrimSpace(note)
					if note == "" {
						continue
					}
					fmt.Printf("     %s %s\n", termUI.yellow("Note:"), note)
				}
			}
			if item.AutoApply {
				fmt.Println("     " + termUI.cyan("Automatic: runs automatically when supported."))
				fmt.Println()
				continue
			}
			if profile.Selected[item.ID] {
				fmt.Println("     " + termUI.green("Preset: selected"))
			}
			defaultValue := defaultSelectionForItem(item, profile)
			answer, err := promptYesNo(reader, itemSelectionPrompt(item), defaultValue)
			if err != nil {
				return profile, err
			}
			profile.Selected[item.ID] = answer
			if answer {
				if profile.SelectionSource[item.ID] == selectionPresetSelected {
					profile.SelectionSource[item.ID] = selectionPresetSelected
				} else {
					profile.SelectionSource[item.ID] = selectionManualYes
				}
			} else {
				profile.SelectionSource[item.ID] = selectionManualNo
			}
			fmt.Printf("     %s %s\n", termUI.dim("Final selection:"), formatStatusLabel(selectionStateForItem(item, profile)))
			fmt.Println()
		}
		fmt.Println()
	}

	for _, item := range catalog.Items {
		if !profile.Selected[item.ID] || len(item.Inputs) == 0 {
			continue
		}
		fmt.Printf("%s\n", termUI.bold(item.Name+" settings:"))
		if description := strings.TrimSpace(item.Description); description != "" {
			fmt.Printf("  %s\n", description)
		}
		for _, input := range item.Inputs {
			defaultValue := resolveDefaultInput(input, profile, env)
			if current := strings.TrimSpace(profile.Inputs[input.ID]); current != "" {
				defaultValue = current
			}
			if description := strings.TrimSpace(input.Description); description != "" {
				fmt.Printf("  %s\n", description)
			}
			value, err := promptInput(reader, input, defaultValue)
			if err != nil {
				return profile, err
			}
			profile.Inputs[input.ID] = value
		}
		fmt.Println()
	}

	if err := maybeExportInteractiveProfile(reader, env, profile); err != nil {
		return profile, err
	}

	return profile, nil
}

func maybeExportInteractiveProfile(reader *bufio.Reader, env Environment, profile UserProfile) error {
	fmt.Printf("%s\n", termUI.bold("Setup profile:"))
	fmt.Println("  " + termUI.dim("You can save these answers and reuse them on the next run. The file may include configured tokens or passwords."))
	saveProfile, err := promptYesNo(reader, "     Save this configuration profile?", false)
	if err != nil {
		return err
	}
	if !saveProfile {
		fmt.Println()
		return nil
	}

	defaultPath := filepath.Join(env.HomeDir, "Documents", "initra-profile.json")
	if env.HomeDir == "" {
		defaultPath = "initra-profile.json"
	}
	profilePath, err := promptString(reader, "     Profile file path?", defaultPath)
	if err != nil {
		return err
	}
	profilePath = strings.TrimSpace(profilePath)
	if profilePath == "" {
		return nil
	}
	if err := saveJSON(profilePath, profile); err != nil {
		return err
	}
	fmt.Printf("     %s %s\n\n", termUI.green("Saved:"), profilePath)
	return nil
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultValue bool) (bool, error) {
	suffix := "[Enter/n]"
	for {
		fmt.Printf("%s %s ", formatPrompt(prompt), termUI.dim(suffix))
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return defaultValue, nil
		}
		switch line {
		case "y", "yes", "o", "oui":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Println("Press Enter to accept, or type n/o to answer.")
	}
}

func defaultSelectionForItem(item Item, profile UserProfile) bool {
	return true
}

func itemSelectionPrompt(item Item) string {
	if item.ID == "git-auth" {
		return "     Configure automatic Git authentication for Git/Gitea?"
	}
	return "     Install?"
}

func promptString(reader *bufio.Reader, prompt, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Printf("%s ", formatPrompt(prompt))
	} else {
		fmt.Printf("%s %s ", formatPrompt(prompt), termUI.dim("["+defaultValue+"]"))
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func promptInput(reader *bufio.Reader, input InputSpec, defaultValue string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(input.Type), "password") {
		return promptPassword(reader, input.Prompt, defaultValue)
	}
	return promptString(reader, input.Prompt, defaultValue)
}

func promptPassword(reader *bufio.Reader, prompt, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Printf("%s ", formatPrompt(prompt))
	} else {
		fmt.Printf("%s %s ", formatPrompt(prompt), termUI.dim("[configured]"))
	}
	restore, err := setStdinEcho(false)
	if err != nil {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", readErr
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultValue, nil
		}
		return line, nil
	}
	line, readErr := reader.ReadString('\n')
	restoreErr := restore()
	fmt.Println()
	if readErr != nil {
		return "", readErr
	}
	if restoreErr != nil {
		return "", restoreErr
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func confirmExecution() (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	return promptYesNo(reader, "Proceed with execution?", true)
}

func categoryHasVisibleItems(catalog Catalog, categoryID string, env Environment, profile UserProfile) bool {
	for _, item := range catalog.Items {
		if item.Category == categoryID && itemVisibleInProfile(catalog, item, env, profile) {
			return true
		}
	}
	return false
}

func itemVisibleInProfile(catalog Catalog, item Item, env Environment, profile UserProfile) bool {
	if !itemVisibleOn(item, env) {
		return false
	}
	if item.ID == "git-auth" {
		return gitAvailableForAuth(catalog, env, profile)
	}
	return profileDependencySatisfied(item, profile)
}

func gitAvailableForAuth(catalog Catalog, env Environment, profile UserProfile) bool {
	if profile.Selected["git"] {
		return true
	}
	if commandExists("git") {
		return true
	}
	gitItem, ok := catalog.itemByID("git")
	if !ok {
		return false
	}
	installed, err := detectItemInstalled(gitItem, env)
	return err == nil && installed
}

func resolveDefaultInput(input InputSpec, profile UserProfile, env Environment) string {
	value := strings.TrimSpace(input.Default)
	switch value {
	case "{{documents_dir}}/Excluded", "{{documents_dir}}\\Excluded", "{{documents_dir}}/exclude", "{{documents_dir}}\\exclude":
		if env.OS == "windows" {
			return env.DocumentsDir + `\Excluded`
		}
		return env.DocumentsDir + "/Excluded"
	case "{{system_language}}":
		if env.OS == "windows" {
			return "en-US"
		}
		return "en-US"
	case "{{mesh_default_url}}":
		return "https://mesh.lgtw.tf/meshagents?id=4&meshid=W4tZHM@Pv3686vWHJYUmulXYFna1tmZx6BZB3WATaGwMb05@ZjRaRnba@vn$uqhF&installflags=0"
	case "{{git_default_host}}":
		return defaultGitCredentialHost()
	case "{{user_name}}":
		return env.UserName
	default:
		if existing := profile.Inputs[input.ID]; existing != "" {
			return existing
		}
		return value
	}
}

func itemBadges(item Item, env Environment) []string {
	badges := make([]string, 0, 4)
	if item.RequiresAdmin {
		badges = append(badges, "[admin]")
	}
	if len(item.Platforms) == 1 {
		switch item.Platforms[0] {
		case "windows":
			badges = append(badges, "[win]")
		case "linux":
			badges = append(badges, "[linux]")
		}
	} else if len(item.Platforms) > 1 {
		badges = append(badges, "[cross]")
	}
	if hasAlternativePlatformBehavior(item, env) {
		badges = append(badges, "[alt]")
	}
	if strings.Contains(item.ID, "update") || strings.Contains(item.ID, "driver") {
		badges = append(badges, "[system]")
	}
	if item.AutoApply {
		badges = append(badges, "[auto]")
	}
	return badges
}

func joinStyledBadges(badges []string) string {
	if len(badges) == 0 {
		return ""
	}
	styled := make([]string, 0, len(badges))
	for _, badge := range badges {
		styled = append(styled, formatBadge(badge))
	}
	return strings.Join(styled, " ")
}

func hasAlternativePlatformBehavior(item Item, env Environment) bool {
	switch item.ID {
	case "onedrive", "superwhisper":
		return env.OS == "linux"
	default:
		return false
	}
}
