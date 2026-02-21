# Skill: <Name>

<!-- Governing: SPEC-0023 REQ-1 — Skill File Format -->

## Purpose

A brief description of what this skill does and when it should be invoked.

## Tier Requirement

<!-- OPTIONAL: Remove this section if the skill is available at all tiers. -->

- **Minimum tier**: Tier <1|2|3>
- **Reason**: <Why this tier is required>

## Scope Rules

<!-- OPTIONAL: Remove this section if there are no scope restrictions. -->

- **Allowed targets**: <What this skill may operate on>
- **Forbidden targets**: <What this skill must never touch>

## Tool Discovery

Before executing, verify the following tools are available:

```
Tools required:
- <ToolName1>: <What it's used for>
- <ToolName2>: <What it's used for>
```

If a required tool is not available, stop and report: "Skill <Name> requires <ToolName> which is not available."

## Execution

Step-by-step instructions for the agent to follow:

1. <First step>
2. <Second step>
3. <Third step>

## Dry-Run Behavior

<!-- OPTIONAL: Remove this section if the skill has no dry-run variant. -->

When `$CLAUDEOPS_DRY_RUN` is `true`:
- <What the skill does differently>
- <What it skips>

## Validation

After execution, verify success:

1. <First validation check>
2. <Second validation check>

If validation fails: <What to do — retry, escalate, or report>
