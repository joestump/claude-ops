package gitprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubProvider implements GitProvider using the GitHub REST API.
type GitHubProvider struct {
	token   string
	client  *http.Client
	baseURL string // defaults to "https://api.github.com"
}

// NewGitHubProvider creates a GitHubProvider with the given personal access token.
func NewGitHubProvider(token string) *GitHubProvider {
	return &GitHubProvider{
		token: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.github.com",
	}
}

func (g *GitHubProvider) Name() string { return "github" }

func (g *GitHubProvider) CreateBranch(ctx context.Context, repo RepoRef, branch string, base string) error {
	// Get the SHA of the base branch.
	refURL := fmt.Sprintf("%s/repos/%s/%s/git/refs/heads/%s", g.baseURL, repo.Owner, repo.Name, base)
	var refResp struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := g.doJSON(ctx, http.MethodGet, refURL, nil, &refResp); err != nil {
		return fmt.Errorf("github: create branch: get base ref: %w", err)
	}

	// Create the new branch ref.
	createURL := fmt.Sprintf("%s/repos/%s/%s/git/refs", g.baseURL, repo.Owner, repo.Name)
	body := map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": refResp.Object.SHA,
	}
	if err := g.doJSON(ctx, http.MethodPost, createURL, body, nil); err != nil {
		return fmt.Errorf("github: create branch: %w", err)
	}
	return nil
}

func (g *GitHubProvider) CommitFiles(ctx context.Context, repo RepoRef, branch string, message string, files []FileChange) error {
	for _, f := range files {
		if f.Action == "delete" {
			if err := g.deleteFile(ctx, repo, branch, message, f); err != nil {
				return fmt.Errorf("github: commit files: %w", err)
			}
			continue
		}

		// For create/update, check if the file already exists to get its SHA.
		contentsURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", g.baseURL, repo.Owner, repo.Name, f.Path)

		var existing struct {
			SHA string `json:"sha"`
		}
		existingSHA := ""
		err := g.doJSON(ctx, http.MethodGet, contentsURL+"?ref="+branch, nil, &existing)
		if err == nil {
			existingSHA = existing.SHA
		}

		body := map[string]string{
			"message": message,
			"content": base64.StdEncoding.EncodeToString([]byte(f.Content)),
			"branch":  branch,
		}
		if existingSHA != "" {
			body["sha"] = existingSHA
		}

		if err := g.doJSON(ctx, http.MethodPut, contentsURL, body, nil); err != nil {
			return fmt.Errorf("github: commit files: put %s: %w", f.Path, err)
		}
	}
	return nil
}

func (g *GitHubProvider) deleteFile(ctx context.Context, repo RepoRef, branch string, message string, f FileChange) error {
	contentsURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", g.baseURL, repo.Owner, repo.Name, f.Path)

	// Get the file's current SHA (required for deletion).
	var existing struct {
		SHA string `json:"sha"`
	}
	if err := g.doJSON(ctx, http.MethodGet, contentsURL+"?ref="+branch, nil, &existing); err != nil {
		return fmt.Errorf("delete %s: get sha: %w", f.Path, err)
	}

	body := map[string]string{
		"message": message,
		"sha":     existing.SHA,
		"branch":  branch,
	}
	if err := g.doJSON(ctx, http.MethodDelete, contentsURL, body, nil); err != nil {
		return fmt.Errorf("delete %s: %w", f.Path, err)
	}
	return nil
}

func (g *GitHubProvider) CreatePR(ctx context.Context, repo RepoRef, pr PRRequest) (*PRResult, error) {
	prURL := fmt.Sprintf("%s/repos/%s/%s/pulls", g.baseURL, repo.Owner, repo.Name)
	body := map[string]string{
		"title": pr.Title,
		"body":  pr.Body,
		"head":  pr.HeadBranch,
		"base":  pr.BaseBranch,
	}

	var prResp struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := g.doJSON(ctx, http.MethodPost, prURL, body, &prResp); err != nil {
		return nil, fmt.Errorf("github: create pr: %w", err)
	}

	// Add labels if specified.
	if len(pr.Labels) > 0 {
		labelsURL := fmt.Sprintf("%s/repos/%s/%s/issues/%d/labels", g.baseURL, repo.Owner, repo.Name, prResp.Number)
		labelsBody := map[string][]string{
			"labels": pr.Labels,
		}
		if err := g.doJSON(ctx, http.MethodPost, labelsURL, labelsBody, nil); err != nil {
			return nil, fmt.Errorf("github: create pr: add labels: %w", err)
		}
	}

	return &PRResult{
		Number: prResp.Number,
		URL:    prResp.HTMLURL,
	}, nil
}

