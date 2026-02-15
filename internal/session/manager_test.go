package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	t.Cleanup(func() { database.Close() })
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
		parsed, err := time.Parse("20060102-150405", ts)
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

func TestResultsDirUsed(t *testing.T) {
	m, cfg := testManager(t)
	// Use a subdirectory that doesn't exist yet to ensure it's created properly.
	cfg.ResultsDir = filepath.Join(t.TempDir(), "sub", "results")
	os.MkdirAll(cfg.ResultsDir, 0o755)

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
