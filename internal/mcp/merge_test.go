package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeConfigs_SavesBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")
	if err := os.MkdirAll(reposDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("MergeConfigs: %v", err)
	}

	// Baseline file should exist.
	baselinePath := mcpPath + ".baseline"
	if _, err := os.Stat(baselinePath); os.IsNotExist(err) {
		t.Fatal("baseline file was not created")
	}

	// Baseline should match the original content.
	got := readTestJSON(t, baselinePath)
	servers, _ := got["mcpServers"].(map[string]any)
	if servers == nil || servers["docker"] == nil {
		t.Fatal("baseline does not contain original mcpServers")
	}
}

func TestMergeConfigs_RestoresBaselineEachRun(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")

	// Create a repo with an extra server.
	repoDir := filepath.Join(reposDir, "my-repo", ".claude-ops")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestJSON(t, filepath.Join(repoDir, "mcp.json"), map[string]any{
		"mcpServers": map[string]any{
			"custom": map[string]any{"command": "custom-mcp"},
		},
	})

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	// First run: merges "custom" in.
	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("first MergeConfigs: %v", err)
	}

	got := readTestJSON(t, mcpPath)
	servers := got["mcpServers"].(map[string]any)
	if servers["custom"] == nil {
		t.Fatal("expected 'custom' server after first merge")
	}

	// Remove the repo config.
	_ = os.RemoveAll(filepath.Join(reposDir, "my-repo"))

	// Second run: should restore baseline, "custom" should be gone.
	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("second MergeConfigs: %v", err)
	}

	got = readTestJSON(t, mcpPath)
	servers = got["mcpServers"].(map[string]any)
	if servers["custom"] != nil {
		t.Fatal("'custom' server should not persist after repo is removed")
	}
	if servers["docker"] == nil {
		t.Fatal("baseline 'docker' server should still be present")
	}
}

func TestMergeConfigs_RepoOverridesBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")

	repoDir := filepath.Join(reposDir, "override-repo", ".claude-ops")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestJSON(t, filepath.Join(repoDir, "mcp.json"), map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "custom-docker-mcp"},
		},
	})

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("MergeConfigs: %v", err)
	}

	got := readTestJSON(t, mcpPath)
	servers := got["mcpServers"].(map[string]any)
	docker, _ := servers["docker"].(map[string]any)
	if docker == nil || docker["command"] != "custom-docker-mcp" {
		t.Fatalf("expected repo to override baseline docker server, got %v", docker)
	}
}

func TestMergeConfigs_MultipleRepos(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")

	for _, repo := range []struct {
		name   string
		server string
		cmd    string
	}{
		{"repo-a", "server-a", "cmd-a"},
		{"repo-b", "server-b", "cmd-b"},
	} {
		dir := filepath.Join(reposDir, repo.name, ".claude-ops")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeTestJSON(t, filepath.Join(dir, "mcp.json"), map[string]any{
			"mcpServers": map[string]any{
				repo.server: map[string]any{"command": repo.cmd},
			},
		})
	}

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("MergeConfigs: %v", err)
	}

	got := readTestJSON(t, mcpPath)
	servers := got["mcpServers"].(map[string]any)

	for _, name := range []string{"docker", "server-a", "server-b"} {
		if servers[name] == nil {
			t.Errorf("expected server %q in merged config", name)
		}
	}
}

func TestMergeConfigs_NoRepos(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")
	if err := os.MkdirAll(reposDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("MergeConfigs: %v", err)
	}

	got := readTestJSON(t, mcpPath)
	servers := got["mcpServers"].(map[string]any)
	if servers["docker"] == nil {
		t.Fatal("baseline should be preserved when no repos exist")
	}
}

func TestMergeConfigs_NoMcpServersKeyInRepo(t *testing.T) {
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	reposDir := filepath.Join(tmpDir, "repos")

	repoDir := filepath.Join(reposDir, "empty-repo", ".claude-ops")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Repo config has no mcpServers key.
	writeTestJSON(t, filepath.Join(repoDir, "mcp.json"), map[string]any{
		"someOtherKey": "value",
	})

	baseline := map[string]any{
		"mcpServers": map[string]any{
			"docker": map[string]any{"command": "docker-mcp"},
		},
	}
	writeTestJSON(t, mcpPath, baseline)

	if err := MergeConfigs(mcpPath, reposDir); err != nil {
		t.Fatalf("MergeConfigs: %v", err)
	}

	got := readTestJSON(t, mcpPath)
	servers := got["mcpServers"].(map[string]any)
	if servers["docker"] == nil {
		t.Fatal("baseline should be preserved when repo has no mcpServers")
	}
}

func writeTestJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return result
}
