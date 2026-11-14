package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// The routing characterization table pins what the HTTP surface answers for a
// broad set of requests: every route with its method, a wrong method, a trailing
// slash, an unknown subpath, a missing credential and a preflight. The table is
// testdata/routes_characterization.tsv; one row per request:
//
//	auth <TAB> METHOD <TAB> target <TAB> body <TAB> => <TAB> status <TAB> key=value ...
//
// auth is none, full, read, ingestkey, session or session-nocsrf. The target may
// hold placeholders such as {issue} that the fixture fills with seeded ids. The
// expectation is the status and the headers or body shape worth pinning (allow,
// acao, ct, loc, json keys, short text body). A line "@mode reader" switches the
// following rows to a server that forwards writes to a stub writer.
//
// Run with -update-routes to rewrite the expectations after an intended routing
// change, then read the diff: every changed row must be one the change meant.
var updateRoutes = flag.Bool("update-routes", false, "rewrite testdata/routes_characterization.tsv expectations")

const routesTable = "testdata/routes_characterization.tsv"

type routeRow struct {
	raw                  string
	mode                 string
	auth, method, target string
	body                 string
	want                 string
}

type routeFixture struct {
	writer, reader *Server
	keys           map[string]string
	placeholders   map[string]string
	cookie         *http.Cookie
	csrf           string
	n              int
}

