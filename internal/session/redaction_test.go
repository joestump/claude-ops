package session

import (
	"strings"
	"testing"
)

func TestRedactionFilter_RawCredential(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_PASS", "s3cretP@ss")

	rf := NewRedactionFilter()
	input := `{"result": "logged in with s3cretP@ss successfully"}`
	got := rf.Redact(input)

	if strings.Contains(got, "s3cretP@ss") {
		t.Errorf("raw credential should be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:BROWSER_CRED_SONARR_PASS]") {
		t.Errorf("expected redaction placeholder, got: %s", got)
	}
}

func TestRedactionFilter_URLEncodedCredential(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_PASS", "p@ssw0rd")

	rf := NewRedactionFilter()
	// URL-encoded form of p@ssw0rd is p%40ssw0rd
	input := `{"url": "https://sonarr.example.com/login?pass=p%40ssw0rd"}`
	got := rf.Redact(input)

	if strings.Contains(got, "p%40ssw0rd") {
		t.Errorf("URL-encoded credential should be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:BROWSER_CRED_SONARR_PASS:urlencoded]") {
		t.Errorf("expected urlencoded redaction placeholder, got: %s", got)
	}
}

func TestRedactionFilter_ShortCredentialWarning(t *testing.T) {
	t.Setenv("BROWSER_CRED_TEST_PIN", "123")

	rf := NewRedactionFilter()
	input := "pin is 123 ok"
	got := rf.Redact(input)

	if strings.Contains(got, " 123 ") {
		t.Errorf("short credential should still be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:BROWSER_CRED_TEST_PIN]") {
		t.Errorf("expected redaction placeholder for short credential, got: %s", got)
	}
}

func TestRedactionFilter_NoCredentials(t *testing.T) {
	// No BROWSER_CRED_* env vars set (t.Setenv not called).
	rf := NewRedactionFilter()
	input := "nothing to redact here"
	got := rf.Redact(input)

	if got != input {
		t.Errorf("no-op expected, got: %s", got)
	}
}

func TestRedactionFilter_MultipleCredentials(t *testing.T) {
	t.Setenv("BROWSER_CRED_SONARR_USER", "admin")
	t.Setenv("BROWSER_CRED_SONARR_PASS", "hunter2")

	rf := NewRedactionFilter()
	input := "user=admin pass=hunter2 done"
	got := rf.Redact(input)

	if strings.Contains(got, "admin") && !strings.Contains(got, "[REDACTED:BROWSER_CRED_SONARR_USER]") {
		t.Errorf("username should be redacted, got: %s", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("password should be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:BROWSER_CRED_SONARR_USER]") {
		t.Errorf("expected user placeholder, got: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:BROWSER_CRED_SONARR_PASS]") {
		t.Errorf("expected pass placeholder, got: %s", got)
	}
}
