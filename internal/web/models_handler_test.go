package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/joestump/claude-ops/internal/models"
)

// stubUpstream returns an httptest server speaking the OpenAI /v1/models shape,
// plus a Discoverer pointed at it.
func stubUpstream(t *testing.T, ids ...string) (*httptest.Server, *models.Discoverer) {
	t.Helper()
	body := `{"object":"list","data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `"}`
	}
	body += `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	d := models.New(func() string { return srv.URL }, func() string { return "" })
	return srv, d
}

// Governing: SPEC-0035 REQ "Available Models API Endpoint" — list with metadata.
func TestAPIModelsAvailable_List(t *testing.T) {
	e := newTestEnv(t)
	_, d := stubUpstream(t, "gemini-2.5-pro", "deepseek-chat")
	e.srv.discoverer = d

	req := httptest.NewRequest("GET", "/api/v1/models/available", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var resp APIAvailableModelsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DiscoveryAvailable {
		t.Fatalf("expected discovery_available=true")
	}
	if len(resp.Models) != 2 || resp.Models[0] != "deepseek-chat" || resp.Models[1] != "gemini-2.5-pro" {
		t.Fatalf("expected sorted models, got %v", resp.Models)
	}
	if resp.LastRefreshed == nil {
		t.Fatalf("expected last_refreshed to be set")
	}
}

// Governing: SPEC-0035 REQ "Graceful Degradation" — unavailable returns 200 + empty + flag.
func TestAPIModelsAvailable_Unavailable(t *testing.T) {
	e := newTestEnv(t)
	// Discoverer with no base URL → unavailable, no request attempted.
	e.srv.discoverer = models.New(func() string { return "" }, func() string { return "" })

	req := httptest.NewRequest("GET", "/api/v1/models/available", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when unavailable, got %d", w.Code)
	}
	var resp APIAvailableModelsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DiscoveryAvailable {
		t.Fatalf("expected discovery_available=false")
	}
	if len(resp.Models) != 0 {
		t.Fatalf("expected empty models, got %v", resp.Models)
	}
	if resp.Models == nil {
		t.Fatalf("expected non-nil (JSON []) models slice")
	}
}

// Governing: SPEC-0035 REQ "Available Models API Endpoint" — explicit refresh endpoint.
func TestAPIModelsRefresh(t *testing.T) {
	e := newTestEnv(t)
	_, d := stubUpstream(t, "m1")
	e.srv.discoverer = d

	req := httptest.NewRequest("POST", "/api/v1/models/available/refresh", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp APIAvailableModelsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.DiscoveryAvailable || len(resp.Models) != 1 {
		t.Fatalf("expected refreshed list, got %+v", resp)
	}
}

// Governing: SPEC-0035 REQ "Configuration UI Model Selection" — dropdown populated.
func TestConfigPage_DropdownPopulated(t *testing.T) {
	e := newTestEnv(t) // cfg.Tier1Model="haiku", Tier2="sonnet", Tier3="opus"
	_, d := stubUpstream(t, "gemini-2.5-pro", "deepseek-chat")
	e.srv.discoverer = d

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	html := w.Body.String()
	if !strings.Contains(html, `<select id="tier1_model"`) {
		t.Fatalf("expected a select for tier1_model when discovery available")
	}
	for _, id := range []string{"gemini-2.5-pro", "deepseek-chat"} {
		if !strings.Contains(html, `value="`+id+`"`) {
			t.Fatalf("expected discovered model %q as an option", id)
		}
	}
}

// Governing: SPEC-0035 REQ "Configuration UI Model Selection" — current non-discovered value preserved.
func TestConfigPage_PreservesCurrentValue(t *testing.T) {
	e := newTestEnv(t) // Tier1Model="haiku" (not in discovered list below)
	_, d := stubUpstream(t, "gemini-2.5-pro")
	e.srv.discoverer = d

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	html := w.Body.String()
	// The current value "haiku" must appear as a selected option even though it
	// is not among the discovered models.
	if !strings.Contains(html, `<option value="haiku" selected>haiku</option>`) {
		t.Fatalf("expected current non-discovered tier1 value 'haiku' preserved as selected option")
	}
}

// Governing: SPEC-0035 REQ "Graceful Degradation" — free-text fallback when unavailable.
func TestConfigPage_FreeTextFallback(t *testing.T) {
	e := newTestEnv(t)
	e.srv.discoverer = models.New(func() string { return "" }, func() string { return "" })

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected config page to render when discovery unavailable, got %d", w.Code)
	}
	html := w.Body.String()
	if !strings.Contains(html, `<input type="text" id="tier1_model"`) {
		t.Fatalf("expected free-text input fallback for tier1_model when discovery unavailable")
	}
	if !strings.Contains(html, `value="haiku"`) {
		t.Fatalf("expected current tier1 value preserved in free-text fallback")
	}
}

// Governing: SPEC-0035 REQ "Configuration UI Model Selection" — HTMX refresh swap.
func TestConfigModelsRefresh_Swap(t *testing.T) {
	e := newTestEnv(t)
	_, d := stubUpstream(t, "new-model-x")
	e.srv.discoverer = d

	form := url.Values{}
	form.Set("tier1_model", "keep-this") // an unsaved selection that must survive refresh
	req := httptest.NewRequest("POST", "/config/models/refresh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	html := w.Body.String()
	if !strings.Contains(html, `id="model-fields"`) {
		t.Fatalf("expected the model-fields swap target in the response")
	}
	if !strings.Contains(html, `value="new-model-x"`) {
		t.Fatalf("expected refreshed model option in swap, got: %s", html)
	}
	// The submitted (unsaved) tier1 value must be preserved across refresh.
	if !strings.Contains(html, `value="keep-this" selected`) {
		t.Fatalf("expected unsaved tier1 selection 'keep-this' preserved across refresh")
	}
}
