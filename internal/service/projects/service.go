package projects

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type Repository interface {
	ListProjects(context.Context) ([]domain.Project, error)
	CreateProject(ctx context.Context, name, slug string) (domain.Project, error)
	EnsureProject(ctx context.Context, slug string) (domain.Project, error)
	EnsureProjectPending(ctx context.Context, slug string) (domain.Project, error)
	ApproveProject(ctx context.Context, slug string) error
	DeleteProject(ctx context.Context, slug string) error
	ProjectBySlug(ctx context.Context, slug string) (domain.Project, error)
	DefaultProjectID() int64

	// Alias operations
	CreateAlias(ctx context.Context, aliasSlug string, projectID int64) error
	DeleteAlias(ctx context.Context, aliasSlug string) error
	ResolveAlias(ctx context.Context, slug string) (int64, error)
	ListAliases(ctx context.Context) ([]domain.ProjectAlias, error)

	// Rename and merge
	RenameProject(ctx context.Context, oldSlug, newSlug, newName string) error
	MergeProjects(ctx context.Context, sourceSlug, targetSlug string) error
	MergeProjectsAs(ctx context.Context, sourceSlug, targetSlug, environment string) error
	MergeProjectsReport(ctx context.Context, sourceSlug, targetSlug, environment string, dryRun bool) (domain.MergeReport, error)
	ArchiveProject(ctx context.Context, slug string, quietFor time.Duration) (time.Time, error)
	UnarchiveProject(ctx context.Context, slug string) error

	// Volume policy: retention window and event sampling.
	UpdateProjectLimits(ctx context.Context, slug string, retentionDays *int, sampling string) error
	SampleAfter() int64

	// FunnelBarn link shown next to the By surface table.
	UpdateProjectFunnelBarnURL(ctx context.Context, slug, url string) error

	// Silence rules opt-out (bugbarn#252): empty = watched, "off" = skipped.
	UpdateProjectSilenceWatch(ctx context.Context, slug, value string) error
	ProjectSilenceWatch(ctx context.Context, slug string) (string, error)

	// Group operations
	CreateGroup(ctx context.Context, name, slug string) (domain.ProjectGroup, error)
	ListGroups(context.Context) ([]domain.ProjectGroup, error)
	DeleteGroup(ctx context.Context, slug string) error
	AssignProjectToGroup(ctx context.Context, projectSlug, groupSlug string) error
	RemoveProjectFromGroup(ctx context.Context, projectSlug string) error
	ListGroupProjects(ctx context.Context, groupSlug string) ([]domain.Project, error)

	ProjectUsageAll(ctx context.Context) (map[int64]domain.ProjectUsage, error)

	ListAPIKeys(context.Context) ([]domain.APIKey, error)
	CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256, scope string) (domain.APIKey, error)
	DeleteAPIKey(ctx context.Context, id int64) error
	EnsureSetupAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) error
	ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error)
	TouchAPIKey(ctx context.Context, keySHA256 string) error
	APIKeyByID(ctx context.Context, id int64) (domain.APIKey, error)
	RevokeSetupKey(ctx context.Context, slug, keySHA256 string) error
	ListSetupKeyRevocations(ctx context.Context) ([]domain.SetupKeyRevocation, error)

	GetSettings(context.Context) (map[string]string, error)
	UpdateSettings(context.Context, map[string]string) error
}

// HeldReplayer drains the held-events backlog accumulated while a project was
// pending, persisting it through the normal ingest pipeline. It is satisfied by
// *ingestproc.Replayer and wired only on the writer.
type HeldReplayer interface {
	ReplayHeld(ctx context.Context, projectID int64) (int, error)
}

type Service struct {
	repo     Repository
	logger   *slog.Logger
	replayer HeldReplayer
	usage    usageCache
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "projects")}
}

