package models

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// modelsJSON builds an Anthropic-style models list response body.
func modelsJSON(ids ...string) string {
	body := `{"data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `","type":"model"}`
	}
	body += `],"has_more":false}`
	return body
}

// Governing: SPEC-0035 REQ "Upstream Model Query" — fetch, parse, dedupe, sort.
func TestAvailable_ParsesAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("expected /v1/models, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON("gemini-2.5-pro", "deepseek-chat", "claude-opus-4-8", "deepseek-chat")))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" })
	res := d.Available(context.Background())

	if !res.Available {
		t.Fatalf("expected discovery available")
	}
	want := []string{"claude-opus-4-8", "deepseek-chat", "gemini-2.5-pro"}
	if len(res.Models) != len(want) {
		t.Fatalf("got %v, want %v", res.Models, want)
	}
	for i := range want {
		if res.Models[i] != want[i] {
			t.Fatalf("got %v, want %v (deduped+sorted)", res.Models, want)
		}
	}
	if res.LastRefreshed.IsZero() {
		t.Errorf("expected LastRefreshed to be set")
	}
}

// Governing: SPEC-0035 REQ "Upstream Model Query" — trailing slash on base URL.
func TestAvailable_TrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON()))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL + "/" }, func() string { return "" })
	d.Available(context.Background())
	if gotPath != "/v1/models" {
		t.Fatalf("expected no doubled slash, got path %q", gotPath)
	}
}

// Governing: SPEC-0035 REQ "Upstream Model Query" — the upstream credential is
// sent to the gateway. The Anthropic SDK authenticates via the x-api-key header
// (LiteLLM accepts this for Anthropic-compatible routing).
func TestAvailable_SendsAPIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON("m")))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "secret-key" })
	d.Available(context.Background())
	if gotKey != "secret-key" {
		t.Fatalf("expected x-api-key header to carry the upstream key, got %q", gotKey)
	}
}

// Governing: SPEC-0035 REQ "Graceful Degradation" — base URL unset, no request attempted.
func TestAvailable_BaseURLUnset(t *testing.T) {
	called := false
	d := New(func() string { return "" }, func() string { return "" },
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, fmt.Errorf("should not be called")
		})}))

	res := d.Available(context.Background())
	if called {
		t.Fatal("expected no upstream request when base URL unset")
	}
	if res.Available {
		t.Fatal("expected discovery unavailable when base URL unset")
	}
	if len(res.Models) != 0 {
		t.Fatalf("expected empty model list, got %v", res.Models)
	}
}

// Governing: SPEC-0035 REQ "Graceful Degradation" — non-2xx upstream response.
func TestAvailable_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" })
	res := d.Available(context.Background())
	if res.Available {
		t.Fatal("expected discovery unavailable on non-2xx")
	}
}

// Governing: SPEC-0035 REQ "Graceful Degradation" — unparseable body.
func TestAvailable_UnparseableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" })
	res := d.Available(context.Background())
	if res.Available {
		t.Fatal("expected discovery unavailable on parse error")
	}
}

// Governing: SPEC-0035 REQ "Discovered Model Caching and Refresh" — cache hit within TTL.
func TestAvailable_CacheHit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON("m")))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" }, WithTTL(time.Hour))
	d.Available(context.Background())
	d.Available(context.Background())
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 upstream call within TTL, got %d", got)
	}
}

// Governing: SPEC-0035 REQ "Discovered Model Caching and Refresh" — explicit refresh bypasses TTL.
func TestRefresh_BypassesTTL(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON("m")))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" }, WithTTL(time.Hour))
	d.Available(context.Background())
	d.Refresh(context.Background())
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected refresh to re-query (2 calls), got %d", got)
	}
}

// Governing: SPEC-0035 REQ "Rate Limiting" — concurrent refreshes collapse to one in-flight call.
func TestRefresh_SingleFlight(t *testing.T) {
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release // hold the request open so concurrent callers pile up
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsJSON("m")))
	}))
	defer srv.Close()

	d := New(func() string { return srv.URL }, func() string { return "" })

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Refresh(context.Background())
		}()
	}
	// Give the goroutines time to converge on the single-flight guard.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected single in-flight upstream call, got %d", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
