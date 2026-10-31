package releases

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// NewIssueWindow returns the span in which an issue counts as new in a release:
// from the release's observed time up to the observed time of the next release
// in the same environment. before is zero for the latest release of its
// environment, which leaves the span open. Releases of other environments do not
// end the span.
func (s *Service) NewIssueWindow(ctx context.Context, id string) (from, before time.Time, err error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.NewIssueWindow")
	defer span.End()

	release, err := s.repo.GetRelease(ctx, id)
	if err != nil {
		tracing.SetError(span, err)
		return time.Time{}, time.Time{}, err
	}
	all, err := s.repo.ListReleases(ctx)
	if err != nil {
		tracing.SetError(span, err)
		return time.Time{}, time.Time{}, err
	}
	for _, other := range all {
		if other.Environment != release.Environment || !other.ObservedAt.After(release.ObservedAt) {
			continue
		}
		if before.IsZero() || other.ObservedAt.Before(before) {
			before = other.ObservedAt
		}
	}
	return release.ObservedAt, before, nil
}
