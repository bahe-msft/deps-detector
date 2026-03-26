package main

import (
	"context"
	"fmt"
	"io"
	"sync"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/anomalyco/deps-check/internal/analyzer"
	"github.com/anomalyco/deps-check/internal/github"
	"github.com/anomalyco/deps-check/internal/model"
	"github.com/anomalyco/deps-check/internal/resolve"
	"github.com/anomalyco/deps-check/internal/source"
)

// verifyParams holds all parameters needed by the verification pipeline.
type verifyParams struct {
	VersionRange  model.VersionRange
	FromIntegrity string
	ToIntegrity   string
	Model         string
	Progress      io.Writer // progress messages destination
}

// verifyReport is the full result of running the verification pipeline.
type verifyReport struct {
	VersionRange    model.VersionRange
	Repo            model.RepoRef
	RepoURL         string
	IntegrityChecks []model.IntegrityCheck
	Analyses        []analyzer.SourceAnalysis
	Result          *model.AnalysisResult
}

// runPipeline executes the full verification pipeline for a single dependency
// upgrade. It resolves the package, checks integrity, gathers intelligence,
// runs analysis agents, and produces a consolidated report.
//
// The copilotClient parameter allows sharing a single Copilot client across
// multiple pipeline runs. If nil, a new client is created and stopped when done.
func runPipeline(ctx context.Context, params verifyParams, copilotClient *copilot.Client) (*verifyReport, error) {
	vr := params.VersionRange
	progress := params.Progress

	// Resolve package to source repository.
	registry := resolve.NewRegistry()
	registry.Register(&resolve.GoResolver{})

	fmt.Fprintf(progress, "🔍 Resolving %s:%s\n", vr.Language, vr.Dep)

	resolved, err := registry.Resolve(ctx, vr.Language, vr.Dep, vr.To)
	if err != nil {
		return nil, fmt.Errorf("resolving package: %w", err)
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
		{version: vr.From, localHash: params.FromIntegrity, label: "from"},
		{version: vr.To, localHash: params.ToIntegrity, label: "to"},
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
		return nil, fmt.Errorf("no change information could be gathered")
	}

	// Start or reuse Copilot client.
	ownClient := false
	client := copilotClient
	if client == nil {
		client = copilot.NewClient(&copilot.ClientOptions{
			LogLevel: "debug",
		})
		if err := client.Start(ctx); err != nil {
			return nil, fmt.Errorf("starting Copilot client: %w", err)
		}
		ownClient = true
	}
	defer func() {
		if ownClient {
			client.Stop()
		}
	}()

	llmModel := params.Model
	if llmModel == "" {
		llmModel = "gpt-5.4-mini"
	}

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
		return nil, fmt.Errorf("all analysis agents failed: %v", agentErrs)
	}

	// Phase 2: Summarizer agent consolidates all analyses.
	fmt.Fprintf(progress, "\n  🤖 [summarizer] Consolidating analyses...\n")
	summarizer := analyzer.NewSummarizerAgent(client, llmModel)

	analysisResult, err := summarizer.Summarize(ctx, vr, analyses)
	if err != nil {
		return nil, fmt.Errorf("during summarization: %w", err)
	}

	// Inject integrity mismatch findings.
	for _, ic := range integrityChecks {
		if ic.Status == model.IntegrityMismatch {
			analysisResult.Findings = append(analysisResult.Findings, model.Finding{
				Title:       fmt.Sprintf("Integrity mismatch for %s", ic.Version),
				Description: fmt.Sprintf("The locally-known hash (%s) does not match the remote registry hash (%s). This may indicate the tag was deleted and re-pushed with different content (retagging attack).", ic.Local, ic.Remote),
				Severity:    model.RiskCritical,
				Source:      "integrity_check",
			})
			if analysisResult.RiskLevel == model.RiskNone || analysisResult.RiskLevel == model.RiskLow || analysisResult.RiskLevel == model.RiskMedium {
				analysisResult.RiskLevel = model.RiskCritical
			}
		}
	}

	return &verifyReport{
		VersionRange:    vr,
		Repo:            repo,
		RepoURL:         resolved.RepoURL,
		IntegrityChecks: integrityChecks,
		Analyses:        analyses,
		Result:          analysisResult,
	}, nil
}
