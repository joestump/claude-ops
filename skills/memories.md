# Skill: Memory Recording

You can persist operational knowledge across sessions by emitting memory markers in your output. Memories are stored in a database and injected into future sessions' system prompts.

## Format

```
[MEMORY:category] observation text
[MEMORY:category:service] observation text about a specific service
```

## Categories

- **timing**: Startup delays, timeout patterns, response time baselines
- **dependency**: Service ordering, prerequisites, startup sequences
- **behavior**: Quirks, workarounds, known issues, expected error patterns
- **remediation**: What works, what doesn't, successful fix patterns
- **maintenance**: Scheduled tasks, periodic needs, cleanup requirements

## Guidelines

- **Be extremely selective.** Most runs should record ZERO memories. Only record something that would change how you handle a future incident.
- Memories persist across sessions and consume context window — every memory you save costs tokens on every future run.
- Be specific and actionable: "Jellyfin takes 60s to start after restart — wait before health check" not "Jellyfin is slow"
- If you discover something contradicts an existing memory, emit a corrected version

## What is NOT a Memory

- Service health status ("service X is healthy/down", "container restarted successfully")
- Routine check or investigation results ("checked 60 services, all healthy", "logs show normal operation")
- Baseline performance confirmations ("all services responding within timeout", "no performance degradation", "response times normal")
- Timeout or parameter adjustments that produced expected results ("extended timeout to 10s, services confirmed healthy")
- Available updates or version numbers
- DNS resolution results or container states
- Current resource usage ("memory at 45%", "disk at 60%")
- Anything that describes the *current state* rather than a *reusable operational insight*

**Rule of thumb**: If the observation is "everything is fine" or "I adjusted a check parameter and things worked," that is not a memory. Only record insights that would change your *approach* to a future incident — not confirmations that the current approach worked.

## What IS a Memory

- A service that requires a specific startup sequence or wait time
- A workaround for a known bug or quirk
- A dependency relationship that isn't obvious from the inventory
- A remediation approach that worked (or failed) for a specific failure mode
- Infrastructure patterns that affect how you should investigate issues
- A root cause pattern that took significant investigation to find (e.g., "OOM kills on jellyfin happen when transcoding 4K")
- A multi-service recovery sequence that worked (e.g., "restart postgres first, wait 30s, then restart dependents")
- Infrastructure constraints discovered during recovery (e.g., "ie01 only has 2GB free on /var — redeploys need disk check first")
