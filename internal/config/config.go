package config

import "github.com/spf13/viper"

// Version is set at build time via -ldflags.
var Version = "dev"

// Config holds all runtime configuration for Claude Ops.
// Governing: SPEC-0008 REQ-12 — environment variable compatibility (CLAUDEOPS_* env vars via viper).
type Config struct {
	Interval      int
	Prompt        string
	Tier1Model    string
	Tier2Model    string
	Tier3Model    string
	StateDir      string
	ResultsDir    string
	ReposDir      string
	AllowedTools    string // Governing: SPEC-0010 REQ-5 "Tool filtering via --allowedTools"
	DisallowedTools string // Governing: ADR-0023 "AllowedTools-Based Tier Enforcement" — command-prefix blocklist per tier
	DryRun        bool
	AppriseURLs   string
	MCPConfig     string
	DashboardPort int
	MaxTier       int
	Tier2Prompt   string
	Tier3Prompt   string
	MemoryBudget          int
	BrowserAllowedOrigins string
	// Governing: SPEC-0021 REQ "Summarization Model"
	SummaryModel string
}

// Load reads configuration from viper, which merges flag values, env vars,
// and defaults (set up by the cobra command in cmd/claudeops).
func Load() Config {
	return Config{
		Interval:      viper.GetInt("interval"),
		Prompt:        viper.GetString("prompt"),
		Tier1Model:    viper.GetString("tier1_model"),
		Tier2Model:    viper.GetString("tier2_model"),
		Tier3Model:    viper.GetString("tier3_model"),
		StateDir:      viper.GetString("state_dir"),
		ResultsDir:    viper.GetString("results_dir"),
		ReposDir:      viper.GetString("repos_dir"),
		AllowedTools:    viper.GetString("allowed_tools"),
		DisallowedTools: viper.GetString("disallowed_tools"),
		DryRun:        viper.GetBool("dry_run"),
		AppriseURLs:   viper.GetString("apprise_urls"),
		MCPConfig:     viper.GetString("mcp_config"),
		DashboardPort: viper.GetInt("dashboard_port"),
		MaxTier:       viper.GetInt("max_tier"),
		Tier2Prompt:   viper.GetString("tier2_prompt"),
		Tier3Prompt:   viper.GetString("tier3_prompt"),
		MemoryBudget:          viper.GetInt("memory_budget"),
		BrowserAllowedOrigins: viper.GetString("browser_allowed_origins"),
		SummaryModel:          viper.GetString("summary_model"),
	}
}
