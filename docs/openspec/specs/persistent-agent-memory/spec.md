# SPEC-0015: Persistent Agent Memory System

## Overview

Give the Claude Ops agent the ability to persist operational knowledge across sessions. Memories are stored in SQLite, injected into the system prompt at session start, and managed by the operator through the dashboard. See ADR-0015.

## Requirements

### Requirement: Memories Table Schema

The system SHALL store memories in a `memories` SQLite table created by migration 005. Each memory MUST have an id, service (OPTIONAL, nullable for general memories), category, observation, confidence score, active flag, created_at, updated_at, session_id, and tier.

#### Scenario: Migration creates memories table

- **WHEN** the application starts and migration 005 has not been applied
- **THEN** the `memories` table is created with columns: `id` (INTEGER PRIMARY KEY AUTOINCREMENT), `service` (TEXT, nullable), `category` (TEXT NOT NULL), `observation` (TEXT NOT NULL), `confidence` (REAL NOT NULL DEFAULT 0.7), `active` (INTEGER NOT NULL DEFAULT 1), `created_at` (TEXT NOT NULL), `updated_at` (TEXT NOT NULL), `session_id` (INTEGER REFERENCES sessions(id)), `tier` (INTEGER NOT NULL DEFAULT 1)
- **THEN** indexes are created on `(service, active)`, `(confidence, active)`, and `(category)`

#### Scenario: Migration is idempotent

- **WHEN** the application starts and migration 005 has already been applied
- **THEN** the migration is skipped and the existing `memories` table is unchanged

### Requirement: Memory Categories

Every memory MUST have a category. The category MUST be one of: `timing`, `dependency`, `behavior`, `remediation`, `maintenance`. The system SHALL reject or ignore memories with unrecognized categories.

#### Scenario: Valid category accepted

- **WHEN** a memory marker is parsed with category `timing`
- **THEN** the memory is inserted into the `memories` table with `category = 'timing'`

#### Scenario: Invalid category rejected

- **WHEN** a memory marker is parsed with category `misc` (not in the allowed set)
- **THEN** the memory SHALL NOT be inserted into the database
- **THEN** a warning log message is emitted indicating the invalid category

### Requirement: Memory Marker Format

The session manager SHALL parse memory markers from assistant text blocks in the stream-json output. The marker format MUST be `[MEMORY:<category>] <observation>` or `[MEMORY:<category>:<service>] <observation>`. Markers SHALL only be parsed from assistant text blocks -- never from tool results, user messages, or system events.

#### Scenario: General memory marker (no service)

- **WHEN** the LLM outputs `[MEMORY:remediation] DNS checks sometimes fail transiently during WireGuard reconnects -- retry once before escalating`
- **THEN** a memory is created with category=`remediation`, service=NULL, observation=`DNS checks sometimes fail transiently during WireGuard reconnects -- retry once before escalating`, confidence=0.7, tier set to the current session tier

#### Scenario: Service-specific memory marker

- **WHEN** the LLM outputs `[MEMORY:timing:jellyfin] Takes 60s to start after restart -- wait before checking health`
- **THEN** a memory is created with category=`timing`, service=`jellyfin`, observation=`Takes 60s to start after restart -- wait before checking health`, confidence=0.7

#### Scenario: Marker in tool result ignored

- **WHEN** a tool result block contains text matching the `[MEMORY:...]` pattern
- **THEN** it SHALL NOT be parsed as a memory (only assistant text blocks produce memories)

#### Scenario: Memory linked to session

- **WHEN** a memory marker is parsed during session ID 42
- **THEN** the resulting memory row has `session_id = 42` and `created_at` and `updated_at` set to the current UTC timestamp

### Requirement: All Tiers Can Write Memories

Memory markers SHALL be parsed from all tier sessions (Tier 1, 2, and 3). The `tier` column MUST record which tier created the memory.

#### Scenario: Tier 1 creates a memory

