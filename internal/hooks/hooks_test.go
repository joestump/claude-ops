package hooks

// Governing: ADR-0029 (hooks lifecycle guardrails), SPEC-0030 REQ-2, REQ-3
// Tests for Claude Code hook scripts in .claude/hooks/.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hooksDir returns the absolute path to the .claude/hooks/ directory.
func hooksDir(t *testing.T) string {
	t.Helper()
	// Walk up from the test file to find the repo root.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is internal/hooks/hooks_test.go, repo root is two levels up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, ".claude", "hooks")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("hooks directory not found at %s", dir)
	}
	return dir
}

// settingsPath returns the absolute path to .claude/settings.json.
func settingsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, ".claude", "settings.json")
}

// expectedHookScripts lists the hook scripts that must exist.
var expectedHookScripts = []string{
	"cooldown-check.sh",
	"event-emit.sh",
	"session-context.sh",
	"notify-apprise.sh",
}

// ---------------------------------------------------------------------------
// 1. Hook script existence and permissions
// ---------------------------------------------------------------------------

func TestHookScriptsExist(t *testing.T) {
	dir := hooksDir(t)
	for _, name := range expectedHookScripts {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected hook script %s does not exist", name)
			continue
		}
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		// Check executable bit.
		if info.Mode()&0111 == 0 {
			t.Errorf("hook script %s is not executable (mode %s)", name, info.Mode())
		}
	}
}

