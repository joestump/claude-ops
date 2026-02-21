# Skill: Browser Automation

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Interact with web UIs for tasks that cannot be accomplished via APIs or CLIs. Primary use cases include credential rotation through web-based admin panels, interacting with services that only expose web interfaces, and verifying visual state of web applications. This skill requires the Chrome DevTools MCP sidecar or a Playwright-compatible environment.

## Tier Requirement

Tier 2 minimum. Browser automation can perform mutating actions (form submissions, credential changes). Tier 1 agents MUST NOT execute this skill and MUST escalate to Tier 2. Read-only browser inspection (taking screenshots, checking page content) is still classified as Tier 2 because it requires the browser sidecar infrastructure.

## Tool Discovery

This skill uses the following tools in preference order:
1. **MCP**: `mcp__chrome-devtools__navigate_page`, `mcp__chrome-devtools__click`, `mcp__chrome-devtools__fill`, `mcp__chrome-devtools__take_screenshot`, `mcp__chrome-devtools__take_snapshot`, `mcp__chrome-devtools__evaluate_script` — check if available in tool listing
2. **CLI**: `playwright` — check with `which npx` (run via `npx playwright`)

**Note**: Browser automation requires either the Chrome sidecar container (for Chrome DevTools MCP) or a local Playwright installation. If neither is available, this skill cannot execute. Unlike other skills, there is no universal HTTP fallback — the purpose of this skill is to interact with UIs that do not have API equivalents.

## Execution

### Navigate to a Web Page

#### Using MCP: mcp__chrome-devtools__navigate_page

1. Call `mcp__chrome-devtools__navigate_page` with the target URL.
2. Wait for the page to load.
3. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__navigate_page (MCP)`

#### Using CLI: playwright

1. This is a complex fallback. Create a temporary script or use Playwright's CLI:
   ```bash
   npx playwright open "<url>"
   ```
   For automated scripts, use Playwright's codegen or a pre-written script.
2. Log: `[skill:browser-automation] Using: playwright (CLI)`
3. Also log: `[skill:browser-automation] WARNING: Chrome DevTools MCP not available, falling back to playwright (CLI)`

### Fill a Form Field

#### Using MCP: mcp__chrome-devtools__fill

1. Call `mcp__chrome-devtools__fill` with the selector and value.
2. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__fill (MCP)`

### Click an Element

#### Using MCP: mcp__chrome-devtools__click

1. Call `mcp__chrome-devtools__click` with the element selector.
2. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__click (MCP)`

### Fill a Complete Form

#### Using MCP: mcp__chrome-devtools__fill_form

1. Call `mcp__chrome-devtools__fill_form` with a mapping of selectors to values.
2. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__fill_form (MCP)`

### Take a Screenshot

#### Using MCP: mcp__chrome-devtools__take_screenshot

1. Call `mcp__chrome-devtools__take_screenshot` to capture the current page state.
2. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__take_screenshot (MCP)`

### Take a DOM Snapshot

#### Using MCP: mcp__chrome-devtools__take_snapshot

1. Call `mcp__chrome-devtools__take_snapshot` to get a text representation of the page DOM.
2. Useful for reading page content without visual rendering.
3. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__take_snapshot (MCP)`

### Execute JavaScript

#### Using MCP: mcp__chrome-devtools__evaluate_script

1. Call `mcp__chrome-devtools__evaluate_script` with the JavaScript code.
2. Use for extracting data from the page or performing actions not easily expressed via selectors.
3. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__evaluate_script (MCP)`

### Handle Dialogs (Alerts, Confirms, Prompts)

#### Using MCP: mcp__chrome-devtools__handle_dialog

1. Call `mcp__chrome-devtools__handle_dialog` to accept or dismiss the dialog.
2. Log: `[skill:browser-automation] Using: mcp__chrome-devtools__handle_dialog (MCP)`

### Credential Rotation Workflow

This is a common multi-step workflow combining several browser actions:

1. Navigate to the service's admin panel or settings page.
2. Take a snapshot to understand the page structure.
3. Locate the credential/API key field.
4. Fill the new credential value.
5. Click the save/submit button.
6. Take a screenshot to verify the change was applied.
7. Validate by checking for a success message in the page content.

Log: `[skill:browser-automation] Credential rotation workflow using Chrome DevTools MCP`

## Validation

After navigating to a page:
1. Take a snapshot or screenshot to confirm the page loaded.
2. Check for error messages (404, 500, connection refused).

After filling a form and submitting:
1. Take a snapshot after submission.
2. Check for success/error messages in the page content.
3. Report the outcome.

After credential rotation:
1. Verify the new credential works by attempting to use it (e.g., API call with new key).
2. Report success or failure.

If no browser tools are available:
1. Report: `[skill:browser-automation] ERROR: No suitable tool found for browser automation (Chrome DevTools MCP and Playwright both unavailable)`
2. Recommend enabling the Chrome sidecar container via `docker compose --profile browser up -d`.

## Scope Rules

This skill MUST NOT:
- Change passwords, secrets, or encryption keys unless explicitly instructed by a playbook
- Navigate to or interact with services not defined in repo inventories
- Submit forms that modify network configuration (DNS, VPN, firewall rules)
- Download or upload files without explicit playbook authorization

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- MUST NOT click buttons, submit forms, or fill fields with real values.
- MAY navigate to pages and take screenshots/snapshots (read-only observation).
- MUST still perform tool discovery and selection.
- Log: `[skill:browser-automation] DRY RUN: Would navigate to <url>, fill <fields>, and click <button> using <tool>`
