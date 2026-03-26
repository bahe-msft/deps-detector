package main

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the top-level command for deps-detector.
var rootCmd = &cobra.Command{
	Use:   "deps-detector",
	Short: "Supply chain risk auditor for dependency upgrades",
	Long: `deps-detector — supply chain risk auditor for dependency upgrades

Analyzes dependency version upgrades by gathering intelligence from multiple
sources (release notes, commits, diffs) and using LLM agents to assess
supply-chain risk. Integrity checks compare local hashes against remote
registries to detect retagging attacks.

Supported languages:
  go   — resolves via proxy.golang.org, integrity via sum.golang.org

Prerequisites:
  gh      — GitHub CLI, authenticated (used for fetching repo data)
  copilot — GitHub Copilot CLI (used for LLM analysis via the Copilot SDK)

Environment variables:
  COPILOT_CLI_PATH — Path to the Copilot CLI executable (optional)`,
}

func execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
