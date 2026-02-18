package gitprovider

import "testing"

func TestGenerateBranchName(t *testing.T) {
	tests := []struct {
		changeType string
		title      string
		want       string
	}{
		{"check", "Add health check for jellyfin", "claude-ops/check/add-health-check-for-jellyfin"},
		{"", "Fix broken DNS", "claude-ops/fix/fix-broken-dns"},
		{"fix", "Hello!!!  World??", "claude-ops/fix/hello-world"},
		{"playbook", "A very long title that should be truncated because it exceeds fifty characters limit", "claude-ops/playbook/a-very-long-title-that-should-be-truncated-because"},
		{"check", "  leading and trailing  ", "claude-ops/check/leading-and-trailing"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := GenerateBranchName(tt.changeType, tt.title)
			if got != tt.want {
				t.Errorf("GenerateBranchName(%q, %q) = %q, want %q", tt.changeType, tt.title, got, tt.want)
			}
		})
	}
}
