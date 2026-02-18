package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/joestump/claude-ops/internal/gitprovider"
)

// --- Tool Definitions ---

func createPRTool() mcp.Tool {
	return mcp.NewToolWithRawSchema(
		"create_pr",
		"Create a branch, commit files, and open a pull request. Enforces scope validation and tier restrictions server-side.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_owner": {
					"type": "string",
					"description": "Repository owner (org or user)"
				},
				"repo_name": {
					"type": "string",
					"description": "Repository name"
				},
				"title": {
					"type": "string",
					"description": "Pull request title"
				},
				"body": {
					"type": "string",
					"description": "Pull request body (markdown)"
				},
				"files": {
					"type": "array",
					"description": "File changes to commit",
					"items": {
						"type": "object",
						"properties": {
							"path": {
								"type": "string",
								"description": "Relative file path within the repo"
							},
							"content": {
								"type": "string",
								"description": "New file content"
							},
							"action": {
								"type": "string",
								"enum": ["create", "update", "delete"],
								"description": "File change action"
							}
						},
						"required": ["path", "content", "action"]
					}
				},
				"clone_url": {
					"type": "string",
					"description": "Clone URL for provider resolution (optional)"
				},
				"base_branch": {
					"type": "string",
					"description": "Base branch to create PR against (default: main)"
				},
				"change_type": {
					"type": "string",
					"description": "Change type for branch naming (default: fix)"
				}
			},
			"required": ["repo_owner", "repo_name", "title", "body", "files"]
		}`),
	)
}

func listPRsTool() mcp.Tool {
	return mcp.NewToolWithRawSchema(
		"list_prs",
		"List open pull requests filtered by the claude-ops label.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_owner": {
					"type": "string",
					"description": "Repository owner (org or user)"
				},
				"repo_name": {
					"type": "string",
					"description": "Repository name"
				},
				"clone_url": {
					"type": "string",
					"description": "Clone URL for provider resolution (optional)"
				}
			},
			"required": ["repo_owner", "repo_name"]
		}`),
	)
}

