# CI/CD Integration

## Prerequisites

Every pipeline that talks to BugBarn needs an API key. Create one with the CLI (run inside the BugBarn pod or via `kubectl exec`):

```sh
bugbarn apikey create --project <project-slug> --name "my-app-staging"
# Key (shown once, store securely): <64-hex-char string>
```

Store that value as a **repository secret** (e.g. `BUGBARN_API_KEY`) and the BugBarn base URL as a **repository variable** (e.g. `BUGBARN_ENDPOINT`).

---

## Multi-Project Setup

BugBarn isolates data by **project**. Every API key is bound to exactly one project at creation time — all events, releases, and source maps ingested with that key are stored under the corresponding project.

This model maps naturally onto monorepos with multiple sites or services: create one BugBarn project per component and one API key per component per environment.

### Create projects

```sh
# Create projects via the CLI (run inside the BugBarn pod)
kubectl -n bugbarn exec deploy/bugbarn -- bugbarn project create --slug frontend --name "Frontend"
kubectl -n bugbarn exec deploy/bugbarn -- bugbarn project create --slug backend  --name "Backend"
kubectl -n bugbarn exec deploy/bugbarn -- bugbarn project create --slug marketing --name "Marketing site"
```

### Create API keys per project per environment

```sh
# testing environment
kubectl -n bugbarn-testing exec deploy/bugbarn -- \
  bugbarn apikey create --project frontend  --name "frontend-testing"
kubectl -n bugbarn-testing exec deploy/bugbarn -- \
  bugbarn apikey create --project backend   --name "backend-testing"

# staging environment
kubectl -n bugbarn-staging exec deploy/bugbarn -- \
  bugbarn apikey create --project frontend  --name "frontend-staging"
kubectl -n bugbarn-staging exec deploy/bugbarn -- \
  bugbarn apikey create --project backend   --name "backend-staging"
```

Store each key as a separate repository secret, e.g. `BUGBARN_FRONTEND_API_KEY`, `BUGBARN_BACKEND_API_KEY`.

### Monorepo workflow pattern

In a monorepo that deploys multiple sites, use a matrix or parallel jobs — one per component — each with its own API key secret:

```yaml
jobs:
  deploy:
    strategy:
      matrix:
        include:
          - component: frontend
            secret_name: BUGBARN_FRONTEND_API_KEY
          - component: backend
            secret_name: BUGBARN_BACKEND_API_KEY
    steps:
      # ... build and deploy steps ...

      - name: Post release marker to BugBarn
        env:
          BUGBARN_ENDPOINT: ${{ vars.BUGBARN_ENDPOINT }}
          BUGBARN_API_KEY: ${{ secrets[matrix.secret_name] }}
        run: |
          curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/releases" \
            -H "Content-Type: application/json" \
            -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
            -d "{
              \"name\": \"${GITHUB_SHA:0:7}\",
              \"environment\": \"staging\",
              \"version\": \"${GITHUB_SHA}\",
              \"commitSha\": \"${GITHUB_SHA}\",
              \"url\": \"${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}\",
              \"notes\": \"Deployed ${{ matrix.component }}\"
            }" || true
```

The key determines the project — no extra header is needed.

---

## Marking Releases

A release marker ties a commit SHA to a deploy event. BugBarn displays these on the issues timeline so you can immediately see which deploy introduced or fixed an error.

### curl

```sh
curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/releases" \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
  -d "{
    \"name\": \"${GITHUB_SHA:0:7}\",
    \"environment\": \"${DEPLOY_ENV}\",
    \"version\": \"${GITHUB_SHA}\",
    \"commitSha\": \"${GITHUB_SHA}\",
    \"url\": \"${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}\",
    \"notes\": \"Deployed by GitHub Actions run #${GITHUB_RUN_NUMBER}\"
  }"
```

### GitHub Actions step

