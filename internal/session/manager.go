package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/joestump/claude-ops/internal/config"
	"github.com/joestump/claude-ops/internal/db"
	"github.com/joestump/claude-ops/internal/hub"
)

// Manager runs Claude CLI sessions on a recurring interval.
// Governing: SPEC-0008 REQ-5 "Claude Code CLI Session Management"
// — manages CLI invocations as subprocesses, supports scheduling at a
// configurable interval, and tracks concurrent sessions (one active per tier).
type Manager struct {
	cfg      *config.Config
	db       *db.DB
	hub      *hub.Hub
	runner   ProcessRunner
	redactor *RedactionFilter // Governing: SPEC-0014 REQ "Log Redaction of Credential Values" — applied to all output streams

	// PreSessionHook is called before each session starts.
	// If it returns an error, the session is skipped.
	PreSessionHook func() error

	mu          sync.Mutex
	running     bool
	cmd         *exec.Cmd
	triggerCh   chan string
	lastAdHocID chan int64
}

// New creates a Manager with the given configuration.
func New(cfg *config.Config, database *db.DB, h *hub.Hub, runner ProcessRunner) *Manager {
	return &Manager{
		cfg:         cfg,
		db:          database,
		hub:         h,
		runner:      runner,
		redactor:    NewRedactionFilter(),
		triggerCh:   make(chan string, 1),
		lastAdHocID: make(chan int64, 1),
	}
}

// Governing: SPEC-0012 REQ "Busy Rejection When Session Already Running" (mutex check + channel buffer rejects concurrent triggers)
// TriggerAdHoc sends a prompt to trigger an immediate session.
// Returns the session ID once created, or error if busy.
// Governing: SPEC-0012 "TriggerAdHoc Public API" — channel-based trigger, busy rejection
func (m *Manager) TriggerAdHoc(prompt string) (int64, error) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return 0, fmt.Errorf("session already running")
	}
	m.mu.Unlock()

	select {
	case m.triggerCh <- prompt:
		// Wait for the session ID to be assigned.
		id := <-m.lastAdHocID
		return id, nil
	default:
		return 0, fmt.Errorf("trigger queue full")
	}
}

// IsRunning reports whether a session is currently executing.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Run starts the session loop. It runs one session immediately, then waits
// cfg.Interval seconds after each session completes before starting the next.
// It returns when the context is cancelled and any in-flight session has
// finished (or been killed after the grace period).
// Governing: SPEC-0008 REQ-5 "Scheduled session invocation"
// — invokes sessions at the configured interval after each completion.
func (m *Manager) Run(ctx context.Context) error {
	for {
		m.runEscalationChain(ctx, "scheduled", nil)

		fmt.Printf("[%s] Sleeping %ds until next run...\n\n",
			time.Now().UTC().Format(time.RFC3339), m.cfg.Interval)

		select {
		case <-ctx.Done():
			return nil
		case prompt := <-m.triggerCh:
			m.runAdHoc(ctx, prompt)
		case <-time.After(time.Duration(m.cfg.Interval) * time.Second):
		}
	}
}

// Governing: SPEC-0012 REQ "Ad-Hoc Session Uses runOnce with Custom Prompt" (custom prompt via promptOverride, identical lifecycle to scheduled)
// runAdHoc handles a manually triggered session with full escalation support.
func (m *Manager) runAdHoc(ctx context.Context, prompt string) {
	m.runEscalationChain(ctx, "manual", &prompt)
}

