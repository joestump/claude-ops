package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/session"
)

// handleIndex renders the overview dashboard.
// Governing: SPEC-0008 REQ-10 — overview/home page: service summary and last check time.
// Governing: SPEC-0021 REQ "TL;DR Page Rendering", REQ "Dashboard Stats HUD", REQ "Unified Activity Feed"
// Governing: SPEC-0013 "Real-Time Overview" — serves polling endpoint for HTMX auto-refresh
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Fetch aggregate dashboard stats.
	var stats *db.DashboardStats
	if st, err := s.db.GetDashboardStats(); err != nil {
		log.Printf("handleIndex: GetDashboardStats: %v", err)
	} else {
		stats = st
	}

	// Fetch the most recent session for the Last Run HUD row.
	var lastSession *SessionView
	if latest, err := s.db.LatestSession(); err != nil {
		log.Printf("handleIndex: LatestSession: %v", err)
	} else if latest != nil {
		v := ToSessionView(*latest)
		lastSession = &v
	}

	// Fetch the most recent session that has a short LLM summary.
	var lastSummary *SessionView
	if sessions, err := s.db.ListSessions(20, 0); err != nil {
		log.Printf("handleIndex: ListSessions (summary): %v", err)
	} else {
		for _, sess := range sessions {
			if sess.Summary != nil {
				v := ToSessionView(sess)
				lastSummary = &v
				break
			}
		}
	}

	// Build the unified activity feed.
	var activitySessions []db.Session
	if sessions, err := s.db.ListSessions(15, 0); err != nil {
		log.Printf("handleIndex: ListSessions (activity): %v", err)
	} else {
		activitySessions = sessions
	}

	var activityEvents []db.Event
	if evts, err := s.db.ListEvents(50, 0, nil, nil); err != nil {
		log.Printf("handleIndex: ListEvents: %v", err)
	} else {
		activityEvents = evts
	}

	var activityMemories []db.Memory
	if mems, err := s.db.ListMemories(nil, nil, 15, 0); err != nil {
		log.Printf("handleIndex: ListMemories: %v", err)
	} else {
		activityMemories = mems
	}

	activity := buildActivityFeed(activitySessions, activityEvents, activityMemories)

	data := struct {
		Stats       *db.DashboardStats
		LastSession *SessionView
		LastSummary *SessionView
		Activity    []ActivityItem
		NextRun     time.Time
		Interval    int
	}{
		Stats:       stats,
		LastSession: lastSession,
		LastSummary: lastSummary,
		Activity:    activity,
		NextRun:     time.Now().UTC().Add(time.Duration(s.cfg.Interval) * time.Second),
		Interval:    s.cfg.Interval,
	}

	s.render(w, r, "index.html", data)
}

