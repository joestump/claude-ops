package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/hub"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	resultsDir := t.TempDir()
	return &config.Config{
		Interval:     1,
		Prompt:       "/dev/null",
		Tier1Model:   "haiku",
		Tier2Model:   "sonnet",
		Tier3Model:   "opus",
		StateDir:     t.TempDir(),
		ResultsDir:   resultsDir,
		ReposDir:     t.TempDir(),
		AllowedTools: "Bash,Read",
		DryRun:       true,
		AppriseURLs:  "",
		MCPConfig:    "/tmp/mcp.json",
	}
}

func testManager(t *testing.T) (*Manager, *config.Config) {
	t.Helper()
	cfg := testConfig(t)
	database, err := db.Open(filepath.Join(cfg.StateDir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	h := hub.New()
	return New(cfg, database, h, &CLIRunner{}), cfg
}

func TestBuildEnvContext(t *testing.T) {
	m, cfg := testManager(t)

	ctx := m.buildEnvContext()

	expected := []string{
		"CLAUDEOPS_DRY_RUN=true",
		"CLAUDEOPS_STATE_DIR=" + cfg.StateDir,
		"CLAUDEOPS_RESULTS_DIR=" + cfg.ResultsDir,
		"CLAUDEOPS_REPOS_DIR=" + cfg.ReposDir,
		"CLAUDEOPS_TIER2_MODEL=sonnet",
		"CLAUDEOPS_TIER3_MODEL=opus",
	}

	for _, e := range expected {
		if !strings.Contains(ctx, e) {
			t.Errorf("envContext missing %q; got %q", e, ctx)
		}
	}

	if strings.Contains(ctx, "CLAUDEOPS_APPRISE_URLS") {
		t.Error("envContext should not contain APPRISE_URLS when empty")
	}
}

func TestBuildEnvContextWithApprise(t *testing.T) {
	m, _ := testManager(t)
	m.cfg.AppriseURLs = "ntfy://example.com/test"

	ctx := m.buildEnvContext()

	if !strings.Contains(ctx, "CLAUDEOPS_APPRISE_URLS=ntfy://example.com/test") {
		t.Errorf("envContext should contain APPRISE_URLS; got %q", ctx)
	}
}

func TestPreSessionHookCalled(t *testing.T) {
	m, _ := testManager(t)

	called := false
	m.PreSessionHook = func() error {
		called = true
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = m.runOnce(ctx, "", "scheduled") // Will fail (no claude binary) but hook should fire.

	if !called {
		t.Error("PreSessionHook was not called")
	}
}

func TestLogFileCreated(t *testing.T) {
	m, cfg := testManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = m.runOnce(ctx, "", "scheduled")

	// Check that a log file was created in ResultsDir.
	entries, err := os.ReadDir(cfg.ResultsDir)
	if err != nil {
		t.Fatalf("read results dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "run-") && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}

	if !found {
		t.Error("no run-*.log file created in results dir")
	}
}

func TestSkipIfRunning(t *testing.T) {
	m, cfg := testManager(t)

	// Simulate a running session.
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()

	ctx := context.Background()
	err := m.runOnce(ctx, "", "scheduled")

	if err != nil {
		t.Errorf("expected nil error for skip, got %v", err)
	}

	// Verify no log file was created (session was skipped).
	entries, _ := os.ReadDir(cfg.ResultsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "run-") {
			t.Error("log file should not be created when session is skipped")
		}
	}
}

func TestRunRespectsContextCancellation(t *testing.T) {
	m, _ := testManager(t)
	m.cfg.Interval = 3600 // Long interval so we test cancellation, not tick.

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx)
	}()

	// Give it a moment to start the first runOnce (which will fail fast since
	// claude binary is not available), then cancel.
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run should return nil on context cancel, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestLogFileNameFormat(t *testing.T) {
	m, cfg := testManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	before := time.Now()
	_ = m.runOnce(ctx, "", "scheduled")

	entries, err := os.ReadDir(cfg.ResultsDir)
	if err != nil {
		t.Fatalf("read results dir: %v", err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "run-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		// Extract timestamp portion: run-YYYYMMDD-HHMMSS.log
		ts := strings.TrimPrefix(name, "run-")
		ts = strings.TrimSuffix(ts, ".log")
		parsed, err := time.ParseInLocation("20060102-150405", ts, time.Local)
		if err != nil {
			t.Errorf("log file name %q has unparseable timestamp: %v", name, err)
			continue
		}
		// Timestamp should be close to when we ran.
		if parsed.Before(before.Add(-2*time.Second)) || parsed.After(before.Add(5*time.Second)) {
			t.Errorf("log file timestamp %v is not close to run time %v", parsed, before)
		}
	}
}

func TestNewReturnsManager(t *testing.T) {
	m, cfg := testManager(t)

	if m == nil {
		t.Fatal("New returned nil")
	}
	if m.cfg != cfg {
		t.Error("Manager.cfg does not point to provided config")
	}
}

func TestBrowserAllowedOriginsInEnvContext(t *testing.T) {
	m, _ := testManager(t)
	m.cfg.BrowserAllowedOrigins = "https://sonarr.stump.rocks,https://prowlarr.stump.rocks"

	ctx := m.buildEnvContext()

	if !strings.Contains(ctx, "BROWSER_ALLOWED_ORIGINS=https://sonarr.stump.rocks,https://prowlarr.stump.rocks") {
		t.Errorf("envContext should contain BROWSER_ALLOWED_ORIGINS; got %q", ctx)
	}
}

func TestBrowserAllowedOriginsOmittedWhenEmpty(t *testing.T) {
	m, _ := testManager(t)
	// BrowserAllowedOrigins defaults to ""

	ctx := m.buildEnvContext()

	if strings.Contains(ctx, "BROWSER_ALLOWED_ORIGINS") {
		t.Errorf("envContext should not contain BROWSER_ALLOWED_ORIGINS when empty; got %q", ctx)
	}
}

func TestRedactionInStreamPipeline(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_PASS", "supersecret")

	m, cfg := testManager(t)
	// Re-create redactor after setting env var.
	m.redactor = NewRedactionFilter()

	// Mock runner that outputs a stream event containing the credential.
	m.runner = &mockRunner{
		output: `{"type":"assistant","message":{"content":[{"type":"text","text":"password is supersecret"}]}}` + "\n",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = m.runOnce(ctx, "", "scheduled")

	// Read the log file and verify redaction.
	entries, err := os.ReadDir(cfg.ResultsDir)
	if err != nil {
		t.Fatalf("read results dir: %v", err)
	}

	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "run-") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cfg.ResultsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		content := string(data)
		if strings.Contains(content, "supersecret") {
			t.Errorf("log file should not contain raw credential; got:\n%s", content)
		}
		if !strings.Contains(content, "[REDACTED:BROWSER_CRED_SONARR_PASS]") {
			t.Errorf("log file should contain redaction placeholder; got:\n%s", content)
		}
		return
	}
	t.Error("no log file found")
}

func TestRedactorInitializedInNew(t *testing.T) {
	m, _ := testManager(t)
	if m.redactor == nil {
		t.Fatal("Manager.redactor should not be nil after New()")
	}
}

func TestResultsDirUsed(t *testing.T) {
	m, cfg := testManager(t)
	// Use a subdirectory that doesn't exist yet to ensure it's created properly.
	cfg.ResultsDir = filepath.Join(t.TempDir(), "sub", "results")
	if err := os.MkdirAll(cfg.ResultsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = m.runOnce(ctx, "", "scheduled")

	entries, _ := os.ReadDir(cfg.ResultsDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "run-") {
			found = true
		}
	}
	if !found {
		t.Error("log file not created in custom results dir")
	}
}

// orphanPipeRunner simulates the bug where the CLI process exits (waitFn returns)
// but child processes (e.g. MCP servers) keep the stdout pipe fd open, preventing
// the scanner from seeing EOF. Without the fix, the session hangs forever in
// "running" state because streamDone never closes.
//
// Regression test for: session #237 stuck at "running" for 38 hours.
type orphanPipeRunner struct {
	events []string
}

func (o *orphanPipeRunner) Start(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ string) (io.ReadCloser, func() error, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	// Write all events (including the result event), then leave the pipe open
	// to simulate child processes holding the fd.
	go func() {
		for _, event := range o.events {
			_, _ = fmt.Fprintln(pw, event)
		}
		// Intentionally do NOT close pw — this simulates MCP server
		// child processes inheriting the stdout pipe fd.
	}()

	waitFn := func() error {
		// Simulate the main CLI process exiting promptly after writing output.
		// In the real bug, cmd.Wait() returns but the pipe stays open.
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	return pr, waitFn, nil
}

// TestSessionCompletesWhenPipeOrphaned verifies that a session completes even
// when child processes keep the stdout pipe open after the CLI process exits.
// This is the exact scenario that caused session #237 to hang for 38 hours.
func TestSessionCompletesWhenPipeOrphaned(t *testing.T) {
	m, cfg := testManager(t)

	events := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"All services healthy."}]}}`,
		`{"type":"result","result":"Health check complete.","is_error":false,"total_cost_usd":0.03,"num_turns":3,"duration_ms":5000}`,
	}

	m.runner = &orphanPipeRunner{events: events}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err := m.runOnce(ctx, "", "scheduled")
	elapsed := time.Since(start)

	// Session should complete within ~10 seconds (5s grace + overhead),
	// NOT hang until the 30-second context timeout.
	if elapsed > 15*time.Second {
		t.Fatalf("session took %v — likely hung on orphaned pipe (regression)", elapsed)
	}

	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}

	// Verify the session was finalized as "completed" in the DB.
	sessions, err := m.db.ListSessions(1, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions in DB after runOnce")
	}

	sess := sessions[0]
	if sess.Status != "completed" {
		t.Errorf("Status = %q, want completed", sess.Status)
	}

	// Verify the result event was still captured despite the orphaned pipe.
	if sess.CostUSD == nil {
		t.Fatal("CostUSD is nil — result event was not captured")
	}
	if *sess.CostUSD < 0.029 || *sess.CostUSD > 0.031 {
		t.Errorf("CostUSD = %f, want ~0.03", *sess.CostUSD)
	}

	// Verify log file exists.
	entries, _ := os.ReadDir(cfg.ResultsDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "run-") && strings.HasSuffix(e.Name(), ".log") {
			found = true
		}
	}
	if !found {
		t.Error("no log file created")
	}
}

// pipeRunner uses os.Pipe to simulate the real CLI subprocess pipe behavior.
// The writer closes the write-end after all events, giving the scanner EOF.
// waitFn blocks until writing is done (simulating process exit) and returns
// without closing the read-end — the manager's concurrent-wait logic handles
// pipe cleanup for orphaned-pipe scenarios (see orphanPipeRunner).
type pipeRunner struct {
	events    []string
	resultIdx int // index of the result event (-1 if none)

	// mu guards waitCalled so the test can verify ordering.
	mu         sync.Mutex
	waitCalled bool
}

func (p *pipeRunner) Start(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ string) (io.ReadCloser, func() error, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i, event := range p.events {
			// Add a yield gap before the result event to widen the race window.
			if i == p.resultIdx {
				time.Sleep(5 * time.Millisecond)
			}
			_, _ = fmt.Fprintln(pw, event)
		}
		_ = pw.Close() // EOF for the scanner
	}()

	waitFn := func() error {
		<-writerDone // block until "process" finishes writing
		p.mu.Lock()
		p.waitCalled = true
		p.mu.Unlock()
		return nil
	}

	return pr, waitFn, nil
}

// TestResultEventCapturedFromPipe verifies that the result event (the last
// event emitted by the CLI, containing cost/turns/response metadata) is
// always captured and saved to the database.
//
// Regression test for: cmd.Wait() closing the stdout pipe before the scanner
// goroutine finished reading, causing the result event to be silently lost.
// The fix ensures the pipe is fully drained before Wait() is called.
func TestResultEventCapturedFromPipe(t *testing.T) {
	m, cfg := testManager(t)

	events := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Checking services..."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"All services healthy."}]}}`,
		`{"type":"result","result":"Health check complete. All 6 services operational.","is_error":false,"total_cost_usd":0.0542,"num_turns":7,"duration_ms":45000}`,
	}

	runner := &pipeRunner{
		events:    events,
		resultIdx: 3, // result is the last event
	}
	m.runner = runner

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := m.runOnce(ctx, "", "scheduled")
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}

	// Verify Wait was called (sanity check that the runner worked).
	runner.mu.Lock()
	if !runner.waitCalled {
		t.Error("waitFn was never called")
	}
	runner.mu.Unlock()

	// Find the session in the database and check result metadata.
	sessions, err := m.db.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions in DB after runOnce")
	}

	sess := sessions[0]

	if sess.CostUSD == nil {
		t.Fatal("CostUSD is nil — result event was not captured (pipe race regression)")
	}
	if *sess.CostUSD < 0.054 || *sess.CostUSD > 0.055 {
		t.Errorf("CostUSD = %f, want ~0.0542", *sess.CostUSD)
	}

	if sess.NumTurns == nil || *sess.NumTurns != 7 {
		t.Errorf("NumTurns = %v, want 7", sess.NumTurns)
	}

	if sess.DurationMs == nil || *sess.DurationMs != 45000 {
		t.Errorf("DurationMs = %v, want 45000", sess.DurationMs)
	}

	if sess.Response == nil || !strings.Contains(*sess.Response, "All 6 services") {
		t.Errorf("Response not captured; got %v", sess.Response)
	}

	// Verify the session completed successfully.
	if sess.Status != "completed" {
		t.Errorf("Status = %q, want completed", sess.Status)
	}

	// Verify log file was written and contains the result event.
	entries, _ := os.ReadDir(cfg.ResultsDir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "run-") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(cfg.ResultsDir, e.Name()))
		if !strings.Contains(string(data), "total_cost_usd") {
			t.Error("log file should contain the result event JSON")
		}
	}
}

