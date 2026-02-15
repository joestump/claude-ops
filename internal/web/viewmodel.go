package web

import (
	"time"

	"github.com/joestump/claude-ops/internal/db"
)

const timeFormat = time.RFC3339

// SessionView is a template-friendly representation of a db.Session with parsed times.
type SessionView struct {
	ID         int64
	Tier       int
	Model      string
	Status     string
	StartedAt  time.Time
	EndedAt    *time.Time
	ExitCode   *int
	LogFile    string
	Response   string
	CostUSD    *float64
	NumTurns   *int
	DurationMs *int64
	Trigger    string
	PromptText string

	// Escalation chain fields.
	ParentSessionID *int64
	ParentSession   *SessionView
	ChildSessions   []SessionView
	ChainCost       float64
	IsChainRoot     bool
	ChainLength     int
}

// HealthCheckView is a template-friendly representation of a db.HealthCheck with parsed times.
type HealthCheckView struct {
	ID             int64
	SessionID      *int64
	Service        string
	CheckType      string
	Status         string
	ResponseTimeMs *int
	ErrorDetail    string
	CheckedAt      time.Time
}

// ServiceStatus summarizes a service's current state for templates.
type ServiceStatus struct {
	Name       string
	Status     string
	LastCheck  *time.Time
	CheckCount int
}

// EventView is a template-friendly representation of a db.Event with parsed times.
type EventView struct {
	ID        int64
	SessionID *int64
	Level     string
	Service   string
	Message   string
	CreatedAt time.Time
}

// CooldownView is a template-friendly representation of a cooldown action.
type CooldownView struct {
	Service    string
	ActionType string
	Count      int
	LastAction time.Time
}

// ToSessionView converts a db.Session to a SessionView.
func ToSessionView(s db.Session) SessionView {
	v := SessionView{
		ID:       s.ID,
		Tier:     s.Tier,
		Model:    s.Model,
		Status:   s.Status,
		ExitCode: s.ExitCode,
	}
	if t, err := time.Parse(timeFormat, s.StartedAt); err == nil {
		v.StartedAt = t
	}
	if s.EndedAt != nil {
		if t, err := time.Parse(timeFormat, *s.EndedAt); err == nil {
			v.EndedAt = &t
		}
	}
	if s.LogFile != nil {
		v.LogFile = *s.LogFile
	}
	if s.Response != nil {
		v.Response = *s.Response
	}
	v.CostUSD = s.CostUSD
	v.NumTurns = s.NumTurns
	v.DurationMs = s.DurationMs
	v.Trigger = s.Trigger
	if s.PromptText != nil {
		v.PromptText = *s.PromptText
	}
	v.ParentSessionID = s.ParentSessionID
	return v
}

// ToSessionViews converts a slice of db.Session to SessionView.
func ToSessionViews(sessions []db.Session) []SessionView {
	views := make([]SessionView, len(sessions))
	for i, s := range sessions {
		views[i] = ToSessionView(s)
	}
	return views
}

// ToHealthCheckView converts a db.HealthCheck to a HealthCheckView.
func ToHealthCheckView(h db.HealthCheck) HealthCheckView {
	v := HealthCheckView{
		ID:             h.ID,
		SessionID:      h.SessionID,
		Service:        h.Service,
		CheckType:      h.CheckType,
		Status:         h.Status,
		ResponseTimeMs: h.ResponseTimeMs,
	}
	if h.ErrorDetail != nil {
		v.ErrorDetail = *h.ErrorDetail
	}
	if t, err := time.Parse(timeFormat, h.CheckedAt); err == nil {
		v.CheckedAt = t
	}
	return v
}

// ToHealthCheckViews converts a slice of db.HealthCheck to HealthCheckView.
func ToHealthCheckViews(checks []db.HealthCheck) []HealthCheckView {
	views := make([]HealthCheckView, len(checks))
	for i, h := range checks {
		views[i] = ToHealthCheckView(h)
	}
	return views
}

// ToServiceStatuses converts db.ServiceStatus records to template-friendly ServiceStatus.
func ToServiceStatuses(statuses []db.ServiceStatus) []ServiceStatus {
	views := make([]ServiceStatus, len(statuses))
	for i, s := range statuses {
		views[i] = ServiceStatus{
			Name:       s.Service,
			Status:     s.Status,
			CheckCount: s.CheckCount,
		}
		if s.LastCheck != nil {
			if t, err := time.Parse(timeFormat, *s.LastCheck); err == nil {
				views[i].LastCheck = &t
			}
		}
	}
	return views
}

// ToEventView converts a db.Event to an EventView.
func ToEventView(e db.Event) EventView {
	v := EventView{
		ID:        e.ID,
		SessionID: e.SessionID,
		Level:     e.Level,
		Message:   e.Message,
	}
	if e.Service != nil {
		v.Service = *e.Service
	}
	if t, err := time.Parse(timeFormat, e.CreatedAt); err == nil {
		v.CreatedAt = t
	}
	return v
}

// ToEventViews converts a slice of db.Event to EventView.
func ToEventViews(events []db.Event) []EventView {
	views := make([]EventView, len(events))
	for i, e := range events {
		views[i] = ToEventView(e)
	}
	return views
}

// MemoryView is a template-friendly representation of a db.Memory with parsed times.
type MemoryView struct {
	ID          int64
	Service     string
	Category    string
	Observation string
	Confidence  float64
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	SessionID   *int64
	Tier        int
}

// ToMemoryView converts a db.Memory to a MemoryView.
func ToMemoryView(m db.Memory) MemoryView {
	v := MemoryView{
		ID:          m.ID,
		Category:    m.Category,
		Observation: m.Observation,
		Confidence:  m.Confidence,
		Active:      m.Active,
		SessionID:   m.SessionID,
		Tier:        m.Tier,
	}
	if m.Service != nil {
		v.Service = *m.Service
	} else {
		v.Service = "global"
	}
	if t, err := time.Parse(timeFormat, m.CreatedAt); err == nil {
		v.CreatedAt = t
	}
	if t, err := time.Parse(timeFormat, m.UpdatedAt); err == nil {
		v.UpdatedAt = t
	}
	return v
}

// ToMemoryViews converts a slice of db.Memory to MemoryView.
func ToMemoryViews(memories []db.Memory) []MemoryView {
	views := make([]MemoryView, len(memories))
	for i, m := range memories {
		views[i] = ToMemoryView(m)
	}
	return views
}

// ToCooldownViews converts db.RecentCooldown records to template-friendly CooldownView.
func ToCooldownViews(cooldowns []db.RecentCooldown) []CooldownView {
	views := make([]CooldownView, len(cooldowns))
	for i, c := range cooldowns {
		views[i] = CooldownView{
			Service:    c.Service,
			ActionType: c.ActionType,
			Count:      c.Count,
		}
		if t, err := time.Parse(timeFormat, c.LastAction); err == nil {
			views[i].LastAction = t
		}
	}
	return views
}
