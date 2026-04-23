package setup

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func buildProfileInteractively(catalog Catalog, env Environment, base UserProfile) (UserProfile, error) {
	reader := bufio.NewReader(os.Stdin)
	profile := base.clone()

	fmt.Println("Initra")
	fmt.Println("------")
	fmt.Println("Interactive setup profile")
	fmt.Printf("Preset: %s\n", profile.Preset)
	fmt.Printf("Target: %s/%s", env.OS, env.Arch)
	if env.OS == "windows" {
		fmt.Printf(" | %s", env.Windows.ProductName)
	} else if env.DistroName != "" {
		fmt.Printf(" | %s", env.DistroName)
	}
	fmt.Println()
	fmt.Println("Tip: press Enter to accept an item, or type n to skip it.")
	fmt.Println()

	for _, category := range catalog.Categories {
		if !categoryHasVisibleItems(catalog, category.ID, env, profile) {
			continue
		}
		fmt.Printf("[%s]\n", category.Name)
		fmt.Printf("  %s\n", category.Description)
		fmt.Println()
		manualIndex := 0
		for _, item := range catalog.Items {
			if item.Category != category.ID || !itemVisibleOn(item, env) || !profileDependencySatisfied(item, profile) {
				continue
			}
			badges := itemBadges(item, env)
			if item.AutoApply {
				if len(badges) > 0 {
					fmt.Printf("  - %s  %s\n", item.Name, strings.Join(badges, " "))
				} else {
					fmt.Printf("  - %s\n", item.Name)
				}
			} else if len(badges) > 0 {
				manualIndex++
				fmt.Printf("  %s. %s  %s\n", strconv.Itoa(manualIndex), item.Name, strings.Join(badges, " "))
			} else {
				manualIndex++
				fmt.Printf("  %s. %s\n", strconv.Itoa(manualIndex), item.Name)
			}
			if description := strings.TrimSpace(item.Description); description != "" {
				fmt.Printf("     %s\n", description)
			}
			if len(item.Notes) > 0 {
				for _, note := range item.Notes {
					note = strings.TrimSpace(note)
					if note == "" {
						continue
					}
					fmt.Printf("     Note: %s\n", note)
				}
			}
			if item.AutoApply {
				fmt.Println("     Automatic: runs automatically when supported.")
				fmt.Println()
				continue
			}
			if profile.Selected[item.ID] {
				fmt.Println("     Preset: selected")
			}
			defaultValue := true
			answer, err := promptYesNo(reader, "     Install?", defaultValue)
			if err != nil {
				return profile, err
			}
			profile.Selected[item.ID] = answer
			fmt.Println()
		}
		fmt.Println()
	}

	for _, item := range catalog.Items {
		if !profile.Selected[item.ID] || len(item.Inputs) == 0 {
			continue
		}
		fmt.Printf("%s settings:\n", item.Name)
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
			value, err := promptString(reader, input.Prompt, defaultValue)
			if err != nil {
				return profile, err
			}
			profile.Inputs[input.ID] = value
		}
		fmt.Println()
	}

	return profile, nil
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultValue bool) (bool, error) {
	suffix := "[Enter/n]"
	if defaultValue {
		suffix = "[Enter/n]"
	}
	for {
		fmt.Printf("%s %s ", prompt, suffix)
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return defaultValue, nil
		}
		switch line {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Println("Press Enter to accept, or type n to refuse.")
	}
}

func promptString(reader *bufio.Reader, prompt, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Printf("%s ", prompt)
	} else {
		fmt.Printf("%s [%s] ", prompt, defaultValue)
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

func confirmExecution() (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	return promptYesNo(reader, "Proceed with execution?", true)
}

func categoryHasVisibleItems(catalog Catalog, categoryID string, env Environment, profile UserProfile) bool {
	for _, item := range catalog.Items {
		if item.Category == categoryID && itemVisibleOn(item, env) && profileDependencySatisfied(item, profile) {
			return true
		}
	}
	return false
}

func resolveDefaultInput(input InputSpec, profile UserProfile, env Environment) string {
	value := strings.TrimSpace(input.Default)
	switch value {
	case "{{documents_dir}}/exclude", "{{documents_dir}}\\exclude":
		if env.OS == "windows" {
			return env.DocumentsDir + `\exclude`
		}
		return env.DocumentsDir + "/exclude"
	case "{{system_language}}":
		if env.OS == "windows" {
			return "en-US"
		}
		return "en-US"
	case "{{mesh_default_url}}":
		return "https://mesh.lgtw.tf/meshagents?id=4&meshid=W4tZHM@Pv3686vWHJYUmulXYFna1tmZx6BZB3WATaGwMb05@ZjRaRnba@vn$uqhF&installflags=0"
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

func hasAlternativePlatformBehavior(item Item, env Environment) bool {
	switch item.ID {
	case "onedrive", "superwhisper":
		return env.OS == "linux"
	default:
		return false
	}
}
