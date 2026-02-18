package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGiteaProvider_Name(t *testing.T) {
	p := NewGiteaProvider("https://gitea.example.com", "test-token")
	if got := p.Name(); got != "gitea" {
		t.Errorf("Name() = %q, want %q", got, "gitea")
	}
}

func TestGiteaProvider_BaseURLTrailingSlash(t *testing.T) {
	p := NewGiteaProvider("https://gitea.example.com/", "test-token")
	if p.baseURL != "https://gitea.example.com" {
		t.Errorf("baseURL = %q, want trailing slash stripped", p.baseURL)
	}
}

func TestGiteaProvider_CreateBranch(t *testing.T) {
	var gotBody map[string]string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/joe/home-cluster/branches" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "my-token")
	err := p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "home-cluster"}, "feature-x", "main")
	if err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}
	if gotAuth != "token my-token" {
		t.Errorf("auth header = %q, want %q", gotAuth, "token my-token")
	}
	if gotBody["new_branch_name"] != "feature-x" {
		t.Errorf("new_branch_name = %q, want %q", gotBody["new_branch_name"], "feature-x")
	}
	if gotBody["old_branch_name"] != "main" {
		t.Errorf("old_branch_name = %q, want %q", gotBody["old_branch_name"], "main")
	}
}

func TestGiteaProvider_CreateBranch_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	err := p.CreateBranch(context.Background(), RepoRef{Owner: "o", Name: "r"}, "b", "main")
	if err == nil {
		t.Fatal("expected error for non-201 status")
	}
}

func TestGiteaProvider_CommitFiles_Create(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "feature", "add config", []FileChange{
		{Path: "config.yaml", Content: "key: value", Action: "create"},
	})
	if err != nil {
		t.Fatalf("CommitFiles() error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST for create", gotMethod)
	}
	if gotPath != "/api/v1/repos/joe/repo/contents/config.yaml" {
		t.Errorf("path = %s, want /api/v1/repos/joe/repo/contents/config.yaml", gotPath)
	}
	if gotBody["branch"] != "feature" {
		t.Errorf("branch = %q, want %q", gotBody["branch"], "feature")
	}
	if gotBody["content"] == "" {
		t.Error("content should be base64-encoded, got empty")
	}
}

func TestGiteaProvider_CommitFiles_Update(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Method == http.MethodGet {
			// Return existing file with SHA
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "abc123"})
			return
		}
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT for update, got %s", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["sha"] != "abc123" {
			t.Errorf("sha = %q, want %q", body["sha"], "abc123")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "o", Name: "r"}, "main", "update", []FileChange{
		{Path: "file.txt", Content: "new content", Action: "update"},
	})
	if err != nil {
		t.Fatalf("CommitFiles(update) error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 requests (GET sha + PUT update), got %d", callCount)
	}
}

func TestGiteaProvider_CommitFiles_Delete(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "def456"})
			return
		}
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["sha"] != "def456" {
			t.Errorf("sha = %q, want %q", body["sha"], "def456")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "o", Name: "r"}, "main", "remove", []FileChange{
		{Path: "old.txt", Content: "", Action: "delete"},
	})
	if err != nil {
		t.Fatalf("CommitFiles(delete) error: %v", err)
	}
}

func TestGiteaProvider_CommitFiles_UnsupportedAction(t *testing.T) {
	p := NewGiteaProvider("http://localhost", "tok")
	err := p.CommitFiles(context.Background(), RepoRef{Owner: "o", Name: "r"}, "main", "msg", []FileChange{
		{Path: "f", Action: "rename"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported action")
	}
}

func TestGiteaProvider_CreatePR(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/joe/repo/pulls" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   42,
			"html_url": "https://gitea.example.com/joe/repo/pulls/42",
		})
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	result, err := p.CreatePR(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRRequest{
		Title:      "Fix config",
		Body:       "Details here",
		HeadBranch: "feature-x",
		BaseBranch: "main",
		Labels:     []string{"claude-ops"},
	})
	if err != nil {
		t.Fatalf("CreatePR() error: %v", err)
	}
	if result.Number != 42 {
		t.Errorf("Number = %d, want 42", result.Number)
	}
	if result.URL != "https://gitea.example.com/joe/repo/pulls/42" {
		t.Errorf("URL = %q", result.URL)
	}
	if gotBody["head"] != "feature-x" {
		t.Errorf("head = %q, want %q", gotBody["head"], "feature-x")
	}
	if gotBody["base"] != "main" {
		t.Errorf("base = %q, want %q", gotBody["base"], "main")
	}
}

