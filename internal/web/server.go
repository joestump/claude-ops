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
	"time"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// SSEHub is the interface the web server uses to subscribe to session streams.
type SSEHub interface {
	Subscribe(sessionID int) (<-chan string, func())
}

// SessionTrigger is the interface for triggering ad-hoc sessions.
type SessionTrigger interface {
	TriggerAdHoc(prompt string) (int64, error)
	IsRunning() bool
}

// Server is the HTTP server for the Claude Ops dashboard.
type Server struct {
	cfg    *config.Config
	hub    SSEHub
	db     *db.DB
	mgr    SessionTrigger
	mux    *http.ServeMux
	tmpl   *template.Template
	server *http.Server
}

// New creates a new web server. Pass nil for hub if SSE streaming is not yet available.
func New(cfg *config.Config, hub SSEHub, database *db.DB, mgr SessionTrigger) *Server {
	s := &Server{
		cfg: cfg,
		hub: hub,
		db:  database,
		mgr: mgr,
		mux: http.NewServeMux(),
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
			case "degraded":
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
			case "degraded":
				return "dot-degraded"
			case "down", "failed", "timed_out":
				return "dot-down"
			case "running":
				return "dot-running"
			default:
				return "dot-unknown"
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
			return template.HTML(buf.String())
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
		"fmtFloat": func(v float64) string {
			return fmt.Sprintf("$%.4f", v)
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
	}

	s.tmpl = template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"),
	)
}

func (s *Server) registerRoutes() {
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /sessions", s.handleSessions)
	s.mux.HandleFunc("GET /sessions/{id}", s.handleSession)
	s.mux.HandleFunc("GET /sessions/{id}/stream", s.handleSessionStream)
	s.mux.HandleFunc("GET /events", s.handleEvents)
	s.mux.HandleFunc("GET /cooldowns", s.handleCooldowns)
	s.mux.HandleFunc("GET /config", s.handleConfigGet)
	s.mux.HandleFunc("POST /config", s.handleConfigPost)
	s.mux.HandleFunc("POST /sessions/trigger", s.handleTriggerSession)
}

// render executes a template. If HX-Request header is set, render just the
// content block; otherwise render the full layout wrapping the content.
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
		w.Write(buf.Bytes())
		return
	}

	layoutData := struct {
		Page    string
		Content template.HTML
	}{
		Page:    name,
		Content: template.HTML(buf.String()),
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout.html", layoutData); err != nil {
		log.Printf("layout+%s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
