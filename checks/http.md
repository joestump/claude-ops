<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->
# HTTP Health Checks

## When to Run

For every service that has a web endpoint (URL, hostname, or port with HTTP/HTTPS).

## How to Check

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

```bash
# Basic availability check with response code and timing
curl -s -o /dev/null -w "HTTP %{http_code} in %{time_total}s" --max-time 10 <url>
```

Replace `<url>` with the service's actual URL from the inventory (e.g., `https://jellyfin.example.com`). If the service exposes a dedicated health endpoint, use that instead (e.g., `https://jellyfin.example.com/health`).

## What's Healthy

- HTTP 200-299: healthy
- HTTP 301-399: healthy (redirect, likely to a login page)
- HTTP 401/403: healthy (service is up, just requires auth)
- HTTP 500-599: unhealthy (server error)
- HTTP 502/503/504: unhealthy (service down or overloaded)
- Connection refused / timeout: down

## What to Record

For each endpoint checked:
- Service name
- URL checked
- HTTP status code
- Response time (seconds)
- Whether it's healthy/degraded/down

## Special Cases

<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

- Services behind authentication may return 401/403 — this is expected and healthy
- Some services redirect to a setup wizard on first run — note this but don't flag as unhealthy
- If a service has a dedicated `/health` or `/api/health` endpoint, prefer that over the root URL
- For services with homepage widget URLs (e.g., `homepage.widget.url`), check those as they often expose richer health info
