package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anomalyco/deps-check/internal/model"
)

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

	// Progress messages go to stderr in JSON mode to keep stdout clean.
	var progress io.Writer = os.Stdout
	if vFlags.jsonOutput {
		progress = os.Stderr
	}

	ctx := cmd.Context()

	report, err := runPipeline(ctx, verifyParams{
		VersionRange: model.VersionRange{
			Language: language,
			Dep:      module,
			From:     vFlags.from,
			To:       vFlags.to,
		},
		FromIntegrity: vFlags.fromIntegrity,
		ToIntegrity:   vFlags.toIntegrity,
		Model:         vFlags.llmModel,
		Progress:      progress,
	}, nil)
	if err != nil {
		return err
	}

	if vFlags.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	} else {
		printResult(report.VersionRange, report.Result)
	}

	return nil
}
