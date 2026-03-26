# deps-detector

Supply-chain risk auditor for dependency upgrades. Analyzes version transitions by gathering intelligence from multiple sources (release notes, commits, diffs) and using LLM agents to assess risk.

## Prerequisites

- **go** — Go toolchain (used for module resolution and integrity verification via `go mod download`)
- **gh** — GitHub CLI, authenticated
- **copilot** — GitHub Copilot CLI (used via the Copilot SDK)

## Install

```sh
go build -o deps-detector ./cmd/deps-detector/
```

## Commands

### verify

Verify a single dependency upgrade between two versions.

```sh
deps-detector verify go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2
```

With integrity hashes for retagging detection:

```sh
deps-detector verify go:github.com/go-logr/logr \
  --from v1.4.1 --to v1.4.2 \
  --from-integrity "h1:pKouT5E8xu9zeFC39JXRDukb6JFQPXM5p5I91188VAQ=" \
  --to-integrity "h1:6pFjapn8bFcIbiKo3XT4j/BhANplGihG6tvd+8rYgrY="
```

JSON output:

```sh
deps-detector verify --json go:github.com/go-logr/logr --from v1.4.1 --to v1.4.2
```

#### Sample output

```
🔍 Resolving go:github.com/go-logr/logr
  📦 Source: https://github.com/go-logr/logr (go-logr/logr)

🔒 Checking integrity...
  ⚠️  No --from-integrity provided for v1.4.1 — cannot detect retagging
  ⚠️  No --to-integrity provided for v1.4.2 — cannot detect retagging

🔍 Analyzing github.com/go-logr/logr (v1.4.1..v1.4.2)

  ⏳ Gathering from diff...
  ⏳ Gathering from release_notes...
  ⏳ Gathering from commits...
  ✅ release_notes: 1 report(s)
  ✅ diff: 2 report(s)
  ✅ commits: 1 report(s)

🤖 Running analysis agents...

  🤖 [commits] Analyzing...
  🤖 [diff] Analyzing...
  🤖 [release_notes] Analyzing...
  ✅ [release_notes] Done
  ✅ [commits] Done
  ✅ [diff] Done

  🤖 [summarizer] Consolidating analyses...

════════════════════════════════════════════════════════════
  SUPPLY CHAIN RISK REPORT: go:github.com/go-logr/logr v1.4.1..v1.4.2
════════════════════════════════════════════════════════════

  Risk Level: ⚪ NONE

  All three analysis sources unanimously agree this is a benign patch
  release. The changes consist of a focused bug fix for slog group
  rendering by project co-founder Tim Hockin, routine lint cleanup,
  and ~36 Dependabot CI action bumps — all merged by known maintainer
  Patrick Ohly. No supply chain risk indicators were detected.

  No suspicious findings detected.

════════════════════════════════════════════════════════════
```

### from-diff

Detect dependency changes from a unified diff and verify each upgrade automatically. Reads from stdin.

```sh
git diff | deps-detector from-diff
```

Scope to dependency files:

```sh
git diff HEAD~1 -- go.sum | deps-detector from-diff
```

Compare branches:

```sh
git diff main..feature -- go.sum | deps-detector from-diff --json
```

From a patch file:

```sh
deps-detector from-diff < changes.patch
```

#### Sample output

When `go.sum` changes include a version upgrade and a newly added dependency:

```
📋 Reading diff from stdin...

📦 Detected 2 dependency change(s):

  Upgrades (1):
    go:github.com/go-logr/logr v1.4.1 → v1.4.2
  New dependencies (1):
    + go:github.com/spf13/cobra v1.10.2

🔎 Verifying 1 upgrade(s)...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  [1/1] go:github.com/go-logr/logr v1.4.1 → v1.4.2
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🔍 Resolving go:github.com/go-logr/logr
  📦 Source: https://github.com/go-logr/logr (go-logr/logr)

🔒 Checking integrity...
  ✅ v1.4.1 (from): integrity match
  ✅ v1.4.2 (to): integrity match

🔍 Analyzing github.com/go-logr/logr (v1.4.1..v1.4.2)

  ⏳ Gathering from diff...
  ⏳ Gathering from commits...
  ⏳ Gathering from release_notes...
  ✅ release_notes: 1 report(s)
  ✅ diff: 2 report(s)
  ✅ commits: 1 report(s)

🤖 Running analysis agents...

  🤖 [commits] Analyzing...
  🤖 [diff] Analyzing...
  🤖 [release_notes] Analyzing...
  ✅ [release_notes] Done
  ✅ [commits] Done
  ✅ [diff] Done

  🤖 [summarizer] Consolidating analyses...

════════════════════════════════════════════════════════════
  SUPPLY CHAIN RISK REPORT: go:github.com/go-logr/logr v1.4.1..v1.4.2
════════════════════════════════════════════════════════════

  Risk Level: ⚪ NONE

  This is a routine, low-risk patch release of a well-maintained Go
  logging library. All three analysis sources independently confirm
  that the changes consist of minor bug fixes and lint cleanup by
  established maintainer Tim Hockin, automated CI dependency bumps
  by Dependabot, and no new runtime dependencies or suspicious
  patterns. No supply chain risk indicators were identified.

  No suspicious findings detected.

════════════════════════════════════════════════════════════

════════════════════════════════════════════════════════════
  BATCH SUMMARY: 1 upgrade(s) verified
════════════════════════════════════════════════════════════

  ⚪ NONE   go:github.com/go-logr/logr v1.4.1→v1.4.2

════════════════════════════════════════════════════════════
```

Integrity hashes are automatically extracted from the `go.sum` diff and passed through for retagging detection.

## Supported ecosystems

| Language | Manifest | Lock file | Toolchain | Registry |
|----------|----------|-----------|-----------|----------|
| Go | `go.mod` | `go.sum` | `go mod download -json` | Honors `GOPROXY`, `GOPRIVATE`, `GOSUMDB` |

## Environment variables

| Variable | Description |
|----------|-------------|
| `GOPROXY` | Go module proxy list (respected via `go mod download`) |
| `GOPRIVATE` | Modules that should bypass the proxy and checksum DB |
| `GOSUMDB` | Go checksum database URL |
| `GONOSUMCHECK` | Modules to skip checksum verification for |
| `COPILOT_CLI_PATH` | Path to the Copilot CLI executable (optional) |
