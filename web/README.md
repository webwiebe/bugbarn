# Web UI

This directory contains the static BugBarn UI.

Run it locally with any static server:

```bash
cd web
python3 -m http.server 4173
```

Open `http://localhost:4173/` in a browser. By default the UI talks to the same origin. To point it at a separate API, set the `api` query parameter, for example:

```text
http://localhost:4173/?api=http://localhost:8080
```

The UI expects these endpoints when they are available:

- `GET /api/v1/issues`
- `GET /api/v1/issues/{id}`
- `GET /api/v1/issues/{id}/events`
- `GET /api/v1/events/{id}`
- `GET /api/v1/live/events`
