package db

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenAndMigrate(t *testing.T) {
	d := openTestDB(t)

	// Verify tables exist by inserting and reading back.
	id, err := d.InsertSession(&Session{
		Tier:       1,
		Model:      "haiku",
		PromptFile: "/tmp/test.md",
		Status:     "running",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if id < 1 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	s, err := d.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s == nil {
		t.Fatal("expected session, got nil")
	}
	if s.Model != "haiku" {
		t.Fatalf("expected model haiku, got %q", s.Model)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	d := openTestDB(t)

	s, err := d.GetSession(9999)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil for non-existent session, got %+v", s)
	}
}

func TestUpdateSession(t *testing.T) {
	d := openTestDB(t)

	id, err := d.InsertSession(&Session{
		Tier:       1,
		Model:      "haiku",
		PromptFile: "/tmp/test.md",
		Status:     "running",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	endedAt := time.Now().UTC().Format(time.RFC3339)
	exitCode := 0
	logFile := "/tmp/run.log"
	if err := d.UpdateSession(id, "completed", &endedAt, &exitCode, &logFile); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	s, err := d.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Status != "completed" {
		t.Fatalf("expected status completed, got %q", s.Status)
	}
	if s.ExitCode == nil || *s.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", s.ExitCode)
	}
}

func TestListSessions(t *testing.T) {
	d := openTestDB(t)

	for i := 0; i < 5; i++ {
		_, err := d.InsertSession(&Session{
			Tier:       1,
			Model:      "haiku",
			PromptFile: "/tmp/test.md",
			Status:     "completed",
			StartedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatalf("InsertSession: %v", err)
		}
	}

	sessions, err := d.ListSessions(3)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestHealthCheck(t *testing.T) {
	d := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339)
	id, err := d.InsertHealthCheck(&HealthCheck{
		Service:   "caddy",
		CheckType: "http",
		Status:    "healthy",
		CheckedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertHealthCheck: %v", err)
	}
	if id < 1 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	checks, err := d.QueryHealthChecks("caddy", "2000-01-01T00:00:00Z", "2099-01-01T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("QueryHealthChecks: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Service != "caddy" {
		t.Fatalf("expected service caddy, got %q", checks[0].Service)
	}
}

func TestHealthStreak(t *testing.T) {
	d := openTestDB(t)

	count, err := d.GetHealthStreak("caddy")
	if err != nil {
		t.Fatalf("GetHealthStreak: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for unknown service, got %d", count)
	}

	if err := d.SetHealthStreak("caddy", 5); err != nil {
		t.Fatalf("SetHealthStreak: %v", err)
	}

	count, err = d.GetHealthStreak("caddy")
	if err != nil {
		t.Fatalf("GetHealthStreak: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5, got %d", count)
	}

	// Upsert should update.
	if err := d.SetHealthStreak("caddy", 10); err != nil {
		t.Fatalf("SetHealthStreak upsert: %v", err)
	}
	count, err = d.GetHealthStreak("caddy")
	if err != nil {
		t.Fatalf("GetHealthStreak after upsert: %v", err)
	}
	if count != 10 {
		t.Fatalf("expected 10, got %d", count)
	}
}

func TestConfig(t *testing.T) {
	d := openTestDB(t)

	val, err := d.GetConfig("missing", "default")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if val != "default" {
		t.Fatalf("expected default, got %q", val)
	}

	if err := d.SetConfig("key1", "value1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	val, err = d.GetConfig("key1", "default")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if val != "value1" {
		t.Fatalf("expected value1, got %q", val)
	}

	// Upsert should update.
	if err := d.SetConfig("key1", "value2"); err != nil {
		t.Fatalf("SetConfig upsert: %v", err)
	}
	val, err = d.GetConfig("key1", "default")
	if err != nil {
		t.Fatalf("GetConfig after upsert: %v", err)
	}
	if val != "value2" {
		t.Fatalf("expected value2, got %q", val)
	}
}

func TestCooldown(t *testing.T) {
	d := openTestDB(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.InsertCooldownAction(&CooldownAction{
		Service:    "caddy",
		ActionType: "restart",
		Timestamp:  now,
		Success:    true,
		Tier:       1,
	})
	if err != nil {
		t.Fatalf("InsertCooldownAction: %v", err)
	}

	count, err := d.CheckCooldown("caddy", "restart", time.Hour)
	if err != nil {
		t.Fatalf("CheckCooldown: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 cooldown action, got %d", count)
	}

	// Different service should return 0.
	count, err = d.CheckCooldown("other", "restart", time.Hour)
	if err != nil {
		t.Fatalf("CheckCooldown: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for different service, got %d", count)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	// Open and close twice — migrations should be idempotent.
	d1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	d1.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	d2.Close()
}

func TestUpdateSessionResult(t *testing.T) {
	d := openTestDB(t)

	id, err := d.InsertSession(&Session{
		Tier:       2,
		Model:      "sonnet",
		PromptFile: "/tmp/prompt.md",
		Status:     "running",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	// Store result metadata.
	response := "## Health Report\nAll services healthy."
	costUSD := 0.0042
	numTurns := 3
	durationMs := int64(12500)

	if err := d.UpdateSessionResult(id, response, costUSD, numTurns, durationMs); err != nil {
		t.Fatalf("UpdateSessionResult: %v", err)
	}

	// Verify via GetSession.
	s, err := d.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Response == nil || *s.Response != response {
		t.Fatalf("expected response %q, got %v", response, s.Response)
	}
	if s.CostUSD == nil || *s.CostUSD != costUSD {
		t.Fatalf("expected cost_usd %f, got %v", costUSD, s.CostUSD)
	}
	if s.NumTurns == nil || *s.NumTurns != numTurns {
		t.Fatalf("expected num_turns %d, got %v", numTurns, s.NumTurns)
	}
	if s.DurationMs == nil || *s.DurationMs != durationMs {
		t.Fatalf("expected duration_ms %d, got %v", durationMs, s.DurationMs)
	}
}

func TestSessionResultNullBeforeUpdate(t *testing.T) {
	d := openTestDB(t)

	// Insert a session without calling UpdateSessionResult (simulates
	// pre-migration rows or sessions killed before completion).
	id, err := d.InsertSession(&Session{
		Tier:       1,
		Model:      "haiku",
		PromptFile: "/tmp/prompt.md",
		Status:     "completed",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	s, err := d.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Response != nil {
		t.Fatalf("expected nil Response, got %q", *s.Response)
	}
	if s.CostUSD != nil {
		t.Fatalf("expected nil CostUSD, got %v", *s.CostUSD)
	}
	if s.NumTurns != nil {
		t.Fatalf("expected nil NumTurns, got %v", *s.NumTurns)
	}
	if s.DurationMs != nil {
		t.Fatalf("expected nil DurationMs, got %v", *s.DurationMs)
	}
}

func TestSessionResultViaListAndLatest(t *testing.T) {
	d := openTestDB(t)

	id, err := d.InsertSession(&Session{
		Tier:       2,
		Model:      "sonnet",
		PromptFile: "/tmp/prompt.md",
		Status:     "completed",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	response := "All clear."
	if err := d.UpdateSessionResult(id, response, 0.01, 5, 30000); err != nil {
		t.Fatalf("UpdateSessionResult: %v", err)
	}

	// Verify via ListSessions.
	sessions, err := d.ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Response == nil || *sessions[0].Response != response {
		t.Fatalf("ListSessions: expected response %q, got %v", response, sessions[0].Response)
	}
	if sessions[0].NumTurns == nil || *sessions[0].NumTurns != 5 {
		t.Fatalf("ListSessions: expected num_turns 5, got %v", sessions[0].NumTurns)
	}

	// Verify via LatestSession.
	latest, err := d.LatestSession()
	if err != nil {
		t.Fatalf("LatestSession: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest session, got nil")
	}
	if latest.Response == nil || *latest.Response != response {
		t.Fatalf("LatestSession: expected response %q, got %v", response, latest.Response)
	}
	if latest.CostUSD == nil || *latest.CostUSD != 0.01 {
		t.Fatalf("LatestSession: expected cost_usd 0.01, got %v", latest.CostUSD)
	}
	if latest.DurationMs == nil || *latest.DurationMs != 30000 {
		t.Fatalf("LatestSession: expected duration_ms 30000, got %v", latest.DurationMs)
	}
}

func TestMigration005ParentSessionID(t *testing.T) {
	d := openTestDB(t)

	// Insert a session without parent — should succeed with NULL parent.
	id1, err := d.InsertSession(&Session{
		Tier:       1,
		Model:      "haiku",
		PromptFile: "/tmp/test.md",
		Status:     "completed",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("InsertSession (no parent): %v", err)
	}

	s1, err := d.GetSession(id1)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s1.ParentSessionID != nil {
		t.Fatalf("expected nil ParentSessionID, got %v", *s1.ParentSessionID)
	}

	// Insert a session with parent.
	id2, err := d.InsertSession(&Session{
		Tier:            2,
		Model:           "sonnet",
		PromptFile:      "/tmp/tier2.md",
		Status:          "completed",
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		ParentSessionID: &id1,
	})
	if err != nil {
		t.Fatalf("InsertSession (with parent): %v", err)
	}

	s2, err := d.GetSession(id2)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s2.ParentSessionID == nil || *s2.ParentSessionID != id1 {
		t.Fatalf("expected ParentSessionID %d, got %v", id1, s2.ParentSessionID)
	}
}

func TestGetEscalationChain(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Create a 3-tier escalation chain: tier1 -> tier2 -> tier3.
	id1, err := d.InsertSession(&Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/t1.md",
		Status: "completed", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("insert tier 1: %v", err)
	}

	id2, err := d.InsertSession(&Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2.md",
		Status: "completed", StartedAt: now, ParentSessionID: &id1,
	})
	if err != nil {
		t.Fatalf("insert tier 2: %v", err)
	}

	id3, err := d.InsertSession(&Session{
		Tier: 3, Model: "opus", PromptFile: "/tmp/t3.md",
		Status: "completed", StartedAt: now, ParentSessionID: &id2,
	})
	if err != nil {
		t.Fatalf("insert tier 3: %v", err)
	}

	// From the leaf (tier 3), walk up to root.
	chain, err := d.GetEscalationChain(id3)
	if err != nil {
		t.Fatalf("GetEscalationChain: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("expected 3 sessions in chain, got %d", len(chain))
	}
	// Should be ordered root-to-leaf (by id ASC).
	if chain[0].ID != id1 {
		t.Errorf("chain[0] should be root (id=%d), got id=%d", id1, chain[0].ID)
	}
	if chain[1].ID != id2 {
		t.Errorf("chain[1] should be tier 2 (id=%d), got id=%d", id2, chain[1].ID)
	}
	if chain[2].ID != id3 {
		t.Errorf("chain[2] should be leaf (id=%d), got id=%d", id3, chain[2].ID)
	}

	// From the root, should only return itself (no parent links to follow).
	rootChain, err := d.GetEscalationChain(id1)
	if err != nil {
		t.Fatalf("GetEscalationChain from root: %v", err)
	}
	if len(rootChain) != 1 {
		t.Fatalf("expected 1 session from root, got %d", len(rootChain))
	}
}

func TestGetChildSessions(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Create parent with two children.
	parentID, err := d.InsertSession(&Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/t1.md",
		Status: "completed", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	child1ID, err := d.InsertSession(&Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2.md",
		Status: "completed", StartedAt: now, ParentSessionID: &parentID,
	})
	if err != nil {
		t.Fatalf("insert child 1: %v", err)
	}

	child2ID, err := d.InsertSession(&Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2b.md",
		Status: "completed", StartedAt: now, ParentSessionID: &parentID,
	})
	if err != nil {
		t.Fatalf("insert child 2: %v", err)
	}

	children, err := d.GetChildSessions(parentID)
	if err != nil {
		t.Fatalf("GetChildSessions: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	if children[0].ID != child1ID || children[1].ID != child2ID {
		t.Errorf("expected children [%d, %d], got [%d, %d]", child1ID, child2ID, children[0].ID, children[1].ID)
	}

	// Session with no children should return empty slice.
	noChildren, err := d.GetChildSessions(child1ID)
	if err != nil {
		t.Fatalf("GetChildSessions (no children): %v", err)
	}
	if len(noChildren) != 0 {
		t.Fatalf("expected 0 children, got %d", len(noChildren))
	}
}