// TestResultEventCapturedUnderContention runs the pipe-drain test multiple
// times to increase confidence that the result event is never lost, even
// under goroutine scheduling pressure. Before the fix (draining the pipe
// before calling Wait), this test would intermittently fail.
// ---------------------------------------------------------------------------
// Governing: ADR-0030, SPEC-0031 — Structured Output Tests
// ---------------------------------------------------------------------------

// TestParseMemoryKey verifies that parseMemoryKey splits "service:category" keys
// and handles plain "category" keys correctly.
// Governing: ADR-0030, SPEC-0031 REQ-7
func TestParseMemoryKey(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		value        string
		wantCategory string
		wantService  *string
		wantObs      string
	}{
		{
			name:         "simple category key",
			key:          "hostname",
			value:        "ie01.example.com",
			wantCategory: "hostname",
			wantService:  nil,
			wantObs:      "ie01.example.com",
		},
		{
			name:         "service-prefixed key",
			key:          "jellyfin:config",
			value:        "uses SQLite",
			wantCategory: "config",
			wantService:  strPtr("jellyfin"),
			wantObs:      "uses SQLite",
		},
		{
			name:         "empty key",
			key:          "",
			value:        "some value",
			wantCategory: "",
			wantService:  nil,
			wantObs:      "some value",
		},
		{
			name:         "empty value",
			key:          "timing",
			value:        "",
			wantCategory: "timing",
			wantService:  nil,
			wantObs:      "",
		},
		{
			name:         "key with multiple colons",
			key:          "caddy:reverse:proxy",
			value:        "uses automatic HTTPS",
			wantCategory: "reverse:proxy",
			wantService:  strPtr("caddy"),
			wantObs:      "uses automatic HTTPS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMemoryKey(tc.key, tc.value)
			if got.Category != tc.wantCategory {
				t.Errorf("Category = %q, want %q", got.Category, tc.wantCategory)
			}
			if tc.wantService == nil {
				if got.Service != nil {
					t.Errorf("Service = %v, want nil", got.Service)
				}
			} else {
				if got.Service == nil {
					t.Errorf("Service = nil, want %q", *tc.wantService)
				} else if *got.Service != *tc.wantService {
					t.Errorf("Service = %q, want %q", *got.Service, *tc.wantService)
				}
			}
			if got.Observation != tc.wantObs {
				t.Errorf("Observation = %q, want %q", got.Observation, tc.wantObs)
			}
		})
	}
}

