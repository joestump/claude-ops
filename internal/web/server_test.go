package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/gitprovider"
	"github.com/joestump/claude-ops/internal/hub"
)

type mockTrigger struct {
	running    bool
	nextID     int64
	nextErr    error
	lastPrompt string // captures the prompt passed to TriggerAdHoc
}

func (m *mockTrigger) TriggerAdHoc(prompt string) (int64, error) {
	m.lastPrompt = prompt
	if m.nextErr != nil {
		return 0, m.nextErr
	}
	return m.nextID, nil
}

func (m *mockTrigger) IsRunning() bool { return m.running }

type testEnv struct {
	srv     *Server
	hub     *hub.Hub
	trigger *mockTrigger
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithTrigger(t, &mockTrigger{nextErr: fmt.Errorf("not implemented in test")})
}

func newTestEnvWithTrigger(t *testing.T, trigger *mockTrigger) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	cfg := &config.Config{
		Interval:      3600,
		Tier1Model:    "haiku",
		Tier2Model:    "sonnet",
		Tier3Model:    "opus",
		MaxTier:       3,
		StateDir:      t.TempDir(),
		ResultsDir:    t.TempDir(),
		ReposDir:      t.TempDir(),
		DashboardPort: 0,
	}

	h := hub.New()
	registry := gitprovider.NewRegistry()
	return &testEnv{
		srv:     New(cfg, h, database, trigger, registry),
		hub:     h,
		trigger: trigger,
	}
}

func TestIndexReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /: expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET /: expected text/html content type, got %q", ct)
	}
}

// Governing: SPEC-0021 REQ "TL;DR Page Rendering"
func TestTLDRPageRendering(t *testing.T) {
	e := newTestEnv(t)

	// Insert a session with a summary.
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/test.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if err := e.srv.db.UpdateSessionSummary(id, "All 6 services healthy. No issues found."); err != nil {
		t.Fatalf("UpdateSessionSummary: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /: expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Page heading must read "TL;DR".
	if !strings.Contains(body, "TL;DR") {
		t.Error("expected page to contain 'TL;DR' heading")
	}

	// Sidebar nav label must show emoji + TL;DR.
	if !strings.Contains(body, "\xf0\x9f\xa4\x94") { // U+1F914 thinking face emoji
		t.Error("expected sidebar to contain thinking face emoji")
	}

	// Summary text should be displayed.
	if !strings.Contains(body, "All 6 services healthy. No issues found.") {
		t.Error("expected page to display session summary text")
	}
}

// Governing: SPEC-0021 REQ "TL;DR Page Rendering"
func TestTLDRFallbackToResponse(t *testing.T) {
	e := newTestEnv(t)

	// Insert a session WITHOUT a summary, but with a response.
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/test.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if err := e.srv.db.UpdateSessionResult(id, "## Health Report\nAll services healthy.", 0.001, 2, 5000); err != nil {
		t.Fatalf("UpdateSessionResult: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /: expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should fall back to rendered markdown response when summary is NULL.
	if !strings.Contains(body, "Health Report") {
		t.Error("expected page to fall back to full response when summary is NULL")
	}
}

func TestSessionsReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/sessions", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /sessions: expected 200, got %d", w.Code)
	}
}

func TestSessionNotFound(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/sessions/999", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /sessions/999: expected 404, got %d", w.Code)
	}
}

func TestEventsReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /events: expected 200, got %d", w.Code)
	}
}

func TestServicesRouteRemoved(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/services", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("GET /services: expected non-200 (route removed), got %d", w.Code)
	}
}

func TestCooldownsReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/cooldowns", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /cooldowns: expected 200, got %d", w.Code)
	}
}

func TestConfigGetReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /config: expected 200, got %d", w.Code)
	}
}

