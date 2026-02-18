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
	t.Cleanup(func() { _ = d.Close() })
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

	sessions, err := d.ListSessions(3, 0)
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
	_ = d1.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = d2.Close()
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
	sessions, err := d.ListSessions(10, 0)
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

// --- Memory Tests ---

func TestMigration006(t *testing.T) {
	d := openTestDB(t)

	// Verify the memories table exists by running a query against it.
	var count int
	err := d.Conn().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count)
	if err != nil {
		t.Fatalf("memories table should exist: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows in fresh table, got %d", count)
	}
}

func TestInsertAndGetMemory(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "jellyfin"

	id, err := d.InsertMemory(&Memory{
		Service:     &svc,
		Category:    "timing",
		Observation: "Takes 60s to start after restart",
		Confidence:  0.8,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        2,
	})
	if err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}
	if id < 1 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	m, err := d.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.Service == nil || *m.Service != "jellyfin" {
		t.Fatalf("expected service jellyfin, got %v", m.Service)
	}
	if m.Category != "timing" {
		t.Fatalf("expected category timing, got %q", m.Category)
	}
	if m.Observation != "Takes 60s to start after restart" {
		t.Fatalf("expected observation, got %q", m.Observation)
	}
	if m.Confidence != 0.8 {
		t.Fatalf("expected confidence 0.8, got %f", m.Confidence)
	}
	if !m.Active {
		t.Fatal("expected active=true")
	}
	if m.Tier != 2 {
		t.Fatalf("expected tier 2, got %d", m.Tier)
	}

	// Non-existent ID returns nil.
	m2, err := d.GetMemory(9999)
	if err != nil {
		t.Fatalf("GetMemory non-existent: %v", err)
	}
	if m2 != nil {
		t.Fatalf("expected nil for non-existent memory, got %+v", m2)
	}
}

func TestUpdateMemory(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "caddy"

	id, err := d.InsertMemory(&Memory{
		Service:     &svc,
		Category:    "dependency",
		Observation: "Must start after WireGuard",
		Confidence:  0.7,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        1,
	})
	if err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}

	if err := d.UpdateMemory(id, "Must start after WireGuard — confirmed", 0.95, true); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	m, err := d.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if m.Observation != "Must start after WireGuard — confirmed" {
		t.Fatalf("expected updated observation, got %q", m.Observation)
	}
	if m.Confidence != 0.95 {
		t.Fatalf("expected confidence 0.95, got %f", m.Confidence)
	}
}

func TestDeleteMemory(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)

	id, err := d.InsertMemory(&Memory{
		Category:    "behavior",
		Observation: "Temporary observation",
		Confidence:  0.5,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tier:        1,
	})
	if err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}

	if err := d.DeleteMemory(id); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	m, err := d.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory after delete: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil after delete, got %+v", m)
	}
}

func TestListMemoriesWithFilters(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc1 := "jellyfin"
	svc2 := "caddy"

	// Insert memories for different services and categories.
	if _, err := d.InsertMemory(&Memory{Service: &svc1, Category: "timing", Observation: "obs1", Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1}); err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}
	if _, err := d.InsertMemory(&Memory{Service: &svc1, Category: "behavior", Observation: "obs2", Confidence: 0.6, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 2}); err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}
	if _, err := d.InsertMemory(&Memory{Service: &svc2, Category: "timing", Observation: "obs3", Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1}); err != nil {
		t.Fatalf("InsertMemory: %v", err)
	}
	_, _ = d.InsertMemory(&Memory{Category: "remediation", Observation: "obs4", Confidence: 0.7, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 3})

	// No filters — all 4.
	all, err := d.ListMemories(nil, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories (no filters): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4, got %d", len(all))
	}
	// Should be ordered by confidence DESC.
	if all[0].Confidence < all[1].Confidence {
		t.Fatal("expected confidence DESC ordering")
	}

	// Filter by service.
	byService, err := d.ListMemories(&svc1, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories (service filter): %v", err)
	}
	if len(byService) != 2 {
		t.Fatalf("expected 2 for jellyfin, got %d", len(byService))
	}

	// Filter by category.
	cat := "timing"
	byCat, err := d.ListMemories(nil, &cat, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories (category filter): %v", err)
	}
	if len(byCat) != 2 {
		t.Fatalf("expected 2 timing memories, got %d", len(byCat))
	}

	// Both filters.
	both, err := d.ListMemories(&svc1, &cat, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories (both filters): %v", err)
	}
	if len(both) != 1 {
		t.Fatalf("expected 1 for jellyfin+timing, got %d", len(both))
	}

	// Limit.
	limited, err := d.ListMemories(nil, nil, 2, 0)
	if err != nil {
		t.Fatalf("ListMemories (limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 with limit, got %d", len(limited))
	}
}

