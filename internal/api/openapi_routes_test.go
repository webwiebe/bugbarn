package api

import (
	"bufio"
	"bytes"
	"regexp"
	"sort"
	"strings"
	"testing"

	openapispec "github.com/wiebe-xyz/bugbarn/api"
)

// The routes registered on the server's ServeMux (routes*.go) are the source of
// truth for what the API serves. Two tests keep api/openapi.yaml honest:
//
//   - every registered operation is described in api/openapi.yaml;
//   - every operation in api/openapi.yaml is registered, so the spec holds no
//     route the server dropped.
//
// Path parameters are compared by position, not by name: the spec says
// {issueId} where the pattern says {id}. Preflights, wrong-method answers and
// trailing-slash twins are registered as fallbacks and are not operations.

// specExcludedPrefixes are routed paths that api/openapi.yaml leaves out on
// purpose. Everything under /internal/ is reader-to-writer plumbing (HMAC-signed
// session and key-touch calls) or an operator database export, never part of the
// public API. Browser redirect endpoints are not excluded: the OIDC login,
// callback and logged-out routes are documented with their 302 responses.
var specExcludedPrefixes = []string{"/internal/"}

var patternWildcard = regexp.MustCompile(`\{[^}]*\}`)

// normalizePath replaces every wildcard name with {} so a pattern and a spec
// path compare equal when they differ only in parameter names.
func normalizePath(path string) string { return patternWildcard.ReplaceAllString(path, "{}") }

// servedOperations lists the "METHOD /path" of every registered operation the
// spec is expected to describe, with normalized wildcards.
func servedOperations(t *testing.T) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	for _, pattern := range (&Server{}).routePatterns() {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || !strings.HasPrefix(path, "/") {
			t.Fatalf("operation %q has no method; register a fallback instead", pattern)
		}
		if isSpecExcluded(path) {
			continue
		}
		ops[method+" "+normalizePath(path)] = true
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
			ops[strings.ToUpper(m[1])+" "+normalizePath(current)] = true
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
	served := servedOperations(t)
	if len(served) < 80 {
		t.Fatalf("only %d operations registered; the route table lost routes", len(served))
	}
	spec := specOperations(t)
	for _, op := range sortedKeys(served) {
		if !spec[op] {
			t.Errorf("%s is routed but missing from api/openapi.yaml", op)
		}
	}
	for _, op := range sortedKeys(spec) {
		if !served[op] {
			t.Errorf("%s is in api/openapi.yaml but no route serves it (stale spec entry)", op)
		}
	}
}

// The excluded prefixes must still match a route, so the exclusion list cannot
// outlive the routes it covers.
func TestSpecExcludedPrefixesMatchRoutes(t *testing.T) {
	t.Parallel()
	rt := (&Server{}).router()
	all := append(append([]string(nil), rt.operations...), rt.fallbacks...)
	for _, prefix := range specExcludedPrefixes {
		found := false
		for _, pattern := range all {
			if _, path, _ := strings.Cut(pattern, " "); strings.HasPrefix(path, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("specExcludedPrefixes entry %q matches no registered route; remove it", prefix)
		}
	}
}
