# Claude Ops Manifest

This is the Claude Ops watchdog's own repository. It contains check definitions, remediation playbooks, tier prompts, and configuration for the monitoring agent.

## Kind

Watchdog (self-reference)

## Capabilities

- **self-reference**: Claude can read its own instructions, checks, and playbooks from this repo

## Rules

- Never modify files in this repo at runtime
- Read-only access to prompts, checks, and playbooks
- This repo is the source of truth for how Claude Ops behaves
