package gitprovider

import (
	"fmt"
	"path"
	"strings"
)

// allowedPatterns lists the file path patterns the agent may propose changes to.
var allowedPatterns = []string{
	"checks/*.md",
	"playbooks/*.md",
	".claude-ops/checks/*.md",
	".claude-ops/playbooks/*.md",
	".claude-ops/skills/*.md",
	"CLAUDE-OPS.md",
}

// deniedPatterns lists the file path patterns the agent must never modify.
var deniedPatterns = []string{
	"prompts/*.md",
	"CLAUDE.md",
	"entrypoint.sh",
	"*.yaml",
	"*.yml",
	"Dockerfile*",
	"*.go",
	"*.env*",
}

// ValidateScope checks that every file in the changeset is within the allowed
// scope for agent-proposed changes. It returns an error describing the first
// violation found.
func ValidateScope(files []FileChange) error {
	for _, f := range files {
		if err := validatePath(f.Path); err != nil {
			return err
		}
	}
	return nil
}

func validatePath(p string) error {
	// Reject path traversal attempts.
	if strings.Contains(p, "..") {
		return fmt.Errorf("path %q contains path traversal", p)
	}

	// Check denied patterns first -- explicit deny takes precedence.
	for _, pattern := range deniedPatterns {
		if matched, _ := path.Match(pattern, p); matched {
			return fmt.Errorf("path %q matches denied pattern %q", p, pattern)
		}
		// Also match against the basename for patterns like "Dockerfile*".
		if matched, _ := path.Match(pattern, path.Base(p)); matched {
			return fmt.Errorf("path %q matches denied pattern %q", p, pattern)
		}
	}

	// Check allowed patterns.
	for _, pattern := range allowedPatterns {
		if matched, _ := path.Match(pattern, p); matched {
			return nil
		}
	}

	return fmt.Errorf("path %q is not in the allowed scope for agent changes", p)
}
