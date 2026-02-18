package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubProvider_Name(t *testing.T) {
	p := NewGitHubProvider("test-token")
	if p.Name() != "github" {
		t.Errorf("expected name %q, got %q", "github", p.Name())
	}
}

func TestGitHubProvider_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("my-secret-token")
	p.baseURL = srv.URL

	// The request will fail but we can still check the auth header.
	_ = p.CreateBranch(context.Background(), RepoRef{Owner: "o", Name: "r"}, "b", "main")

	if gotAuth != "token my-secret-token" {
		t.Errorf("expected auth header %q, got %q", "token my-secret-token", gotAuth)
	}
}

func TestGitHubProvider_AcceptHeader(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL
	_ = p.CreateBranch(context.Background(), RepoRef{Owner: "o", Name: "r"}, "b", "main")

	if gotAccept != "application/vnd.github.v3+json" {
		t.Errorf("expected accept header %q, got %q", "application/vnd.github.v3+json", gotAccept)
	}
}

func TestGitHubProvider_CreateBranch_Success(t *testing.T) {
	var createdRef string
	var createdSHA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/refs/heads/main"):
			json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]string{"sha": "abc123"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/git/refs"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			createdRef = body["ref"]
			createdSHA = body["sha"]
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature-x", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdRef != "refs/heads/feature-x" {
		t.Errorf("expected ref %q, got %q", "refs/heads/feature-x", createdRef)
	}
	if createdSHA != "abc123" {
		t.Errorf("expected sha %q, got %q", "abc123", createdSHA)
	}
}

