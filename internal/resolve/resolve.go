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
