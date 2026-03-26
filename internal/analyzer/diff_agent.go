package analyzer

import (
	"context"
	_ "embed"

	copilot "github.com/github/copilot-sdk/go"

	"deps-detector/internal/model"
)

//go:embed assets/prompt_diff.txt
var promptDiff string

// DiffAgent analyzes code diffs between two versions for supply chain attack indicators.
type DiffAgent struct {
	client *copilot.Client
	model  string
}

func NewDiffAgent(client *copilot.Client, model string) *DiffAgent {
	return &DiffAgent{client: client, model: model}
}

func (a *DiffAgent) Name() string { return "diff" }

func (a *DiffAgent) Analyze(ctx context.Context, vr model.VersionRange, reports []model.ChangeReport) (string, error) {
	return sendPrompt(ctx, a.client, a.model, promptDiff, formatReports(vr, reports))
}
