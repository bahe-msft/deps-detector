package source

import (
	"context"
	"fmt"
	"strings"

	"deps-detector/internal/github"
	"deps-detector/internal/model"
)

// Diff fetches the file-level diff between two tags, including patches.
type Diff struct {
	GH *github.Client
}

func (s *Diff) Name() string { return "diff" }

func (s *Diff) Gather(ctx context.Context, repo model.RepoRef, vr model.VersionRange) ([]model.ChangeReport, error) {
	result, err := s.GH.GetDiffSummary(ctx, repo, vr.From, vr.To)
	if err != nil {
		return nil, fmt.Errorf("getting diff: %w", err)
	}

	var reports []model.ChangeReport

	// File listing summary.
	reports = append(reports, model.ChangeReport{
		Source: s.Name(),
		Title:  fmt.Sprintf("%d files changed across %d commits", len(result.Files), result.TotalCommits),
		Body:   "Changed files:\n" + strings.Join(result.Files, "\n"),
		URL:    result.DiffURL,
	})

	// The actual patch content — this is the most useful signal for the LLM.
	if result.Diff != "" {
		reports = append(reports, model.ChangeReport{
			Source: s.Name(),
			Title:  "Code diff (patches)",
			Body:   result.Diff,
			URL:    result.DiffURL,
		})
	}

	return reports, nil
}
