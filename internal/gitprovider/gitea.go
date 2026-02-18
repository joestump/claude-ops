package gitprovider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GiteaProvider implements GitProvider for Gitea instances using the v1 REST API.
type GiteaProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewGiteaProvider creates a GiteaProvider for the given Gitea instance.
func NewGiteaProvider(baseURL, token string) *GiteaProvider {
	return &GiteaProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GiteaProvider) Name() string { return "gitea" }

func (g *GiteaProvider) CreateBranch(ctx context.Context, repo RepoRef, branch string, base string) error {
	body := map[string]string{
		"new_branch_name": branch,
		"old_branch_name": base,
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches", g.baseURL, repo.Owner, repo.Name)
	resp, err := g.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("gitea: create branch: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("gitea: create branch: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (g *GiteaProvider) CommitFiles(ctx context.Context, repo RepoRef, branch string, message string, files []FileChange) error {
	for _, f := range files {
		if err := g.commitFile(ctx, repo, branch, message, f); err != nil {
			return err
		}
	}
	return nil
}

func (g *GiteaProvider) commitFile(ctx context.Context, repo RepoRef, branch string, message string, f FileChange) error {
	contentsURL := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s", g.baseURL, repo.Owner, repo.Name, f.Path)

	switch f.Action {
	case "create":
		body := map[string]string{
			"message": message,
			"content": base64.StdEncoding.EncodeToString([]byte(f.Content)),
			"branch":  branch,
		}
		resp, err := g.doJSON(ctx, http.MethodPost, contentsURL, body)
		if err != nil {
			return fmt.Errorf("gitea: create file %s: %w", f.Path, err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("gitea: create file %s: unexpected status %d", f.Path, resp.StatusCode)
		}

	case "update":
		sha, err := g.getFileSHA(ctx, repo, branch, f.Path)
		if err != nil {
			return fmt.Errorf("gitea: update file %s: get sha: %w", f.Path, err)
		}
		body := map[string]string{
			"message": message,
			"content": base64.StdEncoding.EncodeToString([]byte(f.Content)),
			"branch":  branch,
			"sha":     sha,
		}
		resp, err := g.doJSON(ctx, http.MethodPut, contentsURL, body)
		if err != nil {
			return fmt.Errorf("gitea: update file %s: %w", f.Path, err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("gitea: update file %s: unexpected status %d", f.Path, resp.StatusCode)
		}

	case "delete":
		sha, err := g.getFileSHA(ctx, repo, branch, f.Path)
		if err != nil {
			return fmt.Errorf("gitea: delete file %s: get sha: %w", f.Path, err)
		}
		body := map[string]string{
			"message": message,
			"branch":  branch,
			"sha":     sha,
		}
		resp, err := g.doJSON(ctx, http.MethodDelete, contentsURL, body)
		if err != nil {
			return fmt.Errorf("gitea: delete file %s: %w", f.Path, err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("gitea: delete file %s: unexpected status %d", f.Path, resp.StatusCode)
		}

	default:
		return fmt.Errorf("gitea: unsupported file action %q", f.Action)
	}
	return nil
}

func (g *GiteaProvider) getFileSHA(ctx context.Context, repo RepoRef, branch string, path string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s?ref=%s", g.baseURL, repo.Owner, repo.Name, path, branch)
	resp, err := g.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("file not found: status %d", resp.StatusCode)
	}
	var result struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.SHA, nil
}

func (g *GiteaProvider) CreatePR(ctx context.Context, repo RepoRef, pr PRRequest) (*PRResult, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls", g.baseURL, repo.Owner, repo.Name)
	body := map[string]string{
		"title": pr.Title,
		"body":  pr.Body,
		"head":  pr.HeadBranch,
		"base":  pr.BaseBranch,
	}
	resp, err := g.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("gitea: create pr: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("gitea: create pr: unexpected status %d", resp.StatusCode)
	}
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("gitea: create pr: decode response: %w", err)
	}

	// Labels use integer IDs in Gitea. Applying labels by name requires a
	// lookup step (GET /api/v1/repos/{owner}/{repo}/labels) to map names to
	// IDs. This is not implemented; labels in PRRequest.Labels are ignored.

	return &PRResult{
		Number: result.Number,
		URL:    result.HTMLURL,
	}, nil
}

func (g *GiteaProvider) GetPRStatus(ctx context.Context, repo RepoRef, prNumber int) (*PRStatus, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d", g.baseURL, repo.Owner, repo.Name, prNumber)
	resp, err := g.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gitea: get pr status: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitea: get pr status: unexpected status %d", resp.StatusCode)
	}
	var result struct {
		Number    int    `json:"number"`
		State     string `json:"state"`
		Merged    bool   `json:"merged"`
		Mergeable bool   `json:"mergeable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("gitea: get pr status: decode response: %w", err)
	}

	state := result.State
	if result.Merged {
		state = "merged"
	}

	// Fetch reviews.
	reviews, err := g.getPRReviews(ctx, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("gitea: get pr status: reviews: %w", err)
	}

	return &PRStatus{
		Number:    result.Number,
		State:     state,
		Mergeable: result.Mergeable,
		Reviews:   reviews,
	}, nil
}

func (g *GiteaProvider) getPRReviews(ctx context.Context, repo RepoRef, prNumber int) ([]Review, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d/reviews", g.baseURL, repo.Owner, repo.Name, prNumber)
	resp, err := g.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var rawReviews []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawReviews); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	var reviews []Review
	for _, r := range rawReviews {
		state := mapReviewState(r.State)
		reviews = append(reviews, Review{
			Author: r.User.Login,
			State:  state,
			Body:   r.Body,
		})
	}
	return reviews, nil
}

// mapReviewState maps Gitea review states to the canonical states used by PRStatus.
func mapReviewState(giteaState string) string {
	switch strings.ToUpper(giteaState) {
	case "APPROVED":
		return "approved"
	case "REQUEST_CHANGES":
		return "changes_requested"
	default:
		return "commented"
	}
}

func (g *GiteaProvider) ListOpenPRs(ctx context.Context, repo RepoRef, filter PRFilter) ([]PRSummary, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls?state=open", g.baseURL, repo.Owner, repo.Name)
	resp, err := g.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gitea: list open prs: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitea: list open prs: unexpected status %d", resp.StatusCode)
	}
	var rawPRs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawPRs); err != nil {
		return nil, fmt.Errorf("gitea: list open prs: decode response: %w", err)
	}

	var summaries []PRSummary
	for _, pr := range rawPRs {
		if filter.Author != "" && pr.User.Login != filter.Author {
			continue
		}
		if len(filter.Labels) > 0 && !hasAllLabels(pr.Labels, filter.Labels) {
			continue
		}
		summaries = append(summaries, PRSummary{
			Number: pr.Number,
			Title:  pr.Title,
		})
	}
	return summaries, nil
}

func hasAllLabels(prLabels []struct {
	Name string `json:"name"`
}, required []string) bool {
	have := make(map[string]bool, len(prLabels))
	for _, l := range prLabels {
		have[l.Name] = true
	}
	for _, r := range required {
		if !have[r] {
			return false
		}
	}
	return true
}

// doJSON executes an HTTP request with JSON content type and authorization.
func (g *GiteaProvider) doJSON(ctx context.Context, method, url string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.client.Do(req)
}