```yaml
- name: Post release marker to BugBarn
  if: vars.BUGBARN_ENDPOINT != ''
  env:
    BUGBARN_ENDPOINT: ${{ vars.BUGBARN_ENDPOINT }}
    BUGBARN_API_KEY: ${{ secrets.BUGBARN_API_KEY }}
    DEPLOY_ENV: staging   # or testing, production
  run: |
    curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/releases" \
      -H "Content-Type: application/json" \
      -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
      -d "{
        \"name\": \"${GITHUB_SHA:0:7}\",
        \"environment\": \"${DEPLOY_ENV}\",
        \"version\": \"${GITHUB_SHA}\",
        \"commitSha\": \"${GITHUB_SHA}\",
        \"url\": \"${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}\",
        \"notes\": \"Deployed by GitHub Actions run #${GITHUB_RUN_NUMBER}\"
      }" || echo "BugBarn release marker skipped"
```

The `|| echo` keeps a BugBarn outage from failing a successful deploy.

---

## Uploading Source Maps

Source maps let BugBarn display original file names, line numbers, and source snippets in stack traces instead of minified noise.

### API contract

```
POST /api/v1/source-maps
Content-Type: multipart/form-data
X-BugBarn-Api-Key: <key>

Fields:
  release          string (required)  — must match the name used in the release marker
  bundle_url       string (required)  — public URL of the corresponding minified JS bundle
  source_map       file   (required)  — .map file contents
  source_map_name  string (optional)  — filename hint stored alongside the map
  dist             string (optional)  — variant/distribution, e.g. "web"
```

One request per source map file. The `bundle_url` is used at symbolication time to match a stack frame's file URL against the right source map.

### curl — single file

```sh
curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/source-maps" \
  -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
  -F "release=${GITHUB_SHA}" \
  -F "bundle_url=https://example.com/_next/static/chunks/main.js" \
  -F "source_map_name=main.js.map" \
  -F "source_map=@.next/static/chunks/main.js.map"
```

### TypeScript SDK (Node.js build scripts)

```ts
import { createSourceMapUploader } from "@bugbarn/typescript";

const upload = createSourceMapUploader({
  endpoint: process.env.BUGBARN_ENDPOINT,
  apiKey: process.env.BUGBARN_API_KEY!,
  release: process.env.GITHUB_SHA!,
});

// Pass sourceMapPath (file path) — not sourceMap (content string)
await upload({
  bundleUrl: "https://example.com/_next/static/chunks/main.js",
  sourceMapPath: ".next/static/chunks/main.js.map",
  sourceMapName: "main.js.map",
});
```

### Batch upload — all Next.js source maps

Next.js emits source maps under `.next/static/` when `productionBrowserSourceMaps: true` is set in `next.config.js`. To upload all of them:

```sh
#!/bin/sh
# upload-sourcemaps.sh — run after `next build`, before containerizing
set -e
RELEASE="${GITHUB_SHA}"
PUBLIC_BASE_URL="https://example.com"   # public origin serving /_next/...

find .next/static -name "*.js.map" | while read -r map_file; do
  # .next/static/chunks/foo.js.map → https://example.com/_next/static/chunks/foo.js
  bundle_path="${map_file#.next/}"
  bundle_url="${PUBLIC_BASE_URL}/_next/${bundle_path%.map}"

  curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/source-maps" \
    -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
    -F "release=${RELEASE}" \
    -F "bundle_url=${bundle_url}" \
    -F "source_map_name=$(basename "${map_file}")" \
    -F "source_map=@${map_file}" || echo "  warning: upload failed for ${bundle_url}"
done
```

### Extracting source maps from a Docker image

If source maps are only available inside the built image (e.g. they were stripped from the repo before the CI run), extract them with `docker create` / `docker cp` before uploading:

