package diffparse

import (
	"strings"
	"testing"
)

func TestParseUnifiedDiff_BasicGoSum(t *testing.T) {
	diff := `diff --git a/go.sum b/go.sum
index abc1234..def5678 100644
--- a/go.sum
+++ b/go.sum
@@ -1,5 +1,5 @@
 github.com/davecgh/go-spew v1.1.1 h1:abc123=
 github.com/davecgh/go-spew v1.1.1/go.mod h1:def456=
-github.com/go-logr/logr v1.4.1 h1:OldHash111=
-github.com/go-logr/logr v1.4.1/go.mod h1:OldModHash=
+github.com/go-logr/logr v1.4.2 h1:NewHash222=
+github.com/go-logr/logr v1.4.2/go.mod h1:NewModHash=
`

	diffs, err := ParseUnifiedDiff(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff: %v", err)
	}

	if len(diffs) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(diffs))
	}

	fd := diffs[0]
	if fd.Path != "go.sum" {
		t.Errorf("expected path 'go.sum', got %q", fd.Path)
	}

	if len(fd.Removed) != 2 {
		t.Errorf("expected 2 removed lines, got %d", len(fd.Removed))
	}
	if len(fd.Added) != 2 {
		t.Errorf("expected 2 added lines, got %d", len(fd.Added))
	}
}

func TestParseUnifiedDiff_MultipleFiles(t *testing.T) {
	diff := `diff --git a/go.mod b/go.mod
index abc..def 100644
--- a/go.mod
+++ b/go.mod
@@ -3,3 +3,3 @@
-	github.com/go-logr/logr v1.4.1
+	github.com/go-logr/logr v1.4.2
diff --git a/go.sum b/go.sum
index abc..def 100644
--- a/go.sum
+++ b/go.sum
@@ -1,2 +1,2 @@
-github.com/go-logr/logr v1.4.1 h1:OldHash=
+github.com/go-logr/logr v1.4.2 h1:NewHash=
`

	diffs, err := ParseUnifiedDiff(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff: %v", err)
	}

	if len(diffs) != 2 {
		t.Fatalf("expected 2 file diffs, got %d", len(diffs))
	}

	if diffs[0].Path != "go.mod" {
		t.Errorf("expected first file 'go.mod', got %q", diffs[0].Path)
	}
	if diffs[1].Path != "go.sum" {
		t.Errorf("expected second file 'go.sum', got %q", diffs[1].Path)
	}
}

