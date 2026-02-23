package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/joestump/claude-ops/api"
	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// eventBadgeRe matches [EVENT:level] and [EVENT:level:service] markers in rendered HTML.
// Message capture stops before the next bracket marker or HTML tag.
var eventBadgeRe = regexp.MustCompile(`\[EVENT:(info|warning|critical)(?::([a-zA-Z0-9_-]+))?\]\s*([^\[<]+)`)

// memoryBadgeRe matches [MEMORY:category] and [MEMORY:category:service] markers in rendered HTML.
var memoryBadgeRe = regexp.MustCompile(`\[MEMORY:([a-z]+)(?::([a-zA-Z0-9_-]+))?\]\s*([^\[<]+)`)

// cooldownBadgeRe matches [COOLDOWN:action:service] result — message markers in rendered HTML.
// action is "restart" or "redeployment", service is required, result is "success" or "failure".
var cooldownBadgeRe = regexp.MustCompile(`\[COOLDOWN:(restart|redeployment):([a-zA-Z0-9_-]+)\]\s*(success|failure)\s*[—–-]\s*([^\[<]+)`)

// Governing: SPEC-0008 REQ-14 — Static Asset Embedding (templates embedded via go:embed)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// SSEHub is the interface the web server uses to subscribe to session streams.
type SSEHub interface {
	Subscribe(sessionID int) (<-chan string, func())
}

// SessionTrigger is the interface for triggering ad-hoc sessions.
// Governing: SPEC-0024 REQ-3 (model field maps to starting tier), ADR-0020 (Tier Selection)
type SessionTrigger interface {
	TriggerAdHoc(prompt string, startTier int) (int64, error)
	IsRunning() bool
}

// ServerOption configures optional Server features.
type ServerOption func(*Server)

// WithRawHub sets the raw NDJSON event hub for OpenAI streaming.
// Governing: SPEC-0024 REQ-5 (Streaming Response), ADR-0020
func WithRawHub(h SSEHub) ServerOption {
	return func(s *Server) { s.rawHub = h }
}

// Governing: SPEC-0008 REQ-2 (Web Server — HTTP on configurable port, default 8080)
// Server is the HTTP server for the Claude Ops dashboard.
type Server struct {
	cfg    *config.Config
	hub    SSEHub
	rawHub SSEHub // Governing: SPEC-0024 REQ-5 — raw NDJSON event hub for OpenAI streaming
	db     *db.DB
	mgr    SessionTrigger
	mux    *http.ServeMux
	tmpl   *template.Template
	server *http.Server
}