func TestConfigPostUpdatesValues(t *testing.T) {
	e := newTestEnv(t)

	form := url.Values{}
	form.Set("interval", "1800")
	form.Set("tier1_model", "sonnet")
	form.Set("tier2_model", "opus")
	form.Set("tier3_model", "haiku")
	form.Set("dry_run", "on")

	req := httptest.NewRequest("POST", "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /config: expected 200, got %d", w.Code)
	}

	// Verify config was updated in memory.
	if e.srv.cfg.Interval != 1800 {
		t.Errorf("expected interval 1800, got %d", e.srv.cfg.Interval)
	}
	if e.srv.cfg.Tier1Model != "sonnet" {
		t.Errorf("expected tier1 sonnet, got %q", e.srv.cfg.Tier1Model)
	}
	if !e.srv.cfg.DryRun {
		t.Error("expected dry_run true")
	}

	// Verify config was persisted to DB.
	val, err := e.srv.db.GetConfig("interval", "0")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if val != "1800" {
		t.Errorf("expected DB interval '1800', got %q", val)
	}
}

func TestHTMXPartialRendering(t *testing.T) {
	e := newTestEnv(t)

	// Full page request should include layout (DOCTYPE).
	reqFull := httptest.NewRequest("GET", "/sessions", nil)
	wFull := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wFull, reqFull)
	if !strings.Contains(wFull.Body.String(), "<!DOCTYPE html>") {
		t.Error("full page response should contain DOCTYPE")
	}

	// HTMX partial request should NOT include layout.
	reqPartial := httptest.NewRequest("GET", "/sessions", nil)
	reqPartial.Header.Set("HX-Request", "true")
	wPartial := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wPartial, reqPartial)
	if strings.Contains(wPartial.Body.String(), "<!DOCTYPE html>") {
		t.Error("HTMX partial response should not contain DOCTYPE")
	}
	if !strings.Contains(wPartial.Body.String(), "Sessions") {
		t.Error("HTMX partial response should contain page content")
	}
}

func TestSSEContentType(t *testing.T) {
	e := newTestEnv(t)

	// Publish then close the session so Subscribe returns a closed channel
	// and the SSE handler finishes quickly.
	e.hub.Publish(1, "test")
	e.hub.Close(1)

	req := httptest.NewRequest("GET", "/sessions/1/stream", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("GET /sessions/1/stream: expected text/event-stream, got %q", ct)
	}
}

func TestSessionInvalidID(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/sessions/abc", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GET /sessions/abc: expected 400, got %d", w.Code)
	}
}

func TestStaticFiles(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("expected text/css, got %q", w.Header().Get("Content-Type"))
	}
}

// insertTestSession creates a session in the test DB and returns its ID.
func insertTestSession(t *testing.T, e *testEnv, status string) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	s := &db.Session{
		Tier:       1,
		Model:      "sonnet",
		PromptFile: "/tmp/test.md",
		Status:     status,
		StartedAt:  now,
	}
	id, err := e.srv.db.InsertSession(s)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return id
}

func TestSessionWithResponseRendersMarkdown(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// Add response with markdown content.
	err := e.srv.db.UpdateSessionResult(id, "## Health Report\n\nAll **3 services** are healthy.\n\n- caddy: `running`\n- postgres: `running`\n- redis: `running`\n", 0.0523, 4, 12345)
	if err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Response section should be present.
	if !strings.Contains(body, "Response") {
		t.Error("response section heading missing")
	}

	// Markdown should be rendered as HTML, not raw text.
	if !strings.Contains(body, "<h2>Health Report</h2>") {
		t.Error("expected markdown heading rendered as <h2>")
	}
	if !strings.Contains(body, "<strong>3 services</strong>") {
		t.Error("expected bold text rendered as <strong>")
	}
	if !strings.Contains(body, "<code>running</code>") {
		t.Error("expected inline code rendered as <code>")
	}

	// Prose class should wrap the rendered markdown.
	if !strings.Contains(body, `class="card-base prose"`) {
		t.Error("expected prose class on response card")
	}
}

func TestSessionWithoutResponseOmitsSection(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// No UpdateSessionResult call — response stays NULL.
	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Session header should be present.
	if !strings.Contains(body, fmt.Sprintf("Session #%d", id)) {
		t.Error("session header missing")
	}

	// Response section should NOT be present.
	if strings.Contains(body, `class="card-base prose"`) {
		t.Error("response section should not appear when response is empty")
	}

	// Activity Log should still be present.
	if !strings.Contains(body, "Activity Log") {
		t.Error("activity log section should be present")
	}
}

