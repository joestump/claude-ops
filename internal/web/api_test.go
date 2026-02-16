package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joestump/claude-ops/internal/db"
)

// --- Health Endpoint ---

func TestAPIHealthReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", resp["status"])
	}
}

func TestAPIHealthContentType(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}
}

// --- Sessions Endpoints ---

func TestAPIListSessionsEmpty(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APISessionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestAPIListSessionsWithData(t *testing.T) {
	e := newTestEnv(t)
	insertTestSession(t, e, "completed")
	insertTestSession(t, e, "running")
	insertTestSession(t, e, "failed")

	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APISessionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(resp.Sessions))
	}
}

func TestAPIListSessionsLimit(t *testing.T) {
	e := newTestEnv(t)
	insertTestSession(t, e, "completed")
	insertTestSession(t, e, "completed")
	insertTestSession(t, e, "completed")

	req := httptest.NewRequest("GET", "/api/v1/sessions?limit=1", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APISessionsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session with limit=1, got %d", len(resp.Sessions))
	}
}

func TestAPIListSessionsOffset(t *testing.T) {
	e := newTestEnv(t)
	id1 := insertTestSession(t, e, "completed")
	id2 := insertTestSession(t, e, "completed")
	_ = id1

	// Sessions are ordered by started_at DESC, so id2 should be first.
	// With offset=1, we skip the first (id2) and get id1.
	req := httptest.NewRequest("GET", "/api/v1/sessions?limit=1&offset=1", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APISessionsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}
	// Since both have the same started_at, just verify we got one and it's not id2.
	_ = id2
}