func TestGiteaProvider_CreatePR_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	_, err := p.CreatePR(context.Background(), RepoRef{Owner: "o", Name: "r"}, PRRequest{})
	if err == nil {
		t.Fatal("expected error for non-201 status")
	}
}

func TestGiteaProvider_GetPRStatus_Open(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/joe/repo/pulls/10/reviews" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":    10,
			"state":     "open",
			"merged":    false,
			"mergeable": true,
		})
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 10)
	if err != nil {
		t.Fatalf("GetPRStatus() error: %v", err)
	}
	if status.State != "open" {
		t.Errorf("State = %q, want %q", status.State, "open")
	}
	if !status.Mergeable {
		t.Error("Mergeable = false, want true")
	}
}

func TestGiteaProvider_GetPRStatus_Merged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/joe/repo/pulls/5/reviews" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"user": map[string]string{"login": "reviewer"}, "state": "APPROVED", "body": "lgtm"},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":    5,
			"state":     "closed",
			"merged":    true,
			"mergeable": false,
		})
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 5)
	if err != nil {
		t.Fatalf("GetPRStatus() error: %v", err)
	}
	if status.State != "merged" {
		t.Errorf("State = %q, want %q", status.State, "merged")
	}
	if len(status.Reviews) != 1 {
		t.Fatalf("Reviews count = %d, want 1", len(status.Reviews))
	}
	if status.Reviews[0].Author != "reviewer" {
		t.Errorf("review author = %q, want %q", status.Reviews[0].Author, "reviewer")
	}
	if status.Reviews[0].State != "approved" {
		t.Errorf("review state = %q, want %q", status.Reviews[0].State, "approved")
	}
}

func TestGiteaProvider_GetPRStatus_Closed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/joe/repo/pulls/7/reviews" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":    7,
			"state":     "closed",
			"merged":    false,
			"mergeable": false,
		})
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	status, err := p.GetPRStatus(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, 7)
	if err != nil {
		t.Fatalf("GetPRStatus() error: %v", err)
	}
	if status.State != "closed" {
		t.Errorf("State = %q, want %q", status.State, "closed")
	}
}

func TestGiteaProvider_ListOpenPRs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/joe/repo/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state query = %q, want %q", r.URL.Query().Get("state"), "open")
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number": 1,
				"title":  "PR one",
				"user":   map[string]string{"login": "claude-ops"},
				"labels": []map[string]string{{"name": "automated"}},
			},
			{
				"number": 2,
				"title":  "PR two",
				"user":   map[string]string{"login": "human"},
				"labels": []map[string]string{},
			},
		})
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")

	// No filter
	prs, err := p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{})
	if err != nil {
		t.Fatalf("ListOpenPRs() error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}

	// Filter by author
	prs, err = p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{Author: "claude-ops"})
	if err != nil {
		t.Fatalf("ListOpenPRs(author) error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}
	if prs[0].Number != 1 {
		t.Errorf("PR number = %d, want 1", prs[0].Number)
	}

	// Filter by label
	prs, err = p.ListOpenPRs(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, PRFilter{Labels: []string{"automated"}})
	if err != nil {
		t.Fatalf("ListOpenPRs(labels) error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}
	if prs[0].Number != 1 {
		t.Errorf("PR number = %d, want 1", prs[0].Number)
	}
}

func TestGiteaProvider_ListOpenPRs_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	_, err := p.ListOpenPRs(context.Background(), RepoRef{Owner: "o", Name: "r"}, PRFilter{})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestGiteaProvider_AuthHeader(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "secret-token-123")
	_ = p.CreateBranch(context.Background(), RepoRef{Owner: "o", Name: "r"}, "b", "main")

	if gotHeaders.Get("Authorization") != "token secret-token-123" {
		t.Errorf("Authorization = %q, want %q", gotHeaders.Get("Authorization"), "token secret-token-123")
	}
	if gotHeaders.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q, want %q", gotHeaders.Get("Accept"), "application/json")
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotHeaders.Get("Content-Type"), "application/json")
	}
}

func TestGiteaProvider_BaseURLUsed(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := NewGiteaProvider(srv.URL, "tok")
	_ = p.CreateBranch(context.Background(), RepoRef{Owner: "joe", Name: "repo"}, "b", "main")

	if gotURL != "/api/v1/repos/joe/repo/branches" {
		t.Errorf("request URL = %q, want /api/v1/repos/joe/repo/branches", gotURL)
	}
}
