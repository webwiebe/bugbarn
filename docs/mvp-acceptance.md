# MVP Acceptance Checklist

Use this as the short validation gate for a local BugBarn build.

- [ ] `POST /api/v1/events` accepts authenticated requests with the BugBarn API key header.
- [ ] Canonical, minimal, malformed best-effort, and sender-specific fixtures exist under `specs/001-personal-error-tracker/fixtures/`.
- [ ] The TypeScript sample app can capture an error against local BugBarn.
- [ ] The Python sample app can capture an error against local BugBarn.
- [ ] The load generator can send repeated events without external dependencies.
- [ ] The backlog only keeps PHP as a future SDK item.

