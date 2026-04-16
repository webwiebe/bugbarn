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

## Relationships

- A project has many API keys, issues, events, and facet keys.
- An issue has many events.
- An event belongs to one project and usually one issue.
- An event has many event facets.
- Facet keys are discovered from scrubbed event attributes and resource data.
- Release markers belong to one project and are displayed near issues/events by time and environment.
