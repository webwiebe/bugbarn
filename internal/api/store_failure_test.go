package api

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
)

func closedStoreServer(t *testing.T) *Server {
	t.Helper()
	store := mustOpenStore(t)
	srv := NewServerWithAuth(ingest.NewHandler(auth.New("k"), nil, 1<<20), store, nil, auth.NewSessionManager("s", 3600), nil, nil)
	store.Close()
	return srv
}

// With the store gone, no route hands the database's own error text to the
// client.
func TestEveryRouteKeepsRawDatabaseErrorsFromTheClient(t *testing.T) {
	srv := closedStoreServer(t)
	wildcard := regexp.MustCompile(`\{[^}]*\}`)
	for _, pattern := range srv.routePatterns() {
		method, path, _ := strings.Cut(pattern, " ")
		for _, id := range []string{"1", "default"} {
			body := ""
			if method != "GET" && method != "DELETE" {
				body = `{"name":"n","slug":"default","message":"m"}`
			}
			req := httptest.NewRequest(method, wildcard.ReplaceAllString(path, id), strings.NewReader(body))
			req.Header.Set("X-BugBarn-API-Key", "k")
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			text := rr.Body.String()
			if strings.Contains(text, "sql:") || strings.Contains(text, "database is closed") {
				t.Errorf("%s with id %q leaked a database error: %s", pattern, id, text)
			}
		}
	}
}

// The list endpoints of the main resources answer a 5xx when the store fails,
// so a client retries or alerts instead of reading an empty page.
func TestResourceReadsAnswerServerErrorWhenTheStoreFails(t *testing.T) {
	srv := closedStoreServer(t)
	for _, path := range []string{
		"/api/v1/issues", "/api/v1/projects", "/api/v1/alerts", "/api/v1/monitors", "/api/v1/releases",
		"/api/v1/apikeys", "/api/v1/logs", "/api/v1/analytics/overview", "/api/v1/analytics/pages",
		"/api/v1/settings", "/api/v1/detection/rules", "/api/v1/source-maps",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-BugBarn-API-Key", "k")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code < 500 {
			t.Errorf("GET %s = %d with the store closed, want a 5xx", path, rr.Code)
		}
	}
}
