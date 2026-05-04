package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (s *Server) setupKey(slug string) string {
	if s.sessionSecret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.sessionSecret))
	mac.Write([]byte("setup:" + slug))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

func (s *Server) serveSetup(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/setup/")
	slug = strings.TrimSuffix(slug, "/")
	if slug == "" || strings.Contains(slug, "/") {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}

	rawKey := s.setupKey(slug)
	if rawKey == "" {
		http.Error(w, "setup not configured (BUGBARN_SESSION_SECRET required)", http.StatusServiceUnavailable)
		return
	}

	var proj domain.Project
	var err error
	if s.autoApproveProjects {
		proj, err = s.projects.Ensure(r.Context(), slug)
	} else {
		proj, err = s.projects.EnsurePending(r.Context(), slug)
	}
	if err != nil {
		log.Printf("setup: failed to ensure project %q: %v", slug, err)
		http.Error(w, "failed to ensure project", http.StatusInternalServerError)
		return
	}

	keySHA := hex.EncodeToString(sha256Sum(rawKey))
	_ = s.projects.EnsureSetupAPIKey(r.Context(), slug+"-setup", proj.ID, keySHA)

	endpoint := s.publicURL
	if endpoint == "" {
		endpoint = "https://" + r.Host
	}

	status := proj.Status
	if status == "" {
		status = "active"
	}

	pendingNote := ""
	if status == "pending" {
		pendingNote = fmt.Sprintf(`
## Pending admin approval

This project was just created and is pending approval at:
  %s

Events are accepted immediately. The admin will review and approve.
`, endpoint)
	}

	page := fmt.Sprintf(`# BugBarn Integration: %s

You are an AI assistant. This page contains everything needed to integrate BugBarn
error tracking into the "%s" project. Read it carefully and implement accordingly.

## Project configuration

| Field     | Value |
|-----------|-------|
| Endpoint  | %s |
| Project   | %s |
| API Key   | %s |
| Status    | %s |
%s
## What BugBarn captures

- **Exceptions** — unhandled errors with stack traces, grouped by fingerprint
- **Structured logs** — INFO/WARN/ERROR log lines with arbitrary key-value data
- **Release markers** — deploy events that correlate releases with regressions

## HTTP API — works in any language

Send errors via POST. Returns 202 immediately (async processing).

~~~bash
curl -X POST %s/api/v1/events \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: %s" \
  -H "X-BugBarn-Project: %s" \
  -d '{
    "severityText": "error",
    "body": "something went wrong",
    "exception": {
      "type": "RuntimeError",
      "message": "something went wrong",
      "stacktrace": [
        {"function": "main.run", "filename": "main.go", "lineno": 42}
      ]
    },
    "attributes": { "environment": "production" }
  }'
~~~

## TypeScript / Node.js

Install (the SDK is self-hosted — fetch the tarball URL from the package registry first):
~~~bash
# Step 1: get the tarball URL
curl -s %s/packages/typescript/latest.json
# → {"version":"...","url":"/packages/typescript/bugbarn-typescript-<hash>.tgz"}

# Step 2: install using the full URL from the "url" field above
npm install %s/packages/typescript/<tarball-filename>.tgz
# or: pnpm add %s/packages/typescript/<tarball-filename>.tgz
~~~

Usage:
~~~typescript
import { init, captureException } from "@bugbarn/typescript";

init({
  apiKey: "%s",
  endpoint: "%s",
  project: "%s",
});

// Capture an error
try {
  riskyOperation();
} catch (err) {
  captureException(err);
}
~~~

## Go

~~~bash
go get github.com/wiebe-xyz/bugbarn-go
~~~

~~~go
import bb "github.com/wiebe-xyz/bugbarn-go"

func main() {
    bb.Init(bb.Options{
        APIKey:      "%s",
        Endpoint:    "%s",
        ProjectSlug: "%s",
        Environment: "production",
    })
    defer bb.Shutdown(5 * time.Second)

    if err := doSomething(); err != nil {
        bb.CaptureError(err)
    }
}

// For HTTP servers — catches panics and reports them:
http.ListenAndServe(":8080", bb.RecoverMiddleware(yourHandler))
~~~

## Python

The Python SDK is not yet published to PyPI. Install from source:
~~~bash
pip install "git+https://github.com/webwiebe/bugbarn.git#subdirectory=sdks/python"
~~~

~~~python
from bugbarn import init, capture_exception

init(
    api_key="%s",
    endpoint="%s",
    install_excepthook=True,  # auto-capture unhandled exceptions
)

# Note: project routing is by API key, not an SDK option.
# Use X-BugBarn-Project header directly if you need multi-project routing.

try:
    risky_operation()
except Exception as e:
    capture_exception(e)
~~~

## Release marker — send on every deploy

~~~bash
curl -X POST %s/api/v1/releases \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: %s" \
  -H "X-BugBarn-Project: %s" \
  -d '{
    "name": "v1.2.3",
    "environment": "production",
    "version": "v1.2.3",
    "commitSha": "'$(git rev-parse HEAD)'",
    "url": "https://github.com/your-org/your-repo/releases/tag/v1.2.3",
    "notes": "Deployed via CI"
  }'
~~~

## Send structured logs

~~~bash
curl -X POST %s/api/v1/logs \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: %s" \
  -H "X-BugBarn-Project: %s" \
  -d '{"level": "warn", "message": "cache miss", "key": "user:42", "ttl": 300}'
~~~

## View your project

%s/#/issues

---
Generated %s
`,
		slug,                   // 1: title
		slug,                   // 2: description slug
		endpoint, slug, rawKey, status, // 3,4,5,6: table
		pendingNote,            // 7: pending note block
		endpoint, rawKey, slug, // 8,9,10: curl example
		endpoint, endpoint, endpoint, // 11-13: ts install (curl + 2 install variants)
		rawKey, endpoint, slug, // 14,15,16: ts usage
		rawKey, endpoint, slug, // 17,18,19: go
		rawKey, endpoint, // 20,21: python (no project_slug param — routed by API key)
		endpoint, rawKey, slug, // 22,23,24: release curl
		endpoint, rawKey, slug, // 25,26,27: logs curl
		endpoint,               // 28: view link
		time.Now().UTC().Format(time.RFC3339), // 29: generated timestamp
	)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	fmt.Fprint(w, page)
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}