// TestProcessStructuredEvents verifies that events from structured output
// are correctly inserted into the database.
// Governing: ADR-0030, SPEC-0031 REQ-2
func TestProcessStructuredEvents(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	events := []AgentEvent{
		{Level: "info", Service: "jellyfin", Message: "HTTP 200 OK"},
		{Level: "warning", Service: "loki", Message: "DNS resolution failed"},
		{Level: "critical", Message: "Host ie01 unreachable"},
	}

	m.processStructuredEvents(sid, events)

	// Verify events were inserted by listing all events.
	dbEvents, err := database.ListEvents(10, 0, nil, nil)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(dbEvents) != 3 {
		t.Fatalf("expected 3 events, got %d", len(dbEvents))
	}

	// Events are returned most recent first, but insertion order may vary.
	// Check that each expected event exists.
	foundJellyfin := false
	foundLoki := false
	foundCritical := false
	for _, e := range dbEvents {
		if e.Message == "HTTP 200 OK" {
			foundJellyfin = true
			if e.Level != "info" {
				t.Errorf("jellyfin event level = %q, want info", e.Level)
			}
			if e.Service == nil || *e.Service != "jellyfin" {
				t.Errorf("jellyfin event service = %v, want jellyfin", e.Service)
			}
		}
		if e.Message == "DNS resolution failed" {
			foundLoki = true
			if e.Level != "warning" {
				t.Errorf("loki event level = %q, want warning", e.Level)
			}
			if e.Service == nil || *e.Service != "loki" {
				t.Errorf("loki event service = %v, want loki", e.Service)
			}
		}
		if e.Message == "Host ie01 unreachable" {
			foundCritical = true
			if e.Level != "critical" {
				t.Errorf("critical event level = %q, want critical", e.Level)
			}
			if e.Service != nil {
				t.Errorf("critical event service = %v, want nil", e.Service)
			}
		}
	}
	if !foundJellyfin {
		t.Error("jellyfin event not found in DB")
	}
	if !foundLoki {
		t.Error("loki event not found in DB")
	}
	if !foundCritical {
		t.Error("critical event not found in DB")
	}
}

