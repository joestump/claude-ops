package session

import (
	"strings"
	"testing"
)

func TestResolveCredential_Valid(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_PASS", "s3cret")

	got, err := ResolveCredential(2, "BROWSER_CRED_SONARR_PASS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "s3cret" {
		t.Errorf("expected s3cret, got %s", got)
	}
}

func TestResolveCredential_Tier1Denied(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_PASS", "s3cret")

	_, err := ResolveCredential(1, "BROWSER_CRED_SONARR_PASS")
	if err == nil {
		t.Fatal("expected error for tier 1")
	}
	if !strings.Contains(err.Error(), "Tier 2+") {
		t.Errorf("error should mention Tier 2+, got: %v", err)
	}
}

func TestResolveCredential_MissingEnvVar(t *testing.T) {
	// Ensure the var is not set.
	t.Setenv("BROWSER_CRED_MISSING_PASS", "")

	_, err := ResolveCredential(2, "BROWSER_CRED_MISSING_PASS")
	if err == nil {
		t.Fatal("expected error for missing credential")
	}
	if !strings.Contains(err.Error(), "credential not set") {
		t.Errorf("error should mention credential not set, got: %v", err)
	}
}

func TestResolveCredential_InvalidPrefix(t *testing.T) {
	t.Setenv("MY_SECRET", "value")

	_, err := ResolveCredential(2, "MY_SECRET")
	if err == nil {
		t.Fatal("expected error for invalid prefix")
	}
	if !strings.Contains(err.Error(), "BROWSER_CRED_") {
		t.Errorf("error should mention BROWSER_CRED_ prefix, got: %v", err)
	}
}

func TestBuildBrowserInitScript_WithOrigins(t *testing.T) {
	script := BuildBrowserInitScript("https://sonarr.stump.rocks")
	if script == "" {
		t.Fatal("expected non-empty script")
	}
	if !strings.Contains(script, "'https://sonarr.stump.rocks'") {
		t.Errorf("script should contain the quoted origin, got: %s", script)
	}
	if !strings.Contains(script, "Navigation Blocked") {
		t.Errorf("script should contain blocked message, got: %s", script)
	}
	if !strings.Contains(script, "window.stop()") {
		t.Errorf("script should call window.stop(), got: %s", script)
	}
}

func TestBuildBrowserInitScript_Empty(t *testing.T) {
	script := BuildBrowserInitScript("")
	if script != "" {
		t.Errorf("expected empty script for empty origins, got: %s", script)
	}

	script = BuildBrowserInitScript("   ")
	if script != "" {
		t.Errorf("expected empty script for whitespace-only origins, got: %s", script)
	}
}

func TestBuildBrowserInitScript_MultipleOrigins(t *testing.T) {
	script := BuildBrowserInitScript("https://sonarr.stump.rocks, https://prowlarr.stump.rocks")
	if script == "" {
		t.Fatal("expected non-empty script")
	}
	if !strings.Contains(script, "'https://sonarr.stump.rocks'") {
		t.Errorf("script should contain first origin, got: %s", script)
	}
	if !strings.Contains(script, "'https://prowlarr.stump.rocks'") {
		t.Errorf("script should contain second origin, got: %s", script)
	}
	// Both should be in the same JS array.
	if !strings.Contains(script, "['https://sonarr.stump.rocks', 'https://prowlarr.stump.rocks']") {
		t.Errorf("script should contain JS array with both origins, got: %s", script)
	}
}
