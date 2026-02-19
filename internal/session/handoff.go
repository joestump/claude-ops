package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Handoff carries context from one escalation tier to the next. The Tier 1
// agent writes this file when it detects issues requiring higher-tier
// intervention; the session manager reads it to decide whether (and how)
// to escalate.
type Handoff struct {
	SchemaVersion         int              `json:"schema_version"`
	RecommendedTier       int              `json:"recommended_tier"`
	ServicesAffected      []string         `json:"services_affected"`
	CheckResults          []CheckResult    `json:"check_results"`
	InvestigationFindings string           `json:"investigation_findings,omitempty"`
	RemediationAttempted  string           `json:"remediation_attempted,omitempty"`
	CooldownState         json.RawMessage  `json:"cooldown_state,omitempty"`
}

// CheckResult records the outcome of a single health check performed by
// a tier agent.
type CheckResult struct {
	Service        string `json:"service"`
	CheckType      string `json:"check_type"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	ResponseTimeMs float64 `json:"response_time_ms,omitempty"`
}

// handoffFileName is the well-known file name for the handoff payload.
const handoffFileName = "handoff.json"

// ReadHandoff reads and parses the handoff file from stateDir.
// Returns nil, nil if the file does not exist.
func ReadHandoff(stateDir string) (*Handoff, error) {
	path := filepath.Join(stateDir, handoffFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read handoff: %w", err)
	}

	var h Handoff
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse handoff: %w", err)
	}
	return &h, nil
}

// DeleteHandoff removes the handoff file from stateDir.
// Returns nil if the file does not exist.
func DeleteHandoff(stateDir string) error {
	path := filepath.Join(stateDir, handoffFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete handoff: %w", err)
	}
	return nil
}

// ValidateHandoff checks that the handoff payload is well-formed and that the
// recommended tier does not exceed maxTier.
func ValidateHandoff(h *Handoff, maxTier int) error {
	if h.SchemaVersion != 1 {
		return fmt.Errorf("unrecognized schema_version %d, expected 1", h.SchemaVersion)
	}
	if h.RecommendedTier < 2 || h.RecommendedTier > 3 {
		return fmt.Errorf("recommended_tier must be 2 or 3, got %d", h.RecommendedTier)
	}
	if h.RecommendedTier > maxTier {
		return fmt.Errorf("recommended_tier %d exceeds max_tier %d", h.RecommendedTier, maxTier)
	}
	if len(h.ServicesAffected) == 0 {
		return fmt.Errorf("services_affected must not be empty")
	}
	return nil
}