func TestGoSumParser_CanParse(t *testing.T) {
	p := &GoSumParser{}

	tests := []struct {
		path string
		want bool
	}{
		{"go.sum", true},
		{"vendor/go.sum", true},
		{"go.mod", false},
		{"package.json", false},
		{"some/path/go.sum", true},
		{"", false},
	}

	for _, tt := range tests {
		if got := p.CanParse(tt.path); got != tt.want {
			t.Errorf("CanParse(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestGoSumParser_SingleUpgrade(t *testing.T) {
	p := &GoSumParser{}
	fd := FileDiff{
		Path: "go.sum",
		Removed: []string{
			"github.com/go-logr/logr v1.4.1 h1:OldHash111=",
			"github.com/go-logr/logr v1.4.1/go.mod h1:OldModHash=",
		},
		Added: []string{
			"github.com/go-logr/logr v1.4.2 h1:NewHash222=",
			"github.com/go-logr/logr v1.4.2/go.mod h1:NewModHash=",
		},
	}

	changes := p.Parse(fd)

	// Should find exactly 1 upgrade (go.mod lines are skipped for pairing).
	upgrades := filterUpgrades(changes)
	if len(upgrades) != 1 {
		t.Fatalf("expected 1 upgrade, got %d (total changes: %d)", len(upgrades), len(changes))
	}

	u := upgrades[0]
	if u.Language != "go" {
		t.Errorf("Language = %q, want 'go'", u.Language)
	}
	if u.Module != "github.com/go-logr/logr" {
		t.Errorf("Module = %q, want 'github.com/go-logr/logr'", u.Module)
	}
	if u.FromVersion != "v1.4.1" {
		t.Errorf("FromVersion = %q, want 'v1.4.1'", u.FromVersion)
	}
	if u.ToVersion != "v1.4.2" {
		t.Errorf("ToVersion = %q, want 'v1.4.2'", u.ToVersion)
	}
	if u.FromIntegrity != "h1:OldHash111=" {
		t.Errorf("FromIntegrity = %q, want 'h1:OldHash111='", u.FromIntegrity)
	}
	if u.ToIntegrity != "h1:NewHash222=" {
		t.Errorf("ToIntegrity = %q, want 'h1:NewHash222='", u.ToIntegrity)
	}
}

func TestGoSumParser_MultipleUpgrades(t *testing.T) {
	p := &GoSumParser{}
	fd := FileDiff{
		Path: "go.sum",
		Removed: []string{
			"github.com/go-logr/logr v1.4.1 h1:OldHash1=",
			"github.com/google/uuid v1.5.0 h1:OldUUID=",
		},
		Added: []string{
			"github.com/go-logr/logr v1.4.2 h1:NewHash1=",
			"github.com/google/uuid v1.6.0 h1:NewUUID=",
		},
	}

	changes := p.Parse(fd)
	upgrades := filterUpgrades(changes)

	if len(upgrades) != 2 {
		t.Fatalf("expected 2 upgrades, got %d", len(upgrades))
	}

	// Build a map for easier assertions.
	byModule := make(map[string]DepChange)
	for _, u := range upgrades {
		byModule[u.Module] = u
	}

	logr, ok := byModule["github.com/go-logr/logr"]
	if !ok {
		t.Fatal("expected go-logr/logr upgrade")
	}
	if logr.FromVersion != "v1.4.1" || logr.ToVersion != "v1.4.2" {
		t.Errorf("logr: %s → %s, want v1.4.1 → v1.4.2", logr.FromVersion, logr.ToVersion)
	}

	uuid, ok := byModule["github.com/google/uuid"]
	if !ok {
		t.Fatal("expected google/uuid upgrade")
	}
	if uuid.FromVersion != "v1.5.0" || uuid.ToVersion != "v1.6.0" {
		t.Errorf("uuid: %s → %s, want v1.5.0 → v1.6.0", uuid.FromVersion, uuid.ToVersion)
	}
}

func TestGoSumParser_NewDependency(t *testing.T) {
	p := &GoSumParser{}
	fd := FileDiff{
		Path: "go.sum",
		Added: []string{
			"github.com/new/dep v1.0.0 h1:NewDepHash=",
		},
	}

	changes := p.Parse(fd)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	c := changes[0]
	if c.IsUpgrade() {
		t.Error("new dependency should not be classified as an upgrade")
	}
	if c.FromVersion != "" {
		t.Errorf("FromVersion should be empty, got %q", c.FromVersion)
	}
	if c.ToVersion != "v1.0.0" {
		t.Errorf("ToVersion = %q, want 'v1.0.0'", c.ToVersion)
	}
}

func TestGoSumParser_RemovedDependency(t *testing.T) {
	p := &GoSumParser{}
	fd := FileDiff{
		Path: "go.sum",
		Removed: []string{
			"github.com/old/dep v0.9.0 h1:OldDepHash=",
		},
	}

	changes := p.Parse(fd)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	c := changes[0]
	if c.IsUpgrade() {
		t.Error("removed dependency should not be classified as an upgrade")
	}
	if c.FromVersion != "v0.9.0" {
		t.Errorf("FromVersion = %q, want 'v0.9.0'", c.FromVersion)
	}
	if c.ToVersion != "" {
		t.Errorf("ToVersion should be empty, got %q", c.ToVersion)
	}
}

func TestGoSumParser_InvalidLines(t *testing.T) {
	p := &GoSumParser{}
	fd := FileDiff{
		Path: "go.sum",
		Removed: []string{
			"this is not a valid go.sum line",
			"",
			"github.com/foo/bar v1.0.0 nothash",
		},
		Added: []string{
			"also invalid",
		},
	}

	changes := p.Parse(fd)
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes for invalid lines, got %d", len(changes))
	}
}

func TestExtract_EndToEnd(t *testing.T) {
	diff := `diff --git a/go.sum b/go.sum
index abc..def 100644
--- a/go.sum
+++ b/go.sum
@@ -1,6 +1,6 @@
 github.com/davecgh/go-spew v1.1.1 h1:unchanged=
-github.com/go-logr/logr v1.4.1 h1:OldLogr=
-github.com/go-logr/logr v1.4.1/go.mod h1:OldLogrMod=
+github.com/go-logr/logr v1.4.2 h1:NewLogr=
+github.com/go-logr/logr v1.4.2/go.mod h1:NewLogrMod=
+github.com/brand/new v0.1.0 h1:BrandNew=
diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-// old comment
+// new comment
`

	changes, err := Extract(strings.NewReader(diff), DefaultParsers())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should find: 1 upgrade (logr) + 1 new dep (brand/new) = 2 changes.
	// main.go changes should be ignored (no parser matches).
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}

	upgrades := filterUpgrades(changes)
	if len(upgrades) != 1 {
		t.Fatalf("expected 1 upgrade, got %d", len(upgrades))
	}
	if upgrades[0].Module != "github.com/go-logr/logr" {
		t.Errorf("upgrade module = %q, want 'github.com/go-logr/logr'", upgrades[0].Module)
	}
}

func TestParseGoSumLine(t *testing.T) {
	tests := []struct {
		line    string
		wantOK  bool
		module  string
		version string
		hash    string
		isGoMod bool
	}{
		{
			line:    "github.com/go-logr/logr v1.4.2 h1:abc123=",
			wantOK:  true,
			module:  "github.com/go-logr/logr",
			version: "v1.4.2",
			hash:    "h1:abc123=",
			isGoMod: false,
		},
		{
			line:    "github.com/go-logr/logr v1.4.2/go.mod h1:mod123=",
			wantOK:  true,
			module:  "github.com/go-logr/logr",
			version: "v1.4.2",
			hash:    "h1:mod123=",
			isGoMod: true,
		},
		{
			line:   "",
			wantOK: false,
		},
		{
			line:   "not enough fields",
			wantOK: false,
		},
		{
			line:   "github.com/foo v1.0.0 sha256:notgo",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		entry, ok := parseGoSumLine(tt.line)
		if ok != tt.wantOK {
			t.Errorf("parseGoSumLine(%q): ok = %v, want %v", tt.line, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if entry.module != tt.module {
			t.Errorf("module = %q, want %q", entry.module, tt.module)
		}
		if entry.version != tt.version {
			t.Errorf("version = %q, want %q", entry.version, tt.version)
		}
		if entry.hash != tt.hash {
			t.Errorf("hash = %q, want %q", entry.hash, tt.hash)
		}
		if entry.isGoMod != tt.isGoMod {
			t.Errorf("isGoMod = %v, want %v", entry.isGoMod, tt.isGoMod)
		}
	}
}

func filterUpgrades(changes []DepChange) []DepChange {
	var upgrades []DepChange
	for _, c := range changes {
		if c.IsUpgrade() {
			upgrades = append(upgrades, c)
		}
	}
	return upgrades
}
