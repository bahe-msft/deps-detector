package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/anomalyco/deps-check/internal/model"
)

// sendPrompt creates a Copilot session with the given system prompt,
// sends the user message, and returns the assistant's response text.
func sendPrompt(ctx context.Context, client *copilot.Client, mdl, systemPrompt, userMessage string) (string, error) {
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model: mdl,
		SystemMessage: &copilot.SystemMessageConfig{
			Mode:    "replace",
			Content: systemPrompt,
		},
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	})
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	defer session.Disconnect()

	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reply, err := session.SendAndWait(sendCtx, copilot.MessageOptions{
		Prompt: userMessage,
	})
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}

	if reply == nil || reply.Data.Content == nil {
		return "", fmt.Errorf("no response from copilot")
	}

	return *reply.Data.Content, nil
}

// formatReports builds a markdown user message from change reports.
func formatReports(vr model.VersionRange, reports []model.ChangeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Dependency: %s\n\n", vr)
	for _, r := range reports {
		fmt.Fprintf(&b, "### %s\n", r.Title)
		if r.URL != "" {
			fmt.Fprintf(&b, "Reference: %s\n", r.URL)
		}
		fmt.Fprintf(&b, "\n%s\n\n---\n\n", r.Body)
	}
	return b.String()
}

// parseSummaryResponse parses the summarizer agent's structured JSON response.
func parseSummaryResponse(raw string) (*model.AnalysisResult, error) {
	cleaned := stripCodeFences(raw)

	var parsed struct {
		RiskLevel string `json:"risk_level"`
		Summary   string `json:"summary"`
		Findings  []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
			Source      string `json:"source"`
		} `json:"findings"`
	}

	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return &model.AnalysisResult{
			RiskLevel:   model.RiskMedium,
			Summary:     "Could not parse structured LLM response. Raw output included below.",
			RawResponse: raw,
		}, nil
	}

	result := &model.AnalysisResult{
		RiskLevel:   model.RiskLevel(parsed.RiskLevel),
		Summary:     parsed.Summary,
		RawResponse: raw,
	}

	for _, f := range parsed.Findings {
		result.Findings = append(result.Findings, model.Finding{
			Title:       f.Title,
			Description: f.Description,
			Severity:    model.RiskLevel(f.Severity),
			Source:      f.Source,
		})
	}

	return result, nil
}

// stripCodeFences removes markdown code fences from LLM output.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
