package session

import (
	"context"
	"io"
	"strings"
	"testing"
)

// mockRunner implements ProcessRunner for testing.
type mockRunner struct {
	output string
	err    error
}

func (m *mockRunner) Start(ctx context.Context, model string, promptContent string, allowedTools string, disallowedTools string, appendSystemPrompt string) (io.ReadCloser, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	r := io.NopCloser(strings.NewReader(m.output))
	return r, func() error { return nil }, nil
}

func TestMockRunnerReturnsOutput(t *testing.T) {
	runner := &mockRunner{output: `{"type":"system","subtype":"init"}` + "\n"}
	stdout, wait, err := runner.Start(context.Background(), "haiku", "check health", "Bash,Read", "", "env=test")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !strings.Contains(string(data), "system") {
		t.Errorf("expected system event in output, got %q", string(data))
	}

	if err := wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestMockRunnerStartError(t *testing.T) {
	runner := &mockRunner{err: io.ErrClosedPipe}
	_, _, err := runner.Start(context.Background(), "haiku", "check health", "Bash", "", "env=test")
	if err == nil {
		t.Fatal("expected error from Start")
	}
}

func TestProcessRunnerInterface(t *testing.T) {
	// Verify CLIRunner implements ProcessRunner at compile time.
	var _ ProcessRunner = &CLIRunner{}
	var _ ProcessRunner = &mockRunner{}
}
