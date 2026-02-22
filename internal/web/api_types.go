package web

import (
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/gitprovider"
)

// Governing: SPEC-0017 REQ-2 "JSON Content Type" — all types serialize as application/json
// --- API Response Wrappers ---

// APISessionsResponse wraps a list of sessions for JSON API responses.
type APISessionsResponse struct {
	Sessions []APISession `json:"sessions"`
}

// APIEventsResponse wraps a list of events for JSON API responses.
type APIEventsResponse struct {
	Events []APIEvent `json:"events"`
}

// APIMemoriesResponse wraps a list of memories for JSON API responses.
type APIMemoriesResponse struct {
	Memories []APIMemory `json:"memories"`
}

// APICooldownsResponse wraps a list of cooldowns for JSON API responses.
type APICooldownsResponse struct {
	Cooldowns []APICooldown `json:"cooldowns"`
}

// --- API Resource Types ---

// Governing: SPEC-0017 REQ-3 "Sessions List Endpoint", REQ-4 "Session Detail Endpoint"
// APISession is the JSON representation of a session.
type APISession struct {
	ID              int64        `json:"id"`
	Tier            int          `json:"tier"`
	Model           string       `json:"model"`
	Status          string       `json:"status"`
	StartedAt       string       `json:"started_at"`
	EndedAt         *string      `json:"ended_at"`
	ExitCode        *int         `json:"exit_code"`
	CostUSD         *float64     `json:"cost_usd"`
	NumTurns        *int         `json:"num_turns"`
	DurationMs      *int64       `json:"duration_ms"`
	Trigger         string       `json:"trigger"`
	PromptText      *string      `json:"prompt_text"`
	ParentSessionID *int64       `json:"parent_session_id"`
	Response        *string      `json:"response,omitempty"`
	ParentSession   *APISession  `json:"parent_session,omitempty"`
	ChildSessions   []APISession `json:"child_sessions,omitempty"`
	ChainCost       *float64     `json:"chain_cost,omitempty"`
}

// Governing: SPEC-0017 REQ-6 "Events List Endpoint"
// APIEvent is the JSON representation of an event.
type APIEvent struct {
	ID        int64   `json:"id"`
	SessionID *int64  `json:"session_id"`
	Level     string  `json:"level"`
	Service   *string `json:"service"`
	Message   string  `json:"message"`
	CreatedAt string  `json:"created_at"`
}

