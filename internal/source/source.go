// Package source defines the InfoSource interface and registry.
package source

import (
	"context"

	"github.com/anomalyco/deps-check/internal/model"
)

// InfoSource gathers change intelligence for a dependency version range.
// Each implementation represents a different strategy for discovering
// what changed between two versions (e.g. release notes, commits, diffs).
type InfoSource interface {
	// Name returns a human-readable identifier for this source.
	Name() string
	// Gather collects change information for the given version range.
	// It returns zero or more ChangeReports. Returning an error means
	// the source failed entirely; partial results should still be returned
	// with a nil error.
	Gather(ctx context.Context, repo model.RepoRef, vr model.VersionRange) ([]model.ChangeReport, error)
}
