package source

import (
	"context"
	"fmt"

	"deps-detector/internal/github"
	"deps-detector/internal/model"
)

// ReleaseNotes fetches GitHub release notes for both the from and to versions.
type ReleaseNotes struct {
	GH *github.Client
}

func (s *ReleaseNotes) Name() string { return "release_notes" }

func (s *ReleaseNotes) Gather(ctx context.Context, repo model.RepoRef, vr model.VersionRange) ([]model.ChangeReport, error) {
	var reports []model.ChangeReport

	// Try to get the release for the target version.
	rel, err := s.GH.GetRelease(ctx, repo, vr.To)
	if err != nil {
		// Not all projects create GitHub releases; this is non-fatal.
		return []model.ChangeReport{{
			Source: s.Name(),
			Title:  fmt.Sprintf("No release found for %s", vr.To),
			Body:   fmt.Sprintf("Could not fetch release notes: %v", err),
		}}, nil
	}

	reports = append(reports, model.ChangeReport{
		Source: s.Name(),
		Title:  fmt.Sprintf("Release %s: %s", rel.TagName, rel.Name),
		Body:   rel.Body,
		URL:    rel.HTMLURL,
	})

	return reports, nil
}
