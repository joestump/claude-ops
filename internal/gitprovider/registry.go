package gitprovider

import (
	"fmt"
	"os"
	"strings"
)

// Registry maps provider names to GitProvider implementations.
type Registry struct {
	providers map[string]GitProvider
}

// NewRegistry creates a Registry and registers providers based on environment
// variables. Providers whose required config is missing are registered in a
// disabled state so the system always starts successfully.
func NewRegistry() *Registry {
	r := &Registry{providers: make(map[string]GitProvider)}

	// GitHub: requires GITHUB_TOKEN.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		r.Register("github", NewGitHubProvider(token))
	} else {
		r.Register("github", NewDisabledProvider("github", "GITHUB_TOKEN is not set"))
	}

	// Gitea: requires GITEA_URL and GITEA_TOKEN.
	giteaURL := os.Getenv("GITEA_URL")
	giteaToken := os.Getenv("GITEA_TOKEN")
	if giteaURL != "" && giteaToken != "" {
		r.Register("gitea", NewGiteaProvider(giteaURL, giteaToken))
	} else {
		var missing []string
		if giteaURL == "" {
			missing = append(missing, "GITEA_URL")
		}
		if giteaToken == "" {
			missing = append(missing, "GITEA_TOKEN")
		}
		r.Register("gitea", NewDisabledProvider("gitea", fmt.Sprintf("%s not set", strings.Join(missing, " and "))))
	}

	return r
}

// Register adds or replaces a provider in the registry.
func (r *Registry) Register(name string, provider GitProvider) {
	r.providers[name] = provider
}

// Resolve selects the appropriate provider for a repo. It checks the manifest
// first (explicit declaration), then infers from the clone URL.
func (r *Registry) Resolve(repo RepoRef, manifest *Manifest) (GitProvider, error) {
	// 1. Explicit manifest declaration.
	if manifest != nil && manifest.Provider != "" {
		return r.ResolveByName(manifest.Provider)
	}

	// 2. Infer from clone URL.
	name := inferProvider(repo.CloneURL)
	if name == "" {
		return nil, fmt.Errorf("cannot resolve git provider for repo %s/%s (URL: %s): no matching provider", repo.Owner, repo.Name, repo.CloneURL)
	}
	return r.ResolveByName(name)
}

// ResolveByName returns the provider registered under the given name.
func (r *Registry) ResolveByName(name string) (GitProvider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("git provider %q is not registered", name)
	}
	return p, nil
}

// inferProvider maps a clone URL to a provider name based on known domain patterns.
func inferProvider(cloneURL string) string {
	lower := strings.ToLower(cloneURL)
	if strings.Contains(lower, "github.com") {
		return "github"
	}
	// Gitea instances are identified by common domain patterns.
	// The Gitea URL from env can also be checked, but for inference we
	// rely on known domains from the operator's setup.
	giteaURL := os.Getenv("GITEA_URL")
	if giteaURL != "" {
		// Extract hostname from GITEA_URL for matching.
		host := strings.TrimPrefix(giteaURL, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.Split(host, "/")[0]
		if host != "" && strings.Contains(lower, strings.ToLower(host)) {
			return "gitea"
		}
	}
	return ""
}