// TestProcessStructuredEvents_Empty verifies that an empty events slice is a no-op.
// Governing: ADR-0030, SPEC-0031 REQ-2
func TestProcessStructuredEvents_Empty(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m.processStructuredEvents(sid, []AgentEvent{})

	dbEvents, err := database.ListEvents(10, 0, nil, nil)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(dbEvents) != 0 {
		t.Errorf("expected 0 events, got %d", len(dbEvents))
	}
}

// TestProcessStructuredEvents_NilService verifies that events without a service
// field store nil in the database.
// Governing: ADR-0030, SPEC-0031 REQ-2
func TestProcessStructuredEvents_NilService(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m.processStructuredEvents(sid, []AgentEvent{
		{Level: "info", Message: "General observation"},
	})

	dbEvents, err := database.ListEvents(10, 0, nil, nil)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(dbEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(dbEvents))
	}
	if dbEvents[0].Service != nil {
		t.Errorf("Service = %v, want nil", dbEvents[0].Service)
	}
}

// TestProcessStructuredEvents_LevelNormalization verifies that free-form event
// levels are normalized (e.g., "warn" -> "warning", "error" -> "critical").
// Governing: ADR-0030, SPEC-0031 REQ-2
func TestProcessStructuredEvents_LevelNormalization(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m.processStructuredEvents(sid, []AgentEvent{
		{Level: "warn", Message: "degraded response"},
		{Level: "error", Message: "service down"},
		{Level: "ok", Message: "all good"},
	})

	dbEvents, err := database.ListEvents(10, 0, nil, nil)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(dbEvents) != 3 {
		t.Fatalf("expected 3 events, got %d", len(dbEvents))
	}

	levelMap := make(map[string]string)
	for _, e := range dbEvents {
		levelMap[e.Message] = e.Level
	}
	if levelMap["degraded response"] != "warning" {
		t.Errorf("warn -> %q, want warning", levelMap["degraded response"])
	}
	if levelMap["service down"] != "critical" {
		t.Errorf("error -> %q, want critical", levelMap["service down"])
	}
	if levelMap["all good"] != "info" {
		t.Errorf("ok -> %q, want info", levelMap["all good"])
	}
}

