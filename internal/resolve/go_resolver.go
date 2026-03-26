package resolve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anomalyco/deps-check/internal/model"
)

// GoResolver resolves Go module paths to their source repositories
// by querying the Go module proxy (proxy.golang.org).
type GoResolver struct {
	// ProxyURL is the base URL of the Go module proxy.
	// Defaults to "https://proxy.golang.org" if empty.
	ProxyURL string

	// SumURL is the base URL of the Go checksum database.
	// Defaults to "https://sum.golang.org" if empty.
	SumURL string
}

func (r *GoResolver) Language() string { return "go" }

// proxyInfoResponse is the JSON structure returned by the Go module proxy
// for /<module>/@v/<version>.info requests.
type proxyInfoResponse struct {
	Version string `json:"Version"`
	Origin  *struct {
		VCS  string `json:"VCS"`
		URL  string `json:"URL"`
		Ref  string `json:"Ref"`
		Hash string `json:"Hash"`
	} `json:"Origin"`
}

func (r *GoResolver) Resolve(ctx context.Context, module string, version string) (*ResolvedPackage, error) {
	base := r.ProxyURL
	if base == "" {
		base = "https://proxy.golang.org"
	}

	// The proxy expects module paths to be lowercased with uppercase letters
	// escaped as !<lower>. For simplicity (most modules are lowercase), we
	// use the path directly. A production implementation would apply proper escaping.
	url := fmt.Sprintf("%s/%s/@v/%s.info", base, module, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying Go module proxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Go module proxy returned %d for %s@%s", resp.StatusCode, module, version)
	}

	var info proxyInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding proxy response: %w", err)
	}

	if info.Origin == nil || info.Origin.URL == "" {
		return nil, fmt.Errorf("no origin URL in proxy response for %s@%s", module, version)
	}

	repo, err := parseGitHubURL(info.Origin.URL)
	if err != nil {
		return nil, err
	}

	return &ResolvedPackage{
		Module:  module,
		Repo:    repo,
		VCS:     info.Origin.VCS,
		RepoURL: info.Origin.URL,
	}, nil
}

// parseGitHubURL extracts owner/repo from a GitHub URL.
// Supports https://github.com/owner/repo and https://github.com/owner/repo.git
func parseGitHubURL(rawURL string) (model.RepoRef, error) {
	// Strip scheme.
	u := rawURL
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimSuffix(u, ".git")

	if !strings.HasPrefix(u, "github.com/") {
		return model.RepoRef{}, fmt.Errorf("not a GitHub repository: %s", rawURL)
	}

	// "github.com/owner/repo" → ["github.com", "owner", "repo"]
	parts := strings.SplitN(u, "/", 4)
	if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
		return model.RepoRef{}, fmt.Errorf("cannot parse GitHub owner/repo from %s", rawURL)
	}

	return model.RepoRef{Owner: parts[1], Repo: parts[2]}, nil
}

// ValidateIntegrity fetches the integrity hashes for a Go module version from
// the Go checksum database (sum.golang.org) and validates localHash against
// the remote hash. If localHash is empty, the result status is IntegritySkipped.
//
// The checksum DB returns lines like:
//
//	github.com/foo/bar v1.0.0 h1:<base64>=
//	github.com/foo/bar v1.0.0/go.mod h1:<base64>=
func (r *GoResolver) ValidateIntegrity(ctx context.Context, module, version, localHash string) (*IntegrityResult, error) {
	base := r.SumURL
	if base == "" {
		base = "https://sum.golang.org"
	}

	url := fmt.Sprintf("%s/lookup/%s@%s", base, module, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying Go checksum DB: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Go checksum DB returned %d for %s@%s", resp.StatusCode, module, version)
	}

	// Parse the response line by line. The first few lines contain the hashes:
	//   <module> <version> h1:<hash>=
	//   <module> <version>/go.mod h1:<hash>=
	remote := RemoteIntegrity{}
	prefix := fmt.Sprintf("%s %s ", module, version)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, prefix) {
			rest := strings.TrimPrefix(line, prefix)
			if strings.HasPrefix(rest, "h1:") {
				remote.Hash = rest
			} else if strings.HasPrefix(rest, "/go.mod h1:") {
				remote.ModHash = strings.TrimPrefix(rest, "/go.mod ")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading checksum DB response: %w", err)
	}

	if remote.Hash == "" {
		return nil, fmt.Errorf("no hash found in checksum DB for %s@%s", module, version)
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
