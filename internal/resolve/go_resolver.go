package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/anomalyco/deps-check/internal/model"
)

// GoResolver resolves Go module paths to their source repositories and
// validates integrity hashes by delegating to the Go toolchain.
//
// All operations use `go mod download -json`, which automatically honors
// the user's Go environment: GOPROXY, GOPRIVATE, GONOPROXY, GONOSUMDB,
// GOSUMDB, GONOSUMCHECK, and module path escaping.
type GoResolver struct{}

func (r *GoResolver) Language() string { return "go" }

// goModDownload is the JSON structure returned by `go mod download -json`.
type goModDownload struct {
	Path     string       `json:"Path"`
	Version  string       `json:"Version"`
	Info     string       `json:"Info"`
	GoMod    string       `json:"GoMod"`
	Zip      string       `json:"Zip"`
	Dir      string       `json:"Dir"`
	Sum      string       `json:"Sum"`
	GoModSum string       `json:"GoModSum"`
	Error    string       `json:"Error"`
	Origin   *goModOrigin `json:"Origin"`
}

type goModOrigin struct {
	VCS  string `json:"VCS"`
	URL  string `json:"URL"`
	Hash string `json:"Hash"`
	Ref  string `json:"Ref"`
}

// download runs `go mod download -json <module>@<version>` and returns the
// parsed result. It inherits the caller's environment, so GOPROXY, GOPRIVATE,
// GONOSUMDB, etc. are all respected.
func (r *GoResolver) download(ctx context.Context, module, version string) (*goModDownload, error) {
	arg := module + "@" + version
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", arg)
	out, err := cmd.Output()
	if err != nil {
		// If the command produced output, it may contain a JSON error.
		if len(out) > 0 {
			var dl goModDownload
			if jsonErr := json.Unmarshal(out, &dl); jsonErr == nil && dl.Error != "" {
				return nil, fmt.Errorf("go mod download %s: %s", arg, dl.Error)
			}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go mod download %s: %w\n%s", arg, err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("go mod download %s: %w", arg, err)
	}

	var dl goModDownload
	if err := json.Unmarshal(out, &dl); err != nil {
		return nil, fmt.Errorf("parsing go mod download output: %w", err)
	}

	if dl.Error != "" {
		return nil, fmt.Errorf("go mod download %s: %s", arg, dl.Error)
	}

	return &dl, nil
}

func (r *GoResolver) Resolve(ctx context.Context, module string, version string) (*ResolvedPackage, error) {
	dl, err := r.download(ctx, module, version)
	if err != nil {
		return nil, err
	}

	if dl.Origin == nil || dl.Origin.URL == "" {
		return nil, fmt.Errorf("no origin URL in go mod download response for %s@%s", module, version)
	}

	repo, err := parseGitHubURL(dl.Origin.URL)
	if err != nil {
		return nil, err
	}

	return &ResolvedPackage{
		Module:  dl.Path,
		Repo:    repo,
		VCS:     dl.Origin.VCS,
		RepoURL: dl.Origin.URL,
	}, nil
}

// parseGitHubURL extracts owner/repo from a GitHub URL.
// Supports https://github.com/owner/repo and https://github.com/owner/repo.git
func parseGitHubURL(rawURL string) (model.RepoRef, error) {
	u := rawURL
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimSuffix(u, ".git")

	if !strings.HasPrefix(u, "github.com/") {
		return model.RepoRef{}, fmt.Errorf("not a GitHub repository: %s", rawURL)
	}

	parts := strings.SplitN(u, "/", 4)
	if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
		return model.RepoRef{}, fmt.Errorf("cannot parse GitHub owner/repo from %s", rawURL)
	}

	return model.RepoRef{Owner: parts[1], Repo: parts[2]}, nil
}

// ValidateIntegrity downloads the module via `go mod download -json` and
// compares the resulting Sum (which the Go toolchain verified against the
// configured checksum database) with the user-provided localHash.
func (r *GoResolver) ValidateIntegrity(ctx context.Context, module, version, localHash string) (*IntegrityResult, error) {
	dl, err := r.download(ctx, module, version)
	if err != nil {
		return nil, err
	}

	remote := RemoteIntegrity{
		Hash:    dl.Sum,
		ModHash: dl.GoModSum,
	}

	if remote.Hash == "" {
		return nil, fmt.Errorf("no hash returned by go mod download for %s@%s", module, version)
	}

	result := &IntegrityResult{
		Local:  localHash,
		Remote: remote,
	}

	switch {
	case localHash == "":
		result.Status = model.IntegritySkipped
	case localHash == remote.Hash:
		result.Status = model.IntegrityMatch
	default:
		result.Status = model.IntegrityMismatch
	}

	return result, nil
}
