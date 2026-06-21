// Package models discovers the set of LLM models available on the upstream
// OpenAI/Anthropic-compatible gateway (e.g. LiteLLM) that Claude Ops routes
// through. The gateway is configured via the ANTHROPIC_BASE_URL environment
// variable and authenticated with ANTHROPIC_API_KEY.
//
// Governing: SPEC-0035 (Upstream Model Auto-Discovery). The discovery SOURCE is
// the upstream gateway's GET ${ANTHROPIC_BASE_URL}/v1/models — distinct from
// Claude Ops' OWN served /v1/models endpoint (SPEC-0024 / ADR-0020).
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// defaultTimeout bounds the upstream HTTP request so a slow or unreachable
	// gateway cannot stall configuration rendering.
	// Governing: SPEC-0035 REQ "Upstream Model Query" (bounded timeout, RECOMMENDED 5s).
	defaultTimeout = 5 * time.Second

	// defaultTTL is how long a discovered list is served from cache before a
	// refresh is attempted on next access.
	// Governing: SPEC-0035 REQ "Discovered Model Caching and Refresh" (RECOMMENDED 5m).
	defaultTTL = 5 * time.Minute

	// maxBodyBytes bounds how much of the upstream response we read, so an
	// unexpectedly large body cannot exhaust memory.
	// Governing: SPEC-0035 REQ "Request Body Size Limits".
	maxBodyBytes = 1 << 20 // 1 MiB
)

// Result is a point-in-time snapshot of upstream model discovery, suitable for
// returning to API clients and rendering in the config UI.
// Governing: SPEC-0035 REQ "Available Models API Endpoint" (freshness metadata).
type Result struct {
	// Models is the de-duplicated, sorted list of upstream model IDs. Empty
	// when discovery is unavailable.
	Models []string `json:"models"`
	// LastRefreshed is the time of the last successful upstream query, or the
	// zero value if discovery has never succeeded.
	LastRefreshed time.Time `json:"last_refreshed"`
	// Available reports whether discovery is currently usable (base URL set and
	// the most recent attempt produced a list).
	Available bool `json:"discovery_available"`
}

// Discoverer queries the upstream gateway for available models and caches the
// result with a TTL, collapsing concurrent refreshes via single-flight.
//
// The zero value is not usable; construct with New.
type Discoverer struct {
	// baseURLFn and apiKeyFn are resolved lazily on each refresh so that
	// runtime changes to the environment are picked up without a restart and so
	// the API key is never captured/stored beyond the lifetime of a request.
	baseURLFn func() string
	apiKeyFn  func() string

	client *http.Client
	ttl    time.Duration

	mu            sync.Mutex
	models        []string
	lastRefreshed time.Time
	available     bool
	refreshing    bool       // single-flight guard
	cond          *sync.Cond // wakes waiters when an in-flight refresh finishes
}

// Option configures a Discoverer.
type Option func(*Discoverer)

// WithTTL overrides the cache time-to-live.
func WithTTL(ttl time.Duration) Option {
	return func(d *Discoverer) {
		if ttl > 0 {
			d.ttl = ttl
		}
	}
}

// WithHTTPClient overrides the HTTP client (primarily for tests).
func WithHTTPClient(c *http.Client) Option {
	return func(d *Discoverer) {
		if c != nil {
			d.client = c
		}
	}
}

