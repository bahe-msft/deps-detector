// Package analyzer defines agent interfaces and implementations for
// LLM-based supply chain risk analysis.
//
// The analysis pipeline uses multiple specialized agents:
//   - Sub-task agents (Agent) each analyze a specific data source
//     (release notes, commits, diffs) and produce free-form text analysis.
//   - A summarizer agent (Summarizer) consolidates all sub-agent analyses
//     into a single structured risk assessment.
package analyzer

import (
	"context"

	"deps-detector/internal/model"
)

// SourceAnalysis holds the free-form text output from a sub-task agent.
type SourceAnalysis struct {
	Source   string `json:"source"`   // name of the source that was analyzed
	Analysis string `json:"analysis"` // free-form text analysis
}

// Agent analyzes change reports from a single info source and returns
// a free-form text analysis. Different implementations specialize in
// different source types (release notes, commits, diffs).
type Agent interface {
	// Name returns a human-readable identifier for this agent.
	Name() string
	// Analyze examines the given change reports and returns a free-form
	// text analysis of supply chain risk signals found in the data.
	Analyze(ctx context.Context, vr model.VersionRange, reports []model.ChangeReport) (string, error)
}

// Summarizer consolidates analyses from multiple sub-task agents into
// a single structured risk assessment.
type Summarizer interface {
	Summarize(ctx context.Context, vr model.VersionRange, analyses []SourceAnalysis) (*model.AnalysisResult, error)
}