func TestSessionMetadataDisplaysCostTurnsAPITime(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	err := e.srv.db.UpdateSessionResult(id, "done", 0.1234, 7, 45000)
	if err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Cost should be formatted.
	if !strings.Contains(body, "$0.1234") {
		t.Error("expected formatted cost $0.1234")
	}

	// Turns should be displayed.
	if !strings.Contains(body, "Turns") {
		t.Error("expected Turns label in metadata")
	}
	if !strings.Contains(body, ">7<") {
		t.Error("expected turns value 7")
	}

	// API Time should be formatted as duration.
	if !strings.Contains(body, "API Time") {
		t.Error("expected API Time label in metadata")
	}
	if !strings.Contains(body, "45s") {
		t.Error("expected API time 45s")
	}
}

func TestSessionMetadataHiddenWhenNil(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "running")

	// No result data — cost/turns/API time should be nil.
	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Tier and Model should always show.
	if !strings.Contains(body, "Tier") {
		t.Error("expected Tier label")
	}
	if !strings.Contains(body, "sonnet") {
		t.Error("expected model name")
	}

	// Cost/Turns/API Time should be hidden when nil.
	if strings.Contains(body, "Cost") {
		t.Error("cost should not appear when nil")
	}
	if strings.Contains(body, "Turns") {
		t.Error("turns should not appear when nil")
	}
	if strings.Contains(body, "API Time") {
		t.Error("API time should not appear when nil")
	}
}

