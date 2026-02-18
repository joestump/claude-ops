package gitprovider

import (
	"strings"
	"testing"
)

func TestValidateTier_Tier1Rejected(t *testing.T) {
	files := []FileChange{{Path: "checks/http.md", Action: "create"}}

	for _, tier := range []int{0, 1} {
		err := ValidateTier(tier, files)
		if err == nil {
			t.Errorf("expected tier %d to be rejected, but it passed", tier)
		}
	}
}

func TestValidateTier_Tier2WithFewFiles(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		files := make([]FileChange, n)
		for i := range files {
			files[i] = FileChange{Path: "checks/test.md", Action: "create"}
		}
		if err := ValidateTier(2, files); err != nil {
			t.Errorf("expected tier 2 with %d files to pass, got error: %v", n, err)
		}
	}
}

func TestValidateTier_Tier2WithTooManyFiles(t *testing.T) {
	files := make([]FileChange, 4)
	for i := range files {
		files[i] = FileChange{Path: "checks/test.md", Action: "create"}
	}
	err := ValidateTier(2, files)
	if err == nil {
		t.Error("expected tier 2 with 4 files to be rejected, but it passed")
	}
	if err != nil && !strings.Contains(err.Error(), "tier 3") {
		t.Errorf("expected error to suggest tier 3 escalation, got: %v", err)
	}
}

func TestValidateTier_Tier3Unlimited(t *testing.T) {
	files := make([]FileChange, 20)
	for i := range files {
		files[i] = FileChange{Path: "checks/test.md", Action: "create"}
	}
	if err := ValidateTier(3, files); err != nil {
		t.Errorf("expected tier 3 with 20 files to pass, got error: %v", err)
	}
}

func TestValidateTier_Tier2EmptyFiles(t *testing.T) {
	if err := ValidateTier(2, nil); err != nil {
		t.Errorf("expected tier 2 with zero files to pass, got error: %v", err)
	}
}