// TestProcessStructuredMemories verifies that memories from structured output
// are correctly inserted into the database via upsertMemory.
// Governing: ADR-0030, SPEC-0031 REQ-7
func TestProcessStructuredMemories(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	memories := []AgentMemory{
		{Key: "jellyfin:timing", Value: "Takes 60s to start after restart"},
		{Key: "remediation", Value: "DNS checks fail during WireGuard reconnects"},
	}

	m.processStructuredMemories(sid, 1, memories)

	// Check service-prefixed memory.
	svc := "jellyfin"
	mem, err := database.FindSimilarMemory(&svc, "timing")
	if err != nil {
		t.Fatalf("FindSimilarMemory: %v", err)
	}
	if mem == nil {
		t.Fatal("expected jellyfin:timing memory to be inserted")
	}
	if mem.Observation != "Takes 60s to start after restart" {
		t.Errorf("observation = %q", mem.Observation)
	}

	// Check plain category memory.
	mem2, err := database.FindSimilarMemory(nil, "remediation")
	if err != nil {
		t.Fatalf("FindSimilarMemory: %v", err)
	}
	if mem2 == nil {
		t.Fatal("expected remediation memory to be inserted")
	}
	if mem2.Observation != "DNS checks fail during WireGuard reconnects" {
		t.Errorf("observation = %q", mem2.Observation)
	}
	if mem2.Service != nil {
		t.Errorf("service = %v, want nil", mem2.Service)
	}
}