func TestSessionLogFileFormattedWithFormatStreamEvent(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// Create a temp NDJSON log file.
	logFile := filepath.Join(t.TempDir(), "session.log")
	lines := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"docker ps"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"CONTAINER ID  IMAGE  STATUS"}]}}`,
		`{"type":"result","is_error":false,"result":"All good.","num_turns":2,"total_cost_usd":0.01,"duration_ms":5000}`,
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	// Update session with log file path.
	ended := time.Now().UTC().Format(time.RFC3339)
	exitCode := 0
	if err := e.srv.db.UpdateSession(id, "completed", &ended, &exitCode, &logFile); err != nil {
		t.Fatalf("update session: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should contain formatted HTML output, not raw JSON.
	if strings.Contains(body, `"type":"system"`) {
		t.Error("raw JSON should not appear in activity log")
	}
	if !strings.Contains(body, "term-marker") {
		t.Error("expected term-marker class for session markers")
	}
	if !strings.Contains(body, "session started") {
		t.Error("expected session started marker")
	}
	if !strings.Contains(body, "term-tool-badge") {
		t.Error("expected term-tool-badge class for tool invocations")
	}
	if !strings.Contains(body, "Bash") {
		t.Error("expected Bash tool name in output")
	}
	if !strings.Contains(body, "term-result-content") {
		t.Error("expected term-result-content class for tool results")
	}
	if !strings.Contains(body, "session complete") {
		t.Error("expected session complete marker")
	}

	// Log file path should be shown.
	if !strings.Contains(body, logFile) {
		t.Error("expected log file path in output")
	}
}

func TestSessionRunningShowsSSEStream(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "running")

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Running sessions should have SSE attributes.
	if !strings.Contains(body, `hx-ext="sse"`) {
		t.Error("expected hx-ext=sse for running session")
	}
	if !strings.Contains(body, `sse-connect=`) {
		t.Error("expected sse-connect attribute for running session")
	}

	// Should have follow button.
	if !strings.Contains(body, `follow-btn`) {
		t.Error("running session should have follow button")
	}
}

func TestSessionCompletedShowsStaticLog(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Completed sessions should have static terminal output (id="activity-log").
	if !strings.Contains(body, `id="activity-log"`) {
		t.Error("completed session should have activity-log terminal block")
	}

	// Should NOT have SSE attributes.
	if strings.Contains(body, `hx-ext="sse"`) {
		t.Error("completed session should not have SSE streaming")
	}

	// Should NOT have follow button.
	if strings.Contains(body, `follow-btn`) {
		t.Error("completed session should not have follow button")
	}
}

func TestSessionPageLayoutOrder(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	err := e.srv.db.UpdateSessionResult(id, "## Report\n\nHealthy.", 0.05, 3, 10000)
	if err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	// Verify layout order per spec: back link → header → metadata → response → activity log → log path.
	backIdx := strings.Index(body, "All sessions")
	headerIdx := strings.Index(body, fmt.Sprintf("Session #%d", id))
	metaIdx := strings.Index(body, "meta-label")
	responseIdx := strings.Index(body, "card-base prose")
	activityIdx := strings.Index(body, "Activity Log")

	if backIdx == -1 || headerIdx == -1 || metaIdx == -1 || responseIdx == -1 || activityIdx == -1 {
		t.Fatal("missing expected page sections")
	}
	if backIdx >= headerIdx {
		t.Error("back link should appear before header")
	}
	if headerIdx >= metaIdx {
		t.Error("header should appear before metadata")
	}
	if metaIdx >= responseIdx {
		t.Error("metadata should appear before response")
	}
	if responseIdx >= activityIdx {
		t.Error("response should appear before activity log")
	}
}

func TestSessionDetailShowsEscalationChain(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Create a parent (Tier 1) and child (Tier 2) session.
	parentID, err := e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/t1.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	childID, err := e.srv.db.InsertSession(&db.Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
		ParentSessionID: &parentID,
	})
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}

	// Parent session detail should show escalation to child.
	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", parentID), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Escalation Chain") {
		t.Error("parent session should show Escalation Chain section")
	}
	if !strings.Contains(body, fmt.Sprintf("Session #%d", childID)) {
		t.Error("parent session should link to child session")
	}
	if !strings.Contains(body, "Escalated to") {
		t.Error("parent session should show 'Escalated to' text")
	}

	// Child session detail should show escalation from parent.
	req2 := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", childID), nil)
	w2 := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	body2 := w2.Body.String()
	if !strings.Contains(body2, "Escalation Chain") {
		t.Error("child session should show Escalation Chain section")
	}
	if !strings.Contains(body2, fmt.Sprintf("Session #%d", parentID)) {
		t.Error("child session should link to parent session")
	}
	if !strings.Contains(body2, "Escalated from") {
		t.Error("child session should show 'Escalated from' text")
	}
}

func TestSessionsListShowsChainIndicator(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Create a chain: Tier 1 -> Tier 2.
	parentID, err := e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/t1.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	_, err = e.srv.db.InsertSession(&db.Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
		ParentSessionID: &parentID,
	})
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}

	req := httptest.NewRequest("GET", "/sessions", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Parent session should show escalation arrow.
	if !strings.Contains(body, "&#x2191;") {
		t.Error("parent session should show escalation arrow")
	}

	// Chain members should have a colored left border class.
	if !strings.Contains(body, "chain-dot-") {
		t.Error("chain members should have chain border class for visual grouping")
	}
}

func TestMemoriesPageRenders(t *testing.T) {
	e := newTestEnv(t)

	// Insert a test memory.
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "caddy"
	_, err := e.srv.db.InsertMemory(&db.Memory{
		Service:     &svc,
		Category:    "behavior",
		Observation: "Caddy restarts fix most issues",
		Confidence:  0.85,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        1,
	})
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	req := httptest.NewRequest("GET", "/memories", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /memories: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Memories") {
		t.Error("expected page heading 'Memories'")
	}
	if !strings.Contains(body, "caddy") {
		t.Error("expected service name 'caddy' in memory table")
	}
	if !strings.Contains(body, "Caddy restarts fix most issues") {
		t.Error("expected observation text in memory table")
	}
	if !strings.Contains(body, "85%") {
		t.Error("expected confidence displayed as 85%")
	}
}

func TestMemoriesPageEmpty(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/memories", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /memories: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No memories recorded yet") {
		t.Error("expected empty state message")
	}
}

func TestMemoriesFilterByService(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	svc1 := "caddy"
	svc2 := "postgres"
	if _, err := e.srv.db.InsertMemory(&db.Memory{
		Service: &svc1, Category: "behavior", Observation: "caddy memory",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	}); err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}
	if _, err := e.srv.db.InsertMemory(&db.Memory{
		Service: &svc2, Category: "config", Observation: "postgres memory",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	}); err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}

	req := httptest.NewRequest("GET", "/memories?service=caddy", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "caddy memory") {
		t.Error("expected caddy memory in filtered results")
	}
	if strings.Contains(body, "postgres memory") {
		t.Error("postgres memory should not appear in caddy-filtered results")
	}
}

func TestMemoryCreate(t *testing.T) {
	e := newTestEnv(t)

	form := url.Values{}
	form.Set("service", "redis")
	form.Set("category", "behavior")
	form.Set("observation", "Redis needs restart after OOM")
	form.Set("confidence", "0.9")

	req := httptest.NewRequest("POST", "/memories", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /memories: expected 303, got %d", w.Code)
	}

	// Verify memory was created.
	memories, err := e.srv.db.ListMemories(nil, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}
	if memories[0].Observation != "Redis needs restart after OOM" {
		t.Errorf("unexpected observation: %q", memories[0].Observation)
	}
	if memories[0].Service == nil || *memories[0].Service != "redis" {
		t.Error("expected service 'redis'")
	}
}

func TestMemoryDelete(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	svc := "caddy"
	id, err := e.srv.db.InsertMemory(&db.Memory{
		Service:     &svc,
		Category:    "behavior",
		Observation: "test memory to delete",
		Confidence:  0.5,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        1,
	})
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	req := httptest.NewRequest("POST", fmt.Sprintf("/memories/%d/delete", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /memories/%d/delete: expected 303, got %d", id, w.Code)
	}

	// Verify memory was deleted.
	m, err := e.srv.db.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m != nil {
		t.Error("expected memory to be deleted")
	}
}

func TestMemoryUpdate(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	svc := "caddy"
	id, err := e.srv.db.InsertMemory(&db.Memory{
		Service:     &svc,
		Category:    "behavior",
		Observation: "original observation",
		Confidence:  0.5,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        1,
	})
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	form := url.Values{}
	form.Set("observation", "updated observation")
	form.Set("confidence", "0.95")
	form.Set("active", "on")

	req := httptest.NewRequest("POST", fmt.Sprintf("/memories/%d/update", id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /memories/%d/update: expected 303, got %d", id, w.Code)
	}

	// Verify memory was updated.
	m, err := e.srv.db.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m.Observation != "updated observation" {
		t.Errorf("expected updated observation, got %q", m.Observation)
	}
	if m.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", m.Confidence)
	}
}

func TestStandaloneSessionNoChainIndicator(t *testing.T) {
	e := newTestEnv(t)
	_ = insertTestSession(t, e, "completed")

	req := httptest.NewRequest("GET", "/sessions", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "(chain:") {
		t.Error("standalone session should not show chain indicator")
	}
	if strings.Contains(body, "&#x2514;") {
		t.Error("standalone session should not show indent arrow")
	}
}

// ---------------------------------------------------------------------------
// Cooldown badge rendering in markdown
// ---------------------------------------------------------------------------

func TestCooldownBadgeRendersInMarkdown(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "## Report\n\n[COOLDOWN:restart:jellyfin] success — Container restarted and healthy\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 2, 5000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Badge should be rendered with level-cooldown class.
	if !strings.Contains(body, "level-cooldown") {
		t.Error("expected level-cooldown class in rendered cooldown badge")
	}
	// Action should appear as badge text.
	if !strings.Contains(body, "restart") {
		t.Error("expected 'restart' action in cooldown badge")
	}
	// Service should appear as a service tag.
	if !strings.Contains(body, "jellyfin") {
		t.Error("expected 'jellyfin' service in cooldown badge")
	}
	// Success result badge should appear.
	if !strings.Contains(body, "success") {
		t.Error("expected 'success' result badge")
	}
	// Message should appear.
	if !strings.Contains(body, "Container restarted and healthy") {
		t.Error("expected message text in cooldown badge")
	}
	// Raw marker should NOT appear.
	if strings.Contains(body, "[COOLDOWN:") {
		t.Error("raw [COOLDOWN:...] marker should not appear in rendered output")
	}
}

func TestCooldownBadgeRedeploymentAction(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "[COOLDOWN:redeployment:caddy] success — Full redeployment via Ansible\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.05, 5, 30000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, "level-cooldown") {
		t.Error("expected level-cooldown class for redeployment badge")
	}
	if !strings.Contains(body, "redeployment") {
		t.Error("expected 'redeployment' action in badge")
	}
	if !strings.Contains(body, "caddy") {
		t.Error("expected 'caddy' service in badge")
	}
}

func TestCooldownBadgeFailureUsesRedStyling(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "[COOLDOWN:restart:postgres] failure — Container failed health check\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 1, 3000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	// Failure badges should use level-critical (red) instead of level-cooldown (teal).
	if !strings.Contains(body, "level-critical") {
		t.Error("failure cooldown badge should use level-critical class")
	}
	if !strings.Contains(body, "failure") {
		t.Error("expected 'failure' result in badge")
	}
	if !strings.Contains(body, "Container failed health check") {
		t.Error("expected failure message text")
	}
}

func TestCooldownBadgeMultipleInResponse(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "## Recovery\n\n[COOLDOWN:restart:jellyfin] success — Restarted container\n\n[COOLDOWN:restart:caddy] failure — Timed out\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.02, 3, 8000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	// Both services should render.
	if !strings.Contains(body, "jellyfin") {
		t.Error("expected jellyfin cooldown badge")
	}
	if !strings.Contains(body, "caddy") {
		t.Error("expected caddy cooldown badge")
	}

	// Success badge should use level-cooldown, failure should use level-critical.
	if !strings.Contains(body, "level-cooldown") {
		t.Error("expected level-cooldown for success badge")
	}
	if !strings.Contains(body, "level-critical") {
		t.Error("expected level-critical for failure badge")
	}
}

func TestCooldownBadgeServiceWithHyphen(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "[COOLDOWN:restart:adguard-home] success — Restarted after DNS failure\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 1, 2000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, "adguard-home") {
		t.Error("expected hyphenated service name in cooldown badge")
	}
	if !strings.Contains(body, "level-cooldown") {
		t.Error("expected level-cooldown class")
	}
}

func TestCooldownBadgeCoexistsWithEventAndMemory(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	md := "[EVENT:info:jellyfin] Service was unhealthy\n\n[COOLDOWN:restart:jellyfin] success — Container restarted\n\n[MEMORY:timing:jellyfin] Takes 60s to start after restart\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.03, 4, 15000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	// All three badge types should render.
	if !strings.Contains(body, "level-info") {
		t.Error("expected event badge with level-info")
	}
	if !strings.Contains(body, "level-cooldown") {
		t.Error("expected cooldown badge with level-cooldown")
	}
	if !strings.Contains(body, "level-memory") {
		t.Error("expected memory badge with level-memory")
	}

	// No raw markers should remain.
	if strings.Contains(body, "[EVENT:") {
		t.Error("raw [EVENT:...] marker should not appear")
	}
	if strings.Contains(body, "[COOLDOWN:") {
		t.Error("raw [COOLDOWN:...] marker should not appear")
	}
	if strings.Contains(body, "[MEMORY:") {
		t.Error("raw [MEMORY:...] marker should not appear")
	}
}

func TestCooldownBadgeInvalidActionNotRendered(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// "delete" is not a valid cooldown action — should NOT be converted to a badge.
	md := "[COOLDOWN:delete:jellyfin] success — Deleted container\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 1, 1000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	if strings.Contains(body, "level-cooldown") {
		t.Error("invalid action 'delete' should not produce a cooldown badge")
	}
}

func TestCooldownBadgeMissingServiceNotRendered(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// No service field — cooldown markers require a service.
	md := "[COOLDOWN:restart] success — Restarted something\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 1, 1000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	if strings.Contains(body, "level-cooldown") {
		t.Error("cooldown marker without service should not produce a badge")
	}
}

func TestCooldownBadgeMissingResultNotRendered(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	// No success/failure result — should not match.
	md := "[COOLDOWN:restart:jellyfin] Container restarted\n"
	if err := e.srv.db.UpdateSessionResult(id, md, 0.01, 1, 1000); err != nil {
		t.Fatalf("update session result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	body := w.Body.String()

	if strings.Contains(body, "level-cooldown") {
		t.Error("cooldown marker without success/failure result should not produce a badge")
	}
}