// New creates a new web server. Pass nil for hub if SSE streaming is not yet available.
// rawHub is the raw NDJSON event hub for OpenAI streaming (may be nil).
func New(cfg *config.Config, hub SSEHub, database *db.DB, mgr SessionTrigger, opts ...ServerOption) *Server {
	s := &Server{
		cfg: cfg,
		hub: hub,
		db:  database,
		mgr: mgr,
		mux: http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}

	s.parseTemplates()
	s.registerRoutes()

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.DashboardPort),
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE needs no write timeout
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Start begins serving HTTP requests. It blocks until the server is shut down.
func (s *Server) Start() error {
	log.Printf("dashboard listening on %s", s.server.Addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) parseTemplates() {
	funcMap := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			return t.Format("2006-01-02 15:04:05 UTC")
		},
		"fmtTimePtr": func(t *time.Time) string {
			if t == nil {
				return "--"
			}
			return t.Format("2006-01-02 15:04:05 UTC")
		},
		"fmtDuration": func(start time.Time, end *time.Time) string {
			if end == nil {
				d := time.Since(start).Truncate(time.Second)
				return d.String() + " (running)"
			}
			return end.Sub(start).Truncate(time.Second).String()
		},
		"statusClass": func(status string) string {
			switch status {
			case "healthy", "completed":
				return "status-healthy"
			case "degraded", "escalated":
				return "status-degraded"
			case "down", "failed", "timed_out":
				return "status-down"
			case "running":
				return "status-running"
			default:
				return "status-unknown"
			}
		},
		"statusDot": func(status string) string {
			switch status {
			case "healthy", "completed":
				return "dot-healthy"
			case "degraded", "escalated":
				return "dot-degraded"
			case "down", "failed", "timed_out":
				return "dot-down"
			case "running":
				return "dot-running"
			default:
				return "dot-unknown"
			}
		},
		"statusText": func(status string) string {
			switch status {
			case "healthy", "completed":
				return "text-green"
			case "degraded", "escalated":
				return "text-yellow"
			case "down", "failed", "timed_out":
				return "text-red"
			case "running":
				return "text-blue"
			default:
				return "text-muted"
			}
		},
		"tierLabel": func(tier int) string {
			switch tier {
			case 1:
				return "Observe"
			case 2:
				return "Investigate"
			case 3:
				return "Remediate"
			default:
				return fmt.Sprintf("Tier %d", tier)
			}
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"intVal": func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		},
		"intPtr": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		// Governing: SPEC-0011 "Markdown Response Rendering" — server-side goldmark rendering.
		"renderMarkdown": func(md string) template.HTML {
			gm := goldmark.New(
				goldmark.WithExtensions(
					extension.GFM, // tables, strikethrough, autolinks, task lists
				),
			)
			var buf bytes.Buffer
			if err := gm.Convert([]byte(md), &buf); err != nil {
				return template.HTML(template.HTMLEscapeString(md))
			}
			// Replace [EVENT:level] and [EVENT:level:service] markers with dashboard badges.
			html := eventBadgeRe.ReplaceAllStringFunc(buf.String(), func(match string) string {
				m := eventBadgeRe.FindStringSubmatch(match)
				level, service, msg := m[1], m[2], m[3]
				cls := "level-info"
				switch level {
				case "warning":
					cls = "level-warning"
				case "critical":
					cls = "level-critical"
				}
				badge := `<span class="badge-pill ` + cls + `">` + level + `</span>`
				if service != "" {
					badge += ` <span class="text-xs font-mono text-muted bg-surface px-2 py-0.5 rounded">` + template.HTMLEscapeString(service) + `</span>`
				}
				return `<div class="badge-line">` + badge + ` ` + msg + `</div>`
			})
			// Replace [MEMORY:category] and [MEMORY:category:service] markers with brain badges.
			html = memoryBadgeRe.ReplaceAllStringFunc(html, func(match string) string {
				m := memoryBadgeRe.FindStringSubmatch(match)
				category, service, msg := m[1], m[2], m[3]
				badge := `<span class="badge-pill level-memory">🧠 ` + template.HTMLEscapeString(category) + `</span>`
				if service != "" {
					badge += ` <span class="text-xs font-mono text-muted bg-surface px-2 py-0.5 rounded">` + template.HTMLEscapeString(service) + `</span>`
				}
				return `<div class="badge-line">` + badge + ` ` + msg + `</div>`
			})
			// Replace [COOLDOWN:action:service] markers with cooldown badges.
			html = cooldownBadgeRe.ReplaceAllStringFunc(html, func(match string) string {
				m := cooldownBadgeRe.FindStringSubmatch(match)
				action, service, result, msg := m[1], m[2], m[3], m[4]
				cls := "level-cooldown"
				if result == "failure" {
					cls = "level-critical"
				}
				badge := `<span class="badge-pill ` + cls + `">` + template.HTMLEscapeString(action) + `</span>`
				badge += ` <span class="text-xs font-mono text-muted bg-surface px-2 py-0.5 rounded">` + template.HTMLEscapeString(service) + `</span>`
				resultBadge := `<span class="badge-pill ` + cls + ` text-xs">` + result + `</span>`
				return `<div class="badge-line">` + badge + ` ` + resultBadge + ` ` + msg + `</div>`
			})
			return template.HTML(html)
		},
		"levelClass": func(level string) string {
			switch level {
			case "info":
				return "level-info"
			case "warning":
				return "level-warning"
			case "critical":
				return "level-critical"
			default:
				return "level-info"
			}
		},
		"fmtCost": func(p *float64) string {
			if p == nil {
				return "--"
			}
			return fmt.Sprintf("$%.4f", *p)
		},
		"chainCostDiffers": func(chainCost float64, costUSD *float64) bool {
			if costUSD == nil {
				return chainCost > 0
			}
			diff := chainCost - *costUSD
			if diff < 0 {
				diff = -diff
			}
			return diff > 0.0001
		},
		"fmtFloat": func(v float64) string {
			return fmt.Sprintf("$%.4f", v)
		},
		"fmtPct": func(v float64) string {
			return fmt.Sprintf("%.0f%%", v*100)
		},
		"fmtInterval": func(seconds int) string {
			if seconds < 60 {
				return fmt.Sprintf("%ds", seconds)
			}
			if seconds < 3600 {
				m := seconds / 60
				if seconds%60 == 0 {
					return fmt.Sprintf("%dm", m)
				}
				return fmt.Sprintf("%dm %ds", m, seconds%60)
			}
			h := seconds / 3600
			rem := seconds % 3600
			if rem == 0 {
				return fmt.Sprintf("%dh", h)
			}
			return fmt.Sprintf("%dh %dm", h, rem/60)
		},
		"fmtMs": func(p *int64) string {
			if p == nil {
				return "--"
			}
			d := time.Duration(*p) * time.Millisecond
			if d < time.Second {
				return fmt.Sprintf("%dms", *p)
			}
			return d.Truncate(time.Second).String()
		},
		"fmtMsVal": func(ms int64) string {
			if ms == 0 {
				return "--"
			}
			d := time.Duration(ms) * time.Millisecond
			if d < time.Second {
				return fmt.Sprintf("%dms", ms)
			}
			return d.Truncate(time.Second).String()
		},
		"fmtCostVal": func(f float64) string {
			return fmt.Sprintf("$%.4f", f)
		},
		// maskAPIKey returns a masked version of an API key for display:
		// first 8 chars + "…" + last 4 chars. Returns the full key if
		// it's too short to meaningfully mask.
		"maskAPIKey": func(key string) string {
			const head, tail = 8, 4
			if len(key) <= head+tail+3 {
				return key
			}
			return key[:head] + "…" + key[len(key)-tail:]
		},
	}

	s.tmpl = template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"),
	)
}

