package gitprovider

import (
	"context"
	"fmt"
)

// Governing: SPEC-0018 REQ-13 "Environment Variable Configuration" — disabled state when config is missing
// DisabledProvider implements GitProvider but returns an error on every
// operation. It is used when a provider's required configuration (e.g.,
// tokens) is missing, so the system can start without failing while still
// providing clear error messages if the provider is invoked.
type DisabledProvider struct {
	name   string
	reason string
}

// NewDisabledProvider creates a provider that rejects all operations with a
// descriptive error including the provider name and reason.
func NewDisabledProvider(name, reason string) *DisabledProvider {
	return &DisabledProvider{name: name, reason: reason}
}

func (d *DisabledProvider) Name() string { return d.name }

func (d *DisabledProvider) err() error {
	return fmt.Errorf("git provider %q is disabled: %s", d.name, d.reason)
}

func (d *DisabledProvider) CreateBranch(_ context.Context, _ RepoRef, _, _ string) error {
	return d.err()
}

func (d *DisabledProvider) CommitFiles(_ context.Context, _ RepoRef, _, _ string, _ []FileChange) error {
	return d.err()
}

func (d *DisabledProvider) CreatePR(_ context.Context, _ RepoRef, _ PRRequest) (*PRResult, error) {
	return nil, d.err()
}

func (d *DisabledProvider) GetPRStatus(_ context.Context, _ RepoRef, _ int) (*PRStatus, error) {
	return nil, d.err()
}

func (d *DisabledProvider) ListOpenPRs(_ context.Context, _ RepoRef, _ PRFilter) ([]PRSummary, error) {
	return nil, d.err()
}