// Governing: SPEC-0016 "Supervisor Escalation Logic" — controls all escalation decisions
// runEscalationChain runs Tier 1 and escalates to higher tiers if the agent
// writes a handoff file requesting it. promptOverride is used for ad-hoc
// sessions where the first tier uses a custom prompt instead of the standard
// prompt file.
func (m *Manager) runEscalationChain(ctx context.Context, trigger string, promptOverride *string) {
	// Governing: SPEC-0015 "Staleness Decay" — 0.1/week after 30-day grace, deactivate below 0.3
	// Decay stale memories before each escalation chain.
	if err := m.db.DecayStaleMemories(30, 0.1); err != nil {
		fmt.Fprintf(os.Stderr, "decay stale memories: %v\n", err)
	}

	// Governing: SPEC-0016 "Handoff File Lifecycle" — delete stale handoff before new cycle
	// Clean up any stale handoff file from a previous run.
	if err := DeleteHandoff(m.cfg.StateDir); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup stale handoff: %v\n", err)
	}

	tierModels := map[int]string{
		1: m.cfg.Tier1Model,
		2: m.cfg.Tier2Model,
		3: m.cfg.Tier3Model,
	}
	tierPrompts := map[int]string{
		1: m.cfg.Prompt,
		2: m.cfg.Tier2Prompt,
		3: m.cfg.Tier3Prompt,
	}

	var parentSessionID *int64
	currentTier := 1
	handoffContext := ""
	currentTrigger := trigger

	// Governing: SPEC-0016 "Supervisor Escalation Logic" — MaxTier enforces tier limit
	for currentTier <= m.cfg.MaxTier {
		model := tierModels[currentTier]
		promptFile := tierPrompts[currentTier]

		// Only use the prompt override for the first tier in the chain.
		var po *string
		if currentTier == 1 && promptOverride != nil {
			po = promptOverride
		}

		sessionID, err := m.runTier(ctx, currentTier, model, promptFile, parentSessionID, handoffContext, currentTrigger, po)
		if err != nil {
			fmt.Printf("[%s] ERROR: tier %d session failed: %v\n",
				time.Now().UTC().Format(time.RFC3339), currentTier, err)
			break
		}

		// Check for a handoff file from the completed tier.
		h, err := ReadHandoff(m.cfg.StateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read handoff after tier %d: %v\n", currentTier, err)
			break
		}
		if h == nil {
			// No handoff — escalation chain complete.
			break
		}

		// Validate the handoff.
		if err := ValidateHandoff(h, m.cfg.MaxTier); err != nil {
			fmt.Fprintf(os.Stderr, "invalid handoff from tier %d: %v\n", currentTier, err)
			if delErr := DeleteHandoff(m.cfg.StateDir); delErr != nil {
				fmt.Fprintf(os.Stderr, "cleanup invalid handoff: %v\n", delErr)
			}
			break
		}

		// Governing: SPEC-0016 "Supervisor Escalation Logic" — dry-run prevents escalation
		// Dry-run mode: don't actually escalate.
		if m.cfg.DryRun && h.RecommendedTier >= 2 {
			fmt.Printf("[%s] DRY RUN: would escalate to tier %d for services %v\n",
				time.Now().UTC().Format(time.RFC3339), h.RecommendedTier, h.ServicesAffected)
			if delErr := DeleteHandoff(m.cfg.StateDir); delErr != nil {
				fmt.Fprintf(os.Stderr, "cleanup dry-run handoff: %v\n", delErr)
			}
			break
		}

		// Mark the parent session as escalated.
		if err := m.db.UpdateSessionStatus(sessionID, "escalated"); err != nil {
			fmt.Fprintf(os.Stderr, "update escalated status for session %d: %v\n", sessionID, err)
		}

		// Prepare for next tier.
		handoffContext = buildHandoffContext(h)
		parentSessionID = &sessionID
		currentTier = h.RecommendedTier
		currentTrigger = "escalation" // child sessions are triggered by escalation

		// Delete the handoff file now that we've consumed it.
		if err := DeleteHandoff(m.cfg.StateDir); err != nil {
			fmt.Fprintf(os.Stderr, "delete handoff: %v\n", err)
		}

		fmt.Printf("[%s] Escalating to tier %d for services %v\n",
			time.Now().UTC().Format(time.RFC3339), currentTier, h.ServicesAffected)
	}
}

