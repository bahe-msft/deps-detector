package analyzer

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/anomalyco/deps-check/internal/model"
)

//go:embed assets/prompt_summarizer.txt
var promptSummarizer string

// SummarizerAgent consolidates free-form analyses from multiple sub-task
// agents into a single structured risk assessment.
type SummarizerAgent struct {
	client *copilot.Client
	model  string
}

func NewSummarizerAgent(client *copilot.Client, model string) *SummarizerAgent {
	return &SummarizerAgent{client: client, model: model}
}

func (a *SummarizerAgent) Summarize(ctx context.Context, vr model.VersionRange, analyses []SourceAnalysis) (*model.AnalysisResult, error) {
	var userMsg strings.Builder
	fmt.Fprintf(&userMsg, "## Dependency Upgrade: %s\n\n", vr)
	fmt.Fprintf(&userMsg, "Below are analyses from %d specialized security agents:\n\n", len(analyses))

	for _, sa := range analyses {
		fmt.Fprintf(&userMsg, "### Analysis from: %s\n\n", sa.Source)
		fmt.Fprintf(&userMsg, "%s\n\n---\n\n", sa.Analysis)
	}

	raw, err := sendPrompt(ctx, a.client, a.model, promptSummarizer, userMsg.String())
	if err != nil {
		return nil, fmt.Errorf("summarizer failed: %w", err)
	}

	return parseSummaryResponse(raw)
}
