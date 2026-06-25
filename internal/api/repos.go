package api

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Service repositories are composed from the narrow storage domain stores each
// service actually needs, rather than the whole storage facade. Each composite
// embeds only its domains and satisfies the corresponding service's Repository
// interface by method promotion — making every service's data dependencies
// explicit at the wiring site.

// issueRepo backs the issues service (issues + their events + facets).
type issueRepo struct {
	*storage.IssueStore
	*storage.EventStore
	*storage.FacetStore
}

// IssueRowIDByDisplayID disambiguates the shared-kernel lookup, which all three
// embedded domain stores promote at equal depth.
func (r issueRepo) IssueRowIDByDisplayID(ctx context.Context, displayID string) (int64, error) {
	return r.IssueStore.IssueRowIDByDisplayID(ctx, displayID)
}

// releaseRepo backs the releases service (releases + source maps).
type releaseRepo struct {
	*storage.ReleaseStore
	*storage.SourceMapStore
}

// projectRepo backs the projects service (projects/aliases + groups + API keys
// + settings).
type projectRepo struct {
	*storage.ProjectStore
	*storage.GroupStore
	*storage.APIKeyStore
	*storage.SettingsStore
}

// DefaultProjectID disambiguates the shared-kernel accessor, which all four
// embedded domain stores promote at equal depth.
func (r projectRepo) DefaultProjectID() int64 {
	return r.ProjectStore.DefaultProjectID()
}

// newServiceRepos builds the per-service repositories from a store's domain set.
func newServiceRepos(d storage.Stores) (issueRepo, releaseRepo, projectRepo) {
	return issueRepo{d.Issues, d.Events, d.Facets},
		releaseRepo{d.Releases, d.SourceMaps},
		projectRepo{d.Projects, d.Groups, d.APIKeys, d.Settings}
}
