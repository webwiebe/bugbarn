package api

import (
	"fmt"
	"strings"
	"time"
)

// setupPage holds what the integration page interpolates. The page is the text an
// integrating agent follows, so it is written section by section, each a short
// function, and TestServeSetupPageGolden pins the exact bytes.
type setupPage struct {
	slug, endpoint, rawKey, status string
	// pendingNote and viewSection are whole blocks, empty or newline-led.
	pendingNote, viewSection string
}

func (p setupPage) render(now time.Time) string {
	var b strings.Builder
	for _, section := range []string{
		p.intro(),
		p.httpAPI(),
		p.typescript(),
		p.goSDK(),
		p.python(),
		p.release(),
		p.logs(),
	} {
		b.WriteString(section)
	}
	b.WriteString(p.viewSection)
	b.WriteString("\n---\nGenerated " + now.Format(time.RFC3339) + "\n")
	return b.String()
}

func (p setupPage) intro() string {
	return fmt.Sprintf(`# BugBarn Integration: %s

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
`, p.slug, p.slug, p.endpoint, p.slug, p.rawKey, p.status, p.pendingNote)
}

func (p setupPage) httpAPI() string {
	return fmt.Sprintf(`
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
`, p.endpoint, p.rawKey, p.slug)
}

func (p setupPage) typescript() string {
	return fmt.Sprintf(`
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
`, p.endpoint, p.endpoint, p.endpoint, p.rawKey, p.endpoint, p.slug)
}

func (p setupPage) goSDK() string {
	return fmt.Sprintf(`
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
`, p.rawKey, p.endpoint, p.slug)
}

// python has no project argument: the SDK routes by API key.
func (p setupPage) python() string {
	return fmt.Sprintf(`
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
`, p.rawKey, p.endpoint)
}

func (p setupPage) release() string {
	return fmt.Sprintf(`
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
`, p.endpoint, p.rawKey, p.slug)
}

func (p setupPage) logs() string {
	return fmt.Sprintf(`
## Send structured logs

~~~bash
curl -X POST %s/api/v1/logs \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: %s" \
  -H "X-BugBarn-Project: %s" \
  -d '{"level": "warn", "message": "cache miss", "key": "user:42", "ttl": 300}'
~~~
`, p.endpoint, p.rawKey, p.slug)
}
