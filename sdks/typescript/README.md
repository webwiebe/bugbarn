# BugBarn TypeScript SDK

Small Node.js client for sending uncaught exceptions and captured errors to BugBarn.

## Local package

Until BugBarn publishes packages to a registry, build a local tarball from this repository:

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

This tarball path is intended for local testing. Rapid Root uses pnpm with frozen lockfiles in CI and Docker builds, so a durable testing/staging integration should either vendor this SDK as a Rapid Root workspace package or publish `@bugbarn/typescript` to a package registry first.

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
