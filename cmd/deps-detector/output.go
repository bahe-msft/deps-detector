package main

import (
	"fmt"
	"strings"

	"deps-detector/internal/model"
)

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