```sh
container=$(docker create "${IMAGE}:${TAG}")
docker cp "${container}:/app/.next/static" ./extracted-static
docker rm "${container}"

find extracted-static -name "*.js.map" | while read -r map_file; do
  bundle_path="${map_file#extracted-static/}"
  bundle_url="${PUBLIC_BASE_URL}/_next/${bundle_path%.map}"
  curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/source-maps" \
    -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
    -F "release=${GITHUB_SHA}" \
    -F "bundle_url=${bundle_url}" \
    -F "source_map_name=$(basename "${map_file}")" \
    -F "source_map=@${map_file}" || true
done
```

### When to upload

Upload source maps **after the build, before or alongside the release marker**. They do not need to be uploaded inside the Docker build. The recommended order in a workflow step:

1. Build the application (Next.js, webpack, etc.)
2. Upload source maps → BugBarn
3. Package / push the Docker image (strip source maps from the image if desired)
4. Deploy to the target environment
5. Post release marker → BugBarn

---

## Full GitHub Actions example

```yaml
jobs:
  build-and-deploy:
    steps:
      - uses: actions/checkout@v4

      - name: Install dependencies
        run: pnpm install --frozen-lockfile

      - name: Build
        run: pnpm build
        env:
          NEXT_PUBLIC_APP_ENV: staging

      - name: Upload source maps to BugBarn
        if: vars.BUGBARN_ENDPOINT != ''
        env:
          BUGBARN_ENDPOINT: ${{ vars.BUGBARN_ENDPOINT }}
          BUGBARN_API_KEY: ${{ secrets.BUGBARN_API_KEY }}
          PUBLIC_BASE_URL: https://example.com
        run: |
          find .next/static -name "*.js.map" | while read -r map_file; do
            bundle_path="${map_file#.next/}"
            bundle_url="${PUBLIC_BASE_URL}/_next/${bundle_path%.map}"
            curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/source-maps" \
              -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
              -F "release=${GITHUB_SHA}" \
              -F "bundle_url=${bundle_url}" \
              -F "source_map_name=$(basename "${map_file}")" \
              -F "source_map=@${map_file}" || true
          done

      - name: Build and push Docker image
        run: docker buildx build ...

      - name: Deploy
        run: kubectl apply ...

      - name: Post release marker to BugBarn
        if: vars.BUGBARN_ENDPOINT != ''
        env:
          BUGBARN_ENDPOINT: ${{ vars.BUGBARN_ENDPOINT }}
          BUGBARN_API_KEY: ${{ secrets.BUGBARN_API_KEY }}
          DEPLOY_ENV: staging
        run: |
          curl -sf -X POST "${BUGBARN_ENDPOINT}/api/v1/releases" \
            -H "Content-Type: application/json" \
            -H "X-BugBarn-Api-Key: ${BUGBARN_API_KEY}" \
            -d "{
              \"name\": \"${GITHUB_SHA:0:7}\",
              \"environment\": \"${DEPLOY_ENV}\",
              \"version\": \"${GITHUB_SHA}\",
              \"commitSha\": \"${GITHUB_SHA}\",
              \"url\": \"${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/commit/${GITHUB_SHA}\",
              \"notes\": \"Deployed by GitHub Actions run #${GITHUB_RUN_NUMBER}\"
            }" || true
```

---

## Environment Variables Reference

| Name | Where to set | Description |
|------|-------------|-------------|
| `BUGBARN_ENDPOINT` | Repository or environment variable | Base URL of the BugBarn instance, e.g. `https://bugbarn.example.com`. Omit trailing slash. |
| `BUGBARN_API_KEY` | Repository or environment secret | API key created via `bugbarn apikey create --project <slug>`. Never commit this value. |

For pipelines that deploy to multiple environments (testing, staging), use GitHub's **environment-scoped** secrets and variables. Set `BUGBARN_ENDPOINT` and `BUGBARN_API_KEY` at the environment level so each deploy automatically uses the right instance and key.

For monorepos with multiple components, use **per-component secrets** (e.g. `BUGBARN_FRONTEND_API_KEY`, `BUGBARN_BACKEND_API_KEY`). Each key was created against a specific project so BugBarn routes ingested data accordingly — no extra configuration needed at call time.
