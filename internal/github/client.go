// Package github provides a thin wrapper around the GitHub REST API.
// For the PoC we shell out to the `gh` CLI to avoid dealing with auth tokens directly.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/anomalyco/deps-check/internal/model"
)

// Client wraps GitHub API access via the gh CLI.
type Client struct{}

func NewClient() *Client {
	return &Client{}
}

// Release represents a GitHub release.
type Release struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// Commit represents a GitHub commit (abbreviated).
type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	HTMLURL string `json:"html_url"`
}

// CompareResult represents the output of the compare API.
type CompareResult struct {
	TotalCommits int      `json:"total_commits"`
	Files        []string `json:"files"`
	DiffURL      string   `json:"html_url"`
	Diff         string   `json:"diff"` // populated separately
}

// ghAPI runs `gh api <path>` and returns the raw JSON output.
func ghAPI(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"api", path}, args...)
	cmd := exec.CommandContext(ctx, "gh", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api %s: %w\n%s", path, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// GetRelease fetches a single release by tag.
func (c *Client) GetRelease(ctx context.Context, repo model.RepoRef, tag string) (*Release, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s", repo.Owner, repo.Repo, tag)
	data, err := ghAPI(ctx, path)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	return &r, nil
}

// ListCommitsBetween returns commits between two tags using the compare API.
func (c *Client) ListCommitsBetween(ctx context.Context, repo model.RepoRef, fromTag, toTag string) ([]Commit, error) {
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s", repo.Owner, repo.Repo, fromTag, toTag)
	data, err := ghAPI(ctx, path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
			} `json:"commit"`
			HTMLURL string `json:"html_url"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing commits: %w", err)
	}

	commits := make([]Commit, 0, len(raw.Commits))
	for _, c := range raw.Commits {
		// Take first line of commit message.
		msg := c.Commit.Message
		if idx := strings.Index(msg, "\n"); idx > 0 {
			msg = msg[:idx]
		}
		commits = append(commits, Commit{
			SHA:     c.SHA[:12],
			Message: msg,
			Author:  c.Commit.Author.Name,
			Date:    c.Commit.Author.Date,
			HTMLURL: c.HTMLURL,
		})
	}
	return commits, nil
}

// GetDiffSummary returns file-level diff info between two tags.
func (c *Client) GetDiffSummary(ctx context.Context, repo model.RepoRef, fromTag, toTag string) (*CompareResult, error) {
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s", repo.Owner, repo.Repo, fromTag, toTag)
	data, err := ghAPI(ctx, path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		TotalCommits int    `json:"total_commits"`
		HTMLURL      string `json:"html_url"`
		Files        []struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Patch     string `json:"patch"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing compare: %w", err)
	}

	result := &CompareResult{
		TotalCommits: raw.TotalCommits,
		DiffURL:      raw.HTMLURL,
	}

	var diffBuf strings.Builder
	for _, f := range raw.Files {
		result.Files = append(result.Files, fmt.Sprintf("%s (%s, +%d/-%d)", f.Filename, f.Status, f.Additions, f.Deletions))
		if f.Patch != "" {
			fmt.Fprintf(&diffBuf, "--- %s (%s) ---\n%s\n\n", f.Filename, f.Status, f.Patch)
		}
	}
	result.Diff = diffBuf.String()

	// Truncate diff if too large for LLM context.
	const maxDiffLen = 50000
	if len(result.Diff) > maxDiffLen {
		result.Diff = result.Diff[:maxDiffLen] + "\n... [truncated]"
	}

	return result, nil
}
