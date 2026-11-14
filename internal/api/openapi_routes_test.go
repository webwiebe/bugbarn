package api

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	openapispec "github.com/wiebe-xyz/bugbarn/api"
)

// servedOperations lists every method and path the server routes for public and
// authenticated callers, in the spelling api/openapi.yaml uses for path
// parameters. Three tests keep it honest:
//
//   - every entry exists in api/openapi.yaml;
//   - every operation in api/openapi.yaml has an entry, so the spec holds no
//     route the server dropped;
//   - every path literal in the routes*.go files falls under an entry, so a new
//     route cannot be added there without an entry here, and so a spec entry.
//
// CORS preflights (OPTIONS) are not listed. Add a route in the routes files, add
// its line here, then describe it in api/openapi.yaml.
const servedOperations = `
GET /api/v1/health
GET /api/v1/version
GET /api/v1/runtime-config
POST /api/v1/login
POST /api/v1/logout
GET /api/v1/me
GET /api/v1/openapi.yaml
GET /api/docs
GET /.well-known/iambarn-theme.json
GET /analytics.js
GET /api/v1/oidc/login
GET /api/v1/oidc/callback
GET /api/v1/oidc/logged-out
POST /api/v1/oidc/backchannel-logout
GET /api/v1/setup/{slug}
POST /api/v1/setup/{slug}/key-requests
GET /api/v1/setup/{slug}/key-requests/{id}
POST /api/v1/events
POST /api/v1/logs
POST /api/v1/alertmanager
POST /api/v1/analytics/collect
POST /api/v1/telemetry/security
POST /api/v1/telemetry/metrics
POST /api/v1/monitors/{slug}/check-ins
GET /api/v1/issues
GET /api/v1/issues/sparklines
GET /api/v1/issues/{issueId}
GET /api/v1/issues/{issueId}/events
GET /api/v1/issues/{issueId}/releases
POST /api/v1/issues/{issueId}/resolve
POST /api/v1/issues/{issueId}/reopen
PATCH /api/v1/issues/{issueId}/mute
PATCH /api/v1/issues/{issueId}/unmute
GET /api/v1/events/{eventId}
GET /api/v1/events/stream
GET /api/v1/events/timeline
GET /api/v1/live/events
GET /api/v1/logs
GET /api/v1/logs/histogram
GET /api/v1/logs/facets
GET /api/v1/logs/stream
GET /api/v1/projects
POST /api/v1/projects
GET /api/v1/projects/pending-count
PUT /api/v1/projects/{slug}
DELETE /api/v1/projects/{slug}
POST /api/v1/projects/{slug}/approve
PUT /api/v1/projects/{slug}/limits
PUT /api/v1/projects/{slug}/funnelbarn
GET /api/v1/projects/{slug}/silence-watch
PUT /api/v1/projects/{slug}/silence-watch
POST /api/v1/projects/{slug}/merge
GET /api/v1/groups
POST /api/v1/groups
DELETE /api/v1/groups/{slug}
GET /api/v1/groups/{slug}/projects
POST /api/v1/groups/{slug}/projects
DELETE /api/v1/groups/{slug}/projects/{projectSlug}
GET /api/v1/aliases
POST /api/v1/aliases
DELETE /api/v1/aliases/{alias}
GET /api/v1/releases
POST /api/v1/releases
GET /api/v1/releases/{releaseId}
PUT /api/v1/releases/{releaseId}
DELETE /api/v1/releases/{releaseId}
GET /api/v1/alerts
POST /api/v1/alerts
GET /api/v1/alerts/{alertId}
PUT /api/v1/alerts/{alertId}
DELETE /api/v1/alerts/{alertId}
GET /api/v1/settings
PUT /api/v1/settings
POST /api/v1/settings
GET /api/v1/source-maps
POST /api/v1/source-maps
GET /api/v1/apikeys
POST /api/v1/apikeys/revoke-setup
DELETE /api/v1/apikeys/{keyId}
GET /api/v1/key-requests
POST /api/v1/key-requests/{id}/approve
POST /api/v1/key-requests/{id}/deny
GET /api/v1/monitors
DELETE /api/v1/monitors/{slug}
GET /api/v1/detection/rules
PUT /api/v1/detection/rules/{ruleId}
DELETE /api/v1/detection/rules/{ruleId}
GET /api/v1/facets
GET /api/v1/facets/{key}
GET /api/v1/surfaces
GET /api/v1/analytics/overview
GET /api/v1/analytics/pages
GET /api/v1/analytics/timeline
GET /api/v1/analytics/referrers
GET /api/v1/analytics/segments
GET /api/v1/analytics/flow
GET /api/v1/analytics/scroll
GET /api/v1/analytics/dropout
GET /api/v1/telemetry/security
GET /api/v1/telemetry/hosts
GET /api/v1/telemetry/hosts/{host}/metrics
GET /api/v1/telemetry/hosts/{host}/series
POST /api/v1/telemetry
POST /api/v1/frontend-traces/api/v1/spans
POST /api/v1/client-errors
POST /api/v1/admin/digest
`

