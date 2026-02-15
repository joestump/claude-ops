package session

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// RedactionFilter scans output for known credential values and replaces them
// with [REDACTED:VAR_NAME] placeholders. It builds a replacement dictionary
// from BROWSER_CRED_* environment variables at construction time.
type RedactionFilter struct {
	replacements map[string]string // credential value -> "[REDACTED:VAR_NAME]"
}

// NewRedactionFilter creates a RedactionFilter by scanning os.Environ() for
// BROWSER_CRED_* variables. Both raw and URL-encoded variants of each value
// are added to the replacement dictionary. Values shorter than 4 characters
// trigger a warning about false-positive risk.
func NewRedactionFilter() *RedactionFilter {
	rf := &RedactionFilter{replacements: make(map[string]string)}
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		name := parts[0]
		value := parts[1]
		if !strings.HasPrefix(name, "BROWSER_CRED_") {
			continue
		}
		if len(value) < 4 {
			fmt.Fprintf(os.Stderr, "warning: %s value is shorter than 4 characters; false-positive redaction risk\n", name)
		}
		rf.replacements[value] = "[REDACTED:" + name + "]"
		// Also add URL-encoded variant if it differs from the raw value.
		encoded := url.QueryEscape(value)
		if encoded != value {
			rf.replacements[encoded] = "[REDACTED:" + name + ":urlencoded]"
		}
	}
	return rf
}

// Redact replaces all known credential values in input with their
// [REDACTED:...] placeholders. If no BROWSER_CRED_* variables were found
// at construction time, this is a no-op passthrough.
func (rf *RedactionFilter) Redact(input string) string {
	if len(rf.replacements) == 0 {
		return input
	}
	result := input
	for value, placeholder := range rf.replacements {
		result = strings.ReplaceAll(result, value, placeholder)
	}
	return result
}
