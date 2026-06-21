// Package models discovers the set of LLM models available on the upstream
// Anthropic-compatible gateway (e.g. LiteLLM) that Claude Ops routes through.
// The gateway is configured via the ANTHROPIC_BASE_URL environment variable and
// authenticated with ANTHROPIC_API_KEY.
//
// Discovery uses the official Anthropic Go SDK (already a project dependency, and
// the same client family Claude Ops uses elsewhere) pointed at the configured
// base URL: client.Models.List. This avoids a hand-rolled HTTP client and a
// redundant second LLM SDK.
//
// Governing: SPEC-0035 (Upstream Model Auto-Discovery). The discovery SOURCE is
// the upstream gateway's models endpoint — distinct from Claude Ops' OWN served
// /v1/models endpoint (SPEC-0024 / ADR-0020).
package models

import (
	"context"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
	// Bound the response body size regardless of which client is used, so an
	// unexpectedly large upstream body cannot exhaust memory.
	// Governing: SPEC-0035 REQ "Request Body Size Limits".
	rt := d.client.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	d.client.Transport = &limitedTransport{rt: rt, max: maxBodyBytes}
	return d
}

// limitedTransport caps the size of response bodies returned to the SDK.
type limitedTransport struct {
	rt  http.RoundTripper
	max int64
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedReadCloser{r: io.LimitReader(resp.Body, t.max), c: resp.Body}
	return resp, nil
}

type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }

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

// fetch lists models from the upstream gateway via the Anthropic SDK and returns
// a de-duplicated, sorted list of model IDs. The SDK is pointed at the configured
// base URL with the upstream key; the key is passed only to the SDK (in the auth
// header it sets) and is never logged. The bounded http.Client (with a body-size
// limit on its transport) and the SDK request timeout enforce the time and size
// bounds.
// Governing: SPEC-0035 REQ "Upstream Model Query", REQ "Upstream Call Security and Error Handling".
func (d *Discoverer) fetch(ctx context.Context) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(d.baseURLFn()), "/")

	opts := []option.RequestOption{
		option.WithBaseURL(base),
		option.WithHTTPClient(d.client),
		option.WithRequestTimeout(d.client.Timeout),
		// Discovery is best-effort; don't let the SDK retry a slow/failing
		// gateway and blow past our bound.
		option.WithMaxRetries(0),
	}
	if key := strings.TrimSpace(d.apiKeyFn()); key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	client := anthropic.NewClient(opts...)

	var ids []string
	pager := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for pager.Next() {
		ids = append(ids, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		// The SDK error wraps the HTTP status / transport error (timeout,
		// connection refused, non-2xx) and never contains the API key value.
		log.Printf("model discovery: upstream gateway list failed: %v", err)
		return nil, err
	}

	return dedupeSort(ids), nil
}

// dedupeSort trims, de-duplicates, and sorts model IDs for a stable presentation
// order.
func dedupeSort(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
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