- **WHEN** a Tier 1 (observe) session emits `[MEMORY:behavior:adguard] Returns HTTP 302 redirect when healthy, not 200`
- **THEN** a memory is created with `tier = 1`

#### Scenario: Tier 3 creates a memory

- **WHEN** a Tier 3 (remediate) session emits `[MEMORY:dependency:caddy] Must be started after WireGuard -- fails with no route to host otherwise`
- **THEN** a memory is created with `tier = 3`

### Requirement: Memory Marker Regex

The memory marker regex MUST be `\[MEMORY:(timing|dependency|behavior|remediation|maintenance)(?::([a-zA-Z0-9_-]+))?\]\s*(.+)`. Group 1 is the category, group 2 is the optional service name, and group 3 is the observation text.

#### Scenario: Regex matches well-formed marker

- **WHEN** the text `[MEMORY:dependency:postgres] Dependents should wait 10s after postgres restart` is tested against the regex
- **THEN** group 1 = `dependency`, group 2 = `postgres`, group 3 = `Dependents should wait 10s after postgres restart`

#### Scenario: Regex rejects malformed marker

- **WHEN** the text `[MEMORY:unknown] Some observation` is tested against the regex
- **THEN** the regex does not match (because `unknown` is not in the category enum)

### Requirement: Prompt Injection via buildMemoryContext

The session manager SHALL implement a `buildMemoryContext()` function that queries active memories from the database and formats them as a structured text block. This text MUST be appended to the system prompt via the `--append-system-prompt` CLI argument, alongside the existing environment context from `buildEnvContext()`.

#### Scenario: Memories injected at session start

- **WHEN** a new session starts and the `memories` table contains 5 active memories (confidence >= 0.3)
- **THEN** `buildMemoryContext()` returns a formatted text block containing all 5 memories
- **THEN** the text block is appended to the `--append-system-prompt` argument

#### Scenario: No active memories

- **WHEN** a new session starts and the `memories` table has no active memories above the confidence threshold
- **THEN** `buildMemoryContext()` returns an empty string
- **THEN** no memory section is appended to the system prompt

#### Scenario: Memories grouped by service

- **WHEN** the memory context is rendered and there are memories for services `jellyfin`, `postgres`, and a general memory (service=NULL)
- **THEN** the output is grouped under service headings (`### jellyfin`, `### postgres`, `### general`) with each memory as a bullet point showing category and confidence

### Requirement: Token Budget Enforcement

The memory context MUST NOT exceed 2,000 tokens (estimated as characters / 4). The `buildMemoryContext()` function SHALL query memories ordered by confidence DESC and accumulate them until the budget is exhausted. Memories that would exceed the budget SHALL be excluded.

#### Scenario: Budget enforced with many memories

- **WHEN** there are 50 active memories totaling an estimated 5,000 tokens
- **THEN** `buildMemoryContext()` includes only the highest-confidence memories that fit within 2,000 tokens
- **THEN** a header comment indicates how many memories were included out of the total (e.g., `## Operational Memory (23 of 50 memories, ~1,987 tokens)`)

#### Scenario: Budget is configurable

- **WHEN** the `CLAUDEOPS_MEMORY_BUDGET` environment variable is set to `4000`
- **THEN** the memory token budget is 4,000 tokens instead of the default 2,000

#### Scenario: All memories fit within budget

- **WHEN** there are 3 active memories totaling an estimated 200 tokens
- **THEN** all 3 memories are included and the header reflects the actual count and token usage

### Requirement: Confidence Scoring

Every memory MUST have a confidence score between 0.0 and 1.0 inclusive. New memories created by the agent SHALL default to confidence 0.7. Memories with confidence below 0.3 SHALL be considered inactive and excluded from prompt injection.

#### Scenario: Default confidence on creation

- **WHEN** a memory marker is parsed and inserted into the database
- **THEN** the confidence field is set to 0.7

#### Scenario: Low-confidence memory excluded from prompt