func TestHookScriptsHaveShebang(t *testing.T) {
	dir := hooksDir(t)
	for _, name := range expectedHookScripts {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if !bytes.HasPrefix(data, []byte("#!/")) {
			t.Errorf("hook script %s does not start with a shebang line", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. settings.json validity and hook structure
// ---------------------------------------------------------------------------

func TestSettingsJSONValid(t *testing.T) {
	path := settingsPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	hooks, ok := settings["hooks"]
	if !ok {
		t.Fatal("settings.json missing top-level 'hooks' key")
	}
	hooksMap, ok := hooks.(map[string]any)
	if !ok {
		t.Fatal("settings.json 'hooks' is not an object")
	}

	// Verify expected hook types exist.
	expectedTypes := []string{"PreToolUse", "PostToolUse", "Stop", "SessionStart", "Notification"}
	for _, ht := range expectedTypes {
		if _, exists := hooksMap[ht]; !exists {
			t.Errorf("settings.json missing hook type %q", ht)
		}
	}
}

func TestSettingsJSONHookReferences(t *testing.T) {
	path := settingsPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	// Verify that each command hook references a file that exists in the hooks dir.
	raw := string(data)
	dir := hooksDir(t)
	for _, name := range expectedHookScripts {
		if !strings.Contains(raw, name) {
			t.Errorf("settings.json does not reference hook script %s", name)
		}
		// Verify the referenced file actually exists.
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			t.Errorf("settings.json references %s but file does not exist", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. shellcheck validation
// ---------------------------------------------------------------------------

func TestShellcheck(t *testing.T) {
	shellcheckPath, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck not found, skipping lint test")
	}

	dir := hooksDir(t)
	for _, name := range expectedHookScripts {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			cmd := exec.Command(shellcheckPath, "-s", "bash", "-S", "warning", path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("shellcheck %s failed:\n%s", name, string(out))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. cooldown-check.sh behavioral tests
// ---------------------------------------------------------------------------

// runCooldownCheck executes cooldown-check.sh with the given JSON input and
// environment. Returns stdout, stderr, and exit code.
func runCooldownCheck(t *testing.T, input string, env map[string]string) (string, string, int) {
	t.Helper()
	dir := hooksDir(t)
	script := filepath.Join(dir, "cooldown-check.sh")

	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(input)

	// Build environment.
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("exec cooldown-check.sh: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestCooldownCheck_NonMatchingCommand(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	input := `{"tool_input": {"command": "echo hello"}}`
	_, _, exitCode := runCooldownCheck(t, input, nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for non-matching command, got %d", exitCode)
	}
}

func TestCooldownCheck_EmptyInput(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	_, _, exitCode := runCooldownCheck(t, "{}", nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for empty command, got %d", exitCode)
	}
}

func TestCooldownCheck_AllowWithinLimits(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir()
	cooldownFile := filepath.Join(stateDir, "cooldown.json")

	// One restart in the window — should be allowed (limit is 2).
	cooldownState := map[string]any{
		"services": map[string]any{
			"nginx": map[string]any{
				"restart_timestamps":      []string{"2099-01-01T00:00:00Z"},
				"redeployment_timestamps": []string{},
			},
		},
	}
	data, _ := json.Marshal(cooldownState)
	if err := os.WriteFile(cooldownFile, data, 0644); err != nil {
		t.Fatalf("write cooldown.json: %v", err)
	}

	input := `{"tool_input": {"command": "docker restart nginx"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, _, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0 (allow) with 1/2 restarts, got %d", exitCode)
	}
}

func TestCooldownCheck_DenyExceededRestarts(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir()
	cooldownFile := filepath.Join(stateDir, "cooldown.json")

	// Two restarts with future timestamps (always within window).
	cooldownState := map[string]any{
		"services": map[string]any{
			"nginx": map[string]any{
				"restart_timestamps":      []string{"2099-01-01T00:00:00Z", "2099-01-01T01:00:00Z"},
				"redeployment_timestamps": []string{},
			},
		},
	}
	data, _ := json.Marshal(cooldownState)
	if err := os.WriteFile(cooldownFile, data, 0644); err != nil {
		t.Fatalf("write cooldown.json: %v", err)
	}

	input := `{"tool_input": {"command": "docker restart nginx"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, stderr, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 2 {
		t.Errorf("expected exit 2 (deny) with 2/2 restarts, got %d (stderr: %s)", exitCode, stderr)
	}
	if !strings.Contains(stderr, "Cooldown limit exceeded") {
		t.Errorf("expected cooldown message in stderr, got %q", stderr)
	}
}

func TestCooldownCheck_DenyExceededRedeployments(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir()
	cooldownFile := filepath.Join(stateDir, "cooldown.json")

	// One redeployment with future timestamp (always within 24h window).
	cooldownState := map[string]any{
		"services": map[string]any{
			"jellyfin": map[string]any{
				"restart_timestamps":      []string{},
				"redeployment_timestamps": []string{"2099-01-01T00:00:00Z"},
			},
		},
	}
	data, _ := json.Marshal(cooldownState)
	if err := os.WriteFile(cooldownFile, data, 0644); err != nil {
		t.Fatalf("write cooldown.json: %v", err)
	}

	input := `{"tool_input": {"command": "ansible-playbook redeploy-jellyfin.yml"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, stderr, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 2 {
		t.Errorf("expected exit 2 (deny) with 1/1 redeployments, got %d (stderr: %s)", exitCode, stderr)
	}
	if !strings.Contains(stderr, "Cooldown limit exceeded") {
		t.Errorf("expected cooldown message in stderr, got %q", stderr)
	}
}

func TestCooldownCheck_NoCooldownFile(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir() // empty dir, no cooldown.json
	input := `{"tool_input": {"command": "docker restart nginx"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, _, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0 when cooldown.json missing, got %d", exitCode)
	}
}

func TestCooldownCheck_DockerComposeRestart(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir()
	cooldownFile := filepath.Join(stateDir, "cooldown.json")

	cooldownState := map[string]any{
		"services": map[string]any{
			"redis": map[string]any{
				"restart_timestamps":      []string{"2099-01-01T00:00:00Z", "2099-01-01T01:00:00Z"},
				"redeployment_timestamps": []string{},
			},
		},
	}
	data, _ := json.Marshal(cooldownState)
	if err := os.WriteFile(cooldownFile, data, 0644); err != nil {
		t.Fatalf("write cooldown.json: %v", err)
	}

	input := `{"tool_input": {"command": "docker compose restart redis"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, _, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 2 {
		t.Errorf("expected exit 2 (deny) for docker compose restart, got %d", exitCode)
	}
}

func TestCooldownCheck_HelmUpgrade(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir()
	cooldownFile := filepath.Join(stateDir, "cooldown.json")

	cooldownState := map[string]any{
		"services": map[string]any{
			"grafana": map[string]any{
				"restart_timestamps":      []string{},
				"redeployment_timestamps": []string{"2099-01-01T00:00:00Z"},
			},
		},
	}
	data, _ := json.Marshal(cooldownState)
	if err := os.WriteFile(cooldownFile, data, 0644); err != nil {
		t.Fatalf("write cooldown.json: %v", err)
	}

	input := `{"tool_input": {"command": "helm upgrade grafana stable/grafana"}}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}
	_, _, exitCode := runCooldownCheck(t, input, env)
	if exitCode != 2 {
		t.Errorf("expected exit 2 (deny) for helm upgrade, got %d", exitCode)
	}
}

// ---------------------------------------------------------------------------
// 5. event-emit.sh behavioral tests
// ---------------------------------------------------------------------------

// runEventEmit executes event-emit.sh with the given JSON input and environment.
func runEventEmit(t *testing.T, input string, env map[string]string) (string, string, int) {
	t.Helper()
	dir := hooksDir(t)
	script := filepath.Join(dir, "event-emit.sh")

	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(input)

	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("exec event-emit.sh: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupEventDB(t *testing.T) (string, string) {
	t.Helper()
	sqlite3Path, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "claudeops.db")

	// Create the events table matching the schema used by the application.
	createSQL := `CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		level TEXT NOT NULL,
		service TEXT,
		message TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`

	cmd := exec.Command(sqlite3Path, dbPath, createSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create events table: %v\n%s", err, out)
	}

	return stateDir, dbPath
}

func queryEventCount(t *testing.T, dbPath string) int {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, "SELECT COUNT(*) FROM events;")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query event count: %v", err)
	}
	count := 0
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

func TestEventEmit_NonMatchingCommand(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir, dbPath := setupEventDB(t)
	input := `{"tool_input": {"command": "echo hello"}, "session_id": "test-123"}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	_, _, exitCode := runEventEmit(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for non-matching command, got %d", exitCode)
	}

	count := queryEventCount(t, dbPath)
	if count != 0 {
		t.Errorf("expected 0 events for non-matching command, got %d", count)
	}
}

func TestEventEmit_DockerRestart(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir, dbPath := setupEventDB(t)
	input := `{"tool_input": {"command": "docker restart nginx"}, "session_id": "test-456"}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	_, _, exitCode := runEventEmit(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}

	count := queryEventCount(t, dbPath)
	if count != 1 {
		t.Errorf("expected 1 event after docker restart, got %d", count)
	}

	// Verify the event content.
	cmd := exec.Command("sqlite3", dbPath, "SELECT level, service, message FROM events LIMIT 1;")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	row := strings.TrimSpace(string(out))
	if !strings.Contains(row, "warning") {
		t.Errorf("expected level=warning, got %q", row)
	}
	if !strings.Contains(row, "nginx") {
		t.Errorf("expected service=nginx, got %q", row)
	}
	if !strings.Contains(row, "Container restarted") {
		t.Errorf("expected message to contain 'Container restarted', got %q", row)
	}
}

func TestEventEmit_AnsiblePlaybook(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir, dbPath := setupEventDB(t)
	input := `{"tool_input": {"command": "ansible-playbook redeploy-jellyfin.yml"}, "session_id": "test-789"}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	_, _, exitCode := runEventEmit(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}

	count := queryEventCount(t, dbPath)
	if count != 1 {
		t.Errorf("expected 1 event after ansible-playbook, got %d", count)
	}

	cmd := exec.Command("sqlite3", dbPath, "SELECT level, service, message FROM events LIMIT 1;")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	row := strings.TrimSpace(string(out))
	if !strings.Contains(row, "jellyfin") {
		t.Errorf("expected service=jellyfin, got %q", row)
	}
	if !strings.Contains(row, "Ansible playbook executed") {
		t.Errorf("expected 'Ansible playbook executed' in message, got %q", row)
	}
}

func TestEventEmit_DockerComposeDown(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir, dbPath := setupEventDB(t)
	input := `{"tool_input": {"command": "docker compose down myservice"}, "session_id": "test-101"}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	_, _, exitCode := runEventEmit(t, input, env)
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}

	count := queryEventCount(t, dbPath)
	if count != 1 {
		t.Errorf("expected 1 event after docker compose down, got %d", count)
	}

	cmd := exec.Command("sqlite3", dbPath, "SELECT level FROM events LIMIT 1;")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	level := strings.TrimSpace(string(out))
	if level != "critical" {
		t.Errorf("expected level=critical for docker compose down, got %q", level)
	}
}

func TestEventEmit_NoDB(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	stateDir := t.TempDir() // No database file.
	input := `{"tool_input": {"command": "docker restart nginx"}, "session_id": "test-nodb"}`
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	_, _, exitCode := runEventEmit(t, input, env)
	// Should still exit 0 (fail-open).
	if exitCode != 0 {
		t.Errorf("expected exit 0 when DB missing, got %d", exitCode)
	}
}

func TestEventEmit_EmptyInput(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}

	_, _, exitCode := runEventEmit(t, "{}", nil)
	if exitCode != 0 {
		t.Errorf("expected exit 0 for empty input, got %d", exitCode)
	}
}

func TestEventEmit_MultipleEvents(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	stateDir, dbPath := setupEventDB(t)
	env := map[string]string{"CLAUDEOPS_STATE_DIR": stateDir}

	commands := []string{
		`{"tool_input": {"command": "docker restart nginx"}, "session_id": "s1"}`,
		`{"tool_input": {"command": "docker restart redis"}, "session_id": "s1"}`,
		`{"tool_input": {"command": "docker compose up -d postgres"}, "session_id": "s1"}`,
	}

	for _, input := range commands {
		_, _, exitCode := runEventEmit(t, input, env)
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
	}

	count := queryEventCount(t, dbPath)
	if count != 3 {
		t.Errorf("expected 3 events after 3 commands, got %d", count)
	}
}