// runTier executes a single Claude CLI session for a specific tier.
// It returns the session ID and any error.
// promptOverride is used for ad-hoc sessions (non-nil pointer); promptFile is used otherwise.
func (m *Manager) runTier(ctx context.Context, tier int, model string, promptFile string, parentSessionID *int64, handoffContext string, trigger string, promptOverride *string) (int64, error) {
	// Governing: SPEC-0008 REQ-5 "Session already running"
	// — guards against starting a second session for the same tier.
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		fmt.Println("session still running, skipping")
		return 0, nil
	}
	m.running = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.cmd = nil
		m.mu.Unlock()
	}()

	// Run the pre-session hook (e.g. MCP config merging).
	// Governing: SPEC-0008 REQ-11 "MCP Configuration Merging"
	// — PreSessionHook triggers MergeConfigs before each session invocation.
	if m.PreSessionHook != nil {
		if err := m.PreSessionHook(); err != nil {
			return 0, fmt.Errorf("pre-session hook: %w", err)
		}
	}

	// Build the log file path.
	logFileName := fmt.Sprintf("run-%s.log", time.Now().Format("20060102-150405"))
	logPath := filepath.Join(m.cfg.ResultsDir, logFileName)

	logFile, err := os.Create(logPath)
	if err != nil {
		return 0, fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close() //nolint:errcheck

	// Governing: SPEC-0016 "Supervisor Escalation Logic" — session record with parent_session_id
	// Insert session record into DB.
	// Governing: SPEC-0016 REQ "Per-Tier Cost Attribution" — each tier gets its own session record
	// Governing: SPEC-0016 REQ "Database Schema for Escalation Chains" — parent_session_id links chain
	startedAt := time.Now().UTC().Format(time.RFC3339)
	sess := &db.Session{
		Tier:            tier,
		Model:           model,
		PromptFile:      promptFile,
		Status:          "running",
		StartedAt:       startedAt,
		Trigger:         trigger,
		ParentSessionID: parentSessionID,
	}
	if promptOverride != nil {
		sess.PromptText = promptOverride
		sess.PromptFile = "(ad-hoc)"
	}
	sessionID, err := m.db.InsertSession(sess)
	if err != nil {
		return 0, fmt.Errorf("insert session: %w", err)
	}

	// If this is a manual trigger, send the session ID back to the caller.
	if trigger == "manual" {
		m.lastAdHocID <- sessionID
	}

	// Build environment context string.
	// Governing: SPEC-0015 REQ "Prompt Injection via buildMemoryContext" (memory context appended to --append-system-prompt)
	envCtx := m.buildEnvContext()
	if memCtx := m.buildMemoryContext(); memCtx != "" {
		envCtx += "\n\n" + memCtx
	}
	if handoffContext != "" {
		envCtx += "\n\n" + handoffContext
	}

	// Determine prompt content: use override for ad-hoc sessions,
	// otherwise read from the prompt file.
	var promptContent string
	if promptOverride != nil {
		promptContent = *promptOverride
	} else {
		data, err := os.ReadFile(promptFile)
		if err != nil {
			m.finalizeSession(sessionID, "failed", nil, &logPath)
			return 0, fmt.Errorf("read prompt file %s: %w", promptFile, err)
		}
		promptContent = string(data)
	}

	runStart := time.Now().UTC().Format(time.RFC3339)
	fmt.Printf("[%s] Starting tier %d session (model=%s, prompt=%s, session=%d)...\n",
		runStart, tier, model, promptFile, sessionID)

	// Governing: SPEC-0008 REQ-5 "CLI subprocess creation"
	// — uses ProcessRunner (os/exec) to invoke the claude CLI with model,
	// prompt content, allowed tools, and system prompt arguments.
	stdoutPipe, waitFn, err := m.runner.Start(ctx, model, promptContent, m.cfg.AllowedTools, envCtx)
	if err != nil {
		m.finalizeSession(sessionID, "failed", nil, &logPath)
		return 0, fmt.Errorf("start claude: %w", err)
	}

	// Parse stream-json events and fan out formatted lines to stdout, log, and hub.
	hubID := int(sessionID)
	var resultResponse string
	var resultCostUSD float64
	var resultNumTurns int
	var resultDurationMs int64

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		var lineNum int
		for scanner.Scan() {
			// Governing: SPEC-0014 REQ "Log Redaction of Credential Values" — redact before any output channel
			raw := m.redactor.Redact(scanner.Text())
			ts := time.Now().UTC()

			// Governing: SPEC-0011 "Raw NDJSON Log Preservation" (every raw line written unmodified for auditability)
			// Write timestamped JSON to log file for forensic analysis.
			_, _ = fmt.Fprintf(logFile, "%s\t%s\n", ts.Format(time.RFC3339Nano), raw)

			// Plain text for container stdout logs.
			plainText := FormatStreamEvent(raw)
			if plainText == "" {
				continue
			}

			// Check for result event to capture response and metadata.
			var evt streamEvent
			if err := json.Unmarshal([]byte(raw), &evt); err == nil {
				if evt.Type == "result" {
					if evt.Result != "" {
						resultResponse = evt.Result
					}
					resultCostUSD = evt.TotalCostUSD
					resultNumTurns = evt.NumTurns
					resultDurationMs = evt.DurationMs
				}

				// Parse event and memory markers from assistant text blocks only.
				if evt.Type == "assistant" {
					for _, block := range evt.Message.Content {
						if block.Type == "text" {
							for _, pe := range parseEventMarkers(block.Text) {
								sid := sessionID
								now := time.Now().UTC().Format(time.RFC3339)
								_, _ = m.db.InsertEvent(&db.Event{
									SessionID: &sid,
									Level:     pe.Level,
									Service:   pe.Service,
									Message:   pe.Message,
									CreatedAt: now,
								})
							}
							for _, pm := range parseMemoryMarkers(block.Text) {
								m.upsertMemory(sessionID, tier, pm)
							}
							for _, pc := range parseCooldownMarkers(block.Text) {
								m.insertCooldown(sessionID, tier, pc)
							}
						}
					}
				}
			}

			_, _ = fmt.Fprintln(os.Stdout, plainText)

			// Color-coded HTML for browser SSE stream, wrapped with line number + timestamp.
			htmlLine := FormatStreamEventHTML(raw)
			if htmlLine != "" {
				lineNum++
				wrapped := WrapLogLine(lineNum, ts.Format("15:04:05"), htmlLine)
				m.hub.Publish(hubID, wrapped)
			}
		}
	}()

	// Wait for all stdout to be consumed BEFORE calling cmd.Wait().
	// cmd.Wait() closes the stdout pipe; calling it first can discard
	// buffered data (including the result event with cost/turns metadata).
	select {
	case <-streamDone:
		// Pipe fully drained — now safe to call Wait.
		err := waitFn()
		runEnd := time.Now().UTC().Format(time.RFC3339)
		fmt.Printf("[%s] Tier %d run complete.\n", runEnd, tier)

		status := "completed"
		var exitCode int
		if err != nil {
			status = "failed"
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		m.finalizeSession(sessionID, status, &exitCode, &logPath)

		// Store the final response and metadata from the result event.
		if resultResponse != "" || resultCostUSD > 0 || resultNumTurns > 0 {
			if dbErr := m.db.UpdateSessionResult(sessionID, resultResponse, resultCostUSD, resultNumTurns, resultDurationMs); dbErr != nil {
				fmt.Fprintf(os.Stderr, "failed to store session result %d: %v\n", sessionID, dbErr)
			}
		}

		// Generate and store an LLM summary of the session response.
		// Governing: SPEC-0021 REQ "Session Summary Generation"
		if resultResponse != "" {
			summary, sumErr := summarizeResponse(ctx, resultResponse, m.cfg.SummaryModel)
			if sumErr != nil {
				fmt.Fprintf(os.Stderr, "failed to summarize session %d: %v\n", sessionID, sumErr)
			} else if summary != "" {
				if dbErr := m.db.UpdateSessionSummary(sessionID, summary); dbErr != nil {
					fmt.Fprintf(os.Stderr, "failed to store session summary %d: %v\n", sessionID, dbErr)
				}
			}
		}

		// Close the SSE hub AFTER DB updates so the browser reload sees the final state.
		m.hub.Close(hubID)

		if err != nil {
			return sessionID, fmt.Errorf("claude exited: %w", err)
		}
		return sessionID, nil

	case <-ctx.Done():
		exitCode := 137
		m.finalizeSession(sessionID, "timed_out", &exitCode, &logPath)
		m.hub.Close(hubID)
		return sessionID, ctx.Err()
	}
}

