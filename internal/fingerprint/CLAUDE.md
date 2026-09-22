# Grouping: fingerprint, normalize, privacy

Grouping events into issues is the product. An issue is one row per
`(project_id, fingerprint)` (`UNIQUE` in `storage/migrations/00001_initial_schema.sql`),
so whatever changes the fingerprint changes which issue an event lands in.

## Pipeline order

Both consumers (`cmd/bugbarn/worker.go` for the file spool, `internal/ingestproc` for the
Redis queue) run the same steps through `worker.ProcessRecord`:

1. `normalize.Normalize` maps the SDK payload onto `event.Event`. It calls
   `privacy.Scrub` first.
2. Fingerprint: the SDK-supplied `fingerprint` field if non-empty, used verbatim;
   otherwise `fingerprint.Fingerprint(evt)`. Material and explanation are always computed
   from the event.
3. `worker.SymbolicateEvent` fills `Original*` and `Snippet` on `.js`/`.mjs` frames.
4. `storage.PersistProcessedEvent` upserts the issue by fingerprint.

Symbolication runs **after** fingerprinting and never touches `File`/`Function`/`Module`,
so JS issues group on minified frames. Uploading a source map later does not regroup
anything.

The ingest HTTP handler only runs the cheap `normalize.Validate`; the full pipeline runs
on the consumer side.

## What goes into the hash

`sha256` over a JSON material string (`fingerprint.go`), with no version field and no salt:

- `exceptionType`: normalised `Exception.Type`
- `message`: normalised `Exception.Message`, falling back to `Message`
- `stacktrace`: every frame in order as `module:function:file`. No in-app/library split,
  no frame limit. Line and column are excluded, so a code move does not split an issue.
- `context`: a small allowlist of scalar resource/attribute keys (`environment`,
  `service.name`, `http.route`, `http.status_code`, `region`, ...). See `stableContextKeys`.
- Browser fallback: when type, message and stack are all empty (promise rejections,
  cross-origin errors arriving as `exception: {}`), `RawScrubbed.name` / `properties.*`
  stand in.

`normalize()` in `fingerprint.go` lowercases and replaces UUIDs, IPs, hex ids, numbers,
`:line:col` and the privacy placeholders with tokens. `storage/issues_insert.go`
(`normalizeTitle`) and `internal/issues` have their own, slightly different regex set for
the normalised issue title. Those do not affect grouping.

`internal/normalize` only maps fields (`body`/`message`/`exception.message`, frame key
aliases, severity canonicalisation, breadcrumb cap of 100). It does not rewrite values.

## Privacy

`privacy.Scrub` redacts values under sensitive keys (`authorization`, `cookie`,
`password`, `token`, `session`, `email`, ...) and rewrites emails, IPs, UUIDs and
bearer-style secrets inside strings. `Normalize` reads `Message`, severity, timestamps and
breadcrumbs from the **unscrubbed** payload; attributes, exception, user and `RawScrubbed`
come from the scrubbed map.

## Changing the algorithm regroups production

There is no algorithm version. On every writable `storage.Open`,
`migrateFingerprints` (`storage/migrate_fingerprints.go`) re-hashes each issue's
`representative_event_json` in the background. If the new hash matches another issue it
**merges** the two (moves events, unions facets, deletes the old issue); otherwise it
rewrites the fingerprint in place. Merges cannot be undone. A change here is a data
migration of every issue in production: say so in the PR and expect the merge storm on the
first writer start after deploy.

The same migration ignores SDK-supplied fingerprints: `Fingerprint(evt)` hashes the event
material, so an override like `alertmanager:abc` is recomputed and rewritten on writer
start. Tests that set explicit fingerprints open the store with `autoMigrate=false` for
this reason.

## Tests

`fingerprint_test.go` pins grouping as equal/different pairs (volatile ids, source
locations, bare hex, count variance, raw-scrubbed fallback).
`worker/processor_test.go` covers the SDK override. `migrateFingerprints` and
`internal/sourcemap` have no tests. Add a pair to `fingerprint_test.go` for any grouping
change, both the case that should now merge and one that must stay apart.
