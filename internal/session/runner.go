package session

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// ProcessRunner abstracts the spawning of a Claude CLI subprocess so that
// tests can substitute a mock implementation.
// Governing: SPEC-0008 REQ-5 "CLI subprocess creation" — uses os/exec.Command to invoke the claude CLI binary.
// Governing: SPEC-0008 REQ-7 — subprocess lifecycle management (startup, completion, crash handling).
// Governing: ADR-0023 "AllowedTools-Based Tier Enforcement" — disallowedTools param for command-prefix blocklisting.
type ProcessRunner interface {
	Start(ctx context.Context, model string, promptContent string, allowedTools string, disallowedTools string, appendSystemPrompt string) (stdout io.ReadCloser, wait func() error, err error)
}

// CLIRunner implements ProcessRunner by spawning the real `claude` CLI binary.
type CLIRunner struct{}

// Governing: SPEC-0011 "CLI Invocation with stream-json" (--output-format stream-json for structured NDJSON events)
// Governing: SPEC-0016 "Handoff Context Serialization" — passes handoff context via --append-system-prompt
// Start builds and starts a claude CLI process with stream-json output.
// It returns a reader for stdout, a wait function that blocks until the
// process exits, and any startup error.
// Governing: SPEC-0008 REQ-5 "CLI subprocess creation"
// — passes model, prompt content, allowed tools, disallowed tools, and system prompt arguments
// matching the entrypoint.sh invocation pattern via os/exec.Command.
func (r *CLIRunner) Start(ctx context.Context, model string, promptContent string, allowedTools string, disallowedTools string, appendSystemPrompt string) (io.ReadCloser, func() error, error) {
	// Governing: SPEC-0010 REQ-5 "Tool filtering via --allowedTools"
	// Governing: ADR-0023 "AllowedTools-Based Tier Enforcement"
	// — enforces tool restrictions at CLI runtime via --allowedTools whitelist
	// and --disallowedTools command-prefix blocklist, not just prompt level.
	args := []string{
		"--model", model,
		"-p", promptContent,
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", allowedTools,
	}
	if disallowedTools != "" {
		args = append(args, "--disallowedTools", disallowedTools)
	}
	// Governing: SPEC-0010 REQ-6 — runtime context injection via --append-system-prompt.
	// Prepend an explicit autonomous-mode directive so it appears at the END of the system
	// prompt (highest model priority). Without this, CLAUDE.md's developer-tooling context
	// (build commands, ADRs, coding workflows) causes the agent to greet users and offer
	// coding assistance instead of immediately executing the health check workflow.
	const autonomousDirective = "AUTONOMOUS AGENT MODE: You are running as a scheduled, " +
		"non-interactive infrastructure monitoring agent inside a Docker container. " +
		"The CLAUDE.md in your working directory is the development guide for the Claude Ops " +
		"codebase — you are NOT here to develop or modify it. Do NOT act as a coding assistant. " +
		"Do NOT check git status, offer help menus, or ask for user direction. " +
		"BEGIN IMMEDIATELY by executing the infrastructure health check workflow described " +
		"in the user prompt above.\n\n"
	args = append(args, "--append-system-prompt", autonomousDirective+"Environment: "+appendSystemPrompt)

	cmd := exec.Command("claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Governing: SPEC-0008 REQ-7 — process group isolation for signal forwarding.
	cmd.Stderr = os.Stderr

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	return stdoutPipe, cmd.Wait, nil
}
