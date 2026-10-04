package telemetryview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

type fakeSource struct{ err error }

func (f fakeSource) DB(context.Context) (*telemetrydb.DB, error) { return nil, f.err }

func newTestService(src telemetrydb.Source) *Service {
	s := New(src, src, nil)
	s.now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	return s
}

func TestWindowDefaults(t *testing.T) {
	s := newTestService(nil)
	from, to, err := s.Window(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !to.Equal(s.now()) || to.Sub(from) != DefaultRange {
		t.Fatalf("window = %v..%v", from, to)
	}
	if _, _, err := s.Window(to, from); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("reversed window err = %v, want invalid input", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestService(fakeSource{err: errors.New("must not be reached")})
	cases := map[string]error{}
	_, cases["limit too high"] = s.SearchSecurity(ctx, telemetrydb.SecurityQuery{Limit: MaxSecurityLimit + 1})
	_, cases["negative limit"] = s.SearchSecurity(ctx, telemetrydb.SecurityQuery{Limit: -1})
	_, cases["bad status"] = s.SearchSecurity(ctx, telemetrydb.SecurityQuery{Status: 42})
	_, cases["no host"] = s.HostMetrics(ctx, "")
	_, cases["no metric"] = s.Series(ctx, "h", "", time.Time{}, time.Time{})
	now := s.now()
	_, cases["range too long"] = s.Series(ctx, "h", "load1", now.Add(-MaxSeriesRange-time.Hour), now)
	for name, err := range cases {
		if !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
}

func TestMissingFileIsUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, src := range []telemetrydb.Source{nil, fakeSource{err: telemetrydb.ErrNotCreated}} {
		s := newTestService(src)
		if _, err := s.Hosts(ctx); !errors.Is(err, apperr.ErrUnavailable) {
			t.Errorf("Hosts err = %v, want unavailable", err)
		}
		if _, err := s.SearchSecurity(ctx, telemetrydb.SecurityQuery{}); !errors.Is(err, apperr.ErrUnavailable) {
			t.Errorf("SearchSecurity err = %v, want unavailable", err)
		}
	}
}

func TestOtherErrorsPassThrough(t *testing.T) {
	boom := apperr.Internal("boom", nil)
	s := newTestService(fakeSource{err: boom})
	if _, err := s.Hosts(context.Background()); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("err = %v, want internal", err)
	}
}