// runOnce is kept for backward compatibility with tests. It delegates to runTier.
func (m *Manager) runOnce(ctx context.Context, promptOverride string, trigger string) error {
	var po *string
	if promptOverride != "" {
		po = &promptOverride
	}
	_, err := m.runTier(ctx, 1, m.cfg.Tier1Model, m.cfg.Prompt, nil, "", trigger, po)
	return err
}

// finalizeSession updates the session record in the DB with final status.
func (m *Manager) finalizeSession(id int64, status string, exitCode *int, logPath *string) {
	endedAt := time.Now().UTC().Format(time.RFC3339)
	if err := m.db.UpdateSession(id, status, &endedAt, exitCode, logPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to update session %d: %v\n", id, err)
	}
}

// --- stream-json event parsing ---

// streamEvent is a minimal representation of a Claude CLI stream-json NDJSON line.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message,omitempty"`
	// Fields from the "result" event.
	Result       string  `json:"result,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	NumTurns     int     `json:"num_turns,omitempty"`
	DurationMs   int64   `json:"duration_ms,omitempty"`
	IsError      bool    `json:"is_error,omitempty"`
}

type contentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
}

// FormatStreamEvent parses a raw NDJSON line and returns a human-readable
// formatted string for display in the terminal and SSE hub. Returns "" for
// events that should be suppressed (e.g. unknown types, non-init system events).
// Governing: SPEC-0011 "Event Parsing and Formatting" — formats system, assistant, user, result events
func FormatStreamEvent(raw string) string {
	var evt streamEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return raw // can't parse, show raw
	}

	switch evt.Type {
	case "system":
		if evt.Subtype == "init" {
			return "--- session started ---"
		}
		return ""

	case "assistant":
		var parts []string
		for _, block := range evt.Message.Content {
			switch block.Type {
			case "text":
				text := strings.TrimSpace(stripANSI(block.Text))
				if text != "" {
					parts = append(parts, text)
				}
			case "tool_use":
				input := truncateJSON(string(block.Input), 200)
				parts = append(parts, fmt.Sprintf("[tool] %s: %s", block.Name, input))
			}
		}
		return strings.Join(parts, "\n")

	case "user":
		// Tool results — show a brief summary.
		for _, block := range evt.Message.Content {
			if block.Type == "tool_result" {
				content := stripANSI(extractToolResultContent(block.Content))
				truncated := truncateString(content, 300)
				return fmt.Sprintf("[result] %s", truncated)
			}
		}
		return ""

	case "result":
		var line string
		if evt.IsError {
			line = fmt.Sprintf("--- session error (turns=%d, cost=$%.4f, duration=%dms) ---",
				evt.NumTurns, evt.TotalCostUSD, evt.DurationMs)
		} else {
			line = fmt.Sprintf("--- session complete (turns=%d, cost=$%.4f, duration=%dms) ---",
				evt.NumTurns, evt.TotalCostUSD, evt.DurationMs)
		}
		return line

	default:
		return ""
	}
}

// extractToolResultContent handles tool_result content which can be a string
// or a JSON array of content blocks.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try as a plain string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try as an array of blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return string(raw)
}

// truncateJSON truncates a JSON string to maxLen characters, adding "..." if needed.
func truncateJSON(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateString collapses whitespace and truncates to maxLen characters.
// Used for single-line plain-text display (stdout).
func truncateString(s string, maxLen int) string {
	// Collapse runs of whitespace for display.
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncatePreserve truncates to maxLen characters without collapsing whitespace.
// Used for <pre> blocks where newlines matter.
func truncatePreserve(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip CSI sequence: ESC [ ... final byte (0x40-0x7E)
			j := i + 2
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3F {
				j++ // parameter bytes
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7E {
				j++ // final byte
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// FormatStreamEventHTML returns an HTML-formatted version of a stream event
// with rich markup mimicking the Claude CLI terminal experience. Suitable for
// SSE delivery and browser display. Returns "" for suppressed events.
// Governing: SPEC-0011 "Event Parsing and Formatting" — HTML variant for browser display
func FormatStreamEventHTML(raw string) string {
	var evt streamEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return `<div class="term-line"><span class="term-text">` + htmlEscape(stripANSI(raw)) + `</span></div>`
	}

	switch evt.Type {
	case "system":
		if evt.Subtype == "init" {
			return `<div class="term-separator"><span class="term-marker">session started</span></div>`
		}
		return ""

	case "assistant":
		var parts []string
		for _, block := range evt.Message.Content {
			switch block.Type {
			case "text":
				text := strings.TrimSpace(stripANSI(block.Text))
				if text != "" {
					parts = append(parts, `<div class="term-line term-assistant">`+htmlEscape(text)+`</div>`)
				}
			case "tool_use":
				parts = append(parts, formatToolUseHTML(block))
			}
		}
		return strings.Join(parts, "")

	case "user":
		for _, block := range evt.Message.Content {
			if block.Type == "tool_result" {
				content := stripANSI(extractToolResultContent(block.Content))
				truncated := truncatePreserve(content, 2000)
				return `<div class="term-result-block"><pre class="term-result-content">` + htmlEscape(truncated) + `</pre></div>`
			}
		}
		return ""

	case "result":
		var status, cls string
		if evt.IsError {
			status = "error"
			cls = "term-marker-error"
		} else {
			status = "complete"
			cls = "term-marker"
		}
		meta := fmt.Sprintf("%d turns &middot; $%.4f &middot; %s",
			evt.NumTurns, evt.TotalCostUSD, fmtDurationMs(evt.DurationMs))
		return `<div class="term-separator"><span class="` + cls + `">session ` + status + `</span><span class="term-meta">` + meta + `</span></div>`

	default:
		return ""
	}
}

// formatToolUseHTML renders a tool_use block with a badge and highlighted input.
func formatToolUseHTML(block contentBlock) string {
	name := block.Name
	summary := extractToolSummary(name, block.Input)

	var b strings.Builder
	b.WriteString(`<div class="term-tool-block">`)
	b.WriteString(`<span class="term-tool-badge">` + htmlEscape(name) + `</span>`)
	if summary != "" {
		b.WriteString(` <span class="term-tool-summary">` + htmlEscape(summary) + `</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// extractToolSummary pulls the most useful field from a tool's input for display.
