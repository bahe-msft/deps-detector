package analyzer

import (
	"context"
	_ "embed"

	copilot "github.com/github/copilot-sdk/go"

	"deps-detector/internal/model"
)

//go:embed assets/prompt_release_notes.txt
var promptReleaseNotes string

// ReleaseNotesAgent analyzes GitHub release notes for supply chain risk signals.
type ReleaseNotesAgent struct {
	client *copilot.Client
	model  string
}

func NewReleaseNotesAgent(client *copilot.Client, model string) *ReleaseNotesAgent {
	return &ReleaseNotesAgent{client: client, model: model}
}

func (a *ReleaseNotesAgent) Name() string { return "release_notes" }

func (a *ReleaseNotesAgent) Analyze(ctx context.Context, vr model.VersionRange, reports []model.ChangeReport) (string, error) {
	return sendPrompt(ctx, a.client, a.model, promptReleaseNotes, formatReports(vr, reports))
}
