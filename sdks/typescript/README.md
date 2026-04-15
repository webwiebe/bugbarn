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
});
```

The SDK installs `uncaughtException` and `unhandledRejection` handlers by default. Pass `installDefaultHandlers: false` when you only want manual capture calls.

```ts
import { captureException, init } from "@bugbarn/typescript";

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
}
```

Read the testing API key from the cluster:

```sh
kubectl -n bugbarn-testing get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo
```
