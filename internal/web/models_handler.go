package web

import (
	"net/http"
	"os"

	"github.com/joestump/claude-ops/internal/models"
)

// Governing: SPEC-0035 REQ "Upstream Model Query" — the upstream gateway and its
// credential are configured via the ANTHROPIC_BASE_URL / ANTHROPIC_API_KEY
// environment variables (the same ones the Claude Code CLI subprocess uses).
// Resolved lazily so runtime changes are honored and the key is never stored.
func upstreamBaseURL() string { return os.Getenv("ANTHROPIC_BASE_URL") }
func upstreamAPIKey() string  { return os.Getenv("ANTHROPIC_API_KEY") }

// registerModelRoutes wires the upstream-model-discovery endpoints onto the
// server mux. It is called from New after registerRoutes so the route table in
// server.go is not modified.
// Governing: SPEC-0035 REQ "Available Models API Endpoint".
func (s *Server) registerModelRoutes() {
	s.mux.HandleFunc("GET /api/v1/models/available", s.handleAPIModelsAvailable)
	s.mux.HandleFunc("POST /api/v1/models/available/refresh", s.handleAPIModelsRefresh)
	// HTMX-driven refresh of the config-page tier model controls.
	// Governing: SPEC-0035 REQ "Configuration UI Model Selection".
	s.mux.HandleFunc("POST /config/models/refresh", s.handleConfigModelsRefresh)
}

// APIAvailableModelsResponse is the JSON payload for GET /api/v1/models/available.
// It carries the discovered model IDs plus cache-freshness metadata so clients
// can present a selectable list and decide how to handle a stale/unavailable
// upstream.
// Governing: SPEC-0035 REQ "Available Models API Endpoint".
type APIAvailableModelsResponse struct {
	Models             []string `json:"models"`
	LastRefreshed      *string  `json:"last_refreshed"`
	DiscoveryAvailable bool     `json:"discovery_available"`
}

// handleAPIModelsAvailable returns the discovered upstream models. It refreshes
// from the gateway when the cache is stale, or immediately when ?refresh=true.
// When discovery is unavailable it returns 200 with an empty list and
// discovery_available=false rather than an error status.
// Governing: SPEC-0035 REQ "Available Models API Endpoint", REQ "Graceful Degradation".
func (s *Server) handleAPIModelsAvailable(w http.ResponseWriter, r *http.Request) {
	var res models.Result
	if r.URL.Query().Get("refresh") == "true" {
		res = s.discoverer.Refresh(r.Context())
	} else {
		res = s.discoverer.Available(r.Context())
	}
	writeJSON(w, http.StatusOK, toAPIAvailableModels(res))
}

// handleAPIModelsRefresh forces an upstream re-query and returns the refreshed
// list. Concurrent refreshes collapse to a single in-flight upstream call.
// Governing: SPEC-0035 REQ "Available Models API Endpoint" (explicit refresh),
// REQ "Rate Limiting" (single-flight).
func (s *Server) handleAPIModelsRefresh(w http.ResponseWriter, r *http.Request) {
	res := s.discoverer.Refresh(r.Context())
	writeJSON(w, http.StatusOK, toAPIAvailableModels(res))
}

// configPageData is the template payload for config.html. It carries the
// existing runtime config plus the discovered upstream models (SPEC-0035) so the
// tier fields can render as a dropdown, falling back to free-text when discovery
// is unavailable.
// Governing: SPEC-0008 REQ-10 (config page), SPEC-0035 REQ "Configuration UI Model Selection".
type configPageData struct {
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
	// SPEC-0035 fields:
	AvailableModels    []string
	DiscoveryAvailable bool
	// UpstreamBaseURL is the configured upstream gateway (ANTHROPIC_BASE_URL)
	// that model discovery queries. Shown read-only on the config page so the
	// operator can see which endpoint discovery is sourced from. Empty when the
	// Anthropic default is in use (no gateway configured).
	UpstreamBaseURL string
}

// buildConfigPageData assembles the config-page template payload, including the
// discovered upstream model list. Discovery failure never blocks rendering: on
// an unavailable upstream, AvailableModels is empty and DiscoveryAvailable is
// false, and the template falls back to free-text inputs.
// Governing: SPEC-0035 REQ "Configuration UI Model Selection", REQ "Graceful Degradation".
func (s *Server) buildConfigPageData(r *http.Request, saved bool) configPageData {
	disc := s.discoverer.Available(r.Context())
	return configPageData{
		Interval:              s.cfg.Interval,
		Tier1Model:            s.cfg.Tier1Model,
		Tier2Model:            s.cfg.Tier2Model,
		Tier3Model:            s.cfg.Tier3Model,
		DryRun:                s.cfg.DryRun,
		Saved:                 saved,
		StateDir:              s.cfg.StateDir,
		ResultsDir:            s.cfg.ResultsDir,
		ReposDir:              s.cfg.ReposDir,
		MaxTier:               s.cfg.MaxTier,
		BrowserAllowedOrigins: s.cfg.BrowserAllowedOrigins,
		ChatAPIKey:            os.Getenv("CLAUDEOPS_CHAT_API_KEY"),
		AvailableModels:       disc.Models,
		DiscoveryAvailable:    disc.Available,
		UpstreamBaseURL:       upstreamBaseURL(),
	}
}

// handleConfigModelsRefresh forces an upstream re-query and re-renders just the
// per-tier model controls (the "modelFields" template block) for an HTMX swap.
// It preserves the operator's current (possibly unsaved) tier selections from
// the submitted form so a refresh does not discard in-progress edits.
// Governing: SPEC-0035 REQ "Configuration UI Model Selection".
func (s *Server) handleConfigModelsRefresh(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	disc := s.discoverer.Refresh(r.Context())
	data := configPageData{
		Tier1Model:         firstNonEmpty(r.FormValue("tier1_model"), s.cfg.Tier1Model),
		Tier2Model:         firstNonEmpty(r.FormValue("tier2_model"), s.cfg.Tier2Model),
		Tier3Model:         firstNonEmpty(r.FormValue("tier3_model"), s.cfg.Tier3Model),
		AvailableModels:    disc.Models,
		DiscoveryAvailable: disc.Available,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "modelFields", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func toAPIAvailableModels(res models.Result) APIAvailableModelsResponse {
	out := APIAvailableModelsResponse{
		Models:             res.Models,
		DiscoveryAvailable: res.Available,
	}
	if out.Models == nil {
		out.Models = []string{}
	}
	if !res.LastRefreshed.IsZero() {
		ts := res.LastRefreshed.UTC().Format("2006-01-02T15:04:05Z07:00")
		out.LastRefreshed = &ts
	}
	return out
}
