# Playbook: Rotate API Key

**Tier**: 2 (Sonnet) minimum

## When to Use

- Service API returns 401/403 (authentication failure)
- Indexer or integration reports auth errors
- API key is known to be expired

## Prerequisites

- Confirm the issue is actually an auth problem (not service down)
- Identify which service needs the key rotated and where the key is consumed

## Via REST API (Preferred)

If the service providing the key has an API:

1. **Get a new key from the provider**
   ```bash
   # Example: many services expose their API key in settings
   curl -s -H "X-Api-Key: <admin_key>" "http://<provider>/api/v1/config" | jq '.apiKey'
   ```

2. **Update the consumer**
   ```bash
   # Example: update Prowlarr's indexer with new key
   curl -X PUT -H "X-Api-Key: <prowlarr_key>" \
     -H "Content-Type: application/json" \
     -d '{"apiKey": "<new_key>", ...}' \
     "http://<prowlarr>/api/v1/indexer/<id>"
   ```

3. **Verify**
   - Test the integration endpoint
   - Confirm no more auth errors

## Via Browser Automation (Chrome DevTools MCP)

When the provider has no API for key management:

1. **Navigate to the provider's web UI**
   - Use Chrome DevTools MCP to open the provider's URL
   - Take a snapshot to understand the page structure

2. **Authenticate**
   - Find the login form
   - Enter credentials (from env vars or service config)
   - Submit and wait for redirect

3. **Navigate to API key page**
   - Find the settings/API section
   - Navigate there via clicks

4. **Extract the key**
   - Take a snapshot of the API key page
   - Extract the key value from the page content
   - If the key needs regeneration, click the regenerate button and extract the new value

5. **Update the consumer**
   - Use the consumer's REST API to update the key (see above)

6. **Verify end-to-end**
   - Test the integration
   - Confirm the consumer can authenticate with the new key

## After Rotation

- Log which key was rotated, from where to where
- Send email summary: "Rotated API key for <provider> in <consumer>"
- Do NOT store the key value in logs — just note that it was rotated

## If It Doesn't Work

- If the provider's web UI has changed, don't guess — escalate or notify
- If the consumer's API rejects the update, check API docs and escalate if needed
- Never modify config files directly to update keys — use APIs only
