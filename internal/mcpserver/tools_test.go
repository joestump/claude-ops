package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/joestump/claude-ops/internal/gitprovider"
)

// --- Mock Provider ---

type mockProvider struct {
	name string

	createBranchCalled bool
	commitFilesCalled  bool
	createPRCalled     bool
	getPRStatusCalled  bool
	listOpenPRsCalled  bool

	createPRResult    *gitprovider.PRResult
	createPRErr       error
	getPRStatusResult *gitprovider.PRStatus
	getPRStatusErr    error
	listOpenPRsResult []gitprovider.PRSummary
	listOpenPRsErr    error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) CreateBranch(_ context.Context, _ gitprovider.RepoRef, _, _ string) error {
	m.createBranchCalled = true
	return nil
}

func (m *mockProvider) CommitFiles(_ context.Context, _ gitprovider.RepoRef, _ string, _ string, _ []gitprovider.FileChange) error {
	m.commitFilesCalled = true
	return nil
}

func (m *mockProvider) CreatePR(_ context.Context, _ gitprovider.RepoRef, _ gitprovider.PRRequest) (*gitprovider.PRResult, error) {
	m.createPRCalled = true
	return m.createPRResult, m.createPRErr
}

func (m *mockProvider) GetPRStatus(_ context.Context, _ gitprovider.RepoRef, _ int) (*gitprovider.PRStatus, error) {
	m.getPRStatusCalled = true
	return m.getPRStatusResult, m.getPRStatusErr
}

func (m *mockProvider) ListOpenPRs(_ context.Context, _ gitprovider.RepoRef, _ gitprovider.PRFilter) ([]gitprovider.PRSummary, error) {
	m.listOpenPRsCalled = true
	return m.listOpenPRsResult, m.listOpenPRsErr
}

// --- Helpers ---

func newRegistry(mock *mockProvider) *gitprovider.Registry {
	r := gitprovider.NewRegistry()
	r.Register("github", mock)
	return r
}

func makeCreatePRRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "create_pr",
			Arguments: args,
		},
	}
}

func makeListPRsRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "list_prs",
			Arguments: args,
		},
	}
}

func makeGetPRStatusRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_pr_status",
			Arguments: args,
		},
	}
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T, not TextContent", result.Content[0])
	}
	return tc.Text
}

// --- Tests ---

func TestCreatePR_Disabled(t *testing.T) {
	mock := &mockProvider{name: "github"}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 2, dryRun: false, prEnabled: false}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Add check",
		"body":       "Should be rejected",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "checks/test.md", "content": "# Check", "action": "create"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when PR creation is disabled")
	}
	text := resultText(t, result)
	if text == "" || !strings.Contains(text, "disabled") {
		t.Errorf("expected disabled error message, got: %s", text)
	}

	if mock.createBranchCalled || mock.commitFilesCalled || mock.createPRCalled {
		t.Error("expected no provider methods to be called when disabled")
	}
}

func TestCreatePR_Success(t *testing.T) {
	mock := &mockProvider{
		name: "github",
		createPRResult: &gitprovider.PRResult{
			Number: 42,
			URL:    "https://github.com/joe/test-repo/pull/42",
		},
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 2, dryRun: false, prEnabled: true}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Add health check",
		"body":       "Adds a new check for service X",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "checks/service-x.md", "content": "# Check", "action": "create"},
			map[string]any{"path": "checks/service-y.md", "content": "# Check", "action": "create"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	var pr createPRResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &pr); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("expected PR number 42, got %d", pr.Number)
	}
	if pr.URL != "https://github.com/joe/test-repo/pull/42" {
		t.Errorf("unexpected URL: %s", pr.URL)
	}
	if pr.DryRun {
		t.Error("expected dry_run to be false")
	}

	if !mock.createBranchCalled {
		t.Error("expected CreateBranch to be called")
	}
	if !mock.commitFilesCalled {
		t.Error("expected CommitFiles to be called")
	}
	if !mock.createPRCalled {
		t.Error("expected CreatePR to be called")
	}
}

func TestCreatePR_ScopeValidation(t *testing.T) {
	mock := &mockProvider{name: "github"}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 2, dryRun: false, prEnabled: true}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Update prompt",
		"body":       "Modifies a prompt file",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "prompts/tier1-observe.md", "content": "# modified", "action": "update"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected scope validation error")
	}
	text := resultText(t, result)
	if text == "" {
		t.Fatal("expected error message")
	}
	t.Logf("scope validation error: %s", text)

	if mock.createBranchCalled || mock.commitFilesCalled || mock.createPRCalled {
		t.Error("expected no provider methods to be called")
	}
}

func TestCreatePR_Tier1Rejected(t *testing.T) {
	mock := &mockProvider{name: "github"}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 1, dryRun: false, prEnabled: true}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Add check",
		"body":       "New check",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "checks/test.md", "content": "# Check", "action": "create"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tier validation error for tier 1")
	}
	text := resultText(t, result)
	if text == "" {
		t.Fatal("expected error message")
	}
	t.Logf("tier validation error: %s", text)

	if mock.createBranchCalled || mock.commitFilesCalled || mock.createPRCalled {
		t.Error("expected no provider methods to be called")
	}
}

