// Package model defines the core types shared across the application.
package model

import "fmt"

// VersionRange represents the from/to versions for a dependency upgrade.
type VersionRange struct {
	Language string `json:"language"` // ecosystem, e.g. "go", "npm"
	Dep      string `json:"dep"`      // full module/package path, e.g. "github.com/go-logr/logr"
	From     string `json:"from"`     // e.g. "v1.2.3"
	To       string `json:"to"`       // e.g. "v1.2.4"
}

func (v VersionRange) String() string {
	return fmt.Sprintf("%s:%s %s..%s", v.Language, v.Dep, v.From, v.To)
}

// RepoRef is a resolved GitHub repository reference.
type RepoRef struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

func (r RepoRef) String() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Repo)
}

// ChangeReport is the raw intelligence gathered by a single InfoSource.
type ChangeReport struct {
	Source string            `json:"source"` // human-readable name of the source, e.g. "release_notes"
	Title  string            `json:"title"`  // short summary title
	Body   string            `json:"body"`   // full text content
	URL    string            `json:"url"`    // optional link for reference
	Extras map[string]string `json:"extras,omitempty"`
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
	RiskLevel   RiskLevel `json:"risk_level"`
	Summary     string    `json:"summary"`
	Findings    []Finding `json:"findings"`
	RawResponse string    `json:"raw_response"` // keep the full LLM response for debugging
}

// Finding is a single suspicious item flagged by the analyzer.
type Finding struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Severity    RiskLevel `json:"severity"`
	Source      string    `json:"source"` // which ChangeReport source surfaced this
}