// Governing: SPEC-0008 REQ-14 — Static Asset Embedding (static files served from embed.FS)
// Governing: SPEC-0008 REQ-10 — dashboard pages: overview, session view, cooldowns, config, events, memories.
// Governing: SPEC-0013 "Remove Services UI" — no /services routes registered
func (s *Server) registerRoutes() {
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /sessions", s.handleSessions)
	s.mux.HandleFunc("GET /sessions/{id}", s.handleSession)
	s.mux.HandleFunc("GET /sessions/{id}/stream", s.handleSessionStream)
	s.mux.HandleFunc("GET /events", s.handleEvents)
	s.mux.HandleFunc("GET /memories", s.handleMemories)
	s.mux.HandleFunc("POST /memories", s.handleMemoryCreate)
	s.mux.HandleFunc("POST /memories/{id}/update", s.handleMemoryUpdate)
	s.mux.HandleFunc("POST /memories/{id}/delete", s.handleMemoryDelete)
	s.mux.HandleFunc("GET /cooldowns", s.handleCooldowns)
	s.mux.HandleFunc("GET /config", s.handleConfigGet)
	s.mux.HandleFunc("POST /config", s.handleConfigPost)
	s.mux.HandleFunc("POST /sessions/trigger", s.handleTriggerSession)

	// API v1
	// Governing: SPEC-0017 REQ-1 "API Route Registration" — all /api/v1/ routes on same ServeMux
	// Governing: SPEC-0017 REQ-14 "Health Endpoint"
	// Governing: SPEC-0017 REQ-19 "Backward Compatibility" — HTML routes above remain unchanged; all endpoints under /api/v1 prefix
	s.mux.HandleFunc("GET /api/v1/health", s.handleAPIHealth)
	// Governing: SPEC-0017 REQ-3, REQ-4, REQ-5 — session list, detail, and trigger endpoints
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleAPIListSessions)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleAPIGetSession)
	s.mux.HandleFunc("POST /api/v1/sessions/trigger", s.handleAPITriggerSession)
	// Governing: SPEC-0017 REQ-6 through REQ-11 — events, memories CRUD, and cooldowns endpoints
	s.mux.HandleFunc("GET /api/v1/events", s.handleAPIListEvents)
	s.mux.HandleFunc("GET /api/v1/memories", s.handleAPIListMemories)
	s.mux.HandleFunc("POST /api/v1/memories", s.handleAPICreateMemory)
	s.mux.HandleFunc("PUT /api/v1/memories/{id}", s.handleAPIUpdateMemory)
	s.mux.HandleFunc("DELETE /api/v1/memories/{id}", s.handleAPIDeleteMemory)
	s.mux.HandleFunc("GET /api/v1/cooldowns", s.handleAPIListCooldowns)
	// Governing: SPEC-0017 REQ-12 "Config Get Endpoint", REQ-13 "Config Update Endpoint"
	s.mux.HandleFunc("GET /api/v1/config", s.handleAPIGetConfig)
	s.mux.HandleFunc("PUT /api/v1/config", s.handleAPIUpdateConfig)
	// Governing: SPEC-0023 REQ-9 — PR API endpoints removed; PR operations are now skill-based (git-pr.md).

	// Governing: SPEC-0024 REQ-1 (Endpoint Registration), ADR-0020
	// OpenAI-compatible chat endpoint — /v1/ prefix matches OpenAI base URL convention
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("GET /v1/models", s.handleModels)

	// Ollama-compatible endpoints — clients that speak the Ollama protocol
	// can point their base URL at this server without any changes.
	s.mux.HandleFunc("GET /api/version", s.handleOllamaVersion)
	s.mux.HandleFunc("GET /api/tags", s.handleOllamaTags)
	s.mux.HandleFunc("POST /api/chat", s.handleOllamaChat)
	s.mux.HandleFunc("POST /api/generate", s.handleOllamaGenerate)

	// Governing: SPEC-0017 REQ-15 "OpenAPI Specification File" — embedded YAML at /api/openapi.yaml, REQ-16 "Swagger UI"
	s.mux.HandleFunc("GET /api/openapi.yaml", s.handleOpenAPISpec)
	// Governing: SPEC-0017 REQ-16 "Swagger UI" — embedded static assets at /api/docs/
	swaggerSub, _ := fs.Sub(api.SwaggerUIFS, "swagger-ui")
	s.mux.Handle("GET /api/docs/", http.StripPrefix("/api/docs/", http.FileServer(http.FS(swaggerSub))))
}

// render executes a template. If HX-Request header is set, render just the
// content block; otherwise render the full layout wrapping the content.
// Governing: SPEC-0008 REQ-3 — HTMX-Based Interactivity (partial rendering for HX-Request)
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Render the content template to a buffer.
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		_, _ = w.Write(buf.Bytes())
		return
	}

	layoutData := struct {
		Page    string
		Content template.HTML
		Version string
	}{
		Page:    name,
		Content: template.HTML(buf.String()),
		Version: config.Version,
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout.html", layoutData); err != nil {
		log.Printf("layout+%s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// Governing: SPEC-0017 REQ-15 "OpenAPI Specification File" — serves embedded openapi.yaml with YAML content type
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(api.OpenAPISpec)
}