func extractToolSummary(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return truncateJSON(string(input), 120)
	}

	// Show the most relevant field per tool type.
	var key string
	switch toolName {
	case "Bash":
		key = "command"
	case "Read":
		key = "file_path"
	case "Write":
		key = "file_path"
	case "Edit":
		key = "file_path"
	case "Grep":
		key = "pattern"
	case "Glob":
		key = "pattern"
	case "WebFetch":
		key = "url"
	case "WebSearch":
		key = "query"
	case "Task":
		key = "prompt"
	}

	if key != "" {
		if val, ok := fields[key]; ok {
			var s string
			if err := json.Unmarshal(val, &s); err == nil {
				return truncateString(s, 120)
			}
		}
	}

	return truncateJSON(string(input), 120)
}

// fmtDurationMs formats milliseconds into a human-readable duration string.
func fmtDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := ms / 1000
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	remainSecs := secs % 60
	return fmt.Sprintf("%dm%ds", mins, remainSecs)
}

// htmlEscape escapes HTML special characters.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// --- Generic marker parsing ---

// markerMatch holds the captured groups from a marker regex match.
// Every marker regex is expected to have 3 capture groups:
//   group 1: primary field (level, category, action_type)
//   group 2: optional service name (may be empty)
//   group 3: trailing text (message, observation, result+message)
type markerMatch struct {
	Field1  string
	Service string // empty when not present
	Tail    string
}

