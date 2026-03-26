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

func usage() {
	fmt.Fprintf(os.Stderr, `deps-detector — supply chain risk auditor for dependency upgrades

Usage:
  deps-detector [flags] <language>:<module> --from <version> --to <version>

Flags:
  --from <version>              Source version (required)
  --to <version>                Target version (required)
  --from-integrity <hash>       Expected integrity hash for --from version
  --to-integrity <hash>         Expected integrity hash for --to version
  --model <model>               LLM model to use (default: gpt-5.4-mini)
  --json                        Output the full report as JSON

When integrity values are provided, they are compared against the remote
registry (e.g. sum.golang.org for Go). A mismatch indicates possible
retagging and is flagged as a critical finding. When omitted, a warning
is logged since retagging cannot be detected without known hashes.

Examples:
  deps-detector go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2
  deps-detector --json go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2 \
    --from-integrity h1:... --to-integrity h1:...

Supported languages:
  go   — resolves via proxy.golang.org, integrity via sum.golang.org

Prerequisites:
  gh      — GitHub CLI, authenticated (used for fetching repo data)
  copilot — GitHub Copilot CLI (used for LLM analysis via the Copilot SDK)

Environment variables:
  COPILOT_CLI_PATH — Path to the Copilot CLI executable (optional)
`)
	os.Exit(1)
}

// cliArgs holds the parsed command-line arguments.
type cliArgs struct {
	pkg           string // "go:github.com/go-logr/logr"
	from          string
	to            string
	fromIntegrity string
	toIntegrity   string
	model         string
	jsonOutput    bool
}

func parseArgs(args []string) (*cliArgs, error) {
	c := &cliArgs{}
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			c.jsonOutput = true
		case a == "--from":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--from requires a value")
			}
			i++
			c.from = args[i]
		case strings.HasPrefix(a, "--from="):
			c.from = strings.TrimPrefix(a, "--from=")
		case a == "--to":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--to requires a value")
			}
			i++
			c.to = args[i]
		case strings.HasPrefix(a, "--to="):
			c.to = strings.TrimPrefix(a, "--to=")
		case a == "--from-integrity":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--from-integrity requires a value")
			}
			i++
			c.fromIntegrity = args[i]
		case strings.HasPrefix(a, "--from-integrity="):
			c.fromIntegrity = strings.TrimPrefix(a, "--from-integrity=")
		case a == "--to-integrity":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--to-integrity requires a value")
			}
			i++
			c.toIntegrity = args[i]
		case strings.HasPrefix(a, "--to-integrity="):
			c.toIntegrity = strings.TrimPrefix(a, "--to-integrity=")
		case a == "--model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--model requires a value")
			}
			i++
			c.model = args[i]
		case strings.HasPrefix(a, "--model="):
			c.model = strings.TrimPrefix(a, "--model=")
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("unknown flag: %s", a)
		default:
			positional = append(positional, a)
		}
	}

	if len(positional) != 1 {
		return nil, fmt.Errorf("expected exactly one positional argument (<language>:<module>), got %d", len(positional))
	}
	c.pkg = positional[0]

	if c.from == "" {
		return nil, fmt.Errorf("--from is required")
	}
	if c.to == "" {
		return nil, fmt.Errorf("--to is required")
	}

	return c, nil
}

// parsePkg splits "go:github.com/go-logr/logr" into language and module.
func parsePkg(pkg string) (language, module string, err error) {
	idx := strings.Index(pkg, ":")
	if idx < 1 {
		return "", "", fmt.Errorf("missing language prefix, expected lang:module, got %q", pkg)
	}
	return pkg[:idx], pkg[idx+1:], nil
}