// handleSessions renders the session list.
// Governing: SPEC-0013 "Real-Time Sessions List" — serves polling endpoint for HTMX auto-refresh
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.ListSessions(50, 0)
	if err != nil {
		log.Printf("handleSessions: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	views := ToSessionViews(sessions)

	// Build a set of IDs that are parents (have children referencing them).
	parentIDs := make(map[int64]bool)
	for _, v := range views {
		if v.ParentSessionID != nil {
			parentIDs[*v.ParentSessionID] = true
		}
	}

	// Annotate chain membership: HasChildren (escalated up) and IsChainTip (top of chain).
	for i := range views {
		if parentIDs[views[i].ID] {
			views[i].HasChildren = true
		}
		if views[i].ParentSessionID != nil && !parentIDs[views[i].ID] {
			views[i].IsChainTip = true
		}
	}

	// Annotate chain roots and compute chain cost/length by walking descendants.
	for i := range views {
		if views[i].ParentSessionID == nil && !parentIDs[views[i].ID] {
			continue // standalone session, not part of a chain
		}
		if views[i].ParentSessionID == nil {
			views[i].IsChainRoot = true
			// Walk all descendants to compute total chain cost and length.
			var total float64
			if views[i].CostUSD != nil {
				total += *views[i].CostUSD
			}
			length := 1
			queue := []int64{views[i].ID}
			for len(queue) > 0 {
				pid := queue[0]
				queue = queue[1:]
				if children, err := s.db.GetChildSessions(pid); err == nil {
					for _, c := range children {
						length++
						if c.CostUSD != nil {
							total += *c.CostUSD
						}
						queue = append(queue, c.ID)
					}
				}
			}
			views[i].ChainCost = total
			views[i].ChainLength = length
		}
	}

	// Pass 4: propagate chain tip status to all chain members for left-border coloring.
	viewIdx := make(map[int64]int, len(views))
	for i, v := range views {
		viewIdx[v.ID] = i
	}
	for i := range views {
		if views[i].IsChainTip {
			status := views[i].Status
			views[i].ChainOutcome = status
			pid := views[i].ParentSessionID
			for pid != nil {
				if idx, ok := viewIdx[*pid]; ok {
					views[idx].ChainOutcome = status
					pid = views[idx].ParentSessionID
				} else {
					break
				}
			}
		}
	}

	data := struct {
		Sessions []SessionView
	}{
		Sessions: views,
	}

	s.render(w, r, "sessions.html", data)
}

// handleSession renders a single session detail view.
// Governing: SPEC-0008 REQ-10 — session view page: streaming output, tier level, and target service.
// Governing: SPEC-0011 "Session Page Layout" — back link, header, metadata, response, activity log, log path.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	sess, err := s.db.GetSession(id)
	if err != nil {
		log.Printf("handleSession: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	view := ToSessionView(*sess)

	// Load parent session if this session was escalated.
	// Governing: SPEC-0016 REQ "Dashboard Escalation Chain Display" — parent/child links and chain cost
	if sess.ParentSessionID != nil {
		if parent, err := s.db.GetSession(*sess.ParentSessionID); err == nil && parent != nil {
			pv := ToSessionView(*parent)
			view.ParentSession = &pv
		}
	}

	// Load child sessions (escalated from this session).
	if children, err := s.db.GetChildSessions(sess.ID); err == nil && len(children) > 0 {
		view.ChildSessions = ToSessionViews(children)
	}

	// Compute chain cost if this session is part of an escalation chain.
	if sess.ParentSessionID != nil || len(view.ChildSessions) > 0 {
		// Walk up to the root to find all ancestors.
		chain, err := s.db.GetEscalationChain(sess.ID)
		if err == nil && len(chain) > 0 {
			var total float64
			for _, cs := range chain {
				if cs.CostUSD != nil {
					total += *cs.CostUSD
				}
			}
			// Also walk down from current session to include descendants not in the ancestor chain.
			queue := []int64{sess.ID}
			for len(queue) > 0 {
				pid := queue[0]
				queue = queue[1:]
				if descendants, err := s.db.GetChildSessions(pid); err == nil {
					for _, d := range descendants {
						// Avoid double-counting sessions already in the ancestor chain.
						alreadyCounted := false
						for _, cs := range chain {
							if cs.ID == d.ID {
								alreadyCounted = true
								break
							}
						}
						if !alreadyCounted {
							if d.CostUSD != nil {
								total += *d.CostUSD
							}
							queue = append(queue, d.ID)
						}
					}
				}
			}
			view.ChainCost = total
		}
	}

	// Governing: SPEC-0011 "Log File Formatting on Read Path" — format NDJSON log line-by-line.
	// Read and format log file contents if available.
	// Log files contain timestamped NDJSON from --output-format stream-json.
	// Format: "2006-01-02T15:04:05Z\t{json}" per line (or legacy raw JSON).
	// We use FormatStreamEventHTML to produce color-coded HTML output.
	// Governing: SPEC-0011 "Log File Formatting on Read Path" — line-by-line formatting via scanner
	var output string
	if sess.LogFile != nil && *sess.LogFile != "" {
		if f, err := os.Open(*sess.LogFile); err == nil {
			var lines []string
			var lineNum int
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ts, raw, hasTS := session.ParseTimestampedLogLine(scanner.Text())
				formatted := session.FormatStreamEventHTML(raw)
				if formatted != "" {
					lineNum++
					tsStr := ""
					if hasTS {
						tsStr = ts.Format("15:04:05")
					}
					lines = append(lines, session.WrapLogLine(lineNum, tsStr, formatted))
				}
			}
			_ = f.Close()
			output = strings.Join(lines, "\n")
		} else {
			output = fmt.Sprintf("[error reading log: %v]", err)
		}
	}

	tmplData := struct {
		Session SessionView
		Output  template.HTML
	}{
		Session: view,
		Output:  template.HTML(output),
	}

	s.render(w, r, "session.html", tmplData)
}

// handleSessionStream opens an SSE connection for a running session.
// Governing: SPEC-0008 REQ-3 — HTMX-Based Interactivity (hx-ext="sse" for real-time streaming)
// Governing: SPEC-0008 REQ-10 — session view real-time streaming via SSE.
// Governing: SPEC-0011 "SSE Streaming of Formatted Events" — deliver formatted lines to browsers via SSE.
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Tell the browser to wait 30 s before reconnecting. The JS page-reload
	// fires after 2.5 s, destroying the EventSource before the retry fires.
	// This prevents the hub's buffer from being replayed on reconnect and
	// appearing as duplicated output in the terminal.
	_, _ = fmt.Fprintf(w, "retry: 30000\n\n")
	flusher.Flush()

	if s.hub == nil {
		_, _ = fmt.Fprintf(w, "data: [session %d] SSE hub not connected\n\n", id)
		flusher.Flush()
		return
	}

	ch, unsubscribe := s.hub.Subscribe(id)
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				_, _ = fmt.Fprintf(w, "event: done\ndata: session complete\n\n")
				flusher.Flush()
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

// handleEvents renders the events feed.
// Governing: SPEC-0013 "Events Page" — reverse-chronological events with HTMX polling
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.db.ListEvents(100, 0, nil, nil)
	if err != nil {
		log.Printf("handleEvents: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Events []EventView
	}{
		Events: ToEventViews(events),
	}

	s.render(w, r, "events.html", data)
}

// cooldownJSONState mirrors the agent-managed cooldown.json schema.
// Governing: SPEC-0007 REQ-13 (Cooldown State Data Model)
type cooldownJSONState struct {
	Services map[string]cooldownJSONService `json:"services"`
}

type cooldownJSONService struct {
	Restarts          []cooldownJSONAction `json:"restarts"`
	Redeployments     []cooldownJSONAction `json:"redeployments"`
	ConsecutiveHealthy int                 `json:"consecutive_healthy"`
}

type cooldownJSONAction struct {
	Timestamp string `json:"timestamp"`
	Success   bool   `json:"success"`
}

// readCooldownJSON reads and parses the agent-managed cooldown state file.
// Returns nil without error if the file does not exist yet.
func readCooldownJSON(stateDir string) (*cooldownJSONState, error) {
	path := filepath.Join(stateDir, "cooldown.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state cooldownJSONState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// cooldownViewsFromJSON converts the JSON cooldown state into CooldownViews,
// showing all services that have any restarts (4h window) or redeployments (24h window).
// Governing: SPEC-0007 REQ-4 (restart limit 4h), REQ-5 (redeployment limit 24h)
func cooldownViewsFromJSON(state *cooldownJSONState) []CooldownView {
	if state == nil {
		return nil
	}
	now := time.Now().UTC()
	restartWindow := now.Add(-4 * time.Hour)
	redeployWindow := now.Add(-24 * time.Hour)

	var views []CooldownView
	for svc, s := range state.Services {
		// Restarts within 4h window.
		var restartCount int
		var lastRestart time.Time
		for _, a := range s.Restarts {
			if t, err := time.Parse(time.RFC3339, a.Timestamp); err == nil {
				if t.After(restartWindow) {
					restartCount++
					if t.After(lastRestart) {
						lastRestart = t
					}
				}
			}
		}
		if restartCount > 0 {
			views = append(views, CooldownView{
				Service:    svc,
				ActionType: "restart",
				Count:      restartCount,
				Limit:      2,
				LastAction: lastRestart,
				InCooldown: restartCount >= 2,
			})
		}

		// Redeployments within 24h window.
		var redeployCount int
		var lastRedeploy time.Time
		for _, a := range s.Redeployments {
			if t, err := time.Parse(time.RFC3339, a.Timestamp); err == nil {
				if t.After(redeployWindow) {
					redeployCount++
					if t.After(lastRedeploy) {
						lastRedeploy = t
					}
				}
			}
		}
		if redeployCount > 0 {
			views = append(views, CooldownView{
				Service:    svc,
				ActionType: "redeployment",
				Count:      redeployCount,
				Limit:      1,
				LastAction: lastRedeploy,
				InCooldown: redeployCount >= 1,
			})
		}
	}
	return views
}

// handleCooldowns renders the cooldown state.
// Reads from the agent-managed cooldown.json (source of truth), falling back to
// the DB-backed cooldown_actions table if the file is absent.
// Governing: SPEC-0007 REQ-4, REQ-5, REQ-13 — cooldown state and data model.
func (s *Server) handleCooldowns(w http.ResponseWriter, r *http.Request) {
	var views []CooldownView

	state, err := readCooldownJSON(s.cfg.StateDir)
	if err != nil {
		log.Printf("handleCooldowns: read cooldown.json: %v", err)
	}
	if state != nil {
		views = cooldownViewsFromJSON(state)
	} else {
		// Fallback: DB-backed records from [COOLDOWN:...] markers.
		cooldowns, dbErr := s.db.ListRecentCooldowns(24 * time.Hour)
		if dbErr != nil {
			log.Printf("handleCooldowns: db fallback: %v", dbErr)
		} else {
			views = ToCooldownViews(cooldowns)
		}
	}

	data := struct {
		Cooldowns []CooldownView
	}{
		Cooldowns: views,
	}

	s.render(w, r, "cooldowns.html", data)
}

// Governing: SPEC-0015 "Dashboard Memories Page" — /memories with service/category filters and HTMX polling
// handleMemories renders the memories list with optional filters.
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	var serviceFilter *string
	var categoryFilter *string

	if v := r.URL.Query().Get("service"); v != "" {
		serviceFilter = &v
	}
	if v := r.URL.Query().Get("category"); v != "" {
		categoryFilter = &v
	}

	memories, err := s.db.ListMemories(serviceFilter, categoryFilter, 200, 0)
	if err != nil {
		log.Printf("handleMemories: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Memories []MemoryView
		Service  string
		Category string
	}{
		Memories: ToMemoryViews(memories),
	}
	if serviceFilter != nil {
		data.Service = *serviceFilter
	}
	if categoryFilter != nil {
		data.Category = *categoryFilter
	}

	s.render(w, r, "memories.html", data)
}

// Governing: SPEC-0015 "Dashboard Memory CRUD" — operator creates memories manually (session_id=NULL)
// handleMemoryCreate handles POST /memories to create a new memory.
func (s *Server) handleMemoryCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	service := strings.TrimSpace(r.FormValue("service"))
	category := strings.TrimSpace(r.FormValue("category"))
	observation := strings.TrimSpace(r.FormValue("observation"))
	confidenceStr := r.FormValue("confidence")

	if category == "" || observation == "" {
		http.Error(w, "category and observation are required", http.StatusBadRequest)
		return
	}

	confidence := 0.7
	if confidenceStr != "" {
		if v, err := strconv.ParseFloat(confidenceStr, 64); err == nil {
			confidence = v
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m := &db.Memory{
		Category:    category,
		Observation: observation,
		Confidence:  confidence,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        0,
	}
	if service != "" {
		m.Service = &service
	}

	if _, err := s.db.InsertMemory(m); err != nil {
		log.Printf("handleMemoryCreate: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/memories", http.StatusSeeOther)
}

// Governing: SPEC-0015 "Dashboard Memory CRUD" — operator edits observation, confidence, active status
// handleMemoryUpdate handles POST /memories/{id}/update.
func (s *Server) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid memory ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	observation := strings.TrimSpace(r.FormValue("observation"))
	confidenceStr := r.FormValue("confidence")
	active := r.FormValue("active") == "on"

	confidence := 0.7
	if confidenceStr != "" {
		if v, err := strconv.ParseFloat(confidenceStr, 64); err == nil {
			confidence = v
		}
	}

	if err := s.db.UpdateMemory(id, observation, confidence, active); err != nil {
		log.Printf("handleMemoryUpdate: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/memories", http.StatusSeeOther)
}

// Governing: SPEC-0015 "Dashboard Memory CRUD" — operator permanently deletes a memory
// handleMemoryDelete handles POST /memories/{id}/delete.
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid memory ID", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteMemory(id); err != nil {
		log.Printf("handleMemoryDelete: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/memories", http.StatusSeeOther)
}

// handleConfigGet renders the configuration form.
// Governing: SPEC-0008 REQ-10 — configuration page: runtime parameter display and modification.
func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Interval              int
		Tier1Model            string
		Tier2Model            string
		Tier3Model            string
		DryRun                bool
		Saved                 bool
		StateDir              string
		ResultsDir            string
		ReposDir              string
		MaxTier               int
		BrowserAllowedOrigins string
		ChatAPIKey            string
	}{
		Interval:              s.cfg.Interval,
		Tier1Model:            s.cfg.Tier1Model,
		Tier2Model:            s.cfg.Tier2Model,
		Tier3Model:            s.cfg.Tier3Model,
		DryRun:                s.cfg.DryRun,
		StateDir:              s.cfg.StateDir,
		ResultsDir:            s.cfg.ResultsDir,
		ReposDir:              s.cfg.ReposDir,
		MaxTier:               s.cfg.MaxTier,
		BrowserAllowedOrigins: s.cfg.BrowserAllowedOrigins,
		ChatAPIKey:            os.Getenv("CLAUDEOPS_CHAT_API_KEY"),
	}

	s.render(w, r, "config.html", data)
}

// classifyPromptTier calls the Anthropic Messages API with claude-haiku to
// determine the most appropriate starting tier for a user-provided prompt.
// Returns 1, 2, or 3. Falls back to 1 on any error or missing API key.
// Governing: SPEC-0012 "POST /sessions/trigger Endpoint" — auto tier routing
func classifyPromptTier(ctx context.Context, apiKey, prompt string) int {
	if apiKey == "" {
		return 1
	}
	system := "You are an infrastructure ops escalation router. " +
		"Based on the user's request, pick the starting investigation tier:\n" +
		"1 = Observe only (check status, gather info, no changes needed or uncertain)\n" +
		"2 = Safe fix (restart services, fix config, clear caches — something is clearly down)\n" +
		"3 = Full remediation (complex failure, Ansible/Helm redeployment, database issues, multi-service outage)\n\n" +
		"Reply with ONLY the digit 1, 2, or 3. No other text."

	payload, _ := json.Marshal(map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 5,
		"system":     system,
		"messages":   []map[string]any{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	hc := &http.Client{Timeout: 8 * time.Second}
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Content) == 0 {
		return 1
	}
	switch strings.TrimSpace(result.Content[0].Text) {
	case "2":
		return 2
	case "3":
		return 3
	default:
		return 1
	}
}

// handleTriggerSession triggers an ad-hoc session with a custom prompt.
// Governing: SPEC-0012 "POST /sessions/trigger Endpoint" — form-encoded prompt, 400/409/200 responses
func (s *Server) handleTriggerSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	// Determine starting tier from the form field.
	// "auto" (or empty) invokes the LLM router; explicit "1"/"2"/"3" bypasses it.
	startTier := 1
	switch r.FormValue("tier") {
	case "2":
		startTier = 2
	case "3":
		startTier = 3
	case "auto", "":
		startTier = classifyPromptTier(r.Context(), os.Getenv("ANTHROPIC_API_KEY"), prompt)
		log.Printf("handleTriggerSession: LLM routed %q → tier %d", prompt, startTier)
	}

	sessionID, err := s.mgr.TriggerAdHoc(prompt, startTier)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	target := fmt.Sprintf("/sessions/%d", sessionID)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleConfigPost processes configuration form submissions.
// Governing: SPEC-0008 REQ-3 — HTMX-Based Interactivity (hx-post form submission with hx-swap)
// Governing: SPEC-0008 REQ-10 — configuration changes take effect without container restart.
func (s *Server) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	if v := r.FormValue("interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.cfg.Interval = n
			_ = s.db.SetConfig("interval", v)
		}
	}
	if v := r.FormValue("tier1_model"); v != "" {
		s.cfg.Tier1Model = v
		_ = s.db.SetConfig("tier1_model", v)
	}
	if v := r.FormValue("tier2_model"); v != "" {
		s.cfg.Tier2Model = v
		_ = s.db.SetConfig("tier2_model", v)
	}
	if v := r.FormValue("tier3_model"); v != "" {
		s.cfg.Tier3Model = v
		_ = s.db.SetConfig("tier3_model", v)
	}
	s.cfg.DryRun = r.FormValue("dry_run") == "on"
	_ = s.db.SetConfig("dry_run", strconv.FormatBool(s.cfg.DryRun))

	log.Printf("config updated: interval=%d tier1=%s tier2=%s tier3=%s dry_run=%v",
		s.cfg.Interval, s.cfg.Tier1Model, s.cfg.Tier2Model, s.cfg.Tier3Model, s.cfg.DryRun)

	data := struct {
		Interval              int
		Tier1Model            string
		Tier2Model            string
		Tier3Model            string
		DryRun                bool
		Saved                 bool
		StateDir              string
		ResultsDir            string
		ReposDir              string
		MaxTier               int
		BrowserAllowedOrigins string
		ChatAPIKey            string
	}{
		Interval:              s.cfg.Interval,
		Tier1Model:            s.cfg.Tier1Model,
		Tier2Model:            s.cfg.Tier2Model,
		Tier3Model:            s.cfg.Tier3Model,
		DryRun:                s.cfg.DryRun,
		Saved:                 true,
		StateDir:              s.cfg.StateDir,
		ResultsDir:            s.cfg.ResultsDir,
		ReposDir:              s.cfg.ReposDir,
		MaxTier:               s.cfg.MaxTier,
		BrowserAllowedOrigins: s.cfg.BrowserAllowedOrigins,
		ChatAPIKey:            os.Getenv("CLAUDEOPS_CHAT_API_KEY"),
	}

	s.render(w, r, "config.html", data)
}
