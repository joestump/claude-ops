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
// Governing: SPEC-0008 REQ-5 "CLI subprocess creation"
// — uses os/exec.Command to invoke the claude CLI binary.
type ProcessRunner interface {
	Start(ctx context.Context, model string, promptContent string, allowedTools string, appendSystemPrompt string) (stdout io.ReadCloser, wait func() error, err error)
}

// CLIRunner implements ProcessRunner by spawning the real `claude` CLI binary.
type CLIRunner struct{}

// Start builds and starts a claude CLI process with stream-json output.
// It returns a reader for stdout, a wait function that blocks until the
// process exits, and any startup error.
// Governing: SPEC-0008 REQ-5 "CLI subprocess creation"
// — passes model, prompt content, allowed tools, and system prompt arguments
// matching the entrypoint.sh invocation pattern via os/exec.Command.
func (r *CLIRunner) Start(ctx context.Context, model string, promptContent string, allowedTools string, appendSystemPrompt string) (io.ReadCloser, func() error, error) {
	// Governing: SPEC-0010 REQ-5 "Tool filtering via --allowedTools"
	// — enforces tool restrictions at CLI runtime, not just prompt level.
	args := []string{
		"--model", model,
		"-p", promptContent,
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", allowedTools,
		"--append-system-prompt", "Environment: " + appendSystemPrompt,
	}

	cmd := exec.Command("claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
