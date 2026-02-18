package gitprovider

import (
	"strings"
	"testing"
)

func TestRegistry_ResolveFromManifest(t *testing.T) {
	r := &Registry{providers: make(map[string]GitProvider)}
	r.Register("gitea", NewDisabledProvider("gitea", "test"))

	manifest := &Manifest{Provider: "gitea"}
	repo := RepoRef{Owner: "joe", Name: "home-cluster", CloneURL: "https://github.com/joe/home-cluster.git"}

	p, err := r.Resolve(repo, manifest)
	if err != nil {
		t.Fatalf("expected manifest provider to resolve, got error: %v", err)
	}
	if p.Name() != "gitea" {
		t.Errorf("expected provider name %q, got %q", "gitea", p.Name())
	}
}

func TestRegistry_ResolveFromURL_GitHub(t *testing.T) {
	r := &Registry{providers: make(map[string]GitProvider)}
	r.Register("github", NewDisabledProvider("github", "test"))

	repo := RepoRef{Owner: "joe", Name: "claude-ops", CloneURL: "https://github.com/joe/claude-ops.git"}

	p, err := r.Resolve(repo, nil)
	if err != nil {
		t.Fatalf("expected URL-based resolve to work, got error: %v", err)
	}
	if p.Name() != "github" {
		t.Errorf("expected provider name %q, got %q", "github", p.Name())
	}
}

func TestRegistry_ResolveFromURL_Gitea(t *testing.T) {
	t.Setenv("GITEA_URL", "https://gitea.stump.wtf")

	r := &Registry{providers: make(map[string]GitProvider)}
	r.Register("gitea", NewDisabledProvider("gitea", "test"))

	repo := RepoRef{Owner: "joe", Name: "home-cluster", CloneURL: "https://gitea.stump.wtf/joe/home-cluster.git"}

	p, err := r.Resolve(repo, nil)
	if err != nil {
		t.Fatalf("expected URL-based resolve to work for Gitea, got error: %v", err)
	}
	if p.Name() != "gitea" {
		t.Errorf("expected provider name %q, got %q", "gitea", p.Name())
	}
}

func TestRegistry_ResolveNoMatch(t *testing.T) {
	t.Setenv("GITEA_URL", "")

	r := &Registry{providers: make(map[string]GitProvider)}

	repo := RepoRef{Owner: "joe", Name: "repo", CloneURL: "https://gitlab.com/joe/repo.git"}

	_, err := r.Resolve(repo, nil)
	if err == nil {
		t.Fatal("expected error when no provider matches, but got nil")
	}
	if !strings.Contains(err.Error(), "cannot resolve") {
		t.Errorf("expected 'cannot resolve' error, got: %v", err)
	}
}

func TestRegistry_ResolveByName_NotRegistered(t *testing.T) {
	r := &Registry{providers: make(map[string]GitProvider)}

	_, err := r.ResolveByName("gitlab")
	if err == nil {
		t.Fatal("expected error for unregistered provider, but got nil")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("expected 'not registered' error, got: %v", err)
	}
}

func TestRegistry_DisabledProviderReturnsError(t *testing.T) {
	r := &Registry{providers: make(map[string]GitProvider)}
	r.Register("github", NewDisabledProvider("github", "GITHUB_TOKEN is not set"))

	p, err := r.ResolveByName("github")
	if err != nil {
		t.Fatalf("expected disabled provider to resolve, got error: %v", err)
	}

	err = p.CreateBranch(nil, RepoRef{}, "branch", "main")
	if err == nil {
		t.Fatal("expected disabled provider to return error, but got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("expected error to mention reason, got: %v", err)
	}
}

func TestRegistry_ManifestTakesPrecedenceOverURL(t *testing.T) {
	r := &Registry{providers: make(map[string]GitProvider)}
	r.Register("github", NewDisabledProvider("github", "test"))
	r.Register("gitea", NewDisabledProvider("gitea", "test"))

	// URL says github.com but manifest says gitea.
	manifest := &Manifest{Provider: "gitea"}
	repo := RepoRef{Owner: "joe", Name: "repo", CloneURL: "https://github.com/joe/repo.git"}

	p, err := r.Resolve(repo, manifest)
	if err != nil {
		t.Fatalf("expected manifest to take precedence, got error: %v", err)
	}
	if p.Name() != "gitea" {
		t.Errorf("expected manifest provider %q to win, got %q", "gitea", p.Name())
	}
}