// parseMarkers scans text line-by-line for regex matches and returns the
// captured groups. The regex MUST have exactly 3 capture groups.
func parseMarkers(re *regexp.Regexp, text string) []markerMatch {
	var results []markerMatch
	for _, line := range strings.Split(text, "\n") {
		matches := re.FindStringSubmatch(strings.TrimSpace(line))
		if matches == nil {
			continue
		}
		results = append(results, markerMatch{
			Field1:  matches[1],
			Service: matches[2],
			Tail:    matches[3],
		})
	}
	return results
}

// --- Event markers ---

// Governing: SPEC-0013 REQ "Event Marker Parsing" ([EVENT:level] and [EVENT:level:service] from assistant text only)
// eventMarkerRe matches [EVENT:level] or [EVENT:level:service] markers in assistant text.
var eventMarkerRe = regexp.MustCompile(`\[EVENT:(info|warning|critical)(?::([a-zA-Z0-9_-]+))?\]\s*(.+)`)

type parsedEvent struct {
	Level   string
	Service *string
	Message string
}

// parseEventMarkers scans text for event markers and returns parsed events.
func parseEventMarkers(text string) []parsedEvent {
	var events []parsedEvent
	for _, mm := range parseMarkers(eventMarkerRe, text) {
		e := parsedEvent{
			Level:   mm.Field1,
			Message: mm.Tail,
		}
		if mm.Service != "" {
			svc := mm.Service
			e.Service = &svc
		}
		events = append(events, e)
	}
	return events
}

// --- Memory markers ---

// Governing: SPEC-0015 "Memory Marker Regex" — pattern for [MEMORY:category] and [MEMORY:category:service]
// memoryMarkerRe matches [MEMORY:category] or [MEMORY:category:service] markers in assistant text.
var memoryMarkerRe = regexp.MustCompile(`\[MEMORY:([a-z]+)(?::([a-zA-Z0-9_-]+))?\]\s*(.+)`)

type parsedMemory struct {
	Category    string
	Service     *string
	Observation string
}

// Governing: SPEC-0015 "Memory Marker Format" — parses [MEMORY:category:service] from assistant text blocks
// parseMemoryMarkers scans text for memory markers and returns parsed memories.
func parseMemoryMarkers(text string) []parsedMemory {
	var memories []parsedMemory
	for _, mm := range parseMarkers(memoryMarkerRe, text) {
		pm := parsedMemory{
			Category:    mm.Field1,
			Observation: mm.Tail,
		}
		if mm.Service != "" {
			svc := mm.Service
			pm.Service = &svc
		}
		memories = append(memories, pm)
	}
	return memories
}

// --- Cooldown markers ---

// cooldownMarkerRe matches [COOLDOWN:action_type:service] result — message markers in assistant text.
var cooldownMarkerRe = regexp.MustCompile(`\[COOLDOWN:(restart|redeployment):([a-zA-Z0-9_-]+)\]\s*(success|failure)\s*[—–-]\s*(.+)`)

