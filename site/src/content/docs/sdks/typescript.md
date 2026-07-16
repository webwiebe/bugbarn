---
title: TypeScript SDK
description: Integrate BugBarn error tracking into TypeScript and JavaScript applications.
---

# TypeScript SDK

The TypeScript SDK (`@bugbarn/typescript`) works in both Node.js and browser environments. It supports automatic uncaught exception handling, breadcrumbs, user context, and source map uploads.

## Installation

```bash
npm install @bugbarn/typescript
```

The package ships ESM and CJS builds. Requires Node.js 22+ for the Node.js runtime.

## Initialisation

```typescript
import * as bugbarn from "@bugbarn/typescript";

bugbarn.init({
  apiKey: "your-api-key",
  endpoint: "https://bugbarn.example.com/api/v1/events",
  project: "my-app",           // optional -- route events to a specific project
  release: "1.2.3",            // optional -- tag events with a release version
  dist: "web",                 // optional -- distribution variant
  installDefaultHandlers: true, // default true -- installs uncaughtException/unhandledRejection handlers
  autoBreadcrumbs: true,        // default true -- intercepts console, fetch, XHR, and navigation
});
```

### Options reference

| Option | Type | Default | Description |
|---|---|---|---|
| `apiKey` | `string` | required | Project API key |
| `endpoint` | `string` | `/api/v1/events` | BugBarn ingest URL |
| `project` | `string` | `undefined` | Project slug for event routing |
| `release` | `string` | `BUGBARN_RELEASE` env | Release identifier |
| `dist` | `string` | `BUGBARN_DIST` env | Distribution variant |
| `installDefaultHandlers` | `boolean` | `true` | Install `uncaughtException` and `unhandledRejection` handlers |
| `autoBreadcrumbs` | `boolean` | `true` | Auto-capture console, fetch, XHR, and navigation breadcrumbs |
| `transport` | `Transport` | built-in | Custom transport implementation |

## Capturing errors

### captureException

Capture any error (Error objects, strings, or unknown values are normalised automatically).

```typescript
try {
  riskyOperation();
} catch (err) {
  await bugbarn.captureException(err);
}
```

### Capture options

Pass extra context with each capture call:

```typescript
await bugbarn.captureException(err, {
  attributes: { orderId: 42 },
  tags: { environment: "staging" },
  extra: { debugInfo: "additional context" },
  release: "1.2.3",
  dist: "web",
});
```

| Option | Type | Description |
|---|---|---|
| `attributes` | `Record<string, unknown>` | Arbitrary key-value metadata |
| `tags` | `Record<string, string \| number \| boolean \| null>` | Indexed tags for filtering |
| `extra` | `Record<string, unknown>` | Additional unindexed context |
| `release` | `string` | Override the global release for this event |
| `dist` | `string` | Override the global dist for this event |

## Uncaught exception handling

When `installDefaultHandlers` is `true` (the default), the SDK installs handlers for:

- **`uncaughtException`** -- captures the error, flushes, then exits with code 1
- **`unhandledRejection`** -- captures the rejection reason, flushes, then exits with code 1
- **`beforeExit`** -- flushes remaining events before the process exits

To disable this and handle errors manually:

```typescript
bugbarn.init({
  apiKey: "...",
  endpoint: "...",
  installDefaultHandlers: false,
});
```

## Breadcrumbs

Breadcrumbs record a trail of events leading up to an error. They are automatically captured for:

- **Console** -- `console.log`, `console.warn`, `console.error`, etc.
- **HTTP** -- `fetch` and `XMLHttpRequest` calls (method, URL, status code)
- **Navigation** -- `pushState`, `replaceState`, and `hashchange` events (browser)

### Manual breadcrumbs

```typescript
bugbarn.addBreadcrumb({
  timestamp: new Date().toISOString(),
  category: "manual",
  message: "User clicked checkout",
  level: "info",
  data: { cartItems: 3 },
});
```

### Clear breadcrumbs

```typescript
bugbarn.clearBreadcrumbs();
```

## User context

Attach user information to all subsequent events:

```typescript
bugbarn.setUser({ id: "u_123", email: "jane@example.com", username: "jane" });
```

Clear user context on logout:

