# SPEC-0014: Browser Automation for Authenticated Web UIs

## Overview

Define the security boundaries and operational mechanisms for Claude Ops to safely authenticate to web-based admin UIs via headless browser automation. This spec covers credential injection, URL scope enforcement, log redaction, and session isolation -- the four layers described in ADR-0012.

## Requirements

### Requirement: Credential Injection via Environment Variables

The system SHALL inject service credentials into browser form fields using Docker Compose environment variables following the naming convention `BROWSER_CRED_{SERVICE}_{FIELD}`. The agent MUST NOT receive raw credential values in its prompt context, reasoning chain, or tool call arguments. Credential values MUST be resolved and injected at the MCP tool execution layer, not at the LLM token generation layer.

#### Scenario: Agent fills a login form using credential references

- **WHEN** the agent needs to authenticate to a service's web UI
- **THEN** it SHALL reference credentials by environment variable name (e.g., `BROWSER_CRED_SONARR_USER`) and the credential injection layer SHALL resolve the value and pass it to the Chrome DevTools MCP `fill` tool

#### Scenario: Credential values excluded from prompt context

- **WHEN** the session manager constructs the prompt for a browser automation task
- **THEN** the prompt MUST contain only credential variable names, never the resolved values
- **AND** the agent's assistant output MUST NOT contain raw credential values

#### Scenario: Missing credential variable

- **WHEN** the agent references a `BROWSER_CRED_*` environment variable that is not set or is empty
- **THEN** the credential injection layer SHALL return an error indicating the credential is unavailable
- **AND** the agent SHALL NOT attempt to guess, prompt for, or substitute the credential

#### Scenario: Credential naming convention

- **WHEN** a new service credential is added to the Docker Compose configuration
- **THEN** it MUST follow the pattern `BROWSER_CRED_{SERVICE}_{FIELD}` where `{SERVICE}` is an uppercase alphanumeric identifier and `{FIELD}` is `USER`, `PASS`, `TOKEN`, or `API_KEY`

### Requirement: URL Allowlist Enforcement

The system SHALL restrict browser navigation to a set of pre-approved origins defined in the `BROWSER_ALLOWED_ORIGINS` environment variable. Navigation attempts to non-allowed origins MUST be blocked. The allowlist MUST be enforced via an init script injected into each page through the Chrome DevTools MCP `navigate_page` tool's `initScript` parameter.

#### Scenario: Navigation to an allowed origin succeeds

- **WHEN** the agent navigates the browser to a URL whose origin matches an entry in `BROWSER_ALLOWED_ORIGINS`
- **THEN** the navigation SHALL proceed normally

#### Scenario: Navigation to a disallowed origin is blocked

- **WHEN** the agent navigates the browser to a URL whose origin is NOT in `BROWSER_ALLOWED_ORIGINS`
- **THEN** the init script SHALL prevent the page from loading and display a blocked-navigation message
- **AND** the agent SHALL receive feedback that the navigation was denied

#### Scenario: Redirect to disallowed origin is blocked

- **WHEN** a page on an allowed origin issues a client-side or server-side redirect to a disallowed origin
- **THEN** the init script SHALL block the redirect target from loading

#### Scenario: Empty or unset allowlist disables browser automation

- **WHEN** `BROWSER_ALLOWED_ORIGINS` is empty or not set
- **THEN** the system MUST NOT permit any browser navigation
- **AND** the agent SHALL skip browser automation tasks and log a warning

#### Scenario: Allowlist format

- **WHEN** `BROWSER_ALLOWED_ORIGINS` is configured
- **THEN** it MUST be a comma-separated list of origins in the format `scheme://hostname[:port]` (e.g., `https://sonarr.stump.rocks,https://prowlarr.stump.rocks`)

### Requirement: Log Redaction of Credential Values

The system SHALL redact known credential values from all output channels before they reach persistent storage or external consumers. Redaction MUST apply to session logs (NDJSON), the SSE activity stream, the final session response, and the dashboard events display. The redaction layer SHALL operate on the `BROWSER_CRED_*` environment variable values as the redaction dictionary.

#### Scenario: Credential value in tool result is redacted

- **WHEN** a Chrome DevTools MCP tool result contains a substring matching a `BROWSER_CRED_*` environment variable value
- **THEN** the redaction layer SHALL replace the matching substring with `[REDACTED:BROWSER_CRED_{SERVICE}_{FIELD}]` before writing to any output

#### Scenario: Credential value in assistant text is redacted