func TestGetActiveMemories(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "postgres"

	// Active with high confidence — included.
	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "maintenance", Observation: "needs vacuum", Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1})
	// Active but below threshold — excluded.
	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "behavior", Observation: "low conf", Confidence: 0.2, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1})
	// Inactive — excluded.
	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "timing", Observation: "inactive", Confidence: 0.9, Active: false, CreatedAt: now, UpdatedAt: now, Tier: 2})
	// Exactly at threshold — included.
	_, _ = d.InsertMemory(&Memory{Category: "remediation", Observation: "borderline", Confidence: 0.3, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 3})

	active, err := d.GetActiveMemories(100)
	if err != nil {
		t.Fatalf("GetActiveMemories: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active memories, got %d", len(active))
	}
	// First should be highest confidence.
	if active[0].Confidence != 0.8 {
		t.Fatalf("expected highest confidence first (0.8), got %f", active[0].Confidence)
	}
	if active[1].Confidence != 0.3 {
		t.Fatalf("expected second confidence 0.3, got %f", active[1].Confidence)
	}
}

func TestFindSimilarMemory(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "jellyfin"

	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "timing", Observation: "Takes 60s to start", Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 2})
	_, _ = d.InsertMemory(&Memory{Category: "remediation", Observation: "Retry DNS once", Confidence: 0.6, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1})

	// Find by service + category.
	m, err := d.FindSimilarMemory(&svc, "timing")
	if err != nil {
		t.Fatalf("FindSimilarMemory: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.Observation != "Takes 60s to start" {
		t.Fatalf("expected matching observation, got %q", m.Observation)
	}

	// Find general memory (nil service).
	m2, err := d.FindSimilarMemory(nil, "remediation")
	if err != nil {
		t.Fatalf("FindSimilarMemory (nil service): %v", err)
	}
	if m2 == nil {
		t.Fatal("expected general memory, got nil")
	}
	if m2.Observation != "Retry DNS once" {
		t.Fatalf("expected general observation, got %q", m2.Observation)
	}

	// No match.
	m3, err := d.FindSimilarMemory(&svc, "nonexistent")
	if err != nil {
		t.Fatalf("FindSimilarMemory (no match): %v", err)
	}
	if m3 != nil {
		t.Fatalf("expected nil for no match, got %+v", m3)
	}
}

func TestDecayStaleMemories(t *testing.T) {
	d := openTestDB(t)
	svc := "caddy"

	// Insert a stale memory (updated 60 days ago).
	staleTime := time.Now().UTC().AddDate(0, 0, -60).Format(time.RFC3339)
	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "dependency", Observation: "stale obs", Confidence: 0.5, Active: true, CreatedAt: staleTime, UpdatedAt: staleTime, Tier: 1})

	// Insert a fresh memory (updated now).
	freshTime := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.InsertMemory(&Memory{Service: &svc, Category: "timing", Observation: "fresh obs", Confidence: 0.8, Active: true, CreatedAt: freshTime, UpdatedAt: freshTime, Tier: 2})

	// Decay memories older than 30 days by 0.1.
	if err := d.DecayStaleMemories(30, 0.1); err != nil {
		t.Fatalf("DecayStaleMemories: %v", err)
	}

	// Stale memory: 0.5 - 0.1 = 0.4, still active.
	all, err := d.ListMemories(nil, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(all))
	}

	// Find the stale one.
	stale, err := d.FindSimilarMemory(&svc, "dependency")
	if err != nil {
		t.Fatalf("FindSimilarMemory: %v", err)
	}
	if stale == nil {
		t.Fatal("expected stale memory")
	}
	if stale.Confidence < 0.39 || stale.Confidence > 0.41 {
		t.Fatalf("expected confidence ~0.4 after decay, got %f", stale.Confidence)
	}
	if !stale.Active {
		t.Fatal("expected stale memory still active at 0.4")
	}

	// Fresh memory should be unchanged.
	fresh, err := d.FindSimilarMemory(&svc, "timing")
	if err != nil {
		t.Fatalf("FindSimilarMemory (fresh): %v", err)
	}
	if fresh.Confidence != 0.8 {
		t.Fatalf("expected fresh confidence unchanged at 0.8, got %f", fresh.Confidence)
	}

	// Decay again twice more — stale should drop below 0.3 and be deactivated.
	_ = d.DecayStaleMemories(30, 0.1)
	_ = d.DecayStaleMemories(30, 0.1)

	stale2, err := d.GetMemory(stale.ID)
	if err != nil {
		t.Fatalf("GetMemory stale after multi-decay: %v", err)
	}
	if stale2.Active {
		t.Fatalf("expected stale memory deactivated after dropping below 0.3, confidence=%f", stale2.Confidence)
	}
}
