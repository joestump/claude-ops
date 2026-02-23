package web

import (
	"sort"
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
	Summary    string
	CostUSD    *float64
	NumTurns   *int
	DurationMs *int64
	Trigger    string
	PromptText string

	// Escalation chain fields.
	// Governing: SPEC-0016 REQ "Dashboard Escalation Chain Display", REQ "Per-Tier Cost Attribution"
	ParentSessionID *int64
	ParentSession   *SessionView
	ChildSessions   []SessionView
	ChainCost       float64
	IsChainRoot     bool
	ChainLength     int
	HasChildren     bool   // session escalated to a child above
	IsChainTip      bool   // top session of an escalation chain
	ChainOutcome    string // status of the chain tip (set on all chain members)
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
	Limit      int       // max allowed in window (2 for restarts, 1 for redeployments)
	LastAction time.Time
	InCooldown bool      // true when count has reached or exceeded the limit
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
	if s.Summary != nil {
		v.Summary = *s.Summary
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

// ActivityItem is a template-friendly representation of a single entry in the
// unified activity feed (events, session milestones, and memory upserts).
// Governing: SPEC-0021 REQ "Unified Activity Feed"
type ActivityItem struct {
	Type      string   // "event", "session", "memory"
	Level     string   // event level, session status, or "info" for memories
	Message   string
	Service   string
	SessionID *int64
	Timestamp time.Time
	Icon      string // emoji prefix for the item type
}

// buildActivityFeed merges sessions, events, and memories into a unified
// chronological activity feed, returning at most 40 items sorted newest-first.
// Governing: SPEC-0021 REQ "Unified Activity Feed"
func buildActivityFeed(sessions []db.Session, events []db.Event, memories []db.Memory) []ActivityItem {
	var items []ActivityItem

	// Session milestones: started + ended/escalated.
	for _, s := range sessions {
		sid := s.ID
		startedAt, _ := time.Parse(timeFormat, s.StartedAt)
		startMsg := "Session #" + itoa(s.ID) + " started"
		if s.Trigger != "" {
			startMsg += " (" + s.Trigger + ")"
		}
		items = append(items, ActivityItem{
			Type:      "session",
			Level:     "running",
			Message:   startMsg,
			SessionID: &sid,
			Timestamp: startedAt,
			Icon:      "▶",
		})
		if s.EndedAt != nil {
			endedAt, err := time.Parse(timeFormat, *s.EndedAt)
			if err == nil {
				icon := "✓"
				level := s.Status
				msg := "Session #" + itoa(s.ID) + " " + s.Status
				switch s.Status {
				case "escalated":
					icon = "↑"
				case "failed", "timed_out":
					icon = "✗"
				}
				if s.CostUSD != nil {
					msg += " · $" + fmtFloat(*s.CostUSD, 4)
				}
				items = append(items, ActivityItem{
					Type:      "session",
					Level:     level,
					Message:   msg,
					SessionID: &sid,
					Timestamp: endedAt,
					Icon:      icon,
				})
			}
		}
	}

	// Events.
	for _, e := range events {
		ts, _ := time.Parse(timeFormat, e.CreatedAt)
		icon := "⚡"
		switch e.Level {
		case "critical":
			icon = "🔴"
		case "warning":
			icon = "⚠"
		}
		svc := ""
		if e.Service != nil {
			svc = *e.Service
		}
		item := ActivityItem{
			Type:      "event",
			Level:     e.Level,
			Message:   e.Message,
			Service:   svc,
			Timestamp: ts,
			Icon:      icon,
		}
		if e.SessionID != nil {
			sid := *e.SessionID
			item.SessionID = &sid
		}
		items = append(items, item)
	}

	// Memories.
	for _, m := range memories {
		ts, _ := time.Parse(timeFormat, m.UpdatedAt)
		svc := ""
		if m.Service != nil {
			svc = *m.Service
		}
		msg := "[" + m.Category + "]"
		if svc != "" {
			msg += " " + svc + ":"
		}
		msg += " " + m.Observation
		items = append(items, ActivityItem{
			Type:      "memory",
			Level:     "info",
			Message:   msg,
			Service:   svc,
			Timestamp: ts,
			Icon:      "🧠",
		})
	}

	// Sort descending by timestamp.
	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})

	// Cap at 40 items.
	if len(items) > 40 {
		items = items[:40]
	}
	return items
}

// itoa converts an int64 to a decimal string without importing strconv.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// Reverse.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// fmtFloat formats a float64 to a string with the given number of decimal places.
func fmtFloat(f float64, decimals int) string {
	// Use manual formatting to avoid importing fmt in viewmodel.
	// We only need 0-4 decimal places.
	neg := f < 0
	if neg {
		f = -f
	}
	factor := 1.0
	for i := 0; i < decimals; i++ {
		factor *= 10
	}
	rounded := int64(f*factor + 0.5)
	intPart := rounded / int64(factor)
	fracPart := rounded % int64(factor)

	intStr := itoa(intPart)
	fracStr := itoa(fracPart)
	// Pad fractional part with leading zeros.
	for len(fracStr) < decimals {
		fracStr = "0" + fracStr
	}
	result := intStr
	if decimals > 0 {
		result += "." + fracStr
	}
	if neg {
		result = "-" + result
	}
	return result
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