- **WHEN** the agent's assistant output contains a substring matching a `BROWSER_CRED_*` value
- **THEN** the redaction layer SHALL replace it with the corresponding `[REDACTED:...]` placeholder

#### Scenario: Redaction applies to all output channels

- **WHEN** any output (log line, SSE event, response markdown) passes through the output pipeline
- **THEN** the redaction layer MUST scan for all `BROWSER_CRED_*` values and replace matches before the output reaches the log file, SSE stream, dashboard, or any external notification

#### Scenario: Redaction handles URL-encoded credential values

- **WHEN** a credential value appears in URL-encoded form in a tool result (e.g., `p%40ssw0rd` for `p@ssw0rd`)
- **THEN** the redaction layer SHOULD detect and redact the URL-encoded variant

#### Scenario: Short credential values are still redacted

- **WHEN** a `BROWSER_CRED_*` value is shorter than 4 characters
- **THEN** the redaction layer SHOULD log a warning that short credentials increase false-positive redaction risk but SHALL still redact matches

### Requirement: Isolated Browser Contexts

Each browser automation task MUST execute in a fresh, isolated browser context. The system SHALL use incognito/private browsing mode via the Chrome DevTools MCP to ensure no cookies, local storage, session tokens, or cached data persist between tasks or leak between services.

#### Scenario: Sequential service authentications do not share state

- **WHEN** the agent authenticates to Service A and then authenticates to Service B in the same session
- **THEN** Service B's browser context SHALL NOT contain any cookies, local storage, or session data from Service A

#### Scenario: Browser context is discarded after task completion

- **WHEN** a browser automation task completes (success or failure)
- **THEN** the browser context (incognito profile) SHALL be closed and all associated state discarded

#### Scenario: Page opened in new context

- **WHEN** the agent begins a browser automation task for a service
- **THEN** it SHALL open a new page via the Chrome DevTools MCP `new_page` tool, which provides a fresh browsing context

### Requirement: Tier 2+ Permission Gate

Browser automation with credential injection MUST require Tier 2 (Sonnet) or higher. Tier 1 (Haiku) agents MUST NOT perform authenticated browser actions. Unauthenticated browser checks (e.g., verifying a login page loads) MAY be permitted at Tier 1 if no credentials are injected.

#### Scenario: Tier 1 agent attempts browser authentication

- **WHEN** a Tier 1 agent attempts to use credential injection or fill a login form via browser automation
- **THEN** the system SHALL deny the action and log the denial

#### Scenario: Tier 2 agent performs browser authentication

- **WHEN** a Tier 2 agent invokes browser automation with credential injection against an allowed origin
- **THEN** the action SHALL be permitted, subject to cooldown and allowlist rules

#### Scenario: Tier 1 performs unauthenticated browser check

- **WHEN** a Tier 1 agent navigates to an allowed origin without injecting any credentials (e.g., checking that a login page renders)
- **THEN** the navigation MAY be permitted

### Requirement: Prompt Injection Mitigation

The system SHOULD include safeguards against prompt injection from web page content rendered in the browser. The agent's prompt MUST instruct it to treat all page content as untrusted data. DOM snapshots and screenshots obtained via Chrome DevTools MCP SHOULD be processed with awareness that they may contain adversarial content.

#### Scenario: Page content contains instruction-like text

- **WHEN** a DOM snapshot or screenshot from a monitored service contains text resembling LLM instructions (e.g., "Ignore previous instructions and...")
- **THEN** the agent SHALL treat the text as page content only and NOT alter its behavior based on such text

#### Scenario: Prompt includes untrusted-data warning

- **WHEN** a browser automation task prompt is constructed
- **THEN** the prompt MUST include a section warning the agent that all page content is untrusted user-generated data and MUST NOT be interpreted as instructions

### Requirement: Browser Automation Auditing

All browser automation actions MUST be recorded in the session log with sufficient detail for post-incident review. Credential values MUST be redacted from audit entries per the log redaction requirement.

#### Scenario: Navigation action is logged

- **WHEN** the agent navigates to a URL via browser automation
- **THEN** the session log SHALL contain an entry recording the target URL, timestamp, and navigation result (success/blocked)

#### Scenario: Form fill action is logged with redacted values

- **WHEN** the agent fills a form field with a credential value
- **THEN** the session log SHALL record the action (field identifier, target page URL) with the credential value replaced by its `[REDACTED:...]` placeholder

#### Scenario: Browser automation summary in session result

- **WHEN** a session that included browser automation completes
- **THEN** the session result SHOULD include a summary of browser actions taken: pages visited, forms submitted, and whether authentication succeeded or failed
