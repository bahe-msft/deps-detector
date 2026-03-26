// Package model defines the core types shared across the application.
package model

import "fmt"

// VersionRange represents the from/to versions for a dependency upgrade.
type VersionRange struct {
	Dep  string // e.g. "golang.org/x/text" or "lodash"
	From string // e.g. "v1.2.3"
	To   string // e.g. "v1.2.4"
}

func (v VersionRange) String() string {
	return fmt.Sprintf("%s %s..%s", v.Dep, v.From, v.To)
}

// RepoRef is a resolved GitHub repository reference.
type RepoRef struct {
	Owner string
	Repo  string
}

func (r RepoRef) String() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Repo)
}

// ChangeReport is the raw intelligence gathered by a single InfoSource.
type ChangeReport struct {
	Source string // human-readable name of the source, e.g. "release_notes"
	Title  string // short summary title
	Body   string // full text content
	URL    string // optional link for reference
	Extras map[string]string
}

// RiskLevel represents the assessed supply-chain risk.
type RiskLevel string

const (
	RiskCritical RiskLevel = "CRITICAL"
	RiskHigh     RiskLevel = "HIGH"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskLow      RiskLevel = "LOW"
	RiskNone     RiskLevel = "NONE"
)

// AnalysisResult is the final output produced by the LLM analyzer.
type AnalysisResult struct {
	RiskLevel   RiskLevel
	Summary     string
	Findings    []Finding
	RawResponse string // keep the full LLM response for debugging
}

// Finding is a single suspicious item flagged by the analyzer.
type Finding struct {
	Title       string
	Description string
	Severity    RiskLevel
	Source      string // which ChangeReport source surfaced this
}
