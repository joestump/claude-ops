package session

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// FormatStreamEvent – system events
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_SystemInit(t *testing.T) {
	raw := `{"type":"system","subtype":"init"}`
	got := FormatStreamEvent(raw)
	if got != "--- session started ---" {
		t.Errorf("system init: got %q, want %q", got, "--- session started ---")
	}
}

func TestFormatStreamEvent_SystemNonInit(t *testing.T) {
	raw := `{"type":"system","subtype":"other"}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("system non-init: got %q, want empty", got)
	}
}

func TestFormatStreamEvent_SystemNoSubtype(t *testing.T) {
	raw := `{"type":"system"}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("system no subtype: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – assistant text events
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_AssistantText(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, world!"}]}}`
	got := FormatStreamEvent(raw)
	if got != "Hello, world!" {
		t.Errorf("assistant text: got %q, want %q", got, "Hello, world!")
	}
}

func TestFormatStreamEvent_AssistantTextWhitespace(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"  \n  "}]}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("whitespace-only text: got %q, want empty", got)
	}
}

func TestFormatStreamEvent_AssistantTextTrimmed(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"  hello  "}]}}`
	got := FormatStreamEvent(raw)
	if got != "hello" {
		t.Errorf("trimmed text: got %q, want %q", got, "hello")
	}
}

func TestFormatStreamEvent_AssistantEmptyContent(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[]}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("empty content: got %q, want empty", got)
	}
}

func TestFormatStreamEvent_AssistantNoContent(t *testing.T) {
	raw := `{"type":"assistant","message":{}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("no content: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – assistant tool_use events
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_AssistantToolUse(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"docker ps"}}]}}`
	got := FormatStreamEvent(raw)
	want := `[tool] Bash: {"command":"docker ps"}`
	if got != want {
		t.Errorf("tool use:\n got  %q\n want %q", got, want)
	}
}

func TestFormatStreamEvent_AssistantToolUseTruncation(t *testing.T) {
	// Build input that exceeds 200 characters when serialized.
	longVal := strings.Repeat("x", 300)
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"` + longVal + `"}}]}}`
	got := FormatStreamEvent(raw)

	if !strings.HasPrefix(got, "[tool] Bash: ") {
		t.Fatalf("tool use prefix missing: %q", got)
	}
	inputPart := strings.TrimPrefix(got, "[tool] Bash: ")
	// The input should be truncated to 200 chars + "..."
	if len(inputPart) != 203 { // 200 + len("...")
		t.Errorf("tool input length = %d, want 203 (200 + ...)", len(inputPart))
	}
	if !strings.HasSuffix(inputPart, "...") {
		t.Error("truncated tool input should end with ...")
	}
}

