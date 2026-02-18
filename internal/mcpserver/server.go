// Package mcpserver implements an MCP (Model Context Protocol) server that
// exposes git provider operations as typed tools over stdio JSON-RPC.
// It wraps the internal/gitprovider package, enforcing scope validation,
// tier gating, and dry-run behavior server-side.
package mcpserver

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/mark3labs/mcp-go/server"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/gitprovider"
)

// Server holds the MCP server state and configuration.
type Server struct {
	registry  *gitprovider.Registry
	tier      int
	dryRun    bool
	prEnabled bool
}

// NewServer creates an MCP server backed by the given registry.
// Tier and dry-run settings are read from the environment.
func NewServer(registry *gitprovider.Registry) *Server {
	tier := 1 // default to most restrictive
	if v := os.Getenv("CLAUDEOPS_TIER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 3 {
			tier = n
		}
	}

	dryRun := os.Getenv("CLAUDEOPS_DRY_RUN") == "true"
	prEnabled := os.Getenv("CLAUDEOPS_PR_ENABLED") == "true"

	return &Server{
		registry:  registry,
		tier:      tier,
		dryRun:    dryRun,
		prEnabled: prEnabled,
	}
}

// Run starts the MCP stdio server. It blocks until the context is cancelled
// or stdin is closed.
func Run() error {
	registry := gitprovider.NewRegistry()
	s := NewServer(registry)

	mcpServer := server.NewMCPServer(
		"claudeops",
		config.Version,
		server.WithToolCapabilities(true),
	)

	tools := []server.ServerTool{
		{Tool: listPRsTool(), Handler: s.handleListPRs},
		{Tool: getPRStatusTool(), Handler: s.handleGetPRStatus},
	}
	if s.prEnabled {
		tools = append(tools, server.ServerTool{Tool: createPRTool(), Handler: s.handleCreatePR})
	}
	mcpServer.AddTools(tools...)

	stdio := server.NewStdioServer(mcpServer)
	stdio.SetErrorLogger(log.New(os.Stderr, "[mcp] ", log.LstdFlags))

	return stdio.Listen(context.Background(), os.Stdin, os.Stdout)
}
