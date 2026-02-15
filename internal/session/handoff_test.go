package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadHandoffNotExist(t *testing.T) {
	dir := t.TempDir()
	h, err := ReadHandoff(dir)
	if err != nil {
		t.Fatalf("ReadHandoff: %v", err)
	}
	if h != nil {
		t.Fatalf("expected nil for non-existent handoff, got %+v", h)
	}
}

func TestReadHandoffValid(t *testing.T) {
	dir := t.TempDir()
	h := Handoff{
		RecommendedTier:  2,
		ServicesAffected: []string{"caddy", "postgres"},
		CheckResults: []CheckResult{
			{Service: "caddy", CheckType: "http", Status: "down", Error: "connection refused"},
		},
		InvestigationFindings: "caddy container exited",
	}
	data, _ := json.Marshal(h)
	if err := os.WriteFile(filepath.Join(dir, handoffFileName), data, 0644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	got, err := ReadHandoff(dir)
	if err != nil {
		t.Fatalf("ReadHandoff: %v", err)
	}
	if got == nil {
		t.Fatal("expected handoff, got nil")
	}
	if got.RecommendedTier != 2 {
		t.Errorf("expected tier 2, got %d", got.RecommendedTier)
	}
	if len(got.ServicesAffected) != 2 {
		t.Errorf("expected 2 services, got %d", len(got.ServicesAffected))
	}
	if len(got.CheckResults) != 1 {
		t.Errorf("expected 1 check result, got %d", len(got.CheckResults))
	}
}

func TestReadHandoffInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, handoffFileName), []byte("{bad json"), 0644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	_, err := ReadHandoff(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDeleteHandoff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, handoffFileName)

	// Delete non-existent file should not error.
	if err := DeleteHandoff(dir); err != nil {
		t.Fatalf("DeleteHandoff (no file): %v", err)
	}

	// Create and delete.
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := DeleteHandoff(dir); err != nil {
		t.Fatalf("DeleteHandoff: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("handoff file should be deleted")
	}
}

func TestValidateHandoff(t *testing.T) {
	tests := []struct {
		name    string
		h       Handoff
		maxTier int
		wantErr bool
	}{
		{
			name:    "valid tier 2",
			h:       Handoff{RecommendedTier: 2, ServicesAffected: []string{"caddy"}},
			maxTier: 3,
		},
		{
			name:    "valid tier 3",
			h:       Handoff{RecommendedTier: 3, ServicesAffected: []string{"postgres"}},
			maxTier: 3,
		},
		{
			name:    "tier too low",
			h:       Handoff{RecommendedTier: 1, ServicesAffected: []string{"caddy"}},
			maxTier: 3,
			wantErr: true,
		},
		{
			name:    "tier too high",
			h:       Handoff{RecommendedTier: 4, ServicesAffected: []string{"caddy"}},
			maxTier: 3,
			wantErr: true,
		},
		{
			name:    "exceeds max tier",
			h:       Handoff{RecommendedTier: 3, ServicesAffected: []string{"caddy"}},
			maxTier: 2,
			wantErr: true,
		},
		{
			name:    "no services",
			h:       Handoff{RecommendedTier: 2, ServicesAffected: []string{}},
			maxTier: 3,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHandoff(&tt.h, tt.maxTier)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHandoff() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