- **WHEN** `buildMemoryContext()` runs and a memory has confidence 0.2
- **THEN** that memory is not included in the prompt injection text

#### Scenario: Confidence clamped to valid range

- **WHEN** an operator sets a memory's confidence to 1.5 via the dashboard
- **THEN** the system clamps the value to 1.0 before saving

### Requirement: Memory Reinforcement

When the agent emits a memory that closely matches an existing active memory (same service AND same category AND similar observation text), the system SHOULD reinforce the existing memory rather than creating a duplicate. Reinforcement SHALL increase the confidence by 0.1 (capped at 1.0) and update the `updated_at` timestamp.

#### Scenario: Duplicate memory reinforces existing

- **WHEN** an active memory exists with service=`jellyfin`, category=`timing`, observation=`Takes 60s to start after restart`, confidence=0.7
- **WHEN** the agent emits `[MEMORY:timing:jellyfin] Takes about 60 seconds to start after a restart`
- **THEN** the existing memory's confidence increases to 0.8 and `updated_at` is refreshed
- **THEN** no new row is inserted

#### Scenario: Different category creates new memory

- **WHEN** an active memory exists with service=`jellyfin`, category=`timing`, observation=`Takes 60s to start`
- **WHEN** the agent emits `[MEMORY:behavior:jellyfin] Sometimes crashes on first start`
- **THEN** a new memory row is created (different category, not a reinforcement)

### Requirement: Memory Contradiction

When the agent emits a memory that contradicts an existing active memory (same service AND same category but conflicting conclusion), the system SHOULD decrease the existing memory's confidence by 0.2. If the confidence falls below 0.3, the memory SHALL be marked inactive (`active = 0`).

#### Scenario: Contradicting memory reduces confidence

- **WHEN** an active memory exists with service=`caddy`, category=`dependency`, observation=`Must be started after WireGuard`, confidence=0.8
- **WHEN** the agent emits `[MEMORY:dependency:caddy] Can be started independently of WireGuard`
- **THEN** the existing memory's confidence decreases to 0.6
- **THEN** the new contradicting memory is inserted with confidence=0.7

#### Scenario: Contradiction deactivates low-confidence memory

- **WHEN** an active memory exists with confidence=0.4
- **WHEN** a contradicting memory is emitted
- **THEN** the existing memory's confidence drops to 0.2 and `active` is set to 0

### Requirement: Staleness Decay

Memories not reinforced within 30 days SHALL have their confidence decayed. The decay rate SHALL be 0.1 per week after the 30-day grace period. The staleness check SHOULD run once per session (at session start, before building memory context). Memories whose confidence falls below 0.3 due to decay SHALL be marked inactive.

#### Scenario: Fresh memory not decayed

- **WHEN** a memory was last updated 15 days ago
- **THEN** its confidence is unchanged during the staleness check

#### Scenario: Stale memory decayed

- **WHEN** a memory was last updated 44 days ago (14 days past the 30-day grace period = 2 weeks)
- **THEN** its confidence is reduced by 0.2 (0.1 per week x 2 weeks) during the staleness check

#### Scenario: Decay deactivates memory

- **WHEN** a stale memory has confidence 0.4 and has not been updated for 44 days
- **THEN** after decay (0.4 - 0.2 = 0.2), the memory is marked `active = 0`

### Requirement: Dashboard Memories Page

The dashboard SHALL include a `/memories` page accessible from the sidebar navigation, positioned between Events and Cooldowns. The page MUST display all memories (active and inactive) with CRUD capabilities.

#### Scenario: Memories page renders

- **WHEN** the operator navigates to `/memories`
- **THEN** a table is displayed showing: service (or "general"), category, observation, confidence (as a bar or percentage), active status, last updated, and source session link

#### Scenario: Memories page auto-refreshes

- **WHEN** the operator has the memories page open and a new memory is created during a running session
- **THEN** the new memory appears within 5 seconds via HTMX polling

#### Scenario: Filter by service

