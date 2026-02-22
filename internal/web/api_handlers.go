package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/gitprovider"
)

// --- JSON Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// requireJSON checks the Content-Type header and returns false (with a 415 response) if it is not application/json.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

// parseLimitOffset extracts limit and offset query params with defaults and validation.
func parseLimitOffset(r *http.Request, defaultLimit int) (limit, offset int, err error) {
	limit = defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 0 {
			return 0, 0, fmt.Errorf("limit must be a non-negative integer")
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

// --- API Handlers ---

// Governing: SPEC-0017 REQ-14 "Health Endpoint" — GET /api/v1/health
// handleAPIHealth returns a simple health check response.
func (s *Server) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Governing: SPEC-0017 REQ-3 "Sessions List Endpoint" — GET /api/v1/sessions with limit/offset pagination
// handleAPIListSessions returns a paginated list of sessions.
func (s *Server) handleAPIListSessions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r, 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sessions, err := s.db.ListSessions(limit, offset)
	if err != nil {
		log.Printf("handleAPIListSessions: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, APISessionsResponse{Sessions: toAPISessions(sessions)})
}

// Governing: SPEC-0017 REQ-4 "Session Detail Endpoint" — GET /api/v1/sessions/{id} with chain details
// handleAPIGetSession returns a single session with escalation chain details.
func (s *Server) handleAPIGetSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	sess, err := s.db.GetSession(id)
	if err != nil {
		log.Printf("handleAPIGetSession: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	apiSess := toAPISession(*sess)
	apiSess.Response = sess.Response

	// Load parent session.
	if sess.ParentSessionID != nil {
		if parent, err := s.db.GetSession(*sess.ParentSessionID); err == nil && parent != nil {
			p := toAPISession(*parent)
			apiSess.ParentSession = &p
		}
	}

	// Load child sessions.
	if children, err := s.db.GetChildSessions(sess.ID); err == nil && len(children) > 0 {
		apiSess.ChildSessions = toAPISessions(children)
	}

	// Compute chain cost if part of an escalation chain.
	if sess.ParentSessionID != nil || len(apiSess.ChildSessions) > 0 {
		chain, err := s.db.GetEscalationChain(sess.ID)
		if err == nil && len(chain) > 0 {
			var total float64
			for _, cs := range chain {
				if cs.CostUSD != nil {
					total += *cs.CostUSD
				}
			}
			// Walk descendants not in the ancestor chain.
			queue := []int64{sess.ID}
			for len(queue) > 0 {
				pid := queue[0]
				queue = queue[1:]
				if descendants, err := s.db.GetChildSessions(pid); err == nil {
					for _, d := range descendants {
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
			apiSess.ChainCost = &total
		}
	}

	writeJSON(w, http.StatusOK, apiSess)
}

// Governing: SPEC-0017 REQ-5 "Session Trigger Endpoint" — POST /api/v1/sessions/trigger with JSON body
// handleAPITriggerSession triggers an ad-hoc session from a JSON request body.
func (s *Server) handleAPITriggerSession(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	var req APITriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	sessionID, err := s.mgr.TriggerAdHoc(prompt)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	sess, err := s.db.GetSession(sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": sessionID, "status": "running"})
		return
	}

	writeJSON(w, http.StatusCreated, toAPISession(*sess))
}

// Governing: SPEC-0017 REQ-6 "Events List Endpoint" — GET /api/v1/events with level/service filters
// handleAPIListEvents returns a paginated, filterable list of events.
func (s *Server) handleAPIListEvents(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var level *string
	if v := r.URL.Query().Get("level"); v != "" {
		level = &v
	}
	var service *string
	if v := r.URL.Query().Get("service"); v != "" {
		service = &v
	}

	events, err := s.db.ListEvents(limit, offset, level, service)
	if err != nil {
		log.Printf("handleAPIListEvents: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, APIEventsResponse{Events: toAPIEvents(events)})
}

// Governing: SPEC-0017 REQ-7 "Memories List Endpoint" — GET /api/v1/memories with service/category filters
// handleAPIListMemories returns a paginated, filterable list of memories.
func (s *Server) handleAPIListMemories(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var service *string
	if v := r.URL.Query().Get("service"); v != "" {
		service = &v
	}
	var category *string
	if v := r.URL.Query().Get("category"); v != "" {
		category = &v
	}

	memories, err := s.db.ListMemories(service, category, limit, offset)
	if err != nil {
		log.Printf("handleAPIListMemories: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, APIMemoriesResponse{Memories: toAPIMemories(memories)})
}

// Governing: SPEC-0017 REQ-8 "Memory Create Endpoint" — POST /api/v1/memories with JSON body
// handleAPICreateMemory creates a new memory from a JSON request body.
func (s *Server) handleAPICreateMemory(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	var req APICreateMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Category == "" || req.Observation == "" {
		writeError(w, http.StatusBadRequest, "category and observation are required")
		return
	}

	confidence := 0.7
	if req.Confidence != nil {
		confidence = *req.Confidence
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m := &db.Memory{
		Service:     req.Service,
		Category:    req.Category,
		Observation: req.Observation,
		Confidence:  confidence,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        0,
	}

	id, err := s.db.InsertMemory(m)
	if err != nil {
		log.Printf("handleAPICreateMemory: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	created, err := s.db.GetMemory(id)
	if err != nil || created == nil {
		writeJSON(w, http.StatusCreated, toAPIMemory(*m))
		return
	}

	writeJSON(w, http.StatusCreated, toAPIMemory(*created))
}

// Governing: SPEC-0017 REQ-9 "Memory Update Endpoint" — PUT /api/v1/memories/{id} with JSON body
// handleAPIUpdateMemory updates a memory from a JSON request body.
func (s *Server) handleAPIUpdateMemory(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory ID")
		return
	}

	existing, err := s.db.GetMemory(id)
	if err != nil {
		log.Printf("handleAPIUpdateMemory: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	var req APIUpdateMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	observation := existing.Observation
	if req.Observation != nil {
		observation = *req.Observation
	}
	confidence := existing.Confidence
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	active := existing.Active
	if req.Active != nil {
		active = *req.Active
	}

	if err := s.db.UpdateMemory(id, observation, confidence, active); err != nil {
		log.Printf("handleAPIUpdateMemory: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	updated, err := s.db.GetMemory(id)
	if err != nil || updated == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}

	writeJSON(w, http.StatusOK, toAPIMemory(*updated))
}

// Governing: SPEC-0017 REQ-10 "Memory Delete Endpoint" — DELETE /api/v1/memories/{id}
// handleAPIDeleteMemory deletes a memory by ID.
func (s *Server) handleAPIDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory ID")
		return
	}

	existing, err := s.db.GetMemory(id)
	if err != nil {
		log.Printf("handleAPIDeleteMemory: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	if err := s.db.DeleteMemory(id); err != nil {
		log.Printf("handleAPIDeleteMemory: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Governing: SPEC-0017 REQ-11 "Cooldowns List Endpoint" — GET /api/v1/cooldowns
// handleAPIListCooldowns returns cooldown action summaries for the last 24 hours.
func (s *Server) handleAPIListCooldowns(w http.ResponseWriter, r *http.Request) {
	cooldowns, err := s.db.ListRecentCooldowns(24 * time.Hour)
	if err != nil {
		log.Printf("handleAPIListCooldowns: %v", err)
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, APICooldownsResponse{Cooldowns: toAPICooldowns(cooldowns)})
}

// Governing: SPEC-0017 REQ-12 "Config Get Endpoint" — GET /api/v1/config
// handleAPIGetConfig returns the current runtime configuration.
func (s *Server) handleAPIGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, APIConfig{
		Interval:   s.cfg.Interval,
		Tier1Model: s.cfg.Tier1Model,
		Tier2Model: s.cfg.Tier2Model,
		Tier3Model: s.cfg.Tier3Model,
		DryRun:     s.cfg.DryRun,
		MaxTier:    s.cfg.MaxTier,
		StateDir:   s.cfg.StateDir,
		ResultsDir: s.cfg.ResultsDir,
		ReposDir:   s.cfg.ReposDir,
		PREnabled:  s.cfg.PREnabled,
	})
}

// Governing: SPEC-0017 REQ-13 "Config Update Endpoint" — PUT /api/v1/config (partial update)
// handleAPIUpdateConfig applies partial configuration updates from a JSON body.
func (s *Server) handleAPIUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	var req APIUpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Interval != nil {
		if *req.Interval <= 0 {
			writeError(w, http.StatusBadRequest, "interval must be positive")
			return
		}
		s.cfg.Interval = *req.Interval
		_ = s.db.SetConfig("interval", strconv.Itoa(*req.Interval))
	}
	if req.Tier1Model != nil {
		s.cfg.Tier1Model = *req.Tier1Model
		_ = s.db.SetConfig("tier1_model", *req.Tier1Model)
	}
	if req.Tier2Model != nil {
		s.cfg.Tier2Model = *req.Tier2Model
		_ = s.db.SetConfig("tier2_model", *req.Tier2Model)
	}
	if req.Tier3Model != nil {
		s.cfg.Tier3Model = *req.Tier3Model
		_ = s.db.SetConfig("tier3_model", *req.Tier3Model)
	}
	if req.DryRun != nil {
		s.cfg.DryRun = *req.DryRun
		_ = s.db.SetConfig("dry_run", strconv.FormatBool(*req.DryRun))
	}

	log.Printf("API config updated: interval=%d tier1=%s tier2=%s tier3=%s dry_run=%v",
		s.cfg.Interval, s.cfg.Tier1Model, s.cfg.Tier2Model, s.cfg.Tier3Model, s.cfg.DryRun)

	writeJSON(w, http.StatusOK, APIConfig{
		Interval:   s.cfg.Interval,
		Tier1Model: s.cfg.Tier1Model,
		Tier2Model: s.cfg.Tier2Model,
		Tier3Model: s.cfg.Tier3Model,
		DryRun:     s.cfg.DryRun,
		MaxTier:    s.cfg.MaxTier,
		StateDir:   s.cfg.StateDir,
		ResultsDir: s.cfg.ResultsDir,
		ReposDir:   s.cfg.ReposDir,
		PREnabled:  s.cfg.PREnabled,
	})
}

// handleAPICreatePR creates a branch, commits files, and opens a pull request.
func (s *Server) handleAPICreatePR(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.PREnabled {
		writeError(w, http.StatusForbidden, "PR creation is disabled (set CLAUDEOPS_PR_ENABLED=true to enable)")
		return
	}

	var req APICreatePRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RepoOwner == "" || req.RepoName == "" || req.Title == "" || len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "repo_owner, repo_name, title, and files are required")
		return
	}

	if err := gitprovider.ValidateScope(req.Files); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	// Governing: SPEC-0018 REQ-9 "Permission Tier Integration" — tier gate enforced at API level.
	if err := gitprovider.ValidateTier(req.Tier, req.Files); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	// Governing: SPEC-0018 REQ-12 "Dry Run Mode" — log proposed PR details without executing git operations.
	if s.cfg.DryRun {
		log.Printf("[PR] Dry run: would create PR '%s' for %s/%s", req.Title, req.RepoOwner, req.RepoName)
		writeJSON(w, http.StatusOK, APICreatePRResponse{DryRun: true})
		return
	}

	repo := gitprovider.RepoRef{Owner: req.RepoOwner, Name: req.RepoName, CloneURL: req.CloneURL}
	provider, err := s.registry.Resolve(repo, nil)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no git provider for repo: %v", err))
		return
	}

	branch := gitprovider.GenerateBranchName(req.ChangeType, req.Title)
	baseBranch := req.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	if err := provider.CreateBranch(r.Context(), repo, branch, baseBranch); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create branch: %v", err))
		return
	}

	if err := provider.CommitFiles(r.Context(), repo, branch, req.Title, req.Files); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("commit files: %v", err))
		return
	}

	prReq := gitprovider.PRRequest{
		Title:      req.Title,
		Body:       req.Body,
		HeadBranch: branch,
		BaseBranch: baseBranch,
		Labels:     []string{"claude-ops", "automated"},
	}
	result, err := provider.CreatePR(r.Context(), repo, prReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create PR: %v", err))
		return
	}

	log.Printf("[PR] Created PR #%d at %s for %s/%s", result.Number, result.URL, req.RepoOwner, req.RepoName)
	writeJSON(w, http.StatusCreated, APICreatePRResponse{
		Number: result.Number,
		URL:    result.URL,
		Branch: branch,
	})
}

// handleAPIListPRs lists open pull requests for a repository filtered by the claude-ops label.
func (s *Server) handleAPIListPRs(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("repo_owner")
	name := r.URL.Query().Get("repo_name")
	cloneURL := r.URL.Query().Get("clone_url")

	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "repo_owner and repo_name query params required")
		return
	}

	repo := gitprovider.RepoRef{Owner: owner, Name: name, CloneURL: cloneURL}
	provider, err := s.registry.Resolve(repo, nil)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no git provider: %v", err))
		return
	}

	filter := gitprovider.PRFilter{Labels: []string{"claude-ops"}}
	prs, err := provider.ListOpenPRs(r.Context(), repo, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list PRs: %v", err))
		return
	}

	apiPRs := make([]APIPRSummary, len(prs))
	for i, pr := range prs {
		apiPRs[i] = APIPRSummary{
			Number: pr.Number,
			Title:  pr.Title,
			Files:  pr.Files,
		}
	}
	writeJSON(w, http.StatusOK, APIPRListResponse{PRs: apiPRs})
}