// New constructs a Discoverer. baseURLFn and apiKeyFn are called on each refresh
// to resolve the current upstream endpoint and credential; both may return "".
// Governing: SPEC-0035 REQ "Upstream Model Query", REQ "Graceful Degradation".
func New(baseURLFn, apiKeyFn func() string, opts ...Option) *Discoverer {
	d := &Discoverer{
		baseURLFn: baseURLFn,
		apiKeyFn:  apiKeyFn,
		client:    &http.Client{Timeout: defaultTimeout},
		ttl:       defaultTTL,
	}
	d.cond = sync.NewCond(&d.mu)
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Available returns the current discovered models, refreshing from the upstream
// gateway if the cache is stale. It never returns an error: when discovery is
// unavailable it returns a Result with Available=false and the most recent
// successfully-cached list (empty if there has never been one).
// Governing: SPEC-0035 REQ "Discovered Model Caching and Refresh", REQ "Graceful Degradation".
func (d *Discoverer) Available(ctx context.Context) Result {
	return d.get(ctx, false)
}

// Refresh forces an immediate upstream re-query, bypassing the TTL, and returns
// the resulting snapshot. Concurrent refreshes collapse to a single in-flight
// upstream call via single-flight.
// Governing: SPEC-0035 REQ "Discovered Model Caching and Refresh" (explicit refresh),
// REQ "Rate Limiting" (single-flight).
func (d *Discoverer) Refresh(ctx context.Context) Result {
	return d.get(ctx, true)
}

func (d *Discoverer) get(ctx context.Context, force bool) Result {
	d.mu.Lock()

	// No upstream configured: discovery is unavailable, never attempt a request.
	// Governing: SPEC-0035 REQ "Graceful Degradation" (base URL unset).
	if strings.TrimSpace(d.baseURLFn()) == "" {
		d.available = false
		res := d.snapshotLocked()
		d.mu.Unlock()
		return res
	}

	fresh := time.Since(d.lastRefreshed) < d.ttl && !d.lastRefreshed.IsZero()
	if !force && fresh {
		res := d.snapshotLocked()
		d.mu.Unlock()
		return res
	}

	// Single-flight: if a refresh is already running, wait for it rather than
	// issuing a second upstream request.
	if d.refreshing {
		for d.refreshing {
			d.cond.Wait()
		}
		res := d.snapshotLocked()
		d.mu.Unlock()
		return res
	}

	d.refreshing = true
	d.mu.Unlock()

	models, err := d.fetch(ctx)

	d.mu.Lock()
	if err != nil {
		// Discovery failed for this attempt: keep any prior list but mark
		// unavailable. Error already logged in fetch with non-secret context.
		// Governing: SPEC-0035 REQ "Graceful Degradation", REQ "Upstream Call Security and Error Handling".
		d.available = false
	} else {
		d.models = models
		d.lastRefreshed = time.Now()
		d.available = true
	}
	d.refreshing = false
	d.cond.Broadcast()
	res := d.snapshotLocked()
	d.mu.Unlock()
	return res
}

// snapshotLocked returns a copy of the current cache state. Caller must hold d.mu.
func (d *Discoverer) snapshotLocked() Result {
	out := make([]string, len(d.models))
	copy(out, d.models)
	return Result{
		Models:        out,
		LastRefreshed: d.lastRefreshed,
		Available:     d.available,
	}
}

// openAIModelList mirrors the OpenAI-style models response:
// {"object":"list","data":[{"id":"...","object":"model",...}]}.
type openAIModelList struct {
	Object string `json:"object"`
	Data   []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetch performs the upstream GET, parses the body, and returns a de-duplicated,
// sorted list of model IDs. The upstream API key is sent only in the
// Authorization header and never logged.
// Governing: SPEC-0035 REQ "Upstream Model Query", REQ "Upstream Call Security and Error Handling".
func (d *Discoverer) fetch(ctx context.Context) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(d.baseURLFn()), "/")
	url := base + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discover upstream models: build request: %w", err)
	}
	if key := strings.TrimSpace(d.apiKeyFn()); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		// %w preserves the underlying error (timeout, connection refused) which
		// never contains the API key.
		log.Printf("model discovery: request to upstream gateway failed: %v", err)
		return nil, fmt.Errorf("discover upstream models: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("model discovery: upstream gateway returned status=%d", resp.StatusCode)
		return nil, fmt.Errorf("discover upstream models: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		log.Printf("model discovery: reading upstream response failed: %v", err)
		return nil, fmt.Errorf("discover upstream models: read body: %w", err)
	}

	var parsed openAIModelList
	if err := json.Unmarshal(body, &parsed); err != nil {
		log.Printf("model discovery: parsing upstream response failed: %v", err)
		return nil, fmt.Errorf("discover upstream models: parse body: %w", err)
	}

	return dedupeSort(parsed.Data), nil
}

// dedupeSort extracts non-empty model IDs, removes duplicates, and sorts them
// for a stable presentation order.
func dedupeSort(data []struct {
	ID string `json:"id"`
}) []string {
	seen := make(map[string]struct{}, len(data))
	out := make([]string, 0, len(data))
	for _, m := range data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
