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
	VersionRange model.VersionRange        `json:"version_range"`
	Repo         model.RepoRef             `json:"repo"`
	RepoURL      string                    `json:"repo_url"`
	Analyses     []analyzer.SourceAnalysis `json:"analyses"`
	Result       *model.AnalysisResult     `json:"result"`
}

func usage() {
	fmt.Fprintf(os.Stderr, `deps-detector — supply chain risk auditor for dependency upgrades

Usage:
  deps-detector [--json] <language>:<module>@<fromVersion>..<toVersion>

Flags:
  --json   Output the full report as JSON, including all agent analyses

Examples:
  deps-detector go:github.com/go-logr/logr@v1.4.1..v1.4.2
  deps-detector --json go:github.com/go-logr/logr@v1.4.1..v1.4.2

Supported languages:
  go   — resolves via proxy.golang.org

Prerequisites:
  gh      — GitHub CLI, authenticated (used for fetching repo data)
  copilot — GitHub Copilot CLI (used for LLM analysis via the Copilot SDK)

Environment variables:
  LLM_MODEL        — Model to use (default: gpt-4o)
  COPILOT_CLI_PATH — Path to the Copilot CLI executable (optional)
`)
	os.Exit(1)
}

func main() {
	args := os.Args[1:]
	jsonOutput := false

	// Parse flags.
	var positional []string
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		} else if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n\n", a)
			usage()
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) != 1 {
		usage()
	}

	vr, err := parseInput(positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		usage()
	}

	llmModel := os.Getenv("LLM_MODEL")
	if llmModel == "" {
		llmModel = "gpt-4o"
	}

	// Progress messages go to stderr in JSON mode to keep stdout clean.
	var progress io.Writer = os.Stdout
	if jsonOutput {
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
	fmt.Fprintf(progress, "🔍 Analyzing %s (%s..%s)\n\n", vr.Dep, vr.From, vr.To)

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

	if jsonOutput {
		report := jsonReport{
			VersionRange: vr,
			Repo:         repo,
			RepoURL:      resolved.RepoURL,
			Analyses:     analyses,
			Result:       result,
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

// parseInput parses the CLI argument in the format "lang:module@from..to".
// Example: "go:github.com/go-logr/logr@v1.4.1..v1.4.2"
func parseInput(arg string) (model.VersionRange, error) {
	// Split on first ":"  →  language, rest
	colonIdx := strings.Index(arg, ":")
	if colonIdx < 1 {
		return model.VersionRange{}, fmt.Errorf("missing language prefix, expected lang:module@from..to, got %q", arg)
	}
	language := arg[:colonIdx]
	rest := arg[colonIdx+1:] // "github.com/go-logr/logr@v1.4.1..v1.4.2"

	// Split on "@"  →  module, versions
	atIdx := strings.Index(rest, "@")
	if atIdx < 1 {
		return model.VersionRange{}, fmt.Errorf("missing version separator '@', expected lang:module@from..to, got %q", arg)
	}
	module := rest[:atIdx]
	versions := rest[atIdx+1:] // "v1.4.1..v1.4.2"

	// Split on ".."  →  from, to
	parts := strings.SplitN(versions, "..", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return model.VersionRange{}, fmt.Errorf("invalid version range %q, expected from..to", versions)
	}

	return model.VersionRange{
		Language: language,
		Dep:      module,
		From:     parts[0],
		To:       parts[1],
	}, nil
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
