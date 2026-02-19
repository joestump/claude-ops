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

// pipeRunner uses os.Pipe to simulate the real CLI subprocess pipe behavior.
// The Wait function closes the read end of the pipe (matching exec.Cmd.Wait
// behavior), which in the past caused a race condition where the result event
// was lost if Wait closed the pipe before the scanner read the last line.
type pipeRunner struct {
	events    []string
	resultIdx int // index of the result event (-1 if none)

	// mu guards waitCalled so the test can verify ordering.
	mu         sync.Mutex
	waitCalled bool
}

func (p *pipeRunner) Start(_ context.Context, _ string, _ string, _ string, _ string) (io.ReadCloser, func() error, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i, event := range p.events {
			// Add a yield gap before the result event to widen the race window.
			// Without the pipe-drain-before-Wait fix, this gap allows Wait()
			// to close the pipe before the scanner reads the result.
			if i == p.resultIdx {
				time.Sleep(5 * time.Millisecond)
			}
			_, _ = fmt.Fprintln(pw, event)
		}
		_ = pw.Close()
	}()

	waitFn := func() error {
		<-writerDone // block until "process" finishes writing
		p.mu.Lock()
		p.waitCalled = true
		p.mu.Unlock()
		// Simulate exec.Cmd.Wait closing the read end of stdout pipe.
		// This is what caused the original bug: if the scanner hadn't
		// consumed the result event yet, this Close() discarded it.
		return pr.Close()
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