func TestFormatStreamEvent_AssistantToolUseExactly200(t *testing.T) {
	// Input that serializes to exactly 200 bytes should NOT be truncated.
	// The raw JSON for input is captured as-is from the parent JSON parse.
	// We build an input object whose raw representation is exactly 200 bytes.
	// {"c":"<filler>"} => 8 overhead + filler length = 200 => filler = 192
	input := `{"c":"` + strings.Repeat("a", 192) + `"}`
	if len(input) != 200 {
		t.Fatalf("test setup: input length = %d, want 200", len(input))
	}
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":` + input + `}]}}`
	got := FormatStreamEvent(raw)

	inputPart := strings.TrimPrefix(got, "[tool] T: ")
	if strings.HasSuffix(inputPart, "...") {
		t.Errorf("exactly 200-byte input should not be truncated: %q", inputPart)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – mixed assistant events (text + tool_use)
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_AssistantMixedContent(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`
	got := FormatStreamEvent(raw)
	want := "Let me check.\n[tool] Bash: {\"command\":\"ls\"}"
	if got != want {
		t.Errorf("mixed content:\n got  %q\n want %q", got, want)
	}
}

func TestFormatStreamEvent_AssistantMultipleToolUse(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"Bash","input":{"command":"ls"}},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/etc/hosts"}}` +
		`]}}`
	got := FormatStreamEvent(raw)
	if !strings.Contains(got, "[tool] Bash:") || !strings.Contains(got, "[tool] Read:") {
		t.Errorf("multiple tools: got %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), got)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – user tool_result events
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_UserToolResult(t *testing.T) {
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":"some output"}]}}`
	got := FormatStreamEvent(raw)
	if got != "[result] some output" {
		t.Errorf("tool result: got %q, want %q", got, "[result] some output")
	}
}

func TestFormatStreamEvent_UserToolResultTruncation(t *testing.T) {
	longContent := strings.Repeat("z", 400)
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + longContent + `"}]}}`
	got := FormatStreamEvent(raw)

	resultPart := strings.TrimPrefix(got, "[result] ")
	if len(resultPart) != 303 { // 300 + "..."
		t.Errorf("truncated result length = %d, want 303", len(resultPart))
	}
	if !strings.HasSuffix(resultPart, "...") {
		t.Error("truncated result should end with ...")
	}
}

func TestFormatStreamEvent_UserToolResultExactly300(t *testing.T) {
	content := strings.Repeat("a", 300)
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + content + `"}]}}`
	got := FormatStreamEvent(raw)

	resultPart := strings.TrimPrefix(got, "[result] ")
	if strings.HasSuffix(resultPart, "...") {
		t.Error("exactly 300-char result should not be truncated")
	}
	if len(resultPart) != 300 {
		t.Errorf("result length = %d, want 300", len(resultPart))
	}
}

func TestFormatStreamEvent_UserToolResultArrayContent(t *testing.T) {
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}]}}`
	got := FormatStreamEvent(raw)
	if got != "[result] line one line two" {
		t.Errorf("array content: got %q, want %q", got, "[result] line one line two")
	}
}

func TestFormatStreamEvent_UserToolResultEmptyContent(t *testing.T) {
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":""}]}}`
	got := FormatStreamEvent(raw)
	if got != "[result] " {
		t.Errorf("empty tool result content: got %q, want %q", got, "[result] ")
	}
}

func TestFormatStreamEvent_UserToolResultNullContent(t *testing.T) {
	raw := `{"type":"user","message":{"content":[{"type":"tool_result"}]}}`
	got := FormatStreamEvent(raw)
	if got != "[result] " {
		t.Errorf("null tool result content: got %q, want %q", got, "[result] ")
	}
}

func TestFormatStreamEvent_UserNoToolResult(t *testing.T) {
	raw := `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("user non-tool_result: got %q, want empty", got)
	}
}

func TestFormatStreamEvent_UserEmptyContent(t *testing.T) {
	raw := `{"type":"user","message":{"content":[]}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("user empty content: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – result events
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_ResultSuccess(t *testing.T) {
	raw := `{"type":"result","result":"Final response.","is_error":false,"total_cost_usd":0.0123,"num_turns":5,"duration_ms":42000}`
	got := FormatStreamEvent(raw)
	want := "--- session complete (turns=5, cost=$0.0123, duration=42000ms) ---"
	if got != want {
		t.Errorf("result success:\n got  %q\n want %q", got, want)
	}
}

func TestFormatStreamEvent_ResultError(t *testing.T) {
	raw := `{"type":"result","result":"Something went wrong","is_error":true,"total_cost_usd":0.001,"num_turns":1,"duration_ms":500}`
	got := FormatStreamEvent(raw)
	want := "--- session error (turns=1, cost=$0.0010, duration=500ms) ---"
	if got != want {
		t.Errorf("result error:\n got  %q\n want %q", got, want)
	}
}

func TestFormatStreamEvent_ResultZeroCost(t *testing.T) {
	raw := `{"type":"result","result":"done","is_error":false,"total_cost_usd":0,"num_turns":0,"duration_ms":0}`
	got := FormatStreamEvent(raw)
	want := "--- session complete (turns=0, cost=$0.0000, duration=0ms) ---"
	if got != want {
		t.Errorf("zero cost result:\n got  %q\n want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – unknown event types
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_UnknownType(t *testing.T) {
	cases := []string{
		`{"type":"stream_event"}`,
		`{"type":"ping"}`,
		`{"type":"delta"}`,
		`{"type":""}`,
	}
	for _, raw := range cases {
		got := FormatStreamEvent(raw)
		if got != "" {
			t.Errorf("unknown type %s: got %q, want empty", raw, got)
		}
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – malformed JSON
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_MalformedJSON(t *testing.T) {
	cases := []string{
		"not json at all",
		"{truncated",
		"",
		"   ",
		`{"type": broken}`,
		`plain text output from claude`,
	}
	for _, raw := range cases {
		got := FormatStreamEvent(raw)
		if got != raw {
			t.Errorf("malformed JSON %q: got %q, want raw passthrough", raw, got)
		}
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – unicode content
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_UnicodeText(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello \u4e16\u754c! \ud83c\udf0d"}]}}`
	got := FormatStreamEvent(raw)
	if got == "" {
		t.Error("unicode text should not be empty")
	}
	if !strings.Contains(got, "Hello") {
		t.Errorf("unicode text: got %q, expected to contain 'Hello'", got)
	}
}

func TestFormatStreamEvent_UnicodeToolResult(t *testing.T) {
	unicodeContent := strings.Repeat("\u4e16", 200) // 200 CJK characters, each 3 bytes UTF-8
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + unicodeContent + `"}]}}`
	got := FormatStreamEvent(raw)
	// Should not panic and should produce some output.
	if !strings.HasPrefix(got, "[result] ") {
		t.Errorf("unicode tool result: got %q, expected [result] prefix", got)
	}
}

// ---------------------------------------------------------------------------
// FormatStreamEvent – edge cases
// ---------------------------------------------------------------------------

func TestFormatStreamEvent_UnknownContentBlockType(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"image","source":"data:image/png;base64,..."}]}}`
	got := FormatStreamEvent(raw)
	if got != "" {
		t.Errorf("unknown content block type: got %q, want empty", got)
	}
}

func TestFormatStreamEvent_ToolUseEmptyInput(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{}}]}}`
	got := FormatStreamEvent(raw)
	if got != "[tool] Bash: {}" {
		t.Errorf("empty tool input: got %q, want %q", got, "[tool] Bash: {}")
	}
}

func TestFormatStreamEvent_ToolUseNullInput(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":null}]}}`
	got := FormatStreamEvent(raw)
	if !strings.HasPrefix(got, "[tool] Bash:") {
		t.Errorf("null tool input: got %q", got)
	}
}

func TestFormatStreamEvent_NestedJSONInToolInput(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"/tmp/f","content":"{\"key\":\"value\"}"}}]}}`
	got := FormatStreamEvent(raw)
	if !strings.HasPrefix(got, "[tool] Write: ") {
		t.Errorf("nested JSON input: got %q", got)
	}
}

func TestFormatStreamEvent_MultipleToolResultsOnlyFirst(t *testing.T) {
	raw := `{"type":"user","message":{"content":[` +
		`{"type":"tool_result","content":"first"},` +
		`{"type":"tool_result","content":"second"}` +
		`]}}`
	got := FormatStreamEvent(raw)
	if got != "[result] first" {
		t.Errorf("multiple tool results: got %q, want %q", got, "[result] first")
	}
}

func TestFormatStreamEvent_LargeToolInput(t *testing.T) {
	// Very large input (10KB).
	largeVal := strings.Repeat("a", 10000)
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"data":"` + largeVal + `"}}]}}`
	got := FormatStreamEvent(raw)
	inputPart := strings.TrimPrefix(got, "[tool] Write: ")
	if len(inputPart) > 203 {
		t.Errorf("large tool input not truncated: length=%d", len(inputPart))
	}
}

func TestFormatStreamEvent_LargeToolResult(t *testing.T) {
	// Very large result (10KB).
	largeVal := strings.Repeat("b", 10000)
	raw := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + largeVal + `"}]}}`
	got := FormatStreamEvent(raw)
	resultPart := strings.TrimPrefix(got, "[result] ")
	if len(resultPart) > 303 {
		t.Errorf("large tool result not truncated: length=%d", len(resultPart))
	}
}

// ---------------------------------------------------------------------------
// Helper: extractToolResultContent
// ---------------------------------------------------------------------------

func TestExtractToolResultContent_String(t *testing.T) {
	got := extractToolResultContent([]byte(`"hello world"`))
	if got != "hello world" {
		t.Errorf("string content: got %q, want %q", got, "hello world")
	}
}

func TestExtractToolResultContent_Array(t *testing.T) {
	got := extractToolResultContent([]byte(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`))
	if got != "one two" {
		t.Errorf("array content: got %q, want %q", got, "one two")
	}
}

func TestExtractToolResultContent_EmptyArray(t *testing.T) {
	got := extractToolResultContent([]byte(`[]`))
	if got != "" {
		t.Errorf("empty array: got %q, want empty", got)
	}
}

func TestExtractToolResultContent_Nil(t *testing.T) {
	got := extractToolResultContent(nil)
	if got != "" {
		t.Errorf("nil content: got %q, want empty", got)
	}
}

func TestExtractToolResultContent_RawJSON(t *testing.T) {
	got := extractToolResultContent([]byte(`{"unexpected":"format"}`))
	if got != `{"unexpected":"format"}` {
		t.Errorf("raw JSON fallback: got %q", got)
	}
}

func TestExtractToolResultContent_ArrayWithEmptyText(t *testing.T) {
	got := extractToolResultContent([]byte(`[{"type":"text","text":""},{"type":"text","text":"content"}]`))
	if got != "content" {
		t.Errorf("array with empty text: got %q, want %q", got, "content")
	}
}

// ---------------------------------------------------------------------------
// Helper: truncateJSON
// ---------------------------------------------------------------------------

func TestTruncateJSON_Short(t *testing.T) {
	got := truncateJSON("short", 200)
	if got != "short" {
		t.Errorf("short: got %q", got)
	}
}

func TestTruncateJSON_Exact(t *testing.T) {
	s := strings.Repeat("a", 200)
	got := truncateJSON(s, 200)
	if got != s {
		t.Error("exact length should not be truncated")
	}
}

func TestTruncateJSON_Over(t *testing.T) {
	s := strings.Repeat("a", 201)
	got := truncateJSON(s, 200)
	if len(got) != 203 {
		t.Errorf("over length: got len=%d, want 203", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("should end with ...")
	}
}

func TestTruncateJSON_Whitespace(t *testing.T) {
	got := truncateJSON("  hello  ", 200)
	if got != "hello" {
		t.Errorf("whitespace: got %q, want %q", got, "hello")
	}
}

// ---------------------------------------------------------------------------
// Helper: truncateString
// ---------------------------------------------------------------------------

func TestTruncateString_Short(t *testing.T) {
	got := truncateString("hello world", 300)
	if got != "hello world" {
		t.Errorf("short: got %q", got)
	}
}

func TestTruncateString_Exact(t *testing.T) {
	s := strings.Repeat("a", 300)
	got := truncateString(s, 300)
	if got != s {
		t.Error("exact length should not be truncated")
	}
}

func TestTruncateString_Over(t *testing.T) {
	s := strings.Repeat("a", 301)
	got := truncateString(s, 300)
	if len(got) != 303 {
		t.Errorf("over: got len=%d, want 303", len(got))
	}
}

func TestTruncateString_CollapsesWhitespace(t *testing.T) {
	got := truncateString("hello   world\n\ttab", 300)
	if got != "hello world tab" {
		t.Errorf("whitespace collapse: got %q, want %q", got, "hello world tab")
	}
}

func TestTruncateString_WhitespaceCollapseBeforeTruncation(t *testing.T) {
	// String with lots of whitespace that collapses to under 300.
	s := strings.Repeat("a   ", 100) // 400 chars raw, but collapses to "a a a a..." = 199 chars
	got := truncateString(s, 300)
	if strings.HasSuffix(got, "...") {
		t.Error("collapsed whitespace string should not be truncated if under limit")
	}
}
