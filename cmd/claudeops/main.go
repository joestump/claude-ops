package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/gitprovider"
	"github.com/joestump/claude-ops/internal/hub"
	"github.com/joestump/claude-ops/internal/mcp"
	"github.com/joestump/claude-ops/internal/session"
	"github.com/joestump/claude-ops/internal/web"
)

// Governing: SPEC-0008 REQ-1 (Single Binary Entrypoint — compiles to static binary, replaces entrypoint.sh)
func main() {
	rootCmd := &cobra.Command{
		Use:   "claudeops",
		Short: "Autonomous infrastructure watchdog powered by Claude",
		RunE:  run,
	}

	// Register flags with defaults matching the original entrypoint.sh values.
	f := rootCmd.Flags()
	f.Int("interval", 3600, "seconds between health-check sessions")
	f.String("prompt", "/app/prompts/tier1-observe.md", "path to the prompt file")
	f.String("tier1-model", "haiku", "Claude model for Tier 1 (observe)")
	f.String("tier2-model", "sonnet", "Claude model for Tier 2 (investigate)")
	f.String("tier3-model", "opus", "Claude model for Tier 3 (remediate)")
	f.String("state-dir", "/state", "directory for persistent state")
	f.String("results-dir", "/results", "directory for session logs")
	f.String("repos-dir", "/repos", "directory for cloned repositories")
	f.String("allowed-tools", "Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch", "comma-separated Claude tools")
	f.Bool("dry-run", false, "skip actual remediation actions")
	f.Bool("verbose", false, "enable verbose Claude CLI output")
	f.String("apprise-urls", "", "Apprise notification URLs")
	f.String("mcp-config", "/app/.claude/mcp.json", "path to MCP config file")
	f.Int("dashboard-port", 8080, "HTTP port for the dashboard")
	f.Int("max-tier", 3, "maximum escalation tier (1-3)")
	f.String("tier2-prompt", "/app/prompts/tier2-investigate.md", "path to Tier 2 prompt file")
	f.String("tier3-prompt", "/app/prompts/tier3-remediate.md", "path to Tier 3 prompt file")
	f.Int("memory-budget", 2000, "max tokens for memory context injection")
	f.String("browser-allowed-origins", "", "comma-separated allowed origins for browser navigation")
	f.Bool("pr-enabled", false, "enable PR creation via MCP and REST API (default: disabled)")
	f.String("summary-model", "haiku", "Claude model for session summary generation")

	// Bind flags to viper. Viper keys use underscores (tier1_model) so they
	// match the env var suffix after stripping the CLAUDEOPS_ prefix.
	bindFlag := func(viperKey, flagName string) {
		_ = viper.BindPFlag(viperKey, f.Lookup(flagName))
	}
	bindFlag("interval", "interval")
	bindFlag("prompt", "prompt")
	bindFlag("tier1_model", "tier1-model")
	bindFlag("tier2_model", "tier2-model")
	bindFlag("tier3_model", "tier3-model")
	bindFlag("state_dir", "state-dir")
	bindFlag("results_dir", "results-dir")
	bindFlag("repos_dir", "repos-dir")
	bindFlag("allowed_tools", "allowed-tools")
	bindFlag("dry_run", "dry-run")
	bindFlag("verbose", "verbose")
	bindFlag("apprise_urls", "apprise-urls")
	bindFlag("mcp_config", "mcp-config")
	bindFlag("dashboard_port", "dashboard-port")
	bindFlag("max_tier", "max-tier")
	bindFlag("tier2_prompt", "tier2-prompt")
	bindFlag("tier3_prompt", "tier3-prompt")
	bindFlag("memory_budget", "memory-budget")
	bindFlag("browser_allowed_origins", "browser-allowed-origins")
	bindFlag("pr_enabled", "pr-enabled")
	bindFlag("summary_model", "summary-model")

	// Bind CLAUDEOPS_* environment variables. AutomaticEnv with the prefix
	// maps CLAUDEOPS_INTERVAL -> "interval", CLAUDEOPS_TIER1_MODEL -> "tier1_model", etc.
	viper.SetEnvPrefix("CLAUDEOPS")
	viper.AutomaticEnv()
	// Env vars use underscores (CLAUDEOPS_TIER1_MODEL), viper keys also use
	// underscores, so the default replacer is fine. But flag names use hyphens,
	// so we need the replacer for any edge cases.
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Governing: SPEC-0023 REQ-9 — custom MCP server removed; tools are now skill-based.

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg := config.Load()

	fmt.Printf("Claude Ops %s starting\n", config.Version)
	fmt.Printf("  Tier 1 model: %s\n", cfg.Tier1Model)
	fmt.Printf("  Interval: %ds\n", cfg.Interval)
	fmt.Printf("  Prompt: %s\n", cfg.Prompt)
	fmt.Printf("  State: %s\n", cfg.StateDir)
	fmt.Printf("  Results: %s\n", cfg.ResultsDir)
	fmt.Printf("  Repos: %s\n", cfg.ReposDir)
	fmt.Printf("  Dry run: %t\n", cfg.DryRun)
	fmt.Printf("  Dashboard: :%d\n", cfg.DashboardPort)
	fmt.Println()

	// Ensure cooldown state file exists.
	cooldownPath := filepath.Join(cfg.StateDir, "cooldown.json")
	if _, err := os.Stat(cooldownPath); os.IsNotExist(err) {
		initial := map[string]any{
			"services":          map[string]any{},
			"last_run":          nil,
			"last_daily_digest": nil,
		}
		data, _ := json.MarshalIndent(initial, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(cooldownPath, data, 0644); err != nil {
			return fmt.Errorf("failed to create cooldown state: %w", err)
		}
	}

	// Open database.
	database, err := db.Open(filepath.Join(cfg.StateDir, "claudeops.db"))
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close() //nolint:errcheck

	// Create SSE hub.
	sseHub := hub.New()

	// Create session manager with MCP merging as pre-session hook.
	// Governing: SPEC-0008 REQ-5 "Claude Code CLI Session Management"
	// — session manager handles scheduling, subprocess creation, and tier tracking.
	// Governing: SPEC-0008 REQ-11 "MCP Configuration Merging"
	// — PreSessionHook merges .claude-ops/mcp.json from repos before each session.
	mgr := session.New(&cfg, database, sseHub, &session.CLIRunner{})
	mgr.PreSessionHook = func() error {
		fmt.Println("Merging MCP configurations...")
		return mcp.MergeConfigs(cfg.MCPConfig, cfg.ReposDir)
	}

	// Create git provider registry.
	registry := gitprovider.NewRegistry()

	// Governing: SPEC-0008 REQ-2 (Web Server — HTTP on configurable port, default 8080)
	// Create and start web server (needs mgr for ad-hoc session triggers).
	webServer := web.New(&cfg, sseHub, database, mgr, registry)
	go func() {
		if err := webServer.Start(); err != nil {
			log.Printf("web server error: %v", err)
		}
	}()

	// Set up signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		cancel()
	}()

	if err := mgr.Run(ctx); err != nil {
		return fmt.Errorf("session manager: %w", err)
	}

	// Gracefully shut down web server.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*1e9)
	defer shutdownCancel()
	if err := webServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("web server shutdown: %v", err)
	}

	return nil
}
