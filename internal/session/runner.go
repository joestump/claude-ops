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
type ProcessRunner interface {
	Start(ctx context.Context, model string, promptContent string, allowedTools string, appendSystemPrompt string, verbose bool) (stdout io.ReadCloser, wait func() error, err error)
}

// CLIRunner implements ProcessRunner by spawning the real `claude` CLI binary.
type CLIRunner struct{}

// Start builds and starts a claude CLI process with stream-json output.
// It returns a reader for stdout, a wait function that blocks until the
// process exits, and any startup error.
func (r *CLIRunner) Start(ctx context.Context, model string, promptContent string, allowedTools string, appendSystemPrompt string, verbose bool) (io.ReadCloser, func() error, error) {
	args := []string{
		"--model", model,
		"-p", promptContent,
		"--output-format", "stream-json",
		"--allowedTools", allowedTools,
		"--append-system-prompt", "Environment: " + appendSystemPrompt,
	}

	if verbose {
		args = append(args, "--verbose")
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
