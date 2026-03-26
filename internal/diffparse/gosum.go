package diffparse

import (
	"path/filepath"
	"strings"
)

// GoSumParser extracts dependency changes from go.sum file diffs.
//
// go.sum lines have the format:
//
//	<module> <version> <hash>
//	<module> <version>/go.mod <hash>
//
// For example:
//
//	github.com/go-logr/logr v1.4.1 h1:pG4v6F2wMz3Y...=
//	github.com/go-logr/logr v1.4.1/go.mod h1:9T1...=
//
// The parser correlates removed and added lines to detect version transitions.
// Only the content hash lines (not /go.mod lines) are used for version pairing,
// though integrity hashes are extracted from both if available.
type GoSumParser struct{}

func (p *GoSumParser) CanParse(path string) bool {
	return filepath.Base(path) == "go.sum"
}

// goSumEntry is a parsed line from go.sum.
type goSumEntry struct {
	module  string
	version string
	hash    string
	isGoMod bool // true if this is a /go.mod hash line
}

// parseGoSumLine parses a single go.sum line into its components.
// Returns ok=false if the line is not a valid go.sum entry.
func parseGoSumLine(line string) (goSumEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return goSumEntry{}, false
	}

	// Split: <module> <version>[/go.mod] <hash>
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return goSumEntry{}, false
	}

	module := fields[0]
	versionField := fields[1]
	hash := fields[2]

	// Validate hash prefix.
	if !strings.HasPrefix(hash, "h1:") {
		return goSumEntry{}, false
	}

	isGoMod := strings.HasSuffix(versionField, "/go.mod")
	version := strings.TrimSuffix(versionField, "/go.mod")

	return goSumEntry{
		module:  module,
		version: version,
		hash:    hash,
		isGoMod: isGoMod,
	}, true
}

func (p *GoSumParser) Parse(diff FileDiff) []DepChange {
	// Index removed and added entries by module.
	type versionHash struct {
		version string
		hash    string
	}

	// removedByMod tracks content hash entries that were removed, keyed by module.
	removedByMod := make(map[string][]versionHash)
	// addedByMod tracks content hash entries that were added, keyed by module.
	addedByMod := make(map[string][]versionHash)

	for _, line := range diff.Removed {
		entry, ok := parseGoSumLine(line)
		if !ok || entry.isGoMod {
			continue
		}
		removedByMod[entry.module] = append(removedByMod[entry.module], versionHash{
			version: entry.version,
			hash:    entry.hash,
		})
	}

	for _, line := range diff.Added {
		entry, ok := parseGoSumLine(line)
		if !ok || entry.isGoMod {
			continue
		}
		addedByMod[entry.module] = append(addedByMod[entry.module], versionHash{
			version: entry.version,
			hash:    entry.hash,
		})
	}

	// For each module that has both removed and added entries, pair them up as
	// version transitions. We match by position (first removed with first added,
	// etc.). In practice, most modules change from exactly one version to one version.
	var changes []DepChange

	// Collect all modules that appear in both sets.
	modules := make(map[string]struct{})
	for mod := range removedByMod {
		if _, ok := addedByMod[mod]; ok {
			modules[mod] = struct{}{}
		}
	}

	for mod := range modules {
		removed := removedByMod[mod]
		added := addedByMod[mod]

		// Pair each removed version with the corresponding added version.
		// If counts differ, extra entries are treated as pure add/remove and
		// still reported (with empty from/to).
		maxLen := len(removed)
		if len(added) > maxLen {
			maxLen = len(added)
		}

		for i := 0; i < maxLen; i++ {
			dc := DepChange{
				Language: "go",
				Module:   mod,
			}
			if i < len(removed) {
				dc.FromVersion = removed[i].version
				dc.FromIntegrity = removed[i].hash
			}
			if i < len(added) {
				dc.ToVersion = added[i].version
				dc.ToIntegrity = added[i].hash
			}
			changes = append(changes, dc)
		}
	}

	// Also report purely new dependencies (added but not removed).
	for mod, added := range addedByMod {
		if _, ok := modules[mod]; ok {
			continue // already handled above
		}
		for _, a := range added {
			changes = append(changes, DepChange{
				Language:    "go",
				Module:      mod,
				ToVersion:   a.version,
				ToIntegrity: a.hash,
			})
		}
	}

	// Also report purely removed dependencies.
	for mod, removed := range removedByMod {
		if _, ok := modules[mod]; ok {
			continue
		}
		for _, r := range removed {
			changes = append(changes, DepChange{
				Language:      "go",
				Module:        mod,
				FromVersion:   r.version,
				FromIntegrity: r.hash,
			})
		}
	}

	return changes
}
