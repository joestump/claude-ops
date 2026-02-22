<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->
# Service-Specific Checks

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->
<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

These are checks for specific applications that go beyond basic HTTP/container health. Claude should identify which of these apply based on the services discovered in the inventory. Not all services below will be present in every environment — only run checks for services that appear in the repo's inventory.

## When to Run

For any service in the inventory that has an API key, token, or service-specific health endpoint. Run these after the standard HTTP and container checks to get deeper insight into application-level health.

## How to Check

### Arr Stack (Sonarr, Radarr, Prowlarr, etc.)

#### Prowlarr
```bash
# Check indexer status
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v1/indexerstatus"

# Check indexer health
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v1/health"
```
<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->
#### Sonarr / Radarr
```bash
# System health
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v3/health"

# Queue status
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v3/queue/status"
```

### Download Clients

#### SABnzbd
```bash
curl -s "http://<host>/api?mode=queue&apikey=<key>&output=json"
```

#### qBittorrent
```bash
# Requires authentication first
curl -s "http://<host>/api/v2/torrents/info?filter=errored"
```

### Media Servers

#### Jellyfin
```bash
# System info
curl -s -H "X-Emby-Token: <api_key>" "http://<host>/System/Info"

# Active sessions
curl -s -H "X-Emby-Token: <api_key>" "http://<host>/Sessions"
```

#### Plex
```bash
curl -s -H "X-Plex-Token: <token>" "http://<host>/status/sessions"
```

### General Pattern

For any service with an API key or token in the inventory config:
1. Try the health/status endpoint first — if available, it gives the richest status information
2. If 401/403: API key may be expired or revoked. Check whether the service requires authentication before concluding the key is bad — some services return 401 by design on certain endpoints.
3. If 200 with warnings: service is degraded
4. If connection refused: service is down

<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->