package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	alertsvc "github.com/wiebe-xyz/bugbarn/internal/service/alerts"
	analyticssvc "github.com/wiebe-xyz/bugbarn/internal/service/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/service/detectionrules"
	issuesvc "github.com/wiebe-xyz/bugbarn/internal/service/issues"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
	projectsvc "github.com/wiebe-xyz/bugbarn/internal/service/projects"
	releasesvc "github.com/wiebe-xyz/bugbarn/internal/service/releases"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// compose.go is the API layer's composition root: the only file in
// internal/api that may import internal/storage (.golangci-soak.yml,
// api-storage-boundary). It turns a store into the services the handlers call;
// handlers only ever see the services.

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

// SampleAfter disambiguates the shared-kernel accessor, for the same reason as
// DefaultProjectID above.
func (r projectRepo) SampleAfter() int64 {
	return r.ProjectStore.SampleAfter()
}

// newServiceRepos builds the per-service repositories from a store's domain set.
func newServiceRepos(d storage.Stores) (issueRepo, releaseRepo, projectRepo) {
	return issueRepo{d.Issues, d.Events, d.Facets},
		releaseRepo{d.Releases, d.SourceMaps},
		projectRepo{d.Projects, d.Groups, d.APIKeys, d.Settings}
}

func NewServer(ingestHandler *ingest.Handler, store *storage.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	d := store.Domains()
	issues, releases, projects := newServiceRepos(d)
	return &Server{
		ingestHandler:     ingestHandler,
		issues:            issuesvc.New(issues, logger),
		projects:          projectsvc.New(projects, logger),
		releases:          releasesvc.New(releases, logger),
		alerts:            alertsvc.New(d.Alerts, logger),
		logs:              logsvc.New(d.Logs, logger),
		analytics:         analyticssvc.New(d.Analytics, logger),
		detectionRules:    detectionrules.New(d.DetectionRules, logger),
		logger:            logger.With("component", "api"),
		maxSourceMapBytes: defaultMaxSourceMapBytes,
	}
}

func NewServerWithAuth(
	ingestHandler *ingest.Handler,
	store *storage.Store,
	users *auth.UserAuthenticator,
	sessions *auth.SessionManager,
	allowedOrigins []string,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	d := store.Domains()
	issues, releases, projects := newServiceRepos(d)
	s := &Server{
		ingestHandler:     ingestHandler,
		issues:            issuesvc.New(issues, logger),
		projects:          projectsvc.New(projects, logger),
		releases:          releasesvc.New(releases, logger),
		alerts:            alertsvc.New(d.Alerts, logger),
		logs:              logsvc.New(d.Logs, logger),
		analytics:         analyticssvc.New(d.Analytics, logger),
		detectionRules:    detectionrules.New(d.DetectionRules, logger),
		logger:            logger.With("component", "api"),
		users:             users,
		sessions:          sessions,
		allowedOrigins:    allowedOrigins,
		maxSourceMapBytes: defaultMaxSourceMapBytes,
		sessionStore:      sessionstore.NewDirect(store, nil),
		oidcRefreshGrace:  time.Hour,
	}
	return s
}