// specExcludedPrefixes are routed paths that api/openapi.yaml leaves out on
// purpose. Everything under /internal/ is reader-to-writer plumbing (HMAC-signed
// session and key-touch calls) or an operator database export, never part of the
// public API. Browser redirect endpoints are not excluded: the OIDC login,
// callback and logged-out routes are documented with their 302 responses.
var specExcludedPrefixes = []string{"/internal/"}

// routeLiteralPrefixes are the starts of the path literals the scan treats as
// routes. Other string literals in the routes files (the "/" of a prefix join,
// for instance) are not paths.
var routeLiteralPrefixes = []string{"/api/", "/analytics.js", "/.well-known/", "/internal/"}

func parseServedOperations(t *testing.T) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	for _, line := range strings.Split(servedOperations, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		method, path, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(path, "/") {
			t.Fatalf("malformed servedOperations line %q", line)
		}
		key := method + " " + path
		if ops[key] {
			t.Fatalf("duplicate servedOperations line %q", key)
		}
		ops[key] = true
	}
	return ops
}

var (
	specPathLine   = regexp.MustCompile(`^  (/[^\s:]*):\s*$`)
	specMethodLine = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
)

// specOperations reads the method and path pairs under `paths:` in the embedded
// spec. The file keeps a fixed layout (paths at two spaces, methods at four), so
// a line scan reads it without a YAML dependency.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	inPaths := false
	current := ""
	scanner := bufio.NewScanner(bytes.NewReader(openapispec.OpenAPISpec))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			inPaths = line == "paths:"
			current = ""
			continue
		}
		if !inPaths {
			continue
		}
		if m := specPathLine.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if m := specMethodLine.FindStringSubmatch(line); m != nil && current != "" {
			ops[strings.ToUpper(m[1])+" "+current] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("no operations found under paths: in api/openapi.yaml")
	}
	return ops
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// routePathLiterals returns every path the routes*.go files spell out, whether
// as a string literal or as the value of a string constant.
func routePathLiterals(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("routes*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no routes*.go files found: %v", err)
	}
	fset := token.NewFileSet()
	consts := map[string]ast.Expr{}
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if i < len(vs.Values) {
						consts[id.Name] = vs.Values[i]
					}
				}
			}
		}
	}

	var eval func(ast.Expr) (string, bool)
	eval = func(e ast.Expr) (string, bool) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return "", false
			}
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		case *ast.Ident:
			if inner, ok := consts[v.Name]; ok {
				return eval(inner)
			}
		case *ast.BinaryExpr:
			l, lok := eval(v.X)
			r, rok := eval(v.Y)
			return l + r, lok && rok && v.Op == token.ADD
		}
		return "", false
	}

	out := map[string]bool{}
	add := func(s string) {
		for _, p := range routeLiteralPrefixes {
			if strings.HasPrefix(s, p) {
				out[s] = true
				return
			}
		}
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.GenDecl:
				if v.Tok != token.CONST {
					return true
				}
				for _, spec := range v.Specs {
					for _, val := range spec.(*ast.ValueSpec).Values {
						if s, ok := eval(val); ok {
							add(s)
						}
					}
				}
				return false
			case *ast.BasicLit:
				if s, ok := eval(v); ok {
					add(s)
				}
			}
			return true
		})
	}
	return out
}

func isSpecExcluded(path string) bool {
	for _, p := range specExcludedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func TestServedOperationsAreInOpenAPISpec(t *testing.T) {
	t.Parallel()
	served := parseServedOperations(t)
	spec := specOperations(t)
	for _, op := range sortedKeys(served) {
		if !spec[op] {
			t.Errorf("%s is routed but missing from api/openapi.yaml", op)
		}
	}
	for _, op := range sortedKeys(spec) {
		if !served[op] {
			t.Errorf("%s is in api/openapi.yaml but not in servedOperations (stale spec entry or missing table line)", op)
		}
	}
}

func TestRoutePathLiteralsAreCoveredByServedOperations(t *testing.T) {
	t.Parallel()
	served := parseServedOperations(t)
	literals := routePathLiterals(t)
	if len(literals) < 30 {
		t.Fatalf("found %d path literals in routes*.go, the scan no longer matches the routing code", len(literals))
	}
	for _, lit := range sortedKeys(literals) {
		if isSpecExcluded(lit) {
			continue
		}
		path := strings.TrimSuffix(lit, "/")
		covered := false
		for op := range served {
			_, p, _ := strings.Cut(op, " ")
			if p == path || strings.HasPrefix(p, path+"/") {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("routes*.go serves %q, which has no line in servedOperations and no entry in api/openapi.yaml (add both, or list the prefix in specExcludedPrefixes with a reason)", lit)
		}
	}
}

// The excluded prefixes must still match something the routes serve, so the
// exclusion list cannot outlive the routes it covers.
func TestSpecExcludedPrefixesMatchRoutes(t *testing.T) {
	t.Parallel()
	literals := routePathLiterals(t)
	for _, prefix := range specExcludedPrefixes {
		found := false
		for lit := range literals {
			if strings.HasPrefix(lit, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("specExcludedPrefixes entry %q matches no path in routes*.go; remove it", prefix)
		}
	}
}