func (g *GitHubProvider) GetPRStatus(ctx context.Context, repo RepoRef, prNumber int) (*PRStatus, error) {
	prURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", g.baseURL, repo.Owner, repo.Name, prNumber)

	var prResp struct {
		Number    int    `json:"number"`
		State     string `json:"state"`
		Merged    bool   `json:"merged"`
		Mergeable *bool  `json:"mergeable"`
	}
	if err := g.doJSON(ctx, http.MethodGet, prURL, nil, &prResp); err != nil {
		return nil, fmt.Errorf("github: get pr status: %w", err)
	}

	state := prResp.State
	if prResp.Merged {
		state = "merged"
	}

	mergeable := false
	if prResp.Mergeable != nil {
		mergeable = *prResp.Mergeable
	}

	// Fetch reviews.
	reviewsURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", g.baseURL, repo.Owner, repo.Name, prNumber)
	var reviewsResp []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if err := g.doJSON(ctx, http.MethodGet, reviewsURL, nil, &reviewsResp); err != nil {
		return nil, fmt.Errorf("github: get pr status: fetch reviews: %w", err)
	}

	reviews := make([]Review, 0, len(reviewsResp))
	for _, r := range reviewsResp {
		reviews = append(reviews, Review{
			Author: r.User.Login,
			State:  strings.ToLower(r.State),
			Body:   r.Body,
		})
	}

	return &PRStatus{
		Number:    prResp.Number,
		State:     state,
		Mergeable: mergeable,
		Reviews:   reviews,
	}, nil
}

func (g *GitHubProvider) ListOpenPRs(ctx context.Context, repo RepoRef, filter PRFilter) ([]PRSummary, error) {
	listURL := fmt.Sprintf("%s/repos/%s/%s/pulls?state=open", g.baseURL, repo.Owner, repo.Name)

	var prsResp []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := g.doJSON(ctx, http.MethodGet, listURL, nil, &prsResp); err != nil {
		return nil, fmt.Errorf("github: list open prs: %w", err)
	}

	var results []PRSummary
	for _, pr := range prsResp {
		// Filter by author.
		if filter.Author != "" && pr.User.Login != filter.Author {
			continue
		}

		// Filter by labels (all specified labels must be present).
		if len(filter.Labels) > 0 {
			prLabels := make(map[string]bool, len(pr.Labels))
			for _, l := range pr.Labels {
				prLabels[l.Name] = true
			}
			match := true
			for _, required := range filter.Labels {
				if !prLabels[required] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		// Fetch files for each matching PR.
		filesURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files", g.baseURL, repo.Owner, repo.Name, pr.Number)
		var filesResp []struct {
			Filename string `json:"filename"`
		}
		if err := g.doJSON(ctx, http.MethodGet, filesURL, nil, &filesResp); err != nil {
			return nil, fmt.Errorf("github: list open prs: fetch files for #%d: %w", pr.Number, err)
		}

		files := make([]string, 0, len(filesResp))
		for _, f := range filesResp {
			files = append(files, f.Filename)
		}

		results = append(results, PRSummary{
			Number: pr.Number,
			Title:  pr.Title,
			Files:  files,
		})
	}

	return results, nil
}

// doJSON executes an HTTP request with JSON body/response handling.
// If reqBody is non-nil it is marshalled as JSON. If respBody is non-nil
// the response is unmarshalled into it.
func (g *GitHubProvider) doJSON(ctx context.Context, method, url string, reqBody any, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("authentication failed (401): %s", string(respData))
		case http.StatusNotFound:
			return fmt.Errorf("not found (404): %s", string(respData))
		case http.StatusUnprocessableEntity:
			return fmt.Errorf("validation error (422): %s", string(respData))
		default:
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respData))
		}
	}

	if respBody != nil && len(respData) > 0 {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return nil
}
