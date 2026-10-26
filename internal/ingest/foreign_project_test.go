package ingest

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A bound, non-full-scope key that names another project is logged once per
// (key project, header project) pair; own-project, no header and full-scope
// requests are not (#235).
func TestObserveProjectHeaderLogsForeignNamesOncePerPair(t *testing.T) {
	var logs bytes.Buffer
	lookup := func(_ context.Context, _ string) (int64, string, bool, error) {
		return 7, domain.APIKeyScopeIngest, true, nil
	}
	h := NewHandler(auth.New("").WithDBLookup(lookup, func(context.Context, string) error { return nil }), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.SetProjectSlugResolver(func(context.Context, int64) (string, error) { return "alpha", nil })

	send := func(project string) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events", http.NoBody)
		req.Header.Set(auth.HeaderAPIKey, "some-key")
		if project != "" {
			req.Header.Set("x-bugbarn-project", project)
		}
		if _, _, ok, err := h.CheckAPIKey(req); !ok || err != nil {
			t.Fatalf("CheckAPIKey ok=%v err=%v", ok, err)
		}
	}
	send("")
	send("alpha")
	if logs.Len() != 0 {
		t.Fatalf("own project or no header logged: %s", logs.String())
	}
	send("beta")
	send("beta")
	send("gamma")
	got := logs.String()
	if n := strings.Count(got, "bound api key named another project"); n != 2 {
		t.Fatalf("logged %d times, want 2 (beta once, gamma once):\n%s", n, got)
	}
	if !strings.Contains(got, "header_project=beta") || !strings.Contains(got, "key_project=alpha") {
		t.Fatalf("missing fields:\n%s", got)
	}
}

func TestObserveProjectHeaderIgnoresFullScopeKeys(t *testing.T) {
	var logs bytes.Buffer
	lookup := func(_ context.Context, _ string) (int64, string, bool, error) {
		return 7, domain.APIKeyScopeFull, true, nil
	}
	h := NewHandler(auth.New("").WithDBLookup(lookup, func(context.Context, string) error { return nil }), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.SetProjectSlugResolver(func(context.Context, int64) (string, error) { return "alpha", nil })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", http.NoBody)
	req.Header.Set(auth.HeaderAPIKey, "some-key")
	req.Header.Set("x-bugbarn-project", "beta")
	if _, _, ok, _ := h.CheckAPIKey(req); !ok {
		t.Fatal("key rejected")
	}
	if logs.Len() != 0 {
		t.Fatalf("full-scope key logged: %s", logs.String())
	}
}