- **WHEN** the operator selects a service filter on the memories page
- **THEN** only memories for that service (or general if selected) are displayed

#### Scenario: Filter by category

- **WHEN** the operator selects a category filter on the memories page
- **THEN** only memories with that category are displayed

### Requirement: Dashboard Memory CRUD

The operator MUST be able to create, read, update, and delete memories from the dashboard.

#### Scenario: Create memory manually

- **WHEN** the operator clicks "Add Memory" and fills in category=`maintenance`, service=`postgres`, observation=`Needs manual VACUUM FULL weekly`, confidence=0.9
- **THEN** a new memory is inserted with the specified values, `session_id = NULL` (operator-created), and `active = 1`

#### Scenario: Edit memory observation

- **WHEN** the operator clicks edit on a memory and changes the observation text
- **THEN** the memory's `observation` is updated, `updated_at` is refreshed, and confidence is unchanged

#### Scenario: Adjust memory confidence

- **WHEN** the operator adjusts a memory's confidence slider from 0.7 to 0.95
- **THEN** the memory's `confidence` is updated to 0.95 and `updated_at` is refreshed

#### Scenario: Delete memory

- **WHEN** the operator clicks delete on a memory and confirms
- **THEN** the memory row is permanently removed from the database

#### Scenario: Bulk delete

- **WHEN** the operator selects 5 memories and clicks "Delete Selected"
- **THEN** all 5 memory rows are permanently removed from the database

### Requirement: Prompt Integration for Memory Markers

The tier prompts (tier1-observe.md, tier2-investigate.md, tier3-remediate.md) MUST include a "Memory Recording" section that instructs the LLM to emit memory markers using the `[MEMORY:category] observation` or `[MEMORY:category:service] observation` format.

#### Scenario: Tier 1 prompt includes memory instructions

- **WHEN** the tier-1 observe prompt is loaded
- **THEN** it contains a section explaining the `[MEMORY:...]` marker format, listing valid categories, and providing examples

#### Scenario: Tier 2 prompt includes memory instructions

- **WHEN** the tier-2 investigate prompt is loaded
- **THEN** it contains the same memory recording instructions as Tier 1

#### Scenario: Tier 3 prompt includes memory instructions

- **WHEN** the tier-3 remediate prompt is loaded
- **THEN** it contains the same memory recording instructions as Tier 1

### Requirement: Memory Context Format

The injected memory context MUST follow a structured format with a header, service groupings, and per-memory bullet points. Each bullet MUST show the category tag and confidence score.

#### Scenario: Formatted output matches expected structure

- **WHEN** `buildMemoryContext()` runs with memories for `jellyfin` (2 memories) and `general` (1 memory)
- **THEN** the output resembles:
  ```
  ## Operational Memory (3 memories, ~412 tokens)

  ### jellyfin
  - [timing] Takes 60s to start after restart (confidence: 0.9)
  - [behavior] First restart always fails due to DB lock (confidence: 0.8)

  ### general
  - [remediation] DNS checks sometimes fail transiently during WireGuard reconnects (confidence: 0.6)
  ```

### Requirement: Inactive Memory Handling

Inactive memories (`active = 0`) SHALL be retained in the database for audit purposes but MUST NOT be included in prompt injection. The dashboard SHOULD display inactive memories with a visual distinction (e.g., grayed out, strikethrough, or a separate "Inactive" tab).

#### Scenario: Inactive memory excluded from prompt

- **WHEN** `buildMemoryContext()` runs and a memory has `active = 0`
- **THEN** that memory is excluded from the output regardless of its confidence score

#### Scenario: Inactive memory visible in dashboard

- **WHEN** the operator views the memories page
- **THEN** inactive memories are displayed with visual distinction from active ones
- **THEN** inactive memories can still be edited or deleted by the operator

#### Scenario: Reactivate an inactive memory

- **WHEN** the operator edits an inactive memory and sets its confidence above 0.3
- **THEN** the memory's `active` flag is set to 1 and it becomes eligible for prompt injection
