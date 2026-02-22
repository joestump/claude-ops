# Skill: HTTP Request

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Perform HTTP health checks and REST API interactions against services defined in repo inventories. Use this skill to verify that web services are responding, check HTTP status codes, inspect response headers, and interact with service APIs for monitoring and diagnostics.

This is primarily a read-only skill used for health checks and API queries. Mutating HTTP operations (POST, PUT, DELETE to service APIs) are permitted at Tier 2+ for safe remediation tasks like cache clearing or API key updates.

## Tier Requirement

Tier 1 minimum for read-only health checks (GET requests, HEAD requests).
Tier 2 minimum for mutating HTTP operations (POST, PUT, DELETE to service APIs).

## Tool Discovery

This skill uses the following tools in preference order:
1. **Built-in**: `WebFetch` — always available in the Claude Code environment
2. **MCP**: `mcp__fetch__fetch` — check if available in tool listing
3. **CLI**: `curl` — check with `which curl`

## Execution

### HTTP Health Check (GET)

#### Using Built-in: WebFetch

1. Call `WebFetch` with the service URL and a prompt to extract status information.
2. WebFetch fetches the URL and returns processed content.
3. Check for successful response (the tool handles HTTP internally).
4. Log: `[skill:http-request] Using: WebFetch (built-in)`

**Limitations**: WebFetch processes content through an AI model and may not return raw status codes. For precise status code checking, prefer `curl`.

#### Using MCP: mcp__fetch__fetch

1. Call `mcp__fetch__fetch` with the URL.
2. Check the response status code and body.
3. Log: `[skill:http-request] Using: mcp__fetch__fetch (MCP)`
4. If WebFetch was preferred but insufficient (need raw status codes), log: `[skill:http-request] Using: mcp__fetch__fetch (MCP) for precise status code`

#### Using CLI: curl

1. For a basic health check:
   ```bash
   curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 --max-time 30 "<url>"
   ```
2. For a health check with response body:
   ```bash
   curl -s --connect-timeout 10 --max-time 30 "<url>"
   ```
3. For a health check with headers:
   ```bash
   curl -s -I --connect-timeout 10 --max-time 30 "<url>"
   ```
4. Log: `[skill:http-request] Using: curl (CLI)`
5. If WebFetch and MCP were preferred but unavailable, also log: `[skill:http-request] WARNING: WebFetch and Fetch MCP not suitable, falling back to curl (CLI)`

### DNS Health Check

#### Using CLI: dig / nslookup

1. Resolve the hostname:
   ```bash
   dig +short <hostname>
   ```
   or:
   ```bash
   nslookup <hostname>
   ```
2. Confirm the hostname resolves to an expected IP address.
3. Log: `[skill:http-request] Using: dig (CLI)` or `[skill:http-request] Using: nslookup (CLI)`

### REST API Query (GET)

#### Using CLI: curl (with authentication)

1. For authenticated API requests:
   ```bash
   curl -s -H "Authorization: Bearer $TOKEN" --connect-timeout 10 --max-time 30 "<api_url>"
   ```
2. Parse the JSON response to extract relevant fields.
3. Log: `[skill:http-request] Using: curl (CLI)`

### Mutating API Request (POST/PUT/DELETE) — Tier 2+

#### Using CLI: curl

1. Verify the current tier is 2 or higher.
2. Construct the appropriate request:
   ```bash
   curl -s -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
     -d '{"key": "value"}' "<api_url>"
   ```
3. Log: `[skill:http-request] Using: curl (CLI)`

## Validation

After a health check:
1. Confirm an HTTP status code was obtained.
2. Report the result: `<service> (<url>): HTTP <status_code>` — e.g., `shamrock (shamrock.stump.rocks): HTTP 200`.
3. Flag unexpected status codes:
   - 2xx: healthy
   - 3xx: redirect (may be expected — e.g., 302 for auth-protected services)
   - 4xx: client error (401/403 may be expected for authenticated endpoints)
   - 5xx: server error (unhealthy)
   - Connection timeout/refused: unreachable

After a DNS check:
1. Report whether the hostname resolved and to what IP.
2. Flag if resolution failed: `<hostname>: DNS resolution failed`.

After an API request:
1. Confirm the response is valid JSON (or expected format).
2. Report any error messages in the response body.

## Scope Rules

This skill MUST NOT:
- Send HTTP requests to hosts not defined in repo inventories
- Send mutating requests (POST, PUT, DELETE) at Tier 1
- Modify authentication credentials or tokens via API calls without explicit playbook authorization
- Send requests to localhost or 127.0.0.1 unless a repo's CLAUDE-OPS.md explicitly lists localhost as a target host

If a scope violation is detected, the agent MUST:
1. Refuse the operation.
2. Report: `[skill:http-request] SCOPE VIOLATION: <reason>`

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- Read-only health checks (GET, HEAD, DNS) MAY still execute, as they do not modify state.
- Mutating HTTP operations (POST, PUT, DELETE) MUST NOT execute.
- Log: `[skill:http-request] DRY RUN: Would send <method> to <url> using <tool>`