type parsedCooldown struct {
	ActionType string
	Service    string
	Success    bool
	Message    string
}

// parseCooldownMarkers scans text for cooldown markers and returns parsed cooldowns.
func parseCooldownMarkers(text string) []parsedCooldown {
	var cooldowns []parsedCooldown
	for _, line := range strings.Split(text, "\n") {
		matches := cooldownMarkerRe.FindStringSubmatch(strings.TrimSpace(line))
		if matches == nil {
			continue
		}
		cooldowns = append(cooldowns, parsedCooldown{
			ActionType: matches[1],
			Service:    matches[2],
			Success:    matches[3] == "success",
			Message:    matches[4],
		})
	}
	return cooldowns
}

// WrapLogLine wraps formatted HTML content with a line number, timestamp, and anchor.
func WrapLogLine(num int, ts string, content string) string {
	return fmt.Sprintf(`<div class="log-line" id="L%d"><a class="line-num" href="#L%d">%d</a><span class="line-ts">%s</span><div class="line-content">%s</div></div>`,
		num, num, num, ts, content)
}

// ParseTimestampedLogLine splits a log line into timestamp and raw JSON.
// Governing: SPEC-0011 "Log File Formatting on Read Path" — parse timestamped NDJSON for display
// Log lines may be in timestamped format "2006-01-02T15:04:05Z\t{json}" or
// legacy format with just raw JSON.
func ParseTimestampedLogLine(line string) (ts time.Time, raw string, hasTS bool) {
	if idx := strings.IndexByte(line, '\t'); idx > 0 && idx < 40 {
		candidate := line[:idx]
		if t, err := time.Parse(time.RFC3339Nano, candidate); err == nil {
			return t, line[idx+1:], true
		}
	}
	return time.Time{}, line, false
}