// TestProcessStructuredMemories_Empty verifies that an empty memories slice is a no-op.
// Governing: ADR-0030, SPEC-0031 REQ-7
func TestProcessStructuredMemories_Empty(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m.processStructuredMemories(sid, 1, []AgentMemory{})

	// No memories should exist in the DB.
	mems, err := database.ListMemories(nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("expected 0 memories, got %d", len(mems))
	}
}

// TestBuildStructuredEscalationContext verifies that an AgentResponse is formatted
// into a readable markdown section for injection into the next tier's system prompt.
// Governing: ADR-0030, SPEC-0031 REQ-3
func TestBuildStructuredEscalationContext(t *testing.T) {
	resp := &AgentResponse{
		Summary: "Jellyfin is down after Docker restart",
		Events: []AgentEvent{
			{Level: "critical", Service: "jellyfin", Message: "HTTP 503"},
		},
		Escalation: AgentEscalation{
			Needed:       true,
			Reason:       "Jellyfin container is crash-looping",
			Context:      "Container exited with code 137 (OOM killed). Memory usage was 4.2GB.",
			FailedChecks: []string{"http-health-jellyfin", "docker-status-jellyfin"},
		},
		ServicesChecked: []ServiceCheck{
			{Name: "jellyfin", Status: "down", Detail: "HTTP 503 Service Unavailable"},
			{Name: "caddy", Status: "healthy"},
		},
	}

	got := buildStructuredEscalationContext(resp)

	// Verify header
	if !strings.Contains(got, "## Escalation Context") {
		t.Errorf("missing header in:\n%s", got)
	}

	// Verify reason
	if !strings.Contains(got, "**Reason:** Jellyfin container is crash-looping") {
		t.Errorf("missing reason in:\n%s", got)
	}

	// Verify service status section
	if !strings.Contains(got, "### Service Status") {
		t.Errorf("missing Service Status section in:\n%s", got)
	}
	if !strings.Contains(got, "**jellyfin**: down") {
		t.Errorf("missing jellyfin status in:\n%s", got)
	}
	if !strings.Contains(got, "HTTP 503 Service Unavailable") {
		t.Errorf("missing jellyfin detail in:\n%s", got)
	}
	if !strings.Contains(got, "**caddy**: healthy") {
		t.Errorf("missing caddy status in:\n%s", got)
	}

	// Verify failed checks
	if !strings.Contains(got, "### Failed Checks") {
		t.Errorf("missing Failed Checks section in:\n%s", got)
	}
	if !strings.Contains(got, "http-health-jellyfin") {
		t.Errorf("missing failed check in:\n%s", got)
	}

	// Verify investigation context
	if !strings.Contains(got, "### Investigation Findings") {
		t.Errorf("missing Investigation Findings section in:\n%s", got)
	}
	if !strings.Contains(got, "OOM killed") {
		t.Errorf("missing investigation context in:\n%s", got)
	}

	// Verify summary
	if !strings.Contains(got, "### Previous Tier Summary") {
		t.Errorf("missing Previous Tier Summary section in:\n%s", got)
	}
	if !strings.Contains(got, "Jellyfin is down after Docker restart") {
		t.Errorf("missing summary in:\n%s", got)
	}
}