func TestGitHubProvider_CreateBranch_BaseNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature-x", "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing base branch, got nil")
	}
	if !strings.Contains(err.Error(), "github: create branch") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestGitHubProvider_CommitFiles_Create(t *testing.T) {
	var putPath string
	var putBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/") {
			// File doesn't exist yet.
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"not found"}`))
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/contents/") {
			putPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	files := []FileChange{
		{Path: "config/new.yaml", Content: "key: value", Action: "create"},
	}
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature-x", "add config", files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(putPath, "/contents/config/new.yaml") {
		t.Errorf("expected path to end with /contents/config/new.yaml, got %q", putPath)
	}
	if putBody["branch"] != "feature-x" {
		t.Errorf("expected branch %q, got %q", "feature-x", putBody["branch"])
	}
	if putBody["message"] != "add config" {
		t.Errorf("expected message %q, got %q", "add config", putBody["message"])
	}
	// Should not have a sha for new files.
	if _, ok := putBody["sha"]; ok {
		t.Error("expected no sha for new file creation")
	}
}

func TestGitHubProvider_CommitFiles_Update(t *testing.T) {
	var putBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/") {
			// File exists.
			json.NewEncoder(w).Encode(map[string]string{"sha": "existing-sha-123"})
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/contents/") {
			json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	files := []FileChange{
		{Path: "config/existing.yaml", Content: "key: updated", Action: "update"},
	}
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature-x", "update config", files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if putBody["sha"] != "existing-sha-123" {
		t.Errorf("expected sha %q, got %q", "existing-sha-123", putBody["sha"])
	}
}

func TestGitHubProvider_CommitFiles_Delete(t *testing.T) {
	var deleteBody map[string]string
	var gotDelete bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/") {
			json.NewEncoder(w).Encode(map[string]string{"sha": "del-sha-456"})
			return
		}
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/contents/") {
			gotDelete = true
			json.NewDecoder(r.Body).Decode(&deleteBody)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	files := []FileChange{
		{Path: "config/old.yaml", Content: "", Action: "delete"},
	}
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature-x", "remove old config", files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotDelete {
		t.Error("expected DELETE request to be made")
	}
	if deleteBody["sha"] != "del-sha-456" {
		t.Errorf("expected sha %q, got %q", "del-sha-456", deleteBody["sha"])
	}
	if deleteBody["branch"] != "feature-x" {
		t.Errorf("expected branch %q, got %q", "feature-x", deleteBody["branch"])
	}
}

func TestGitHubProvider_CreatePR_Success(t *testing.T) {
	var prBody map[string]string
	var gotLabelsURL string
	var labelBody map[string][]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewDecoder(r.Body).Decode(&prBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"number":   42,
				"html_url": "https://github.com/joe/repo/pull/42",
			})
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/42/labels") {
			gotLabelsURL = r.URL.Path
			json.NewDecoder(r.Body).Decode(&labelBody)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	result, err := p.CreatePR(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRRequest{
		Title:      "Auto fix config",
		Body:       "Automated change",
		HeadBranch: "feature-x",
		BaseBranch: "main",
		Labels:     []string{"claude-ops", "automated"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Number != 42 {
		t.Errorf("expected PR number 42, got %d", result.Number)
	}
	if result.URL != "https://github.com/joe/repo/pull/42" {
		t.Errorf("expected URL %q, got %q", "https://github.com/joe/repo/pull/42", result.URL)
	}
	if prBody["title"] != "Auto fix config" {
		t.Errorf("expected title %q, got %q", "Auto fix config", prBody["title"])
	}
	if prBody["head"] != "feature-x" {
		t.Errorf("expected head %q, got %q", "feature-x", prBody["head"])
	}
	if prBody["base"] != "main" {
		t.Errorf("expected base %q, got %q", "main", prBody["base"])
	}
	if !strings.HasSuffix(gotLabelsURL, "/issues/42/labels") {
		t.Errorf("expected labels URL to target issue 42, got %q", gotLabelsURL)
	}
	if len(labelBody["labels"]) != 2 || labelBody["labels"][0] != "claude-ops" {
		t.Errorf("expected labels [claude-ops, automated], got %v", labelBody["labels"])
	}
}

func TestGitHubProvider_CreatePR_NoLabels(t *testing.T) {
	var labelsCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls") {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"number":   1,
				"html_url": "https://github.com/joe/repo/pull/1",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/labels") {
			labelsCalled = true
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	_, err := p.CreatePR(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRRequest{
		Title:      "No labels PR",
		HeadBranch: "fix",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if labelsCalled {
		t.Error("expected labels endpoint NOT to be called when no labels specified")
	}
}

func TestGitHubProvider_GetPRStatus_Open(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/10") {
			mergeable := true
			json.NewEncoder(w).Encode(map[string]any{
				"number":    10,
				"state":     "open",
				"merged":    false,
				"mergeable": mergeable,
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/10/reviews") {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"user":  map[string]string{"login": "reviewer1"},
					"state": "APPROVED",
					"body":  "Looks good!",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "open" {
		t.Errorf("expected state %q, got %q", "open", status.State)
	}
	if !status.Mergeable {
		t.Error("expected mergeable to be true")
	}
	if len(status.Reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(status.Reviews))
	}
	if status.Reviews[0].Author != "reviewer1" {
		t.Errorf("expected review author %q, got %q", "reviewer1", status.Reviews[0].Author)
	}
	if status.Reviews[0].State != "approved" {
		t.Errorf("expected review state %q, got %q", "approved", status.Reviews[0].State)
	}
}

func TestGitHubProvider_GetPRStatus_Merged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/5") {
			json.NewEncoder(w).Encode(map[string]any{
				"number":    5,
				"state":     "closed",
				"merged":    true,
				"mergeable": nil,
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/5/reviews") {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "merged" {
		t.Errorf("expected state %q, got %q", "merged", status.State)
	}
}

func TestGitHubProvider_GetPRStatus_Closed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/7") {
			json.NewEncoder(w).Encode(map[string]any{
				"number": 7,
				"state":  "closed",
				"merged": false,
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/7/reviews") {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "closed" {
		t.Errorf("expected state %q, got %q", "closed", status.State)
	}
}

func TestGitHubProvider_ListOpenPRs_NoFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"number": 1,
					"title":  "Fix config",
					"user":   map[string]string{"login": "bot"},
					"labels": []map[string]string{{"name": "claude-ops"}},
				},
				{
					"number": 2,
					"title":  "Update docs",
					"user":   map[string]string{"login": "human"},
					"labels": []any{},
				},
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/1/files") {
			json.NewEncoder(w).Encode([]map[string]string{
				{"filename": "config.yaml"},
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/2/files") {
			json.NewEncoder(w).Encode([]map[string]string{
				{"filename": "README.md"},
				{"filename": "docs/guide.md"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	prs, err := p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(prs))
	}
	if prs[0].Number != 1 || prs[0].Title != "Fix config" {
		t.Errorf("unexpected first PR: %+v", prs[0])
	}
	if len(prs[0].Files) != 1 || prs[0].Files[0] != "config.yaml" {
		t.Errorf("expected files [config.yaml], got %v", prs[0].Files)
	}
	if len(prs[1].Files) != 2 {
		t.Errorf("expected 2 files for second PR, got %d", len(prs[1].Files))
	}
}

func TestGitHubProvider_ListOpenPRs_FilterByAuthor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"number": 1,
					"title":  "Bot PR",
					"user":   map[string]string{"login": "bot"},
					"labels": []any{},
				},
				{
					"number": 2,
					"title":  "Human PR",
					"user":   map[string]string{"login": "human"},
					"labels": []any{},
				},
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/1/files") {
			json.NewEncoder(w).Encode([]map[string]string{{"filename": "a.txt"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	prs, err := p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{Author: "bot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR filtered by author, got %d", len(prs))
	}
	if prs[0].Number != 1 {
		t.Errorf("expected PR #1, got #%d", prs[0].Number)
	}
}

func TestGitHubProvider_ListOpenPRs_FilterByLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"number": 1,
					"title":  "Labeled PR",
					"user":   map[string]string{"login": "bot"},
					"labels": []map[string]string{{"name": "claude-ops"}, {"name": "automated"}},
				},
				{
					"number": 2,
					"title":  "Unlabeled PR",
					"user":   map[string]string{"login": "bot"},
					"labels": []any{},
				},
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/1/files") {
			json.NewEncoder(w).Encode([]map[string]string{{"filename": "x.txt"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	prs, err := p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{Labels: []string{"claude-ops"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR filtered by label, got %d", len(prs))
	}
	if prs[0].Number != 1 {
		t.Errorf("expected PR #1, got #%d", prs[0].Number)
	}
}

func TestGitHubProvider_ErrorHandling_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("bad-token")
	p.baseURL = srv.URL

	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "b", "main")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected 'authentication failed' error, got: %v", err)
	}
}

func TestGitHubProvider_ErrorHandling_422(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: return base ref.
			json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]string{"sha": "abc123"},
			})
			return
		}
		// Second call: branch already exists.
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"Reference already exists"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "existing", "main")
	if err == nil {
		t.Fatal("expected error for 422, got nil")
	}
	if !strings.Contains(err.Error(), "validation error") {
		t.Errorf("expected 'validation error' in message, got: %v", err)
	}
}

func TestGitHubProvider_ErrorHandling_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"Internal Server Error"}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("tok")
	p.baseURL = srv.URL

	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "b", "main")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status 500") {
		t.Errorf("expected 'unexpected status 500' error, got: %v", err)
	}
}
