package analyzer

import (
	"context"
	_ "embed"

	copilot "github.com/github/copilot-sdk/go"

	"deps-detector/internal/model"
)

//go:embed assets/prompt_commits.txt
var promptCommits string

// CommitsAgent analyzes commit history between two versions for supply chain risk signals.
type CommitsAgent struct {
	client *copilot.Client
	model  string
}

func NewCommitsAgent(client *copilot.Client, model string) *CommitsAgent {
	return &CommitsAgent{client: client, model: model}
}

func (a *CommitsAgent) Name() string { return "commits" }

func (a *CommitsAgent) Analyze(ctx context.Context, vr model.VersionRange, reports []model.ChangeReport) (string, error) {
	return sendPrompt(ctx, a.client, a.model, promptCommits, formatReports(vr, reports))
}