func TestCreatePR_Tier2FileLimit(t *testing.T) {
	mock := &mockProvider{name: "github"}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 2, dryRun: false, prEnabled: true}

	files := make([]any, 4)
	for i := range files {
		files[i] = map[string]any{
			"path":    fmt.Sprintf("checks/check-%d.md", i),
			"content": "# Check",
			"action":  "create",
		}
	}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Add many checks",
		"body":       "Too many files for tier 2",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files":      files,
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tier 2 file limit error")
	}
	text := resultText(t, result)
	if text == "" {
		t.Fatal("expected error message")
	}
	t.Logf("tier 2 file limit error: %s", text)
}

func TestCreatePR_DryRun(t *testing.T) {
	mock := &mockProvider{name: "github"}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 2, dryRun: true, prEnabled: true}

	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Add check",
		"body":       "Dry run test",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "checks/test.md", "content": "# Check", "action": "create"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success (dry run), got error: %s", resultText(t, result))
	}

	var pr createPRResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &pr); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if !pr.DryRun {
		t.Error("expected dry_run to be true")
	}

	if mock.createBranchCalled || mock.commitFilesCalled || mock.createPRCalled {
		t.Error("expected no provider methods to be called in dry run mode")
	}
}

func TestListPRs_Success(t *testing.T) {
	mock := &mockProvider{
		name: "github",
		listOpenPRsResult: []gitprovider.PRSummary{
			{Number: 10, Title: "Fix DNS check", Files: []string{"checks/dns.md"}},
			{Number: 11, Title: "Add playbook", Files: []string{"playbooks/restart.md"}},
		},
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 1, dryRun: false}

	req := makeListPRsRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"clone_url":  "https://github.com/joe/test-repo.git",
	})

	result, err := s.handleListPRs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	var summaries []prSummaryResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &summaries); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(summaries))
	}
	if summaries[0].Number != 10 {
		t.Errorf("expected first PR number 10, got %d", summaries[0].Number)
	}
	if summaries[1].Title != "Add playbook" {
		t.Errorf("unexpected second PR title: %s", summaries[1].Title)
	}
	if !mock.listOpenPRsCalled {
		t.Error("expected ListOpenPRs to be called")
	}
}

func TestListPRs_Empty(t *testing.T) {
	mock := &mockProvider{
		name:              "github",
		listOpenPRsResult: []gitprovider.PRSummary{},
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 1, dryRun: false}

	req := makeListPRsRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"clone_url":  "https://github.com/joe/test-repo.git",
	})

	result, err := s.handleListPRs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	var summaries []prSummaryResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &summaries); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 PRs, got %d", len(summaries))
	}
}

func TestGetPRStatus_Success(t *testing.T) {
	mock := &mockProvider{
		name: "github",
		getPRStatusResult: &gitprovider.PRStatus{
			Number:    42,
			State:     "open",
			Mergeable: true,
			Reviews: []gitprovider.Review{
				{Author: "alice", State: "approved", Body: "LGTM"},
			},
		},
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 1, dryRun: false}

	req := makeGetPRStatusRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"pr_number":  42,
		"clone_url":  "https://github.com/joe/test-repo.git",
	})

	result, err := s.handleGetPRStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	var status prStatusResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &status); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if status.Number != 42 {
		t.Errorf("expected PR number 42, got %d", status.Number)
	}
	if status.State != "open" {
		t.Errorf("expected state 'open', got %q", status.State)
	}
	if !status.Mergeable {
		t.Error("expected mergeable to be true")
	}
	if len(status.Reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(status.Reviews))
	}
	if status.Reviews[0].Author != "alice" {
		t.Errorf("expected review author 'alice', got %q", status.Reviews[0].Author)
	}
	if status.Reviews[0].State != "approved" {
		t.Errorf("expected review state 'approved', got %q", status.Reviews[0].State)
	}
}

func TestGetPRStatus_NotFound(t *testing.T) {
	mock := &mockProvider{
		name:           "github",
		getPRStatusErr: fmt.Errorf("pull request not found"),
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 1, dryRun: false}

	req := makeGetPRStatusRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"pr_number":  999,
		"clone_url":  "https://github.com/joe/test-repo.git",
	})

	result, err := s.handleGetPRStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for not-found PR")
	}
	text := resultText(t, result)
	if text == "" {
		t.Fatal("expected error message")
	}
	t.Logf("not found error: %s", text)
}

func TestCreatePR_DefaultBranchAndChangeType(t *testing.T) {
	mock := &mockProvider{
		name: "github",
		createPRResult: &gitprovider.PRResult{
			Number: 7,
			URL:    "https://github.com/joe/test-repo/pull/7",
		},
	}
	reg := newRegistry(mock)
	s := &Server{registry: reg, tier: 3, dryRun: false, prEnabled: true}

	// Omit base_branch and change_type to test defaults.
	req := makeCreatePRRequest(map[string]any{
		"repo_owner": "joe",
		"repo_name":  "test-repo",
		"title":      "Update playbook",
		"body":       "Testing defaults",
		"clone_url":  "https://github.com/joe/test-repo.git",
		"files": []any{
			map[string]any{"path": "playbooks/restart.md", "content": "# Restart", "action": "update"},
		},
	})

	result, err := s.handleCreatePR(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	var pr createPRResult
	if err := json.Unmarshal([]byte(resultText(t, result)), &pr); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if pr.Number != 7 {
		t.Errorf("expected PR number 7, got %d", pr.Number)
	}
	// Branch should use default change_type "fix".
	expectedPrefix := "claude-ops/fix/"
	if len(pr.Branch) < len(expectedPrefix) || pr.Branch[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected branch to start with %q, got %q", expectedPrefix, pr.Branch)
	}
}
