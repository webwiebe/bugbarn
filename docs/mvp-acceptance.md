# MVP Acceptance Checklist

Use this as the short validation gate for a local BugBarn build.

- [x] `POST /api/v1/events` accepts authenticated requests with the BugBarn API key header.
- [x] Canonical, minimal, malformed best-effort, and sender-specific fixtures exist under `specs/001-personal-error-tracker/fixtures/`.
- [x] The TypeScript sample app can capture an error against local BugBarn.
- [x] The Python sample app can capture an error against local BugBarn.
- [x] The load generator can send repeated events without external dependencies.
- [x] The backlog only keeps PHP as a future SDK item.
