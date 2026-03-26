package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"

	"github.com/anomalyco/deps-check/internal/diffparse"
	"github.com/anomalyco/deps-check/internal/model"
)

// fromDiffFlags holds the flags for the from-diff command.
type fromDiffFlags struct {
	llmModel   string
	jsonOutput bool
}

var fdFlags fromDiffFlags

// fromDiffCmd represents the from-diff command.
var fromDiffCmd = &cobra.Command{
	Use:   "from-diff",
	Short: "Detect and verify dependency changes from a git diff",
	Long: `Parse a unified diff (typically piped from git diff) to automatically
detect dependency version changes in lock/manifest files, then run the
verification pipeline for each detected upgrade.

Supported dependency files:
  go.sum    — Go module checksums (extracts module, version, integrity hash)

The command reads the diff from stdin. Only version upgrades (where both
a removed and an added version are present for the same module) are
verified by default. Newly added or removed dependencies are listed but
not analyzed.`,
	Example: `  git diff | deps-detector from-diff
  git diff HEAD~1 -- go.sum | deps-detector from-diff
  git diff main..feature -- go.sum go.mod | deps-detector from-diff --json
  deps-detector from-diff < my-changes.patch`,
	Args: cobra.NoArgs,
	RunE: runFromDiff,
}

func init() {
	fromDiffCmd.Flags().StringVar(&fdFlags.llmModel, "model", "gpt-5.4-mini", "LLM model to use")
	fromDiffCmd.Flags().BoolVar(&fdFlags.jsonOutput, "json", false, "output the full report as JSON")

	rootCmd.AddCommand(fromDiffCmd)
}

// batchReport is the JSON output structure for from-diff when --json is used.
type batchReport struct {
	DetectedChanges []depChangeSummary `json:"detected_changes"`
	Reports         []*verifyReport    `json:"reports"`
	Errors          []string           `json:"errors,omitempty"`
}

// depChangeSummary is a JSON-friendly representation of a detected dependency change.
type depChangeSummary struct {
	Language      string `json:"language"`
	Module        string `json:"module"`
	FromVersion   string `json:"from_version,omitempty"`
	ToVersion     string `json:"to_version,omitempty"`
	FromIntegrity string `json:"from_integrity,omitempty"`
	ToIntegrity   string `json:"to_integrity,omitempty"`
	IsUpgrade     bool   `json:"is_upgrade"`
}