// Governing: SPEC-0017 REQ-7 "Memories List Endpoint", REQ-8 "Memory Create Endpoint", REQ-9 "Memory Update Endpoint"
// APIMemory is the JSON representation of a memory.
type APIMemory struct {
	ID          int64   `json:"id"`
	Service     *string `json:"service"`
	Category    string  `json:"category"`
	Observation string  `json:"observation"`
	Confidence  float64 `json:"confidence"`
	Active      bool    `json:"active"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	SessionID   *int64  `json:"session_id"`
	Tier        int     `json:"tier"`
}

// Governing: SPEC-0017 REQ-11 "Cooldowns List Endpoint"
// APICooldown is the JSON representation of a cooldown summary.
type APICooldown struct {
	Service    string `json:"service"`
	ActionType string `json:"action_type"`
	Count      int    `json:"count"`
	LastAction string `json:"last_action"`
}

// Governing: SPEC-0017 REQ-12 "Config Get Endpoint", REQ-13 "Config Update Endpoint"
// APIConfig is the JSON representation of runtime configuration.
type APIConfig struct {
	Interval   int    `json:"interval"`
	Tier1Model string `json:"tier1_model"`
	Tier2Model string `json:"tier2_model"`
	Tier3Model string `json:"tier3_model"`
	DryRun     bool   `json:"dry_run"`
	MaxTier    int    `json:"max_tier"`
	StateDir   string `json:"state_dir"`
	ResultsDir string `json:"results_dir"`
	ReposDir   string `json:"repos_dir"`
	PREnabled  bool   `json:"pr_enabled"`
}

// --- PR API Types ---

// APICreatePRRequest is the JSON body for POST /api/v1/prs.
// Governing: SPEC-0018 REQ-9 "Permission Tier Integration" (Tier field), REQ-12 "Dry Run Mode".
type APICreatePRRequest struct {
	RepoOwner  string                   `json:"repo_owner"`
	RepoName   string                   `json:"repo_name"`
	CloneURL   string                   `json:"clone_url"`
	Tier       int                      `json:"tier"` // Governing: SPEC-0018 REQ-9 — tier carried in request for enforcement.

	Files      []gitprovider.FileChange `json:"files"`
	Title      string                   `json:"title"`
	Body       string                   `json:"body"`
	BaseBranch string                   `json:"base_branch"`
	ChangeType string                   `json:"change_type"`
}

// APICreatePRResponse is returned after creating a pull request.
// Governing: SPEC-0018 REQ-12 "Dry Run Mode" — DryRun field signals no git operations were executed.
type APICreatePRResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// APIPRSummary is a lightweight representation of an open pull request.
type APIPRSummary struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Files  []string `json:"files"`
}

// APIPRListResponse wraps a list of PR summaries.
type APIPRListResponse struct {
	PRs []APIPRSummary `json:"prs"`
}

// --- API Request Types ---

// APITriggerRequest is the JSON body for POST /api/v1/sessions/trigger.
type APITriggerRequest struct {
	Prompt string `json:"prompt"`
}

// APICreateMemoryRequest is the JSON body for POST /api/v1/memories.
type APICreateMemoryRequest struct {
	Service     *string  `json:"service"`
	Category    string   `json:"category"`
	Observation string   `json:"observation"`
	Confidence  *float64 `json:"confidence"`
}

// APIUpdateMemoryRequest is the JSON body for PUT /api/v1/memories/{id}.
type APIUpdateMemoryRequest struct {
	Observation *string  `json:"observation"`
	Confidence  *float64 `json:"confidence"`
	Active      *bool    `json:"active"`
}

// APIUpdateConfigRequest is the JSON body for PUT /api/v1/config.
type APIUpdateConfigRequest struct {
	Interval   *int    `json:"interval"`
	Tier1Model *string `json:"tier1_model"`
	Tier2Model *string `json:"tier2_model"`
	Tier3Model *string `json:"tier3_model"`
	DryRun     *bool   `json:"dry_run"`
}

// --- Conversion Functions ---

func toAPISession(s db.Session) APISession {
	return APISession{
		ID:              s.ID,
		Tier:            s.Tier,
		Model:           s.Model,
		Status:          s.Status,
		StartedAt:       s.StartedAt,
		EndedAt:         s.EndedAt,
		ExitCode:        s.ExitCode,
		CostUSD:         s.CostUSD,
		NumTurns:        s.NumTurns,
		DurationMs:      s.DurationMs,
		Trigger:         s.Trigger,
		PromptText:      s.PromptText,
		ParentSessionID: s.ParentSessionID,
	}
}

func toAPISessions(sessions []db.Session) []APISession {
	out := make([]APISession, len(sessions))
	for i, s := range sessions {
		out[i] = toAPISession(s)
	}
	return out
}

func toAPIEvent(e db.Event) APIEvent {
	return APIEvent{
		ID:        e.ID,
		SessionID: e.SessionID,
		Level:     e.Level,
		Service:   e.Service,
		Message:   e.Message,
		CreatedAt: e.CreatedAt,
	}
}

func toAPIEvents(events []db.Event) []APIEvent {
	out := make([]APIEvent, len(events))
	for i, e := range events {
		out[i] = toAPIEvent(e)
	}
	return out
}

func toAPIMemory(m db.Memory) APIMemory {
	return APIMemory{
		ID:          m.ID,
		Service:     m.Service,
		Category:    m.Category,
		Observation: m.Observation,
		Confidence:  m.Confidence,
		Active:      m.Active,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		SessionID:   m.SessionID,
		Tier:        m.Tier,
	}
}

func toAPIMemories(memories []db.Memory) []APIMemory {
	out := make([]APIMemory, len(memories))
	for i, m := range memories {
		out[i] = toAPIMemory(m)
	}
	return out
}

func toAPICooldown(c db.RecentCooldown) APICooldown {
	return APICooldown{
		Service:    c.Service,
		ActionType: c.ActionType,
		Count:      c.Count,
		LastAction: c.LastAction,
	}
}

func toAPICooldowns(cooldowns []db.RecentCooldown) []APICooldown {
	out := make([]APICooldown, len(cooldowns))
	for i, c := range cooldowns {
		out[i] = toAPICooldown(c)
	}
	return out
}
