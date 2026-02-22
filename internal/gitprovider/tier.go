package gitprovider

import "fmt"

// ValidateTier checks that the given permission tier is allowed to create a PR
// with the specified file changes. Tier 1 agents cannot create PRs. Tier 2
// agents are limited to 3 files per PR. Tier 3 agents have no file limit.
// Governing: SPEC-0018 REQ-9 "Permission Tier Integration" — enforce tier gate at provider interface level.
func ValidateTier(tier int, files []FileChange) error {
	if tier < 2 {
		return fmt.Errorf("tier %d agents cannot create PRs; minimum tier is 2", tier)
	}
	if tier == 2 && len(files) > 3 {
		return fmt.Errorf("tier 2 agents may modify at most 3 files per PR (got %d); consider escalating to tier 3", len(files))
	}
	return nil
}
