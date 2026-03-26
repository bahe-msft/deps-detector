// Package resolve maps package manager coordinates to source repository references.
//
// Each language ecosystem has its own package naming conventions and registry.
// A Resolver implementation knows how to query the appropriate registry
// (e.g. proxy.golang.org for Go, registry.npmjs.org for npm) and discover
// the canonical source repository (currently only GitHub is supported).
package resolve

import (
	"context"
	"fmt"

	"github.com/anomalyco/deps-check/internal/model"
)

// Resolver discovers the source repository for a package in a specific ecosystem.
type Resolver interface {
	// Language returns the ecosystem this resolver handles (e.g. "go", "npm").
	Language() string

	// Resolve maps a package module path and a reference version to its
	// canonical source repository. The version is needed because some
	// registries (like proxy.golang.org) require a valid version to return
	// origin metadata.
	Resolve(ctx context.Context, module string, version string) (*ResolvedPackage, error)

	// ValidateIntegrity fetches the integrity hash for a specific module version
	// from the remote registry (e.g. sum.golang.org for Go) and compares it
	// against the provided localHash. If localHash is empty, the result status
	// is IntegritySkipped; otherwise it is IntegrityMatch or IntegrityMismatch.
	ValidateIntegrity(ctx context.Context, module, version, localHash string) (*IntegrityResult, error)
}

// ResolvedPackage holds the result of mapping a package to its source repo.
type ResolvedPackage struct {
	// Module is the canonical module/package path (e.g. "github.com/go-logr/logr").
	Module string

	// Repo is the resolved GitHub repository reference.
	Repo model.RepoRef

	// VCS is the version control system (e.g. "git").
	VCS string

	// RepoURL is the full repository URL (e.g. "https://github.com/go-logr/logr").
	RepoURL string
}

// RemoteIntegrity holds integrity hashes retrieved from a remote registry.
type RemoteIntegrity struct {
	// Hash is the primary content hash (e.g. "h1:..." for Go modules).
	Hash string

	// ModHash is the go.mod hash (Go-specific, empty for other ecosystems).
	ModHash string
}

// IntegrityResult holds the outcome of validating a local integrity hash
// against the remote registry.
type IntegrityResult struct {
	// Status is the validation outcome: match, mismatch, or skipped.
	Status model.IntegrityStatus

	// Local is the user-provided hash (empty when skipped).
	Local string

	// Remote is the integrity hashes fetched from the registry.
	Remote RemoteIntegrity
}

// Registry holds resolvers keyed by language.
type Registry struct {
	resolvers map[string]Resolver
}

// NewRegistry creates an empty resolver registry.
func NewRegistry() *Registry {
	return &Registry{resolvers: make(map[string]Resolver)}
}

// Register adds a resolver for the given language.
func (r *Registry) Register(resolver Resolver) {
	r.resolvers[resolver.Language()] = resolver
}

// Resolve looks up the resolver for the given language and resolves the package.
func (r *Registry) Resolve(ctx context.Context, language, module, version string) (*ResolvedPackage, error) {
	resolver, ok := r.resolvers[language]
	if !ok {
		return nil, fmt.Errorf("unsupported language: %q", language)
	}
	return resolver.Resolve(ctx, module, version)
}

// ValidateIntegrity looks up the resolver for the given language and validates
// the local integrity hash against the remote registry.
func (r *Registry) ValidateIntegrity(ctx context.Context, language, module, version, localHash string) (*IntegrityResult, error) {
	resolver, ok := r.resolvers[language]
	if !ok {
		return nil, fmt.Errorf("unsupported language: %q", language)
	}
	return resolver.ValidateIntegrity(ctx, module, version, localHash)
}
