package web

import (
	"bufio"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/session"
)

// handleIndex renders the overview dashboard.
// Governing: SPEC-0021 REQ "TL;DR Page Rendering"
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var sessionView *SessionView
	if latest, err := s.db.LatestSession(); err != nil {
		log.Printf("handleIndex: LatestSession: %v", err)
	} else if latest != nil {
		v := ToSessionView(*latest)
		sessionView = &v
	}

	var events []EventView
	if evts, err := s.db.ListEvents(10, 0, nil, nil); err != nil {
		log.Printf("handleIndex: ListEvents: %v", err)
	} else {
		events = ToEventViews(evts)
	}

	var memoryCount int
	if memories, err := s.db.ListMemories(nil, nil, 1000, 0); err != nil {
		log.Printf("handleIndex: ListMemories: %v", err)
	} else {
		memoryCount = len(memories)
	}

	data := struct {
		Events      []EventView
		Session     *SessionView
		NextRun     time.Time
		Interval    int
		MemoryCount int
	}{
		Events:      events,
		Session:     sessionView,
		NextRun:     time.Now().UTC().Add(time.Duration(s.cfg.Interval) * time.Second),
		Interval:    s.cfg.Interval,
		MemoryCount: memoryCount,
	}

	s.render(w, r, "index.html", data)
}

// handleSessions renders the session list.
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

// handleCooldowns renders the cooldown state.
func (s *Server) handleCooldowns(w http.ResponseWriter, r *http.Request) {
	cooldowns, err := s.db.ListRecentCooldowns(24 * time.Hour)
	if err != nil {
		log.Printf("handleCooldowns: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Cooldowns []CooldownView
	}{
		Cooldowns: ToCooldownViews(cooldowns),
	}

	s.render(w, r, "cooldowns.html", data)
}

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
	}

	s.render(w, r, "config.html", data)
}

// handleTriggerSession triggers an ad-hoc session with a custom prompt.
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

	sessionID, err := s.mgr.TriggerAdHoc(prompt)
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
	}

	s.render(w, r, "config.html", data)
}
