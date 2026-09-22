# selflog: BugBarn reporting to itself

`Handler` wraps the process's `slog.Handler`. On every record at `Level >= Error` it calls
`bb.CaptureMessage` (message plus the `error` attr) before passing the record on. Capture is
fire-and-forget with a 2s timeout, so a slow or down ingest endpoint delays a log call by
at most 2s and never fails it.

It is installed only when both `BUGBARN_SELF_ENDPOINT` and `BUGBARN_SELF_API_KEY` are set
(`BUGBARN_SELF_PROJECT` picks the project slug), in `cmd/bugbarn/main.go` (single-process
and writer) and `cmd/bugbarn/reader.go` (reader). Both call `slog.SetDefault`, so
package-level `slog.Error` calls are captured too.

## The cycle

`bb` is `github.com/wiebe-xyz/bugbarn-go`, and `go.mod` has
`replace github.com/wiebe-xyz/bugbarn-go => ./sdks/go`. The server is built against the SDK
in this repo, so an SDK change ships in the next server build and is dogfooded before any
SDK release. It also means a broken `sdks/go` breaks the server build.

Captured messages go to the BugBarn instance at `BUGBARN_SELF_ENDPOINT`, which in production is
itself. They show up as issues in project `bugbarn-service`
(`bb issues --project bugbarn-service`).

## Rules

- An error path that does not go through `slog` at ERROR is invisible. `log.Printf`,
  `fmt.Fprintln(os.Stderr, ...)` and a returned-then-dropped error never reach this handler.
- ERROR means something was lost: an event, a mutation, a notification. Cancellations,
  shutdowns, 404s and client disconnects file bugs against ourselves if logged at ERROR;
  log them at WARN or below. Loops over projects must stop on `ctx.Err()` instead of
  logging one ERROR per remaining project.
- Every ERROR creates or bumps an issue. Only the message and the `error` attr are sent;
  other attrs stay in the local log line.
