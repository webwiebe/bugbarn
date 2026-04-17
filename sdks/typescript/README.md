# BugBarn TypeScript SDK

Small Node.js client for sending uncaught exceptions and captured errors to BugBarn.

## Hosted package

BugBarn serves a packed SDK tarball from its web container:

```sh
pnpm add https://bugbarn.test.wiebe.xyz/packages/typescript/bugbarn-typescript-0.1.0.tgz
```

Use the staging host when wiring a staging app:

```sh
pnpm add https://bugbarn.staging.wiebe.xyz/packages/typescript/bugbarn-typescript-0.1.0.tgz
```

This keeps Rapid Root on a normal package-manager dependency without publishing to the public npm registry.

The package exposes both ESM and CommonJS entrypoints, so bundlers and Node runtimes can choose `import` or `require` without custom externalization rules.

Captured events can carry `release` and `dist` metadata. Set them during `init()` or via `BUGBARN_RELEASE` and `BUGBARN_DIST` so later source-map uploads can be matched to the same build.

## Local package

For local testing without the deployed web container, build a local tarball from this repository:

```sh
cd /Users/wiebe/webwiebe/temu-sentry/sdks/typescript
npm install
npm run build
npm pack
```

Install it in Rapid Root or another local project:

```sh
cd /Users/wiebe/webwiebe/rapid-root
pnpm add /Users/wiebe/webwiebe/temu-sentry/sdks/typescript/bugbarn-typescript-0.1.0.tgz
```

This local tarball path is intended for local testing only. Rapid Root uses pnpm with frozen lockfiles in CI and Docker builds, so testing/staging should use the hosted tarball URL or a package registry.

## Usage

```ts
import { init } from "@bugbarn/typescript";

init({
  apiKey: process.env.BUGBARN_API_KEY ?? "",
  endpoint: "https://bugbarn.test.wiebe.xyz/api/v1/events",
  release: process.env.BUGBARN_RELEASE,
  dist: process.env.BUGBARN_DIST,
});
```

The SDK installs `uncaughtException` and `unhandledRejection` handlers by default. Pass `installDefaultHandlers: false` when you only want manual capture calls. Before exiting short-lived scripts, call `flush(timeoutMs)` or `shutdown(timeoutMs)` so queued events have a bounded amount of time to leave the process.

```ts
import { captureException, flush, init } from "@bugbarn/typescript";

init({
  apiKey: process.env.BUGBARN_API_KEY ?? "",
  endpoint: "https://bugbarn.test.wiebe.xyz/api/v1/events",
  installDefaultHandlers: false,
});

try {
  throw new Error("manual capture");
} catch (error) {
  await captureException(error, {
    tags: {
      app: "rapid-root",
      environment: "testing",
    },
  });
  await flush(2000);
}
```

## Source map uploads

BugBarn expects source map artifacts to be posted to `POST /api/v1/source-maps` on the same backend that receives events. The upload helper sends `multipart/form-data` with:

- `release`
- `dist` when present
- `bundle_url`
- `source_map` as the uploaded artifact

Example:

```ts
import { readFile } from "node:fs/promises";
import { uploadSourceMap } from "@bugbarn/typescript";

await uploadSourceMap({
  apiKey: process.env.BUGBARN_API_KEY ?? "",
  endpoint: "https://bugbarn.test.wiebe.xyz/api/v1/source-maps",
  release: process.env.BUGBARN_RELEASE ?? "",
  dist: process.env.BUGBARN_DIST,
  bundleUrl: "https://example.test/assets/app.js",
  sourceMap: await readFile("./dist/app.js.map", "utf8"),
});
```

Read the testing API key from the cluster:

```sh
kubectl -n bugbarn-testing get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo
```
