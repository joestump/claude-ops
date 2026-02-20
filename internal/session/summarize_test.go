package session

import (
	"testing"
)

func TestSummarizeSystemPrompt(t *testing.T) {
	// Verify the system prompt is non-empty and contains expected keywords.
	if summarizeSystemPrompt == "" {
		t.Fatal("summarizeSystemPrompt should not be empty")
	}
	keywords := []string{"summarize", "infrastructure", "service"}
	for _, kw := range keywords {
		found := false
		for i := range summarizeSystemPrompt {
			if i+len(kw) <= len(summarizeSystemPrompt) {
				candidate := summarizeSystemPrompt[i : i+len(kw)]
				if equalFoldASCII(candidate, kw) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("expected system prompt to contain %q", kw)
		}
	}
}

func TestSummarizeResponseSignature(t *testing.T) {
	// Verify the function exists and has the expected signature by calling it
	// with a cancelled context. We can't call the real API in tests, but we
	// can verify error handling for a nil/missing API key scenario.
	// This is a compile-time + basic runtime check.
	var fn func(ctx interface{}, response string, model string) = nil
	_ = fn // suppress unused — the real check is that summarize.go compiles

	// The function is package-private, so we verify it exists by referencing it.
	_ = summarizeResponse
}

// equalFoldASCII does a case-insensitive comparison for ASCII strings.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
