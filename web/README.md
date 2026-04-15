# Web UI

This directory holds the TypeScript source for the BugBarn web app.

Source lives in `src/` and the browser loads the generated output from `dist/`.

## Commands

```bash
cd web
npm install
npm run build
npm run test
```

`npm run build` compiles `src/app.ts` to `dist/app.js`. `npm run test` runs the TypeScript compiler in no-emit mode and is the fastest way to check the web package.

## Local Run

After building, serve this directory with any static server:

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
