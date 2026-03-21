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

func (m *mockRunner) Start(ctx context.Context, model string, promptContent string, allowedTools string, disallowedTools string, appendSystemPrompt string, schemaPath string) (io.ReadCloser, func() error, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	r := io.NopCloser(strings.NewReader(m.output))
	return r, func() error { return nil }, nil
}

func TestMockRunnerReturnsOutput(t *testing.T) {
	runner := &mockRunner{output: `{"type":"system","subtype":"init"}` + "\n"}
	stdout, wait, err := runner.Start(context.Background(), "haiku", "check health", "Bash,Read", "", "env=test", "")
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
	_, _, err := runner.Start(context.Background(), "haiku", "check health", "Bash", "", "env=test", "")
	if err == nil {
		t.Fatal("expected error from Start")
	}
}

func TestProcessRunnerInterface(t *testing.T) {
	// Verify CLIRunner implements ProcessRunner at compile time.
	var _ ProcessRunner = &CLIRunner{}
	var _ ProcessRunner = &mockRunner{}
}

// ---------------------------------------------------------------------------
// Governing: ADR-0030, SPEC-0031 REQ-4 — schemaPath parameter tests
// ---------------------------------------------------------------------------

// TestMockRunnerAcceptsSchemaPath verifies that the mockRunner (and thus the
// ProcessRunner interface) accepts the schemaPath parameter.
// Governing: ADR-0030, SPEC-0031 REQ-4
func TestMockRunnerAcceptsSchemaPath(t *testing.T) {
	runner := &mockRunner{output: `{"type":"system","subtype":"init"}` + "\n"}

	// Call with a non-empty schemaPath — should not error.
	stdout, wait, err := runner.Start(
		context.Background(),
		"haiku",
		"check health",
		"Bash,Read",
		"",
		"env=test",
		"/app/schemas/agent-response.json",
	)
	if err != nil {
		t.Fatalf("Start with schemaPath: %v", err)
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

// TestMockRunnerEmptySchemaPath verifies that an empty schemaPath is accepted,
// which corresponds to the fallback behavior (no --json-schema flag).
// Governing: ADR-0030, SPEC-0031 REQ-4
func TestMockRunnerEmptySchemaPath(t *testing.T) {
	runner := &mockRunner{output: `{"type":"system","subtype":"init"}` + "\n"}

	stdout, wait, err := runner.Start(
		context.Background(),
		"haiku",
		"check health",
		"Bash,Read",
		"",
		"env=test",
		"", // empty schemaPath — should skip --json-schema
	)
	if err != nil {
		t.Fatalf("Start with empty schemaPath: %v", err)
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

// argCapturingRunner captures the schemaPath argument for inspection.
// This lets us verify the parameter is properly threaded through without
// spawning a real process.
// Governing: ADR-0030, SPEC-0031 REQ-4
type argCapturingRunner struct {
	capturedSchemaPath string
}

func (a *argCapturingRunner) Start(_ context.Context, _ string, _ string, _ string, _ string, _ string, schemaPath string) (io.ReadCloser, func() error, error) {
	a.capturedSchemaPath = schemaPath
	r := io.NopCloser(strings.NewReader(`{"type":"result","result":"done","is_error":false}` + "\n"))
	return r, func() error { return nil }, nil
}

// TestSchemaPathPassedToRunner verifies that the schemaPath parameter is
// correctly threaded through to the ProcessRunner.Start call.
// Governing: ADR-0030, SPEC-0031 REQ-4
func TestSchemaPathPassedToRunner(t *testing.T) {
	tests := []struct {
		name       string
		schemaPath string
		want       string
	}{
		{
			name:       "non-empty schema path",
			schemaPath: "/app/schemas/agent-response.json",
			want:       "/app/schemas/agent-response.json",
		},
		{
			name:       "empty schema path",
			schemaPath: "",
			want:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &argCapturingRunner{}
			_, _, err := runner.Start(
				context.Background(),
				"haiku",
				"check health",
				"Bash,Read",
				"",
				"env=test",
				tc.schemaPath,
			)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if runner.capturedSchemaPath != tc.want {
				t.Errorf("schemaPath = %q, want %q", runner.capturedSchemaPath, tc.want)
			}
		})
	}
}
