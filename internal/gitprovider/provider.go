package gitprovider

import "context"

// Governing: SPEC-0018 REQ-1 "GitProvider Interface" — abstracts CreatePR, ListPRs, GetPRStatus across providers; six required methods in dedicated package
// Governing: SPEC-0018 REQ-10 "Agent MUST NOT Merge Own PRs" — no MergePR method exposed
//
// GitProvider abstracts git hosting operations for PR-based workflows.
// Each implementation targets a specific platform (GitHub, Gitea, etc.).
type GitProvider interface {
	// Name returns the provider identifier (e.g., "github", "gitea").
	Name() string

	// CreateBranch creates a new branch from the given base branch.
	CreateBranch(ctx context.Context, repo RepoRef, branch string, base string) error

	// CommitFiles commits one or more file changes to the specified branch.
	CommitFiles(ctx context.Context, repo RepoRef, branch string, message string, files []FileChange) error

	// CreatePR opens a pull request from the head branch to the base branch.
	CreatePR(ctx context.Context, repo RepoRef, pr PRRequest) (*PRResult, error)

	// GetPRStatus returns the current status of a pull request.
	GetPRStatus(ctx context.Context, repo RepoRef, prNumber int) (*PRStatus, error)

	// ListOpenPRs lists open PRs created by the agent (filtered by author or label).
	ListOpenPRs(ctx context.Context, repo RepoRef, filter PRFilter) ([]PRSummary, error)
}

// RepoRef identifies a repository by owner, name, and clone URL.
type RepoRef struct {
	Owner    string // org or user
	Name     string // repository name
	CloneURL string // for git operations
}

// FileChange represents a single file modification in a commit.
type FileChange struct {
	Path    string // relative path within the repo
	Content string // new file content
	Action  string // "create", "update", "delete"
}

// PRRequest contains the fields needed to open a pull request.
type PRRequest struct {
	Title      string
	Body       string   // markdown description with context
	HeadBranch string
	BaseBranch string
	Labels     []string // e.g., ["claude-ops", "automated"]
}

// PRResult is returned after successfully creating a pull request.
type PRResult struct {
	Number int
	URL    string
}

// PRStatus describes the current state of a pull request.
type PRStatus struct {
	Number    int
	State     string // "open", "closed", "merged"
	Mergeable bool
	Reviews   []Review
}

// Review represents a single review on a pull request.
type Review struct {
	Author string
	State  string // "approved", "changes_requested", "commented"
	Body   string
}

// PRFilter constrains which PRs are returned by ListOpenPRs.
type PRFilter struct {
	Author string
	Labels []string
}

// PRSummary is a lightweight representation of an open pull request.
type PRSummary struct {
	Number int
	Title  string
	Files  []string // paths modified by the PR
}

// Governing: SPEC-0018 REQ-4 "Provider Registry and Discovery" — explicit declaration in CLAUDE-OPS.md manifest
// Manifest holds the parsed Git Provider section from a repo's CLAUDE-OPS.md.
type Manifest struct {
	Provider   string // e.g., "github", "gitea"
	Remote     string // e.g., "https://gitea.stump.wtf/joe/home-cluster.git"
	Owner      string
	Repo       string
	BaseBranch string
}