func runFromDiff(cmd *cobra.Command, _ []string) error {
	// Progress messages go to stderr in JSON mode to keep stdout clean.
	var progress io.Writer = os.Stdout
	if fdFlags.jsonOutput {
		progress = os.Stderr
	}

	// Parse the diff from stdin.
	fmt.Fprintf(progress, "📋 Reading diff from stdin...\n")

	changes, err := diffparse.Extract(os.Stdin, diffparse.DefaultParsers())
	if err != nil {
		return fmt.Errorf("parsing diff: %w", err)
	}

	if len(changes) == 0 {
		fmt.Fprintf(progress, "\nNo dependency changes detected in the diff.\n")
		if fdFlags.jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(batchReport{})
		}
		return nil
	}

	// Categorize changes.
	var upgrades, additions, removals []diffparse.DepChange
	for _, c := range changes {
		switch {
		case c.IsUpgrade():
			upgrades = append(upgrades, c)
		case c.FromVersion == "":
			additions = append(additions, c)
		default:
			removals = append(removals, c)
		}
	}

	// Print summary of detected changes.
	fmt.Fprintf(progress, "\n📦 Detected %d dependency change(s):\n\n", len(changes))

	if len(upgrades) > 0 {
		fmt.Fprintf(progress, "  Upgrades (%d):\n", len(upgrades))
		for _, u := range upgrades {
			fmt.Fprintf(progress, "    %s:%s %s → %s\n", u.Language, u.Module, u.FromVersion, u.ToVersion)
		}
	}
	if len(additions) > 0 {
		fmt.Fprintf(progress, "  New dependencies (%d):\n", len(additions))
		for _, a := range additions {
			fmt.Fprintf(progress, "    + %s:%s %s\n", a.Language, a.Module, a.ToVersion)
		}
	}
	if len(removals) > 0 {
		fmt.Fprintf(progress, "  Removed dependencies (%d):\n", len(removals))
		for _, r := range removals {
			fmt.Fprintf(progress, "    - %s:%s %s\n", r.Language, r.Module, r.FromVersion)
		}
	}

	if len(upgrades) == 0 {
		fmt.Fprintf(progress, "\nNo version upgrades to verify.\n")
		if fdFlags.jsonOutput {
			report := batchReport{
				DetectedChanges: changesToSummaries(changes),
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		return nil
	}

	fmt.Fprintf(progress, "\n🔎 Verifying %d upgrade(s)...\n", len(upgrades))

	ctx := cmd.Context()

	// Start a shared Copilot client for all pipeline runs.
	client := copilot.NewClient(&copilot.ClientOptions{
		LogLevel: "debug",
	})
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("starting Copilot client: %w", err)
	}
	defer client.Stop()

	var reports []*verifyReport
	var errs []string

	for i, u := range upgrades {
		fmt.Fprintf(progress, "\n%s\n", strings.Repeat("━", 60))
		fmt.Fprintf(progress, "  [%d/%d] %s:%s %s → %s\n", i+1, len(upgrades), u.Language, u.Module, u.FromVersion, u.ToVersion)
		fmt.Fprintf(progress, "%s\n\n", strings.Repeat("━", 60))

		report, err := runPipeline(ctx, verifyParams{
			VersionRange: model.VersionRange{
				Language: u.Language,
				Dep:      u.Module,
				From:     u.FromVersion,
				To:       u.ToVersion,
			},
			FromIntegrity: u.FromIntegrity,
			ToIntegrity:   u.ToIntegrity,
			Model:         fdFlags.llmModel,
			Progress:      progress,
		}, client)
		if err != nil {
			errMsg := fmt.Sprintf("%s:%s %s→%s: %v", u.Language, u.Module, u.FromVersion, u.ToVersion, err)
			errs = append(errs, errMsg)
			fmt.Fprintf(progress, "\n  ⚠️  Failed: %v\n", err)
			continue
		}

		reports = append(reports, report)

		if !fdFlags.jsonOutput {
			printResult(report.VersionRange, report.Result)
		}
	}

	// Output.
	if fdFlags.jsonOutput {
		batch := batchReport{
			DetectedChanges: changesToSummaries(changes),
			Reports:         reports,
			Errors:          errs,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(batch); err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	} else {
		printBatchSummary(progress, upgrades, reports, errs)
	}

	return nil
}

func changesToSummaries(changes []diffparse.DepChange) []depChangeSummary {
	summaries := make([]depChangeSummary, len(changes))
	for i, c := range changes {
		summaries[i] = depChangeSummary{
			Language:      c.Language,
			Module:        c.Module,
			FromVersion:   c.FromVersion,
			ToVersion:     c.ToVersion,
			FromIntegrity: c.FromIntegrity,
			ToIntegrity:   c.ToIntegrity,
			IsUpgrade:     c.IsUpgrade(),
		}
	}
	return summaries
}

func printBatchSummary(w io.Writer, upgrades []diffparse.DepChange, reports []*verifyReport, errs []string) {
	fmt.Fprintf(w, "\n%s\n", strings.Repeat("═", 60))
	fmt.Fprintf(w, "  BATCH SUMMARY: %d upgrade(s) verified\n", len(reports))
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("═", 60))

	for _, r := range reports {
		icon := riskForLevel(r.Result.RiskLevel)
		fmt.Fprintf(w, "  %s %-5s  %s:%s %s→%s\n",
			icon, r.Result.RiskLevel,
			r.VersionRange.Language, r.VersionRange.Dep,
			r.VersionRange.From, r.VersionRange.To)
	}

	if len(errs) > 0 {
		fmt.Fprintf(w, "\n  ⚠️  %d upgrade(s) failed:\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(w, "    - %s\n", e)
		}
	}

	fmt.Fprintf(w, "\n%s\n", strings.Repeat("═", 60))
}
