package gitprovider

import "testing"

func TestValidateScope_AllowedFiles(t *testing.T) {
	allowed := []string{
		"checks/http.md",
		"checks/new-service.md",
		"playbooks/restart-postgres.md",
		".claude-ops/checks/custom.md",
		".claude-ops/playbooks/restart-postgres.md",
		".claude-ops/skills/maintenance.md",
		"CLAUDE-OPS.md",
	}
	for _, p := range allowed {
		files := []FileChange{{Path: p, Content: "test", Action: "create"}}
		if err := ValidateScope(files); err != nil {
			t.Errorf("expected path %q to be allowed, got error: %v", p, err)
		}
	}
}

func TestValidateScope_DeniedFiles(t *testing.T) {
	denied := []string{
		"prompts/tier1-observe.md",
		"prompts/tier2-investigate.md",
		"CLAUDE.md",
		"entrypoint.sh",
		"ie.yaml",
		"docker-compose.yaml",
		"values.yml",
		"Dockerfile",
		"Dockerfile.dev",
		"main.go",
		"internal/db/db.go",
		".env",
		".env.example",
	}
	for _, p := range denied {
		files := []FileChange{{Path: p, Content: "test", Action: "update"}}
		if err := ValidateScope(files); err == nil {
			t.Errorf("expected path %q to be denied, but it was allowed", p)
		}
	}
}

func TestValidateScope_PathTraversal(t *testing.T) {
	traversal := []string{
		"../etc/passwd",
		"checks/../../secrets.md",
		"..hidden/file.md",
	}
	for _, p := range traversal {
		files := []FileChange{{Path: p, Content: "test", Action: "create"}}
		err := ValidateScope(files)
		if err == nil {
			t.Errorf("expected path %q to be rejected for traversal, but it was allowed", p)
		}
		if err != nil && !contains(err.Error(), "path traversal") {
			t.Errorf("expected path traversal error for %q, got: %v", p, err)
		}
	}
}

func TestValidateScope_UnknownPaths(t *testing.T) {
	unknown := []string{
		"README.md",
		"docs/architecture.md",
		"scripts/deploy.sh",
		"random/file.txt",
	}
	for _, p := range unknown {
		files := []FileChange{{Path: p, Content: "test", Action: "create"}}
		if err := ValidateScope(files); err == nil {
			t.Errorf("expected unknown path %q to be rejected, but it was allowed", p)
		}
	}
}

func TestValidateScope_MultipleFiles(t *testing.T) {
	files := []FileChange{
		{Path: "checks/http.md", Content: "ok", Action: "create"},
		{Path: "checks/dns.md", Content: "ok", Action: "create"},
	}
	if err := ValidateScope(files); err != nil {
		t.Errorf("expected multiple allowed files to pass, got error: %v", err)
	}
}

func TestValidateScope_MixedAllowedDenied(t *testing.T) {
	files := []FileChange{
		{Path: "checks/http.md", Content: "ok", Action: "create"},
		{Path: "CLAUDE.md", Content: "bad", Action: "update"},
	}
	if err := ValidateScope(files); err == nil {
		t.Error("expected mixed allowed/denied files to fail, but they passed")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
