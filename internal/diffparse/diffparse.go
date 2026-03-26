// Package diffparse extracts dependency upgrade targets from unified diff output.
//
// It parses the output of `git diff` (or similar tools), identifies changes to
// dependency manifest/lock files (e.g. go.sum, package-lock.json), and extracts
// the added/removed dependency versions so that each upgrade can be verified
// by the analysis pipeline.
package diffparse

import (
	"bufio"
	"io"
	"strings"
)

// DepChange represents a single dependency version change detected in a diff.
type DepChange struct {
	// Language is the ecosystem identifier (e.g. "go", "npm").
	Language string

	// Module is the full package/module path (e.g. "github.com/go-logr/logr").
	Module string

	// FromVersion is the old version being replaced (empty if the dep is newly added).
	FromVersion string

	// ToVersion is the new version being introduced (empty if the dep is removed).
	ToVersion string

	// FromIntegrity is the integrity hash for the old version (if available).
	FromIntegrity string

	// ToIntegrity is the integrity hash for the new version (if available).
	ToIntegrity string
}

// IsUpgrade returns true if this change represents a version transition (both
// from and to versions are present). Pure additions or removals are not upgrades.
func (d DepChange) IsUpgrade() bool {
	return d.FromVersion != "" && d.ToVersion != ""
}

// FileDiff holds the parsed added/removed lines for a single file in a unified diff.
type FileDiff struct {
	// Path is the file path (b-side path from the diff header).
	Path string

	// Added contains lines that were added (without the leading '+').
	Added []string

	// Removed contains lines that were removed (without the leading '-').
	Removed []string
}

// ParseUnifiedDiff parses a unified diff (as produced by `git diff`) and
// returns one FileDiff per modified file.
func ParseUnifiedDiff(r io.Reader) ([]FileDiff, error) {
	var diffs []FileDiff
	var current *FileDiff

	scanner := bufio.NewScanner(r)
	// Increase buffer for large diffs.
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Detect new file diff header: "diff --git a/path b/path"
		if strings.HasPrefix(line, "diff --git ") {
			// Flush current file.
			if current != nil {
				diffs = append(diffs, *current)
			}
			current = &FileDiff{}
			// Extract the b-side path.
			// Format: "diff --git a/<path> b/<path>"
			parts := strings.SplitN(line, " b/", 2)
			if len(parts) == 2 {
				current.Path = parts[1]
			}
			continue
		}

		if current == nil {
			continue
		}

		// Skip diff metadata lines.
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "@@") ||
			strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file") ||
			strings.HasPrefix(line, "old mode") || strings.HasPrefix(line, "new mode") ||
			strings.HasPrefix(line, "rename ") || strings.HasPrefix(line, "similarity index") ||
			strings.HasPrefix(line, "Binary files") {
			continue
		}

		// Collect added/removed lines.
		if strings.HasPrefix(line, "+") {
			current.Added = append(current.Added, line[1:])
		} else if strings.HasPrefix(line, "-") {
			current.Removed = append(current.Removed, line[1:])
		}
		// Context lines (starting with ' ') are ignored.
	}

	// Flush last file.
	if current != nil {
		diffs = append(diffs, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return diffs, nil
}

// FileParser extracts dependency changes from a specific type of file diff.
type FileParser interface {
	// CanParse returns true if this parser handles the given file path.
	CanParse(path string) bool

	// Parse extracts dependency changes from the added/removed lines of a file diff.
	Parse(diff FileDiff) []DepChange
}

// Extract parses a unified diff from r and returns all dependency changes found
// across all supported file types. Parsers are tried in order; the first
// matching parser handles each file.
func Extract(r io.Reader, parsers []FileParser) ([]DepChange, error) {
	fileDiffs, err := ParseUnifiedDiff(r)
	if err != nil {
		return nil, err
	}

	var changes []DepChange
	for _, fd := range fileDiffs {
		for _, p := range parsers {
			if p.CanParse(fd.Path) {
				changes = append(changes, p.Parse(fd)...)
				break // first matching parser wins
			}
		}
	}

	return changes, nil
}

// DefaultParsers returns the built-in set of file parsers for all supported
// ecosystems.
func DefaultParsers() []FileParser {
	return []FileParser{
		&GoSumParser{},
	}
}
