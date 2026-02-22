package gitprovider

import (
	"fmt"
	"strings"
)

// Governing: SPEC-0018 REQ-5 "Branch Naming Convention" — claude-ops/{type}/{slug} format
//
// GenerateBranchName creates a branch name from the change type and title.
// The format is claude-ops/{changeType}/{slug} where slug is the title
// lowercased, non-alphanumeric characters replaced by hyphens, consecutive
// hyphens collapsed, and truncated to 50 characters.
func GenerateBranchName(changeType, title string) string {
	if changeType == "" {
		changeType = "fix"
	}
	slug := strings.ToLower(title)
	slug = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, slug)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 50 {
		slug = slug[:50]
		slug = strings.TrimRight(slug, "-")
	}
	return fmt.Sprintf("claude-ops/%s/%s", changeType, slug)
}
