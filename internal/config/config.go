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
	// Governing: SPEC-0024 REQ-11 (Per-Tier Tool Enforcement for Chat Sessions), ADR-0023
	Tier1AllowedTools    string
	Tier1DisallowedTools string
	Tier2AllowedTools    string
	Tier2DisallowedTools string
	Tier3AllowedTools    string
	Tier3DisallowedTools string
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
	// Governing: SPEC-0025 REQ "Webhook Model Configuration"
	WebhookModel        string
	WebhookSystemPrompt string
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
		Tier1AllowedTools:    viper.GetString("tier1_allowed_tools"),
		Tier1DisallowedTools: viper.GetString("tier1_disallowed_tools"),
		Tier2AllowedTools:    viper.GetString("tier2_allowed_tools"),
		Tier2DisallowedTools: viper.GetString("tier2_disallowed_tools"),
		Tier3AllowedTools:    viper.GetString("tier3_allowed_tools"),
		Tier3DisallowedTools: viper.GetString("tier3_disallowed_tools"),
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
		WebhookModel:          viper.GetString("webhook_model"),
		WebhookSystemPrompt:   viper.GetString("webhook_system_prompt"),
	}
}
