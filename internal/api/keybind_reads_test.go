package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// A bound read key sees one project and one project's events (#235). The list of
// projects and groups used to show every project's name and slug, and an event
// of another project must answer 404 like its issue does.
func TestBoundReadKeySeesOnlyItsProject(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()

	home, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGroup(ctx, "Shared", "shared"); err != nil {
		t.Fatal(err)
	}
	issue := persistTestIssue(t, store) // lives in the default project

	addKey := func(name string, projectID int64, scope string) string {
		plain := name + "-secret"
		sum := sha256.Sum256([]byte(plain))
		if _, err := store.CreateAPIKey(ctx, name, projectID, hex.EncodeToString(sum[:]), scope); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	alphaRead := addKey("alpha-read", alpha.ID, storage.APIKeyScopeRead)
	homeRead := addKey("home-read", home.ID, storage.APIKeyScopeRead)
	fullKey := addKey("home-full", home.ID, storage.APIKeyScopeFull)

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth, sessions, nil, nil)

	get := func(key, path string) (int, []byte) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.Header.Set("x-bugbarn-api-key", key)
		server.ServeHTTP(rr, req)
		return rr.Code, rr.Body.Bytes()
	}

	_, body := get(fullKey, "/api/v1/issues/"+issue.ID+"/events")
	var events struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &events); err != nil || len(events.Events) == 0 {
		t.Fatalf("seed event missing: %v %s", err, body)
	}
	eventPath := "/api/v1/events/" + events.Events[0].ID

	slugs := func(key string) []string {
		code, body := get(key, "/api/v1/projects")
		if code != http.StatusOK {
			t.Fatalf("GET /api/v1/projects = %d", code)
		}
		var resp struct {
			Projects []struct {
				Slug string `json:"slug"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(resp.Projects))
		for i, p := range resp.Projects {
			out[i] = p.Slug
		}
		return out
	}

	if code, _ := get(alphaRead, eventPath); code != http.StatusNotFound {
		t.Errorf("foreign event = %d, want 404", code)
	}
	if code, _ := get(homeRead, eventPath); code != http.StatusOK {
		t.Errorf("own event = %d, want 200", code)
	}
	if got := slugs(alphaRead); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("bound key lists projects %v, want [alpha]", got)
	}
	if got := slugs(fullKey); len(got) < 2 {
		t.Errorf("full-scope key lists projects %v, want every project", got)
	}

	var groups struct {
		Groups []any `json:"groups"`
	}
	_, body = get(alphaRead, "/api/v1/groups")
	if err := json.Unmarshal(body, &groups); err != nil || len(groups.Groups) != 0 {
		t.Errorf("bound key sees groups: %v %s", err, body)
	}
	_, body = get(fullKey, "/api/v1/groups")
	if err := json.Unmarshal(body, &groups); err != nil || len(groups.Groups) != 1 {
		t.Errorf("full-scope key must see the group: %v %s", err, body)
	}
}
