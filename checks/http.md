# HTTP Health Checks

## When to Run

For every service that has a web endpoint (URL, hostname, or port with HTTP/HTTPS).

## How to Check

```bash
# Basic availability check with response code and timing
curl -s -o /dev/null -w "HTTP %{http_code} in %{time_total}s" --max-time 10 <url>
```

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

- Services behind authentication may return 401/403 — this is expected and healthy
- Some services redirect to a setup wizard on first run — note this but don't flag as unhealthy
- If a service has a dedicated `/health` or `/api/health` endpoint, prefer that over the root URL
- For services with homepage widget URLs (e.g., `homepage.widget.url`), check those as they often expose richer health info
