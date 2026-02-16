# Skill: Event Reporting

Emit event markers on their own line as you work. These are parsed by the dashboard and displayed as styled badges in the Events tab — do NOT repeat them in your final summary.

## Format

    [EVENT:info] Routine observation message
    [EVENT:warning] Something degraded but not critical
    [EVENT:critical] Needs human attention immediately

To tag a specific service:

    [EVENT:warning:jellyfin] Container restarted, checking stability
    [EVENT:critical:postgres] Connection refused on port 5432

## When to Emit Events

- Service state changes (up/down/degraded)
- Remediation actions taken and their results
- Cooldown limits reached
- Anything requiring human attention
