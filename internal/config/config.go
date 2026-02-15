package config

import "github.com/spf13/viper"

// Config holds all runtime configuration for Claude Ops.
type Config struct {
	Interval      int
	Prompt        string
	Tier1Model    string
	Tier2Model    string
	Tier3Model    string
	StateDir      string
	ResultsDir    string
	ReposDir      string
	AllowedTools  string
	DryRun        bool
	Verbose       bool
	AppriseURLs   string
	MCPConfig     string
	DashboardPort int
	MaxTier       int
	Tier2Prompt   string
	Tier3Prompt   string
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
		AllowedTools:  viper.GetString("allowed_tools"),
		DryRun:        viper.GetBool("dry_run"),
		Verbose:       viper.GetBool("verbose"),
		AppriseURLs:   viper.GetString("apprise_urls"),
		MCPConfig:     viper.GetString("mcp_config"),
		DashboardPort: viper.GetInt("dashboard_port"),
		MaxTier:       viper.GetInt("max_tier"),
		Tier2Prompt:   viper.GetString("tier2_prompt"),
		Tier3Prompt:   viper.GetString("tier3_prompt"),
	}
}
