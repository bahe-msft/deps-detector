package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"

	"github.com/anomalyco/deps-check/internal/analyzer"
	"github.com/anomalyco/deps-check/internal/github"
	"github.com/anomalyco/deps-check/internal/model"
	"github.com/anomalyco/deps-check/internal/resolve"
	"github.com/anomalyco/deps-check/internal/source"
)

// jsonReport is the top-level JSON output structure when --json is used.
type jsonReport struct {
	VersionRange    model.VersionRange        `json:"version_range"`
	Repo            model.RepoRef             `json:"repo"`
	RepoURL         string                    `json:"repo_url"`
	IntegrityChecks []model.IntegrityCheck    `json:"integrity_checks,omitempty"`
	Analyses        []analyzer.SourceAnalysis `json:"analyses"`
	Result          *model.AnalysisResult     `json:"result"`
}

// verifyFlags holds the flags for the verify command.
type verifyFlags struct {
	from          string
	to            string
	fromIntegrity string
	toIntegrity   string
	llmModel      string
	jsonOutput    bool
}

var vFlags verifyFlags

// verifyCmd represents the verify command.
var verifyCmd = &cobra.Command{
	Use:   "verify <language>:<module>",
	Short: "Verify a dependency upgrade for supply chain risks",
	Long: `Verify a dependency upgrade by analyzing changes between two versions.

Gathers intelligence from multiple sources (release notes, commits, diffs)
and uses LLM agents to assess supply-chain risk.

When integrity values are provided, they are compared against the remote
registry (e.g. sum.golang.org for Go). A mismatch indicates possible
retagging and is flagged as a critical finding. When omitted, a warning
is logged since retagging cannot be detected without known hashes.`,
	Example: `  deps-detector verify go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2
  deps-detector verify --json go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2 \
    --from-integrity h1:... --to-integrity h1:...`,
	Args: cobra.ExactArgs(1),
	RunE: runVerify,
}

func init() {
	verifyCmd.Flags().StringVar(&vFlags.from, "from", "", "source version (required)")
	verifyCmd.Flags().StringVar(&vFlags.to, "to", "", "target version (required)")
	verifyCmd.Flags().StringVar(&vFlags.fromIntegrity, "from-integrity", "", "expected integrity hash for --from version")
	verifyCmd.Flags().StringVar(&vFlags.toIntegrity, "to-integrity", "", "expected integrity hash for --to version")
	verifyCmd.Flags().StringVar(&vFlags.llmModel, "model", "gpt-5.4-mini", "LLM model to use")
	verifyCmd.Flags().BoolVar(&vFlags.jsonOutput, "json", false, "output the full report as JSON")

	_ = verifyCmd.MarkFlagRequired("from")
	_ = verifyCmd.MarkFlagRequired("to")

	rootCmd.AddCommand(verifyCmd)
}

// parsePkg splits "go:github.com/go-logr/logr" into language and module.
func parsePkg(pkg string) (language, module string, err error) {
	idx := strings.Index(pkg, ":")
	if idx < 1 {
		return "", "", fmt.Errorf("missing language prefix, expected lang:module, got %q", pkg)
	}
	return pkg[:idx], pkg[idx+1:], nil
}

