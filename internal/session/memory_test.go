package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/hub"
)

func strPtr(s string) *string { return &s }

func testManagerWithDB(t *testing.T) (*Manager, *db.DB) {
	t.Helper()
	cfg := &config.Config{
		Interval:     1,
		Prompt:       "/dev/null",
		Tier1Model:   "haiku",
		Tier2Model:   "sonnet",
		Tier3Model:   "opus",
		StateDir:     t.TempDir(),
		ResultsDir:   t.TempDir(),
		ReposDir:     t.TempDir(),
		AllowedTools: "Bash,Read",
		DryRun:       true,
		MemoryBudget: 2000,
	}
	database, err := db.Open(filepath.Join(cfg.StateDir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	h := hub.New()
	return New(cfg, database, h, &CLIRunner{}), database
}

// ---------------------------------------------------------------------------
// parseMemoryMarkers
// ---------------------------------------------------------------------------

func TestParseMemoryMarkers_WithService(t *testing.T) {
	text := "[MEMORY:timing:jellyfin] Takes 60s to start after restart"
	got := parseMemoryMarkers(text)
	if len(got) != 1 {
		t.Fatalf("expected 1 marker, got %d", len(got))
	}
	if got[0].Category != "timing" {
		t.Errorf("category = %q, want %q", got[0].Category, "timing")
	}
	if got[0].Service == nil || *got[0].Service != "jellyfin" {
		t.Errorf("service = %v, want %q", got[0].Service, "jellyfin")
	}
	if got[0].Observation != "Takes 60s to start after restart" {
		t.Errorf("observation = %q, want %q", got[0].Observation, "Takes 60s to start after restart")
	}
}

func TestParseMemoryMarkers_WithoutService(t *testing.T) {
	text := "[MEMORY:remediation] DNS checks sometimes fail transiently during WireGuard reconnects"
	got := parseMemoryMarkers(text)
	if len(got) != 1 {
		t.Fatalf("expected 1 marker, got %d", len(got))
	}
	if got[0].Category != "remediation" {
		t.Errorf("category = %q, want %q", got[0].Category, "remediation")
	}
	if got[0].Service != nil {
		t.Errorf("service = %v, want nil", got[0].Service)
	}
	if got[0].Observation != "DNS checks sometimes fail transiently during WireGuard reconnects" {
		t.Errorf("observation = %q", got[0].Observation)
	}
}

func TestParseMemoryMarkers_MultipleLines(t *testing.T) {
	text := `Some text before
[MEMORY:timing:jellyfin] Takes 60s to start
Other text
[MEMORY:dependency:caddy] Must start after WireGuard
More text`
	got := parseMemoryMarkers(text)
	if len(got) != 2 {
		t.Fatalf("expected 2 markers, got %d", len(got))
	}
	if got[0].Category != "timing" {
		t.Errorf("first category = %q, want %q", got[0].Category, "timing")
	}
	if got[1].Category != "dependency" {
		t.Errorf("second category = %q, want %q", got[1].Category, "dependency")
	}
}

func TestParseMemoryMarkers_ServiceWithHyphen(t *testing.T) {
	text := "[MEMORY:behavior:adguard-home] Redirects to login on first access"
	got := parseMemoryMarkers(text)
	if len(got) != 1 {
		t.Fatalf("expected 1 marker, got %d", len(got))
	}
	if got[0].Service == nil || *got[0].Service != "adguard-home" {
		t.Errorf("service = %v, want %q", got[0].Service, "adguard-home")
	}
}

func TestParseMemoryMarkers_Invalid(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"uppercase category", "[MEMORY:TIMING] some observation"},
		{"missing observation", "[MEMORY:timing]"},
		{"missing observation with service", "[MEMORY:timing:svc]"},
		{"empty category", "[MEMORY:] some observation"},
		{"no brackets", "MEMORY:timing some observation"},
		{"wrong prefix", "[EVENT:info] not a memory"},
		{"empty text", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMemoryMarkers(tc.text)
			if len(got) != 0 {
				t.Errorf("expected 0 markers for %q, got %d", tc.text, len(got))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildMemoryContext
// ---------------------------------------------------------------------------

func TestBuildMemoryContext(t *testing.T) {
	m, database := testManagerWithDB(t)

	svc1 := "jellyfin"
	svc2 := "caddy"
	now := "2026-02-15T10:00:00Z"

	database.InsertMemory(&db.Memory{
		Service: &svc1, Category: "timing", Observation: "Takes 60s to start after restart",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 2,
	})
	database.InsertMemory(&db.Memory{
		Service: &svc1, Category: "behavior", Observation: "First restart fails due to DB lock",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 2,
	})
	database.InsertMemory(&db.Memory{
		Service: &svc2, Category: "dependency", Observation: "Must start after WireGuard",
		Confidence: 0.95, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 3,
	})
	database.InsertMemory(&db.Memory{
		Service: nil, Category: "remediation", Observation: "DNS checks fail transiently during WireGuard reconnects",
		Confidence: 0.6, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	got := m.buildMemoryContext()

	if !strings.Contains(got, "## Operational Memory") {
		t.Errorf("missing header in:\n%s", got)
	}
	if !strings.Contains(got, "### caddy") {
		t.Errorf("missing caddy section in:\n%s", got)
	}
	if !strings.Contains(got, "### jellyfin") {
		t.Errorf("missing jellyfin section in:\n%s", got)
	}
	if !strings.Contains(got, "### general") {
		t.Errorf("missing general section in:\n%s", got)
	}
	if !strings.Contains(got, "Takes 60s to start after restart") {
		t.Errorf("missing observation in:\n%s", got)
	}
	if !strings.Contains(got, "(confidence: 0.9)") {
		t.Errorf("missing confidence score in:\n%s", got)
	}
	if !strings.Contains(got, "[timing]") {
		t.Errorf("missing category tag in:\n%s", got)
	}
}

func TestBuildMemoryContext_Empty(t *testing.T) {
	m, _ := testManagerWithDB(t)
	got := m.buildMemoryContext()
	if got != "" {
		t.Errorf("expected empty string for no memories, got %q", got)
	}
}

func TestBuildMemoryContext_RespectsTokenBudget(t *testing.T) {
	m, database := testManagerWithDB(t)
	m.cfg.MemoryBudget = 50 // 50 * 4 = 200 chars max

	now := "2026-02-15T10:00:00Z"
	for i := 0; i < 20; i++ {
		svc := "svc"
		database.InsertMemory(&db.Memory{
			Service:     &svc,
			Category:    "behavior",
			Observation: strings.Repeat("x", 100),
			Confidence:  0.9,
			Active:      true,
			CreatedAt:   now,
			UpdatedAt:   now,
			Tier:        1,
		})
	}

	got := m.buildMemoryContext()

	bodyStart := strings.Index(got, "\n### ")
	if bodyStart == -1 {
		if got != "" {
			t.Errorf("unexpected non-empty output with tiny budget: %q", got)
		}
		return
	}
	body := got[bodyStart:]
	lineCount := strings.Count(body, "- [behavior]")
	if lineCount >= 20 {
		t.Errorf("expected fewer than 20 memory lines with tiny budget, got %d", lineCount)
	}
}

func TestBuildMemoryContext_ZeroBudget(t *testing.T) {
	m, database := testManagerWithDB(t)
	m.cfg.MemoryBudget = 0

	now := "2026-02-15T10:00:00Z"
	svc := "test"
	database.InsertMemory(&db.Memory{
		Service: &svc, Category: "timing", Observation: "test observation",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	got := m.buildMemoryContext()
	if got != "" {
		t.Errorf("expected empty string for zero budget, got %q", got)
	}
}

func TestBuildMemoryContext_GroupsByService(t *testing.T) {
	m, database := testManagerWithDB(t)

	now := "2026-02-15T10:00:00Z"
	svc := "jellyfin"
	database.InsertMemory(&db.Memory{
		Service: &svc, Category: "timing", Observation: "Takes 60s to start",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})
	database.InsertMemory(&db.Memory{
		Service: &svc, Category: "behavior", Observation: "DB lock on first restart",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	got := m.buildMemoryContext()

	count := strings.Count(got, "### jellyfin")
	if count != 1 {
		t.Errorf("expected 1 jellyfin header, got %d in:\n%s", count, got)
	}
	if !strings.Contains(got, "Takes 60s to start") {
		t.Errorf("missing first observation")
	}
	if !strings.Contains(got, "DB lock on first restart") {
		t.Errorf("missing second observation")
	}
}

// ---------------------------------------------------------------------------
// upsertMemory
// ---------------------------------------------------------------------------

func TestUpsertMemory_NewMemory(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, err := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	svc := "jellyfin"
	m.upsertMemory(sid, 1, parsedMemory{
		Category:    "timing",
		Service:     &svc,
		Observation: "Takes 60s to start",
	})

	mem, err := database.FindSimilarMemory(&svc, "timing")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if mem == nil {
		t.Fatal("expected memory to be inserted")
	}
	if mem.Observation != "Takes 60s to start" {
		t.Errorf("observation = %q", mem.Observation)
	}
	if mem.Confidence != 0.7 {
		t.Errorf("confidence = %f, want 0.7", mem.Confidence)
	}
}

func TestUpsertMemory_Reinforce(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, _ := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})

	svc := "jellyfin"
	pm := parsedMemory{
		Category:    "timing",
		Service:     &svc,
		Observation: "Takes 60s to start",
	}

	m.upsertMemory(sid, 1, pm)
	m.upsertMemory(sid, 1, pm)

	mem, _ := database.FindSimilarMemory(&svc, "timing")
	if mem == nil {
		t.Fatal("expected memory")
	}
	// 0.7 + 0.1 = 0.8
	if diff := mem.Confidence - 0.8; diff < -0.01 || diff > 0.01 {
		t.Errorf("confidence = %f, want ~0.8", mem.Confidence)
	}
}

func TestUpsertMemory_ReinforceCap(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, _ := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})

	svc := "test"
	now := "2026-02-15T10:00:00Z"
	database.InsertMemory(&db.Memory{
		Service: &svc, Category: "timing", Observation: "test obs",
		Confidence: 0.95, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	m.upsertMemory(sid, 1, parsedMemory{
		Category:    "timing",
		Service:     &svc,
		Observation: "test obs",
	})

	mem, _ := database.FindSimilarMemory(&svc, "timing")
	if mem.Confidence != 1.0 {
		t.Errorf("confidence = %f, want 1.0 (capped)", mem.Confidence)
	}
}

func TestUpsertMemory_Contradict(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, _ := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})

	svc := "jellyfin"
	m.upsertMemory(sid, 1, parsedMemory{
		Category: "timing", Service: &svc, Observation: "Takes 60s to start",
	})
	m.upsertMemory(sid, 2, parsedMemory{
		Category: "timing", Service: &svc, Observation: "Takes 30s to start",
	})

	// Old memory confidence should be 0.7 - 0.1 = 0.6
	oldMem, _ := database.GetMemory(1)
	if oldMem == nil {
		t.Fatal("expected old memory")
	}
	if diff := oldMem.Confidence - 0.6; diff < -0.01 || diff > 0.01 {
		t.Errorf("old confidence = %f, want ~0.6", oldMem.Confidence)
	}

	// New memory should exist with default confidence
	newMem, _ := database.GetMemory(2)
	if newMem == nil {
		t.Fatal("expected new memory")
	}
	if newMem.Observation != "Takes 30s to start" {
		t.Errorf("new observation = %q", newMem.Observation)
	}
	if newMem.Confidence != 0.7 {
		t.Errorf("new confidence = %f, want 0.7", newMem.Confidence)
	}
}

func TestUpsertMemory_GeneralMemory(t *testing.T) {
	m, database := testManagerWithDB(t)

	sid, _ := database.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/dev/null", Status: "running",
		StartedAt: "2026-02-15T10:00:00Z", Trigger: "scheduled",
	})

	m.upsertMemory(sid, 1, parsedMemory{
		Category:    "remediation",
		Service:     nil,
		Observation: "DNS checks fail during WireGuard reconnects",
	})

	mem, _ := database.FindSimilarMemory(nil, "remediation")
	if mem == nil {
		t.Fatal("expected general memory")
	}
	if mem.Service != nil {
		t.Errorf("service = %v, want nil", mem.Service)
	}
}