// buildHandoffContext formats a Handoff into a readable markdown section
// suitable for injection into the next tier's system prompt.
func buildHandoffContext(h *Handoff) string {
	var b strings.Builder
	b.WriteString("## Escalation Context\n\n")
	b.WriteString(fmt.Sprintf("Services affected: %s\n\n", strings.Join(h.ServicesAffected, ", ")))

	if len(h.CheckResults) > 0 {
		b.WriteString("### Check Results\n\n")
		for _, cr := range h.CheckResults {
			status := cr.Status
			line := fmt.Sprintf("- **%s** (%s): %s", cr.Service, cr.CheckType, status)
			if cr.Error != "" {
				line += fmt.Sprintf(" — %s", cr.Error)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if h.InvestigationFindings != "" {
		b.WriteString("### Investigation Findings\n\n")
		b.WriteString(h.InvestigationFindings + "\n\n")
	}

	if h.RemediationAttempted != "" {
		b.WriteString("### Remediation Already Attempted\n\n")
		b.WriteString(h.RemediationAttempted + "\n\n")
	}

	return b.String()
}

// buildEnvContext produces the environment context string appended to the
// Claude system prompt, matching the format in entrypoint.sh.
func (m *Manager) buildEnvContext() string {
	ctx := fmt.Sprintf("CLAUDEOPS_DRY_RUN=%v", m.cfg.DryRun)
	ctx += fmt.Sprintf(" CLAUDEOPS_STATE_DIR=%s", m.cfg.StateDir)
	ctx += fmt.Sprintf(" CLAUDEOPS_RESULTS_DIR=%s", m.cfg.ResultsDir)
	ctx += fmt.Sprintf(" CLAUDEOPS_REPOS_DIR=%s", m.cfg.ReposDir)
	ctx += fmt.Sprintf(" CLAUDEOPS_TIER2_MODEL=%s", m.cfg.Tier2Model)
	ctx += fmt.Sprintf(" CLAUDEOPS_TIER3_MODEL=%s", m.cfg.Tier3Model)

	if m.cfg.AppriseURLs != "" {
		ctx += fmt.Sprintf(" CLAUDEOPS_APPRISE_URLS=%s", m.cfg.AppriseURLs)
	}

	if m.cfg.BrowserAllowedOrigins != "" {
		ctx += fmt.Sprintf(" BROWSER_ALLOWED_ORIGINS=%s", m.cfg.BrowserAllowedOrigins)
	}

	return ctx
}

// Governing: SPEC-0015 "Confidence Scoring", "Memory Reinforcement", "Memory Contradiction" — default 0.7, +0.1 reinforce, -0.1 contradict
// upsertMemory handles the insert-or-update logic for a parsed memory marker.
// If a similar memory exists (same service + category), it either reinforces
// (increases confidence) or contradicts (decreases old, inserts new).
func (m *Manager) upsertMemory(sessionID int64, tier int, pm parsedMemory) {
	existing, err := m.db.FindSimilarMemory(pm.Service, pm.Category)
	if err != nil {
		fmt.Fprintf(os.Stderr, "find similar memory: %v\n", err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if existing != nil {
		if existing.Observation == pm.Observation {
			// Same observation — reinforce confidence.
			newConf := existing.Confidence + 0.1
			if newConf > 1.0 {
				newConf = 1.0
			}
			if err := m.db.UpdateMemory(existing.ID, existing.Observation, newConf, existing.Active); err != nil {
				fmt.Fprintf(os.Stderr, "reinforce memory %d: %v\n", existing.ID, err)
			}
			return
		}
		// Different observation — decrease old confidence.
		newConf := existing.Confidence - 0.1
		active := existing.Active
		if newConf < 0.3 {
			active = false
		}
		if err := m.db.UpdateMemory(existing.ID, existing.Observation, newConf, active); err != nil {
			fmt.Fprintf(os.Stderr, "decay contradicted memory %d: %v\n", existing.ID, err)
		}
	}

	// Insert new memory.
	mem := &db.Memory{
		Service:     pm.Service,
		Category:    pm.Category,
		Observation: pm.Observation,
		Confidence:  0.7,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
		SessionID:   &sessionID,
		Tier:        tier,
	}
	if _, err := m.db.InsertMemory(mem); err != nil {
		fmt.Fprintf(os.Stderr, "insert memory: %v\n", err)
	}
}

// insertCooldown records a parsed cooldown marker as a CooldownAction in the database.
func (m *Manager) insertCooldown(sessionID int64, tier int, pc parsedCooldown) {
	now := time.Now().UTC().Format(time.RFC3339)
	var errMsg *string
	if !pc.Success {
		msg := pc.Message
		errMsg = &msg
	}
	a := &db.CooldownAction{
		Service:    pc.Service,
		ActionType: pc.ActionType,
		Timestamp:  now,
		Success:    pc.Success,
		Tier:       tier,
		Error:      errMsg,
		SessionID:  &sessionID,
	}
	if _, err := m.db.InsertCooldownAction(a); err != nil {
		fmt.Fprintf(os.Stderr, "insert cooldown action: %v\n", err)
	}
}

// Governing: SPEC-0015 REQ "Token Budget Enforcement" (2000-token default, chars/4 estimation)
// Governing: SPEC-0015 REQ "Memory Context Format" (grouped by service, category tag, confidence score)
// buildMemoryContext queries active memories and formats them as a structured
// markdown block for injection into the system prompt. It respects the
// configured MemoryBudget (estimated as characters / 4).
func (m *Manager) buildMemoryContext() string {
	budget := m.cfg.MemoryBudget
	if budget <= 0 {
		return ""
	}

	memories, err := m.db.GetActiveMemories(200)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get active memories: %v\n", err)
		return ""
	}
	if len(memories) == 0 {
		return ""
	}

	// Group memories by service (nil service = "general").
	type memEntry struct {
		Category    string
		Observation string
		Confidence  float64
	}
	groups := make(map[string][]memEntry)
	var order []string
	for _, mem := range memories {
		key := "general"
		if mem.Service != nil {
			key = *mem.Service
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], memEntry{
			Category:    mem.Category,
			Observation: mem.Observation,
			Confidence:  mem.Confidence,
		})
	}

	var b strings.Builder
	budgetChars := budget * 4
	memCount := 0
	lastSvc := ""
	for _, svc := range order {
		entries := groups[svc]
		for _, e := range entries {
			line := fmt.Sprintf("- [%s] %s (confidence: %.1f)\n", e.Category, e.Observation, e.Confidence)
			header := ""
			if svc != lastSvc {
				header = fmt.Sprintf("\n### %s\n", svc)
				lastSvc = svc
			}
			candidate := header + line
			if b.Len()+len(candidate) > budgetChars {
				goto done
			}
			b.WriteString(candidate)
			memCount++
		}
	}
done:

	if memCount == 0 {
		return ""
	}

	// Estimate tokens for the header.
	header := fmt.Sprintf("## Operational Memory (%d memories, ~%d tokens)\n", memCount, b.Len()/4)

	return header + b.String()
}