func runVerify(cmd *cobra.Command, args []string) error {
	language, module, err := parsePkg(args[0])
	if err != nil {
		return err
	}

	vr := model.VersionRange{
		Language: language,
		Dep:      module,
		From:     vFlags.from,
		To:       vFlags.to,
	}

	// Progress messages go to stderr in JSON mode to keep stdout clean.
	var progress io.Writer = os.Stdout
	if vFlags.jsonOutput {
		progress = os.Stderr
	}

	ctx := context.Background()

	// Resolve package to source repository.
	registry := resolve.NewRegistry()
	registry.Register(&resolve.GoResolver{})

	fmt.Fprintf(progress, "🔍 Resolving %s:%s\n", vr.Language, vr.Dep)

	resolved, err := registry.Resolve(ctx, vr.Language, vr.Dep, vr.To)
	if err != nil {
		return fmt.Errorf("resolving package: %w", err)
	}

	repo := resolved.Repo
	fmt.Fprintf(progress, "  📦 Source: %s (%s)\n\n", resolved.RepoURL, repo)

	// Integrity checks.
	var integrityChecks []model.IntegrityCheck
	fmt.Fprintf(progress, "🔒 Checking integrity...\n")

	type integrityInput struct {
		version   string
		localHash string
		label     string
	}
	intInputs := []integrityInput{
		{version: vr.From, localHash: vFlags.fromIntegrity, label: "from"},
		{version: vr.To, localHash: vFlags.toIntegrity, label: "to"},
	}

	for _, ii := range intInputs {
		result, err := registry.ValidateIntegrity(ctx, vr.Language, vr.Dep, ii.version, ii.localHash)
		if err != nil {
			fmt.Fprintf(progress, "  ⚠️  Could not fetch remote integrity for %s (%s): %v\n", ii.version, ii.label, err)
			continue
		}

		check := model.IntegrityCheck{
			Version:   ii.version,
			Status:    result.Status,
			Local:     result.Local,
			Remote:    result.Remote.Hash,
			RemoteMod: result.Remote.ModHash,
		}

		switch result.Status {
		case model.IntegritySkipped:
			fmt.Fprintf(progress, "  ⚠️  No --%s-integrity provided for %s — cannot detect retagging\n", ii.label, ii.version)
		case model.IntegrityMatch:
			fmt.Fprintf(progress, "  ✅ %s (%s): integrity match\n", ii.version, ii.label)
		case model.IntegrityMismatch:
			fmt.Fprintf(progress, "  🔴 %s (%s): INTEGRITY MISMATCH — possible retagging!\n", ii.version, ii.label)
			fmt.Fprintf(progress, "     local:  %s\n", result.Local)
			fmt.Fprintf(progress, "     remote: %s\n", result.Remote.Hash)
		}

		integrityChecks = append(integrityChecks, check)
	}

	fmt.Fprintf(progress, "\n🔍 Analyzing %s (%s..%s)\n\n", vr.Dep, vr.From, vr.To)

	// Initialize info sources.
	gh := github.NewClient()
	sources := []source.InfoSource{
		&source.ReleaseNotes{GH: gh},
		&source.Commits{GH: gh},
		&source.Diff{GH: gh},
	}

	// Gather intelligence from all sources concurrently.
	type sourceResult struct {
		name    string
		reports []model.ChangeReport
	}
	var (
		mu         sync.Mutex
		results    []sourceResult
		gatherErrs []error
		wg         sync.WaitGroup
	)

	for _, src := range sources {
		wg.Add(1)
		go func(s source.InfoSource) {
			defer wg.Done()
			fmt.Fprintf(progress, "  ⏳ Gathering from %s...\n", s.Name())
			r, err := s.Gather(ctx, repo, vr)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				gatherErrs = append(gatherErrs, fmt.Errorf("%s: %w", s.Name(), err))
			} else {
				results = append(results, sourceResult{name: s.Name(), reports: r})
				fmt.Fprintf(progress, "  ✅ %s: %d report(s)\n", s.Name(), len(r))
			}
		}(src)
	}
	wg.Wait()

	for _, e := range gatherErrs {
		fmt.Fprintf(progress, "  ⚠️  %v\n", e)
	}

	if len(results) == 0 {
		return fmt.Errorf("no change information could be gathered")
	}

	// Start the shared Copilot client.
	client := copilot.NewClient(&copilot.ClientOptions{
		LogLevel: "debug",
	})
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("starting Copilot client: %w", err)
	}
	defer client.Stop()

	// Map source names to analysis agents.
	agentForSource := map[string]analyzer.Agent{
		"release_notes": analyzer.NewReleaseNotesAgent(client, vFlags.llmModel),
		"commits":       analyzer.NewCommitsAgent(client, vFlags.llmModel),
		"diff":          analyzer.NewDiffAgent(client, vFlags.llmModel),
	}

	// Phase 1: Run per-source analysis agents concurrently.
	fmt.Fprintf(progress, "\n🤖 Running analysis agents...\n\n")

	var (
		analyses  []analyzer.SourceAnalysis
		agentErrs []error
	)

	for _, sr := range results {
		agent, ok := agentForSource[sr.name]
		if !ok {
			// Fallback: use diff agent for unknown sources.
			agent = analyzer.NewDiffAgent(client, vFlags.llmModel)
		}

		wg.Add(1)
		go func(a analyzer.Agent, name string, reports []model.ChangeReport) {
			defer wg.Done()
			fmt.Fprintf(progress, "  🤖 [%s] Analyzing...\n", a.Name())

			text, err := a.Analyze(ctx, vr, reports)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				agentErrs = append(agentErrs, fmt.Errorf("agent %s: %w", a.Name(), err))
				fmt.Fprintf(progress, "  ⚠️  [%s] Failed: %v\n", a.Name(), err)
			} else {
				analyses = append(analyses, analyzer.SourceAnalysis{Source: name, Analysis: text})
				fmt.Fprintf(progress, "  ✅ [%s] Done\n", a.Name())
			}
		}(agent, sr.name, sr.reports)
	}
	wg.Wait()

	if len(analyses) == 0 {
		return fmt.Errorf("all analysis agents failed: %v", agentErrs)
	}

	// Phase 2: Summarizer agent consolidates all analyses.
	fmt.Fprintf(progress, "\n  🤖 [summarizer] Consolidating analyses...\n")
	summarizer := analyzer.NewSummarizerAgent(client, vFlags.llmModel)

	analysisResult, err := summarizer.Summarize(ctx, vr, analyses)
	if err != nil {
		return fmt.Errorf("during summarization: %w", err)
	}

	// Inject integrity mismatch findings into the result.
	for _, ic := range integrityChecks {
		if ic.Status == model.IntegrityMismatch {
			analysisResult.Findings = append(analysisResult.Findings, model.Finding{
				Title:       fmt.Sprintf("Integrity mismatch for %s", ic.Version),
				Description: fmt.Sprintf("The locally-known hash (%s) does not match the remote registry hash (%s). This may indicate the tag was deleted and re-pushed with different content (retagging attack).", ic.Local, ic.Remote),
				Severity:    model.RiskCritical,
				Source:      "integrity_check",
			})
			// Escalate overall risk level if a mismatch is found.
			if analysisResult.RiskLevel == model.RiskNone || analysisResult.RiskLevel == model.RiskLow || analysisResult.RiskLevel == model.RiskMedium {
				analysisResult.RiskLevel = model.RiskCritical
			}
		}
	}

	if vFlags.jsonOutput {
		report := jsonReport{
			VersionRange:    vr,
			Repo:            repo,
			RepoURL:         resolved.RepoURL,
			IntegrityChecks: integrityChecks,
			Analyses:        analyses,
			Result:          analysisResult,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	} else {
		printResult(vr, analysisResult)
	}

	return nil
}
