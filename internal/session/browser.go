package session

import (
	"fmt"
	"os"
	"strings"
)

// ResolveCredential looks up a BROWSER_CRED_* environment variable by name,
// enforcing Tier 2+ permissions and the BROWSER_CRED_ prefix convention.
func ResolveCredential(tier int, envKey string) (string, error) {
	if tier < 2 {
		return "", fmt.Errorf("browser credential injection requires Tier 2+")
	}
	if !strings.HasPrefix(envKey, "BROWSER_CRED_") {
		return "", fmt.Errorf("invalid credential key: must start with BROWSER_CRED_")
	}
	value := os.Getenv(envKey)
	if value == "" {
		return "", fmt.Errorf("credential not set: %s", envKey)
	}
	return value, nil
}

// BuildBrowserInitScript generates a JavaScript IIFE that restricts browser
// navigation to the origins listed in the comma-separated allowedOrigins
// string. If allowedOrigins is empty, it returns an empty string (the caller
// should skip browser automation).
func BuildBrowserInitScript(allowedOrigins string) string {
	allowedOrigins = strings.TrimSpace(allowedOrigins)
	if allowedOrigins == "" {
		return ""
	}

	parts := strings.Split(allowedOrigins, ",")
	var quoted []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			quoted = append(quoted, "'"+p+"'")
		}
	}
	if len(quoted) == 0 {
		return ""
	}

	jsArray := "[" + strings.Join(quoted, ", ") + "]"

	return `(function() {
  var allowed = ` + jsArray + `;
  var origin = window.location.origin;
  if (allowed.indexOf(origin) === -1) {
    document.documentElement.innerHTML =
      '<h1>Navigation Blocked</h1>' +
      '<p>Origin not in BROWSER_ALLOWED_ORIGINS: ' + origin + '</p>';
    window.stop();
  }
})();`
}
