package source

import (
	"context"
	"fmt"
	"strings"

	"deps-detector/internal/github"
	"deps-detector/internal/model"
)

// Commits fetches the list of commits between two tags.
type Commits struct {
	GH *github.Client
}

func (s *Commits) Name() string { return "commits" }

func (s *Commits) Gather(ctx context.Context, repo model.RepoRef, vr model.VersionRange) ([]model.ChangeReport, error) {
	commits, err := s.GH.ListCommitsBetween(ctx, repo, vr.From, vr.To)
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}

	if len(commits) == 0 {
		return []model.ChangeReport{{
			Source: s.Name(),
			Title:  "No commits found",
			Body:   "The compare API returned no commits between these tags.",
		}}, nil
	}

	var body strings.Builder
	for _, c := range commits {
		fmt.Fprintf(&body, "- %s %s (%s, %s)\n", c.SHA, c.Message, c.Author, c.Date)
	}

	url := ""
	if len(commits) > 0 {
		// Link to the first commit as a reference.
		url = commits[0].HTMLURL
	}

	return []model.ChangeReport{{
		Source: s.Name(),
		Title:  fmt.Sprintf("%d commits between %s and %s", len(commits), vr.From, vr.To),
		Body:   body.String(),
		URL:    url,
	}}, nil
}