// TestBuildStructuredEscalationContext_Minimal verifies that a minimal response
// (no optional fields) produces just the header.
// Governing: ADR-0030, SPEC-0031 REQ-3
func TestBuildStructuredEscalationContext_Minimal(t *testing.T) {
	resp := &AgentResponse{
		Escalation: AgentEscalation{
			Needed: false,
		},
	}

	got := buildStructuredEscalationContext(resp)

	if !strings.Contains(got, "## Escalation Context") {
		t.Errorf("missing header in:\n%s", got)
	}
	// Should NOT contain any optional subsections
	if strings.Contains(got, "### Service Status") {
		t.Errorf("should not have Service Status section for empty response:\n%s", got)
	}
	if strings.Contains(got, "### Failed Checks") {
		t.Errorf("should not have Failed Checks section for empty response:\n%s", got)
	}
	if strings.Contains(got, "### Investigation Findings") {
		t.Errorf("should not have Investigation Findings for empty response:\n%s", got)
	}
	if strings.Contains(got, "### Previous Tier Summary") {
		t.Errorf("should not have Previous Tier Summary for empty response:\n%s", got)
	}
}

// TestBuildStructuredEscalationContext_ServiceDetailOmitted verifies that
// service entries without a detail field don't produce a trailing dash.
// Governing: ADR-0030, SPEC-0031 REQ-3
func TestBuildStructuredEscalationContext_ServiceDetailOmitted(t *testing.T) {
	resp := &AgentResponse{
		ServicesChecked: []ServiceCheck{
			{Name: "caddy", Status: "healthy"},
		},
		Escalation: AgentEscalation{Needed: false},
	}

	got := buildStructuredEscalationContext(resp)

	if strings.Contains(got, "healthy —") {
		t.Errorf("service without detail should not have trailing dash in:\n%s", got)
	}
	if !strings.Contains(got, "**caddy**: healthy") {
		t.Errorf("missing caddy status in:\n%s", got)
	}
}

// strPtr is a helper to create a *string from a string literal.
func strPtr(s string) *string { return &s }

func TestResultEventCapturedUnderContention(t *testing.T) {
	for i := 0; i < 20; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			t.Parallel()

			cfg := testConfig(t)
			database, err := db.Open(filepath.Join(cfg.StateDir, "test.db"))
			if err != nil {
				t.Fatalf("open test db: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })
			h := hub.New()
			m := New(cfg, database, h, &CLIRunner{})

			events := []string{
				`{"type":"system","subtype":"init"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"Investigating issue..."}]}}`,
				`{"type":"result","result":"Fixed.","is_error":false,"total_cost_usd":0.123,"num_turns":4,"duration_ms":8000}`,
			}

			m.runner = &pipeRunner{
				events:    events,
				resultIdx: 2,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := m.runOnce(ctx, "", "scheduled"); err != nil {
				t.Fatalf("runOnce: %v", err)
			}

			sessions, err := m.db.ListSessions(1, 0)
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			if len(sessions) == 0 {
				t.Fatal("no session found")
			}

			s := sessions[0]
			if s.CostUSD == nil {
				t.Fatal("CostUSD is nil — result event lost (pipe race regression)")
			}
			if *s.CostUSD < 0.122 || *s.CostUSD > 0.124 {
				t.Errorf("CostUSD = %f, want ~0.123", *s.CostUSD)
			}
			if s.NumTurns == nil || *s.NumTurns != 4 {
				t.Errorf("NumTurns = %v, want 4", s.NumTurns)
			}
		})
	}
}
