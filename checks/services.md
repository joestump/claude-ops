<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-8 (no build step), REQ-9 (self-documenting) -->

# Service-Specific Checks

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->
<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

These are checks for specific applications that go beyond basic HTTP/container health. Claude should identify which of these apply based on the services discovered in the inventory. Not all services below will be present in every environment — only run checks for services that appear in the repo's inventory.

## Arr Stack (Sonarr, Radarr, Prowlarr, etc.)

### Prowlarr
```bash
# Check indexer status
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v1/indexerstatus"

# Check indexer health
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v1/health"
```

Replace `<api_key>` with the service's API key from the inventory (typically found under `homepage.widget.key` or similar). Replace `<host>` with the service's hostname from the CLAUDE-OPS.md manifest.

- Healthy: all indexers responding, no auth errors
- Degraded: some indexers failing (may need API key rotation)
- Down: Prowlarr itself not responding

### Sonarr / Radarr
```bash
# System health
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v3/health"

# Queue status
curl -s -H "X-Api-Key: <api_key>" "http://<host>/api/v3/queue/status"
```
- Healthy: no health warnings, queue processing normally
- Degraded: health warnings present (disk space, indexer issues)

## Download Clients

### SABnzbd
```bash
curl -s "http://<host>/api?mode=queue&apikey=<key>&output=json"
```
- Healthy: queue accessible, no stalled downloads
- Degraded: stalled downloads present

### qBittorrent
```bash
# Requires authentication first
curl -s "http://<host>/api/v2/torrents/info?filter=errored"
```
- Healthy: no errored torrents
- Degraded: errored torrents present

## Media Servers

### Jellyfin
```bash
# System info
curl -s -H "X-Emby-Token: <api_key>" "http://<host>/System/Info"

# Active sessions
curl -s -H "X-Emby-Token: <api_key>" "http://<host>/Sessions"
```
- Healthy: system info responds, libraries accessible

### Plex
```bash
curl -s -H "X-Plex-Token: <token>" "http://<host>/status/sessions"
```

## General Pattern

For any service with an API key or token in the inventory config:
1. Try the health/status endpoint first — if available, it gives the richest status information
2. If 401/403: API key may be expired or revoked. Check whether the service requires authentication before concluding the key is bad — some services return 401 by design on certain endpoints.
3. If 200 with warnings: service is degraded
4. If connection refused: service is down

API keys are typically found in service configs or the inventory under labels like `homepage.widget.key`, `api_key`, or similar fields. The exact label varies by service — adapt to whatever the inventory uses.