// SetHeldReplayer wires the held-events replayer used to drain a project's
// backlog on approval. Called on the writer; left nil on readers (which forward
// approvals to the writer).
func (s *Service) SetHeldReplayer(r HeldReplayer) {
	s.replayer = r
}

func (s *Service) List(ctx context.Context) ([]domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.List")
	defer span.End()
	projects, err := s.repo.ListProjects(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list projects", err)
	}
	return projects, err
}

func (s *Service) Create(ctx context.Context, name, slug string) (domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Create",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	if err := rejectEnvironmentName(name, slug); err != nil {
		return domain.Project{}, err
	}
	proj, err := s.repo.CreateProject(ctx, name, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "create project", err, "slug", slug)
		return domain.Project{}, err
	}
	s.usage.invalidate()
	s.logger.InfoContext(ctx, "project created", "slug", slug, "id", proj.ID)
	return proj, nil
}

func (s *Service) Ensure(ctx context.Context, slug string) (domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Ensure",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	proj, err := s.repo.EnsureProject(ctx, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "ensure project", err)
	}
	return proj, err
}

func (s *Service) EnsurePending(ctx context.Context, slug string) (domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.EnsurePending",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	proj, err := s.repo.EnsureProjectPending(ctx, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "ensure pending project", err)
	}
	return proj, err
}

func (s *Service) Delete(ctx context.Context, slug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Delete",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	if err := s.repo.DeleteProject(ctx, slug); err != nil {
		// Treat "not found" as success — delete is idempotent.
		if errors.Is(err, apperr.ErrNotFound) {
			s.logger.InfoContext(ctx, "project already deleted", "slug", slug)
			return nil
		}
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "delete project", err, "slug", slug)
		return err
	}
	s.usage.invalidate()
	s.logger.InfoContext(ctx, "project deleted", "slug", slug)
	return nil
}

func (s *Service) Approve(ctx context.Context, slug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Approve",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	if err := s.repo.ApproveProject(ctx, slug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "approve project", err, "slug", slug)
		return err
	}
	s.logger.InfoContext(ctx, "project approved", "slug", slug)

	// Drain any ingest that arrived while the project was pending. The status is
	// now active, so replayed records persist instead of being re-held. A drain
	// failure is logged but does not fail the approval — the backlog remains and
	// is retried on the next approval call.
	if s.replayer != nil {
		proj, err := s.repo.ProjectBySlug(ctx, slug)
		if err != nil {
			apperr.LogFailure(ctx, s.logger, "approve: lookup project for replay", err, "slug", slug)
			return nil
		}
		n, err := s.replayer.ReplayHeld(ctx, proj.ID)
		if err != nil {
			apperr.LogFailure(ctx, s.logger, "approve: replay held events", err, "slug", slug, "replayed", n)
			return nil
		}
		if n > 0 {
			s.logger.InfoContext(ctx, "replayed held events on approval", "slug", slug, "count", n)
		}
	}
	return nil
}

func (s *Service) BySlug(ctx context.Context, slug string) (domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.BySlug",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	proj, err := s.repo.ProjectBySlug(ctx, slug)
	if err != nil {
		tracing.SetError(span, err)
		if errors.Is(err, apperr.ErrUnavailable) {
			// A reader could not read the database even after its retries
			// (SQLITE_READONLY_RECOVERY, #261), so this request failed with a
			// 503. ERROR lets selflog file it; the line names the caller. A
			// retry that recovered is counted in storage and logs nothing.
			info := domain.RequestInfoFromContext(ctx)
			s.logger.ErrorContext(ctx, "project by slug: database unavailable on this reader",
				"slug", slug, "route", info.Route, "user_agent", info.UserAgent, "error", err)
		} else {
			apperr.LogFailure(ctx, s.logger, "project by slug", err, "slug", slug)
		}
		return domain.Project{}, err
	}
	return proj, nil
}

func (s *Service) DefaultProjectID() int64 {
	return s.repo.DefaultProjectID()
}