func getPRStatusTool() mcp.Tool {
	return mcp.NewToolWithRawSchema(
		"get_pr_status",
		"Get the current status of a pull request, including reviews.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_owner": {
					"type": "string",
					"description": "Repository owner (org or user)"
				},
				"repo_name": {
					"type": "string",
					"description": "Repository name"
				},
				"pr_number": {
					"type": "integer",
					"description": "Pull request number"
				},
				"clone_url": {
					"type": "string",
					"description": "Clone URL for provider resolution (optional)"
				}
			},
			"required": ["repo_owner", "repo_name", "pr_number"]
		}`),
	)
}

// --- Tool Handlers ---

// createPRArgs mirrors the JSON schema for create_pr.
type createPRArgs struct {
	RepoOwner  string                   `json:"repo_owner"`
	RepoName   string                   `json:"repo_name"`
	Title      string                   `json:"title"`
	Body       string                   `json:"body"`
	Files      []gitprovider.FileChange `json:"files"`
	CloneURL   string                   `json:"clone_url"`
	BaseBranch string                   `json:"base_branch"`
	ChangeType string                   `json:"change_type"`
}

// createPRResult is the success response for create_pr.
type createPRResult struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	DryRun bool   `json:"dry_run,omitempty"`
}

func (s *Server) handleCreatePR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.prEnabled {
		return mcp.NewToolResultError("PR creation is disabled (set CLAUDEOPS_PR_ENABLED=true to enable)"), nil
	}

	var args createPRArgs
	if err := req.BindArguments(&args); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.RepoOwner == "" || args.RepoName == "" || args.Title == "" || len(args.Files) == 0 {
		return mcp.NewToolResultError("repo_owner, repo_name, title, and files are required"), nil
	}

	// Scope validation.
	if err := gitprovider.ValidateScope(args.Files); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("scope validation failed: %v", err)), nil
	}

	// Tier validation (server-side, from environment).
	if err := gitprovider.ValidateTier(s.tier, args.Files); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("tier validation failed: %v", err)), nil
	}

	// Dry run: return without making API calls.
	if s.dryRun {
		log.Printf("[MCP] Dry run: would create PR '%s' for %s/%s", args.Title, args.RepoOwner, args.RepoName)
		return resultJSON(createPRResult{DryRun: true})
	}

	// Resolve provider.
	repo := gitprovider.RepoRef{Owner: args.RepoOwner, Name: args.RepoName, CloneURL: args.CloneURL}
	provider, err := s.registry.Resolve(repo, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("no git provider for repo %s/%s: %v", args.RepoOwner, args.RepoName, err)), nil
	}

	// Generate branch name.
	branch := gitprovider.GenerateBranchName(args.ChangeType, args.Title)
	baseBranch := args.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Create branch.
	if err := provider.CreateBranch(ctx, repo, branch, baseBranch); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create branch: %v", err)), nil
	}

	// Commit files.
	if err := provider.CommitFiles(ctx, repo, branch, args.Title, args.Files); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("commit files: %v", err)), nil
	}

	// Create PR.
	prReq := gitprovider.PRRequest{
		Title:      args.Title,
		Body:       args.Body,
		HeadBranch: branch,
		BaseBranch: baseBranch,
		Labels:     []string{"claude-ops", "automated"},
	}
	result, err := provider.CreatePR(ctx, repo, prReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create PR: %v", err)), nil
	}

	log.Printf("[MCP] Created PR #%d at %s for %s/%s", result.Number, result.URL, args.RepoOwner, args.RepoName)
	return resultJSON(createPRResult{
		Number: result.Number,
		URL:    result.URL,
		Branch: branch,
	})
}

// repoArgs is shared by list_prs and get_pr_status.
type repoArgs struct {
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	CloneURL  string `json:"clone_url"`
}

// prSummaryResult mirrors the list_prs response.
type prSummaryResult struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Files  []string `json:"files"`
}

func (s *Server) handleListPRs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args repoArgs
	if err := req.BindArguments(&args); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.RepoOwner == "" || args.RepoName == "" {
		return mcp.NewToolResultError("repo_owner and repo_name are required"), nil
	}

	repo := gitprovider.RepoRef{Owner: args.RepoOwner, Name: args.RepoName, CloneURL: args.CloneURL}
	provider, err := s.registry.Resolve(repo, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("no git provider for repo %s/%s: %v", args.RepoOwner, args.RepoName, err)), nil
	}

	filter := gitprovider.PRFilter{Labels: []string{"claude-ops"}}
	prs, err := provider.ListOpenPRs(ctx, repo, filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list PRs: %v", err)), nil
	}

	summaries := make([]prSummaryResult, len(prs))
	for i, pr := range prs {
		summaries[i] = prSummaryResult{
			Number: pr.Number,
			Title:  pr.Title,
			Files:  pr.Files,
		}
	}

	return resultJSON(summaries)
}

// prStatusArgs adds pr_number to the base repo args.
type prStatusArgs struct {
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	PRNumber  int    `json:"pr_number"`
	CloneURL  string `json:"clone_url"`
}

// prStatusResult mirrors the get_pr_status response.
type prStatusResult struct {
	Number    int            `json:"number"`
	State     string         `json:"state"`
	Mergeable bool           `json:"mergeable"`
	Reviews   []reviewResult `json:"reviews"`
}

type reviewResult struct {
	Author string `json:"author"`
	State  string `json:"state"`
	Body   string `json:"body"`
}

func (s *Server) handleGetPRStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args prStatusArgs
	if err := req.BindArguments(&args); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	if args.RepoOwner == "" || args.RepoName == "" || args.PRNumber == 0 {
		return mcp.NewToolResultError("repo_owner, repo_name, and pr_number are required"), nil
	}

	repo := gitprovider.RepoRef{Owner: args.RepoOwner, Name: args.RepoName, CloneURL: args.CloneURL}
	provider, err := s.registry.Resolve(repo, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("no git provider for repo %s/%s: %v", args.RepoOwner, args.RepoName, err)), nil
	}

	status, err := provider.GetPRStatus(ctx, repo, args.PRNumber)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get PR status: %v", err)), nil
	}

	reviews := make([]reviewResult, len(status.Reviews))
	for i, r := range status.Reviews {
		reviews[i] = reviewResult{
			Author: r.Author,
			State:  r.State,
			Body:   r.Body,
		}
	}

	return resultJSON(prStatusResult{
		Number:    status.Number,
		State:     status.State,
		Mergeable: status.Mergeable,
		Reviews:   reviews,
	})
}

// resultJSON marshals v to JSON and returns it as a tool result.
func resultJSON(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