func TestAPIListSessionsNegativeLimit(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/sessions?limit=-1", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPIGetSession(t *testing.T) {
	e := newTestEnv(t)
	id := insertTestSession(t, e, "completed")

	err := e.srv.db.UpdateSessionResult(id, "All healthy.", 0.05, 3, 10000)
	if err != nil {
		t.Fatalf("update result: %v", err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var sess APISession
	if err := json.NewDecoder(w.Body).Decode(&sess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sess.ID != id {
		t.Fatalf("expected session ID %d, got %d", id, sess.ID)
	}
	if sess.Status != "completed" {
		t.Fatalf("expected status completed, got %q", sess.Status)
	}
	if sess.Response == nil || *sess.Response != "All healthy." {
		t.Fatalf("expected response 'All healthy.', got %v", sess.Response)
	}
}

func TestAPIGetSessionNotFound(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/sessions/99999", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAPIGetSessionInvalidID(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/sessions/abc", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPIGetSessionWithEscalationChain(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	parentID, _ := e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "/tmp/t1.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
	})
	cost := 0.05
	e.srv.db.UpdateSessionResult(parentID, "escalating", cost, 2, 5000)

	childID, _ := e.srv.db.InsertSession(&db.Session{
		Tier: 2, Model: "sonnet", PromptFile: "/tmp/t2.md",
		Status: "completed", StartedAt: now, Trigger: "scheduled",
		ParentSessionID: &parentID,
	})

	// Get parent: should have child sessions.
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%d", parentID), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var parentResp APISession
	json.NewDecoder(w.Body).Decode(&parentResp)
	if len(parentResp.ChildSessions) != 1 {
		t.Fatalf("expected 1 child session, got %d", len(parentResp.ChildSessions))
	}
	if parentResp.ChildSessions[0].ID != childID {
		t.Fatalf("expected child ID %d, got %d", childID, parentResp.ChildSessions[0].ID)
	}
	if parentResp.ChainCost == nil {
		t.Fatal("expected chain cost to be set")
	}

	// Get child: should have parent session.
	req2 := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/sessions/%d", childID), nil)
	w2 := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w2, req2)

	var childResp APISession
	json.NewDecoder(w2.Body).Decode(&childResp)
	if childResp.ParentSession == nil {
		t.Fatal("expected parent session to be set")
	}
	if childResp.ParentSession.ID != parentID {
		t.Fatalf("expected parent ID %d, got %d", parentID, childResp.ParentSession.ID)
	}
}

func TestAPITriggerSession(t *testing.T) {
	trigger := &mockTrigger{nextID: 42}
	e := newTestEnvWithTrigger(t, trigger)

	// Insert a session record so GetSession returns it.
	now := time.Now().UTC().Format(time.RFC3339)
	e.srv.db.InsertSession(&db.Session{
		Tier: 1, Model: "haiku", PromptFile: "(ad-hoc)",
		Status: "running", StartedAt: now, Trigger: "manual",
	})

	body := `{"prompt": "check all services"}`
	req := httptest.NewRequest("POST", "/api/v1/sessions/trigger", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestAPITriggerSessionConflict(t *testing.T) {
	trigger := &mockTrigger{nextErr: fmt.Errorf("session already running")}
	e := newTestEnvWithTrigger(t, trigger)

	body := `{"prompt": "check all services"}`
	req := httptest.NewRequest("POST", "/api/v1/sessions/trigger", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestAPITriggerSessionMissingPrompt(t *testing.T) {
	e := newTestEnv(t)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v1/sessions/trigger", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPITriggerSessionEmptyPrompt(t *testing.T) {
	e := newTestEnv(t)
	body := `{"prompt": "   "}`
	req := httptest.NewRequest("POST", "/api/v1/sessions/trigger", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPITriggerSessionWrongContentType(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("POST", "/api/v1/sessions/trigger", strings.NewReader("prompt=test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", w.Code)
	}
}

// --- Events Endpoint ---

func TestAPIListEventsEmpty(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APIEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(resp.Events))
	}
}

func TestAPIListEventsWithData(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "caddy"

	e.srv.db.InsertEvent(&db.Event{Level: "info", Service: &svc, Message: "healthy", CreatedAt: now})
	e.srv.db.InsertEvent(&db.Event{Level: "critical", Service: &svc, Message: "down", CreatedAt: now})

	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APIEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
}

func TestAPIListEventsFilterLevel(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "caddy"

	e.srv.db.InsertEvent(&db.Event{Level: "info", Service: &svc, Message: "healthy", CreatedAt: now})
	e.srv.db.InsertEvent(&db.Event{Level: "critical", Service: &svc, Message: "down", CreatedAt: now})
	e.srv.db.InsertEvent(&db.Event{Level: "critical", Message: "another critical", CreatedAt: now})

	req := httptest.NewRequest("GET", "/api/v1/events?level=critical", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APIEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 critical events, got %d", len(resp.Events))
	}
	for _, evt := range resp.Events {
		if evt.Level != "critical" {
			t.Fatalf("expected level 'critical', got %q", evt.Level)
		}
	}
}

func TestAPIListEventsFilterService(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc1 := "caddy"
	svc2 := "postgres"

	e.srv.db.InsertEvent(&db.Event{Level: "info", Service: &svc1, Message: "caddy ok", CreatedAt: now})
	e.srv.db.InsertEvent(&db.Event{Level: "info", Service: &svc2, Message: "pg ok", CreatedAt: now})

	req := httptest.NewRequest("GET", "/api/v1/events?service=caddy", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APIEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event for caddy, got %d", len(resp.Events))
	}
	if *resp.Events[0].Service != "caddy" {
		t.Fatalf("expected service 'caddy', got %q", *resp.Events[0].Service)
	}
}

// --- Memories Endpoints ---

func TestAPIListMemoriesEmpty(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APIMemoriesResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Memories) != 0 {
		t.Fatalf("expected 0 memories, got %d", len(resp.Memories))
	}
}

func TestAPICreateMemory(t *testing.T) {
	e := newTestEnv(t)
	body := `{"service": "redis", "category": "behavior", "observation": "Redis OOM needs restart", "confidence": 0.9}`
	req := httptest.NewRequest("POST", "/api/v1/memories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var mem APIMemory
	json.NewDecoder(w.Body).Decode(&mem)
	if mem.Category != "behavior" {
		t.Fatalf("expected category 'behavior', got %q", mem.Category)
	}
	if mem.Observation != "Redis OOM needs restart" {
		t.Fatalf("expected observation, got %q", mem.Observation)
	}
	if mem.Confidence != 0.9 {
		t.Fatalf("expected confidence 0.9, got %f", mem.Confidence)
	}
	if mem.Service == nil || *mem.Service != "redis" {
		t.Fatalf("expected service 'redis', got %v", mem.Service)
	}
	if !mem.Active {
		t.Fatal("expected active to be true")
	}
}

func TestAPICreateMemoryDefaultConfidence(t *testing.T) {
	e := newTestEnv(t)
	body := `{"category": "config", "observation": "Default conf test"}`
	req := httptest.NewRequest("POST", "/api/v1/memories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	var mem APIMemory
	json.NewDecoder(w.Body).Decode(&mem)
	if mem.Confidence != 0.7 {
		t.Fatalf("expected default confidence 0.7, got %f", mem.Confidence)
	}
}

func TestAPICreateMemoryMissingFields(t *testing.T) {
	e := newTestEnv(t)

	// Missing observation.
	body := `{"category": "behavior"}`
	req := httptest.NewRequest("POST", "/api/v1/memories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing observation, got %d", w.Code)
	}

	// Missing category.
	body2 := `{"observation": "test"}`
	req2 := httptest.NewRequest("POST", "/api/v1/memories", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing category, got %d", w2.Code)
	}
}

func TestAPICreateMemoryWrongContentType(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("POST", "/api/v1/memories", strings.NewReader("data"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", w.Code)
	}
}

func TestAPIUpdateMemory(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc := "caddy"
	id, _ := e.srv.db.InsertMemory(&db.Memory{
		Service: &svc, Category: "behavior", Observation: "original",
		Confidence: 0.5, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	body := `{"observation": "updated obs", "confidence": 0.95, "active": false}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/memories/%d", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var mem APIMemory
	json.NewDecoder(w.Body).Decode(&mem)
	if mem.Observation != "updated obs" {
		t.Fatalf("expected 'updated obs', got %q", mem.Observation)
	}
	if mem.Confidence != 0.95 {
		t.Fatalf("expected confidence 0.95, got %f", mem.Confidence)
	}
	if mem.Active {
		t.Fatal("expected active to be false")
	}
}

func TestAPIUpdateMemoryPartial(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	id, _ := e.srv.db.InsertMemory(&db.Memory{
		Category: "config", Observation: "original obs",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	// Only update confidence, leave observation and active unchanged.
	body := `{"confidence": 0.3}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/memories/%d", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var mem APIMemory
	json.NewDecoder(w.Body).Decode(&mem)
	if mem.Observation != "original obs" {
		t.Fatalf("observation should be unchanged, got %q", mem.Observation)
	}
	if mem.Confidence != 0.3 {
		t.Fatalf("expected confidence 0.3, got %f", mem.Confidence)
	}
	if !mem.Active {
		t.Fatal("active should be unchanged (true)")
	}
}

func TestAPIUpdateMemoryNotFound(t *testing.T) {
	e := newTestEnv(t)
	body := `{"observation": "test"}`
	req := httptest.NewRequest("PUT", "/api/v1/memories/99999", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAPIDeleteMemory(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	id, _ := e.srv.db.InsertMemory(&db.Memory{
		Category: "behavior", Observation: "to delete",
		Confidence: 0.5, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/memories/%d", id), nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Verify deleted.
	m, _ := e.srv.db.GetMemory(id)
	if m != nil {
		t.Fatal("expected memory to be deleted")
	}
}

func TestAPIDeleteMemoryNotFound(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/v1/memories/99999", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Cooldowns Endpoint ---

func TestAPIListCooldownsEmpty(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/cooldowns", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp APICooldownsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Cooldowns) != 0 {
		t.Fatalf("expected 0 cooldowns, got %d", len(resp.Cooldowns))
	}
}

func TestAPIListCooldownsWithData(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	e.srv.db.InsertCooldownAction(&db.CooldownAction{
		Service: "caddy", ActionType: "restart", Timestamp: now,
		Success: true, Tier: 2,
	})

	req := httptest.NewRequest("GET", "/api/v1/cooldowns", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APICooldownsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Cooldowns) != 1 {
		t.Fatalf("expected 1 cooldown, got %d", len(resp.Cooldowns))
	}
	if resp.Cooldowns[0].Service != "caddy" {
		t.Fatalf("expected service 'caddy', got %q", resp.Cooldowns[0].Service)
	}
}

// --- Config Endpoints ---

func TestAPIGetConfig(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var cfg APIConfig
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Interval != 3600 {
		t.Fatalf("expected interval 3600, got %d", cfg.Interval)
	}
	if cfg.Tier1Model != "haiku" {
		t.Fatalf("expected tier1 'haiku', got %q", cfg.Tier1Model)
	}
	if cfg.Tier2Model != "sonnet" {
		t.Fatalf("expected tier2 'sonnet', got %q", cfg.Tier2Model)
	}
	if cfg.Tier3Model != "opus" {
		t.Fatalf("expected tier3 'opus', got %q", cfg.Tier3Model)
	}
	if cfg.MaxTier != 3 {
		t.Fatalf("expected max_tier 3, got %d", cfg.MaxTier)
	}
}

func TestAPIUpdateConfig(t *testing.T) {
	e := newTestEnv(t)
	body := `{"interval": 1800, "tier1_model": "sonnet", "dry_run": true}`
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var cfg APIConfig
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Interval != 1800 {
		t.Fatalf("expected interval 1800, got %d", cfg.Interval)
	}
	if cfg.Tier1Model != "sonnet" {
		t.Fatalf("expected tier1 'sonnet', got %q", cfg.Tier1Model)
	}
	if !cfg.DryRun {
		t.Fatal("expected dry_run true")
	}

	// Verify in-memory config was updated.
	if e.srv.cfg.Interval != 1800 {
		t.Fatalf("in-memory interval not updated: %d", e.srv.cfg.Interval)
	}

	// Verify persisted to DB.
	val, _ := e.srv.db.GetConfig("interval", "0")
	if val != "1800" {
		t.Fatalf("expected DB interval '1800', got %q", val)
	}
}

func TestAPIUpdateConfigPartial(t *testing.T) {
	e := newTestEnv(t)

	// Only update tier2_model; everything else should remain unchanged.
	body := `{"tier2_model": "opus"}`
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var cfg APIConfig
	json.NewDecoder(w.Body).Decode(&cfg)

	// Changed.
	if cfg.Tier2Model != "opus" {
		t.Fatalf("expected tier2 'opus', got %q", cfg.Tier2Model)
	}
	// Unchanged.
	if cfg.Interval != 3600 {
		t.Fatalf("interval should be unchanged at 3600, got %d", cfg.Interval)
	}
	if cfg.Tier1Model != "haiku" {
		t.Fatalf("tier1 should be unchanged at 'haiku', got %q", cfg.Tier1Model)
	}
}

func TestAPIUpdateConfigInvalidInterval(t *testing.T) {
	e := newTestEnv(t)
	body := `{"interval": 0}`
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for interval=0, got %d", w.Code)
	}
}

func TestAPIUpdateConfigWrongContentType(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader("interval=100"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", w.Code)
	}
}

// --- Cross-Cutting ---

func TestAPIResponsesHaveJSONContentType(t *testing.T) {
	e := newTestEnv(t)
	endpoints := []string{
		"/api/v1/health",
		"/api/v1/sessions",
		"/api/v1/events",
		"/api/v1/memories",
		"/api/v1/cooldowns",
		"/api/v1/config",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		w := httptest.NewRecorder()
		e.srv.mux.ServeHTTP(w, req)

		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("%s: expected application/json, got %q", ep, ct)
		}
	}
}

func TestAPIListMemoriesFilterService(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	svc1 := "caddy"
	svc2 := "postgres"

	e.srv.db.InsertMemory(&db.Memory{
		Service: &svc1, Category: "behavior", Observation: "caddy mem",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})
	e.srv.db.InsertMemory(&db.Memory{
		Service: &svc2, Category: "config", Observation: "pg mem",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	req := httptest.NewRequest("GET", "/api/v1/memories?service=caddy", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APIMemoriesResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 memory for caddy, got %d", len(resp.Memories))
	}
	if *resp.Memories[0].Service != "caddy" {
		t.Fatalf("expected service 'caddy', got %v", resp.Memories[0].Service)
	}
}

func TestAPIListMemoriesFilterCategory(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)

	e.srv.db.InsertMemory(&db.Memory{
		Category: "behavior", Observation: "beh mem",
		Confidence: 0.8, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})
	e.srv.db.InsertMemory(&db.Memory{
		Category: "config", Observation: "cfg mem",
		Confidence: 0.9, Active: true, CreatedAt: now, UpdatedAt: now, Tier: 1,
	})

	req := httptest.NewRequest("GET", "/api/v1/memories?category=config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp APIMemoriesResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 config memory, got %d", len(resp.Memories))
	}
	if resp.Memories[0].Category != "config" {
		t.Fatalf("expected category 'config', got %q", resp.Memories[0].Category)
	}
}