func main() {
	cli, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		usage()
	}

	language, module, err := parsePkg(cli.pkg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		usage()
	}

	vr := model.VersionRange{
		Language: language,
		Dep:      module,
		From:     cli.from,
		To:       cli.to,
	}

	llmModel := cli.model
	if llmModel == "" {
		llmModel = "gpt-5.4-mini"
	}

	// Progress messages go to stderr in JSON mode to keep stdout clean.
	var progress io.Writer = os.Stdout
	if cli.jsonOutput {
		progress = os.Stderr
	}

	ctx := context.Background()

	// Resolve package to source repository.
	registry := resolve.NewRegistry()
	registry.Register(&resolve.GoResolver{})

	fmt.Fprintf(progress, "🔍 Resolving %s:%s\n", vr.Language, vr.Dep)

	resolved, err := registry.Resolve(ctx, vr.Language, vr.Dep, vr.To)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving package: %v\n", err)
		os.Exit(1)
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
		{version: vr.From, localHash: cli.fromIntegrity, label: "from"},
		{version: vr.To, localHash: cli.toIntegrity, label: "to"},
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
		fmt.Fprintf(os.Stderr, "\nNo change information could be gathered. Aborting.\n")
		os.Exit(1)
	}

	// Start the shared Copilot client.
	client := copilot.NewClient(&copilot.ClientOptions{
		LogLevel: "debug",
	})
	if err := client.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting Copilot client: %v\n", err)
		os.Exit(1)
	}
	defer client.Stop()

	// Map source names to analysis agents.
	agentForSource := map[string]analyzer.Agent{
		"release_notes": analyzer.NewReleaseNotesAgent(client, llmModel),
		"commits":       analyzer.NewCommitsAgent(client, llmModel),
		"diff":          analyzer.NewDiffAgent(client, llmModel),
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
			agent = analyzer.NewDiffAgent(client, llmModel)
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
		fmt.Fprintf(os.Stderr, "\nAll analysis agents failed: %v\n", agentErrs)
		os.Exit(1)
	}

	// Phase 2: Summarizer agent consolidates all analyses.
	fmt.Fprintf(progress, "\n  🤖 [summarizer] Consolidating analyses...\n")
	summarizer := analyzer.NewSummarizerAgent(client, llmModel)

	result, err := summarizer.Summarize(ctx, vr, analyses)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error during summarization: %v\n", err)
		os.Exit(1)
	}

	// Inject integrity mismatch findings into the result.
	for _, ic := range integrityChecks {
		if ic.Status == model.IntegrityMismatch {
			result.Findings = append(result.Findings, model.Finding{
				Title:       fmt.Sprintf("Integrity mismatch for %s", ic.Version),
				Description: fmt.Sprintf("The locally-known hash (%s) does not match the remote registry hash (%s). This may indicate the tag was deleted and re-pushed with different content (retagging attack).", ic.Local, ic.Remote),
				Severity:    model.RiskCritical,
				Source:      "integrity_check",
			})
			// Escalate overall risk level if a mismatch is found.
			if result.RiskLevel == model.RiskNone || result.RiskLevel == model.RiskLow || result.RiskLevel == model.RiskMedium {
				result.RiskLevel = model.RiskCritical
			}
		}
	}

	if cli.jsonOutput {
		report := jsonReport{
			VersionRange:    vr,
			Repo:            repo,
			RepoURL:         resolved.RepoURL,
			IntegrityChecks: integrityChecks,
			Analyses:        analyses,
			Result:          result,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
	} else {
		printResult(vr, result)
	}
}

func printResult(vr model.VersionRange, result *model.AnalysisResult) {
	fmt.Println()
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("  SUPPLY CHAIN RISK REPORT: %s\n", vr)
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println()

	icon := riskForLevel(result.RiskLevel)
	fmt.Printf("  Risk Level: %s %s\n\n", icon, result.RiskLevel)
	fmt.Printf("  %s\n\n", result.Summary)

	if len(result.Findings) > 0 {
		fmt.Println(strings.Repeat("─", 60))
		fmt.Printf("  FINDINGS (%d)\n", len(result.Findings))
		fmt.Println(strings.Repeat("─", 60))
		for i, f := range result.Findings {
			icon := riskForLevel(f.Severity)
			fmt.Printf("\n  %d. %s [%s] %s\n", i+1, icon, f.Severity, f.Title)
			fmt.Printf("     Source: %s\n", f.Source)
			for _, line := range wrapText(f.Description, 55) {
				fmt.Printf("     %s\n", line)
			}
		}
	} else {
		fmt.Println("  No suspicious findings detected.")
	}

	fmt.Println()
	fmt.Println(strings.Repeat("═", 60))
}

func riskForLevel(level model.RiskLevel) string {
	switch level {
	case model.RiskCritical:
		return "🔴"
	case model.RiskHigh:
		return "🟠"
	case model.RiskMedium:
		return "🟡"
	case model.RiskLow:
		return "🟢"
	case model.RiskNone:
		return "⚪"
	default:
		return "❓"
	}
}

func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	current := words[0]
	for _, w := range words[1:] {
		if len(current)+1+len(w) > width {
			lines = append(lines, current)
			current = w
		} else {
			current += " " + w
		}
	}
	lines = append(lines, current)
	return lines
}