```typescript
bugbarn.clearUser();
```

## Source maps

Upload source maps so BugBarn can show original file names and line numbers for minified code.

### Single upload

```typescript
import { uploadSourceMap } from "@bugbarn/typescript";

await uploadSourceMap({
  apiKey: "your-api-key",
  endpoint: "https://bugbarn.example.com/api/v1/source-maps",
  release: "1.2.3",
  dist: "web",                    // optional
  project: "my-app",              // optional
  bundleUrl: "https://example.com/assets/app.min.js",
  sourceMapPath: "./dist/app.min.js.map",  // Node.js file path
  sourceMapName: "app.min.js.map",          // optional display name
});
```

### Build script helper

Use `createSourceMapUploader` for uploading multiple maps with shared configuration:

```typescript
import { createSourceMapUploader } from "@bugbarn/typescript";

const upload = createSourceMapUploader({
  apiKey: "your-api-key",
  endpoint: "https://bugbarn.example.com/api/v1/source-maps",
  release: "1.2.3",
  project: "my-app",
});

await upload({
  bundleUrl: "https://example.com/assets/app.js",
  sourceMapPath: "./dist/app.js.map",
});

await upload({
  bundleUrl: "https://example.com/assets/vendor.js",
  sourceMapPath: "./dist/vendor.js.map",
});
```

## Pino log transport

Forward structured logs to BugBarn using the built-in Pino destination:

```typescript
import pino from "pino";
import { createBugBarnDestination } from "@bugbarn/typescript";

// Single destination -- all logs go to BugBarn
const logger = pino(
  createBugBarnDestination({
    endpoint: "https://bugbarn.example.com/api/v1/logs",
    apiKey: "your-api-key",
  })
);

// Multiple destinations -- stdout + BugBarn for warnings and above
const logger = pino(
  pino.multistream([
    { stream: pino.destination(1) },
    {
      stream: createBugBarnDestination({
        endpoint: "https://bugbarn.example.com/api/v1/logs",
        apiKey: "your-api-key",
      }),
      level: "warn",
    },
  ])
);
```

### Pino transport options

| Option | Type | Default | Description |
|---|---|---|---|
| `endpoint` | `string` | required | BugBarn logs endpoint |
| `apiKey` | `string` | required | Project API key |
| `project` | `string` | — | Project slug, sent as `X-BugBarn-Project`. Optional with a project-scoped API key; required with a global key |
| `flushIntervalMs` | `number` | `1000` | Batch flush interval in milliseconds |
| `batchSize` | `number` | `50` | Max logs per batch before immediate flush |
| `level` | `string` | — | Minimum level to send (`trace`…`fatal`). Entries below it are dropped before batching |

Logs must resolve to a project: use a project-scoped API key, or set `project`.
A batch that resolves to neither is rejected with a `400`.

## Flush and shutdown

### flush

Drain all queued events. Returns `true` if fully drained within the timeout.

```typescript
const drained = await bugbarn.flush(5000); // 5 second timeout
```

### shutdown

Flush and detach the transport. Call before process exit.

```typescript
await bugbarn.shutdown(2000);
```

## Full example (Node.js)

```typescript
import * as bugbarn from "@bugbarn/typescript";

bugbarn.init({
  apiKey: process.env.BUGBARN_API_KEY!,
  endpoint: process.env.BUGBARN_ENDPOINT!,
  release: "1.0.0",
});

bugbarn.setUser({ id: "u_42", email: "dev@example.com" });

try {
  throw new Error("something broke");
} catch (err) {
  await bugbarn.captureException(err, {
    attributes: { component: "checkout" },
    tags: { severity: "high" },
  });
}

await bugbarn.shutdown(2000);
```

## Full example (Browser)

```html
<script type="module">
  import * as bugbarn from "@bugbarn/typescript";

  bugbarn.init({
    apiKey: "your-api-key",
    endpoint: "https://bugbarn.example.com/api/v1/events",
    installDefaultHandlers: false,  // no process handlers in browser
  });

  window.addEventListener("error", (event) => {
    bugbarn.captureException(event.error);
  });

  window.addEventListener("unhandledrejection", (event) => {
    bugbarn.captureException(event.reason);
  });
</script>
```
