# Data Model: Personal Error Tracker Foundation

## User

- `id`: stable identifier
- `username`: unique login name
- `password_hash`: password hash, never plaintext
- `created_at`
- `updated_at`

## Project

- `id`
- `name`
- `slug`
- `created_at`
- `updated_at`

## ApiKey

- `id`
- `project_id`
- `name`
- `key_hash`
- `prefix`
- `last_used_at`
- `created_at`
- `revoked_at`

## RawIngestRecord

Represents the durable request-path record before processing.

- `id`
- `project_id`
- `received_at`
- `content_type`
- `payload_size`
- `payload_ref`: spool segment and offset, or equivalent
- `remote_addr_hash`
- `status`: queued, processing, processed, failed, dead_letter
- `attempt_count`
- `last_error`

## Event

One occurrence after normalization and scrubbing.

- `id`
- `project_id`
- `issue_id`
- `raw_ingest_record_id`
- `received_at`
- `observed_at`
- `severity`
- `message`
- `exception_type`
- `exception_message`
- `stacktrace_json`
- `resource_json`
- `attributes_json`
- `scrubbed_payload_json`
- `trace_id`
- `span_id`
- `release`
- `environment`
- `host`
- `fingerprint`
- `fingerprint_material`
- `fingerprint_explanation_json`
- `is_regression`

## Issue

Deduplicated group of related events.

- `id`
- `project_id`
- `fingerprint`
- `title`
- `exception_type`
- `normalized_message`
- `representative_event_id`
- `severity`
- `first_seen_at`
- `last_seen_at`
- `event_count`
- `status`: unresolved, resolved, ignored
- `resolved_at`
- `reopened_at`
- `last_regressed_at`
- `regression_count`
- `fingerprint_material`
- `fingerprint_explanation_json`
- `created_at`
- `updated_at`

## FacetKey

Registry of queryable event attributes.

- `id`
- `project_id`
- `path`: canonical dotted path such as `http.status_code`
- `value_type`: string, number, boolean, timestamp
- `display_name`
- `first_seen_at`
- `last_seen_at`
- `indexed`: whether the worker currently maintains query support

## EventFacet

Queryable event facet values.

- `event_id`
- `facet_key_id`
- `value_string`
- `value_number`
- `value_bool`
- `value_hash`

## ReleaseMarker

Deploy or notable-event record used to correlate regressions with recent changes.

- `id`
- `project_id`
- `name`
- `environment`
- `observed_at`
- `version`
- `commit_sha`
- `url`
- `notes`
- `created_by`
- `created_at`

## AlertRule

User-configured alert definition for issue or event patterns.

- `id`
- `project_id`
- `name`
- `condition`
- `query`
- `target`
- `enabled`
- `last_triggered_at`
- `created_at`
- `updated_at`

## UserSetting

Per-user or workspace preference stored by the settings view.

- `id`
- `user_id`
- `display_name`
- `timezone`
- `default_environment`
- `live_window_minutes`
- `stacktrace_context_lines`
- `created_at`
- `updated_at`

## SourceMapArtifact

Uploaded source map artifact for later symbolication and traceback rendering.

- `id`
- `project_id`
- `release`
- `dist`
- `bundle_url`
- `name`
- `content_type`
- `source_map_blob`
- `size_bytes`
- `created_at`

## Relationships

- A project has many API keys, issues, events, and facet keys.
- An issue has many events.
- An event belongs to one project and usually one issue.
- An event has many event facets.
- Facet keys are discovered from scrubbed event attributes and resource data.
- Release markers belong to one project and are displayed near issues/events by time and environment.
- Alert rules belong to one project and are evaluated against issue/event activity.
- User settings may be scoped per user or workspace depending on deployment configuration.
- Source map artifacts belong to one project and can later be used for traceback symbolication.
