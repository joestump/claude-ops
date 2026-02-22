package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MergeConfigs merges MCP server configurations from mounted repos into the
// main MCP config file. This is the Go equivalent of merge_mcp_configs() in
// entrypoint.sh.
//
// On the first call, the baseline config is saved to {mcpConfigPath}.baseline.
// On every call, the baseline is restored before merging so that removed repo
// configs don't persist across runs.
//
// Repo configs are discovered at {reposDir}/*/.claude-ops/mcp.json. Each
// repo's "mcpServers" entries are merged into the baseline. On name collision,
// the repo config wins (overrides the baseline).
//
// Governing: SPEC-0008 REQ-11 "MCP Configuration Merging"
// — replicates entrypoint.sh merge behavior: discovers .claude-ops/mcp.json
// from mounted repos, merges into baseline, repo configs override on collision.
func MergeConfigs(mcpConfigPath, reposDir string) error {
	// If the MCP config doesn't exist, there's nothing to merge into. This is
	// normal when running locally outside Docker.
	if _, err := os.Stat(mcpConfigPath); os.IsNotExist(err) {
		return nil
	}

	baselinePath := mcpConfigPath + ".baseline"

	// Save baseline on first run.
	if _, err := os.Stat(baselinePath); os.IsNotExist(err) {
		if err := copyFile(mcpConfigPath, baselinePath); err != nil {
			return fmt.Errorf("saving baseline MCP config: %w", err)
		}
	}

	// Restore from baseline before merging.
	if err := copyFile(baselinePath, mcpConfigPath); err != nil {
		return fmt.Errorf("restoring baseline MCP config: %w", err)
	}

	// Load the baseline config into memory.
	merged, err := readJSONFile(mcpConfigPath)
	if err != nil {
		return fmt.Errorf("reading MCP config: %w", err)
	}

	// Ensure mcpServers exists in the baseline.
	baseServers, _ := merged["mcpServers"].(map[string]any)
	if baseServers == nil {
		baseServers = make(map[string]any)
	}

	// Scan for repo MCP configs: {reposDir}/*/.claude-ops/mcp.json
	pattern := filepath.Join(reposDir, "*", ".claude-ops", "mcp.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("globbing repo MCP configs: %w", err)
	}

	for _, repoMCP := range matches {
		// Extract repo name: the directory two levels up from the mcp.json.
		// e.g. /repos/my-repo/.claude-ops/mcp.json -> "my-repo"
		repoName := filepath.Base(filepath.Dir(filepath.Dir(repoMCP)))
		fmt.Printf("  Merging MCP config from %s\n", repoName)

		repoConfig, err := readJSONFile(repoMCP)
		if err != nil {
			return fmt.Errorf("reading MCP config from %s: %w", repoName, err)
		}

		repoServers, _ := repoConfig["mcpServers"].(map[string]any)
		if repoServers == nil {
			continue
		}

		// Merge: repo servers override baseline on name collision.
		for name, server := range repoServers {
			baseServers[name] = server
		}
	}

	merged["mcpServers"] = baseServers

	// Write the merged config back.
	if err := writeJSONFile(mcpConfigPath, merged); err != nil {
		return fmt.Errorf("writing merged MCP config: %w", err)
	}

	return nil
}

// readJSONFile reads a JSON file into a generic map.
func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// writeJSONFile writes a map as indented JSON to a file.
func writeJSONFile(path string, data map[string]any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0644)
}

// copyFile copies src to dst, preserving the source file's permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