func sha(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	home, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	f := &routeFixture{keys: map[string]string{}, placeholders: map[string]string{}}
	addKey := func(label, scope string) int64 {
		plain := "key-" + label
		k, err := store.CreateAPIKey(ctx, label, home.ID, sha(plain), scope)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[label] = plain
		return k.ID
	}
	addKey("full", storage.APIKeyScopeFull)
	addKey("read", storage.APIKeyScopeRead)
	addKey("ingestkey", storage.APIKeyScopeIngest)
	f.placeholders["keyid"] = strconv.FormatInt(addKey("victim", storage.APIKeyScopeRead), 10)

	issue, ev := mustPersistProcessedEvent(t, store, event.ProcessedEvent{
		Fingerprint: "routes-characterization",
		Event: event.Event{
			ReceivedAt: time.Now().UTC(), ObservedAt: time.Now().UTC(), Severity: "ERROR",
			Message:   "characterization",
			Exception: event.Exception{Type: "TestError", Message: "characterization"},
		},
	})
	f.placeholders["issue"] = issue.ID
	f.placeholders["event"] = ev.ID
	rel, err := store.CreateRelease(ctx, storage.Release{Name: "seed", Environment: "prod", ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	f.placeholders["release"] = rel.ID
	al, err := store.CreateAlert(ctx, storage.Alert{Name: "seed", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.placeholders["alert"] = al.ID
	f.placeholders["krid"] = "nope"
	f.placeholders["slug"] = "default"
	f.placeholders["pslug"] = "default"
	f.placeholders["facet"] = "attributes.surface"
	f.placeholders["host"] = "h1"

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	users, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	traces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(traces.Close)
	newServer := func() *Server {
		s := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, users, sessions, nil, nil)
		s.SetInternalSessionSecret("internal-secret")
		s.SetSetupConfig("setup-secret", "https://bugbarn.example")
		s.SetLogHub(logstream.NewHub())
		s.SetFrontendTraces(traces.URL, "k", "bugbarn-web")
		return s
	}
	f.writer = newServer()

	writerStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Stub-Writer", r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(writerStub.Close)
	f.reader = newServer()
	f.reader.SetWriteForwarder(NewWriteForwarder(writerStub.URL))

	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"pass"}`))
	f.writer.ServeHTTP(login, req)
	for _, c := range login.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			f.cookie = c
		}
	}
	if f.cookie == nil {
		t.Fatalf("login: no session cookie (status %d)", login.Code)
	}
	f.csrf = sessions.CSRFToken(f.cookie.Value)
	return f
}

// flushCanceler ends a streaming handler once it has flushed its headers, so an
// SSE route answers deterministically without a timer.
type flushCanceler struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (f flushCanceler) Flush() {
	f.ResponseRecorder.Flush()
	f.cancel()
}

func (f *routeFixture) expand(s string) string {
	for k, v := range f.placeholders {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

func (f *routeFixture) do(row routeRow) string {
	f.n++
	target := f.expand(row.target)
	body := f.expand(row.body)
	req := httptest.NewRequest(row.method, target, strings.NewReader(body))
	req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:4000", (f.n>>16)&255, (f.n>>8)&255, f.n&255)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	switch row.auth {
	case "none":
	case "session", "session-nocsrf":
		req.AddCookie(f.cookie)
		if row.auth == "session" {
			req.Header.Set("X-BugBarn-CSRF", f.csrf)
		}
	default:
		req.Header.Set("X-BugBarn-API-Key", f.keys[row.auth])
	}
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	srv := f.writer
	if row.mode == "reader" {
		srv = f.reader
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(flushCanceler{rec, cancel}, req)
	return describeResponse(rec)
}

func describeResponse(rec *httptest.ResponseRecorder) string {
	parts := []string{strconv.Itoa(rec.Code)}
	h := rec.Header()
	add := func(key, val string) {
		if val != "" {
			parts = append(parts, key+"="+val)
		}
	}
	add("allow", h.Get("Allow"))
	add("acao", h.Get("Access-Control-Allow-Origin"))
	add("acam", h.Get("Access-Control-Allow-Methods"))
	add("loc", h.Get("Location"))
	add("writer", h.Get("X-Stub-Writer"))
	ct := h.Get("Content-Type")
	add("ct", ct)
	body := strings.TrimSpace(rec.Body.String())
	switch {
	case strings.HasPrefix(ct, "application/json"):
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &obj) == nil {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			add("json", strings.Join(keys, ","))
		}
	case strings.HasPrefix(ct, "text/plain") && len(body) <= 120:
		add("body", strings.ReplaceAll(body, "\n", " "))
	}
	return strings.Join(parts, "\t")
}

func readRouteRows(t *testing.T) (lines []string, rows map[int]routeRow) {
	t.Helper()
	data, err := os.ReadFile(routesTable)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	rows = map[int]routeRow{}
	mode := "writer"
	for i, line := range lines {
		if rest, ok := strings.CutPrefix(line, "@mode "); ok {
			mode = strings.TrimSpace(rest)
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 || fields[4] != "=>" {
			t.Fatalf("%s:%d: malformed row %q", routesTable, i+1, line)
		}
		rows[i] = routeRow{
			raw: line, mode: mode, auth: fields[0], method: fields[1], target: fields[2], body: fields[3],
			want: strings.Join(fields[5:], "\t"),
		}
	}
	return lines, rows
}

func TestRoutesCharacterization(t *testing.T) {
	f := newRouteFixture(t)
	lines, rows := readRouteRows(t)
	if len(rows) < 500 {
		t.Fatalf("only %d rows in %s", len(rows), routesTable)
	}
	updated := append([]string(nil), lines...)
	var failures int
	for i := range lines {
		row, ok := rows[i]
		if !ok {
			continue
		}
		got := f.do(row)
		prefix := strings.Join([]string{row.auth, row.method, row.target, row.body, "=>"}, "\t")
		updated[i] = prefix + "\t" + got
		if got != row.want && !*updateRoutes {
			failures++
			if failures <= 60 {
				t.Errorf("%s:%d [%s] %s %s %s\n  want %q\n  got  %q", routesTable, i+1, row.mode, row.auth, row.method, row.target, row.want, got)
			}
		}
	}
	if failures > 60 {
		t.Errorf("... and %d more rows differ", failures-60)
	}
	if *updateRoutes {
		if err := os.WriteFile(routesTable, []byte(strings.Join(updated, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
