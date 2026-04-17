# Source Map Upload Example

This shows how an integrating project uploads source maps to Bug Barn so that
minified stack frames are symbolicated before display.

## 1. Init the SDK with release and dist

```ts
// src/main.ts
import { init } from '@bugbarn/typescript';

init({
  apiKey: process.env.BUGBARN_API_KEY!,
  endpoint: 'https://bugbarn.example.com',
  // release and dist are stamped onto every captured event
  release: process.env.npm_package_version,   // e.g. "1.4.2"
  dist: 'web',                                 // e.g. build target/variant
});
```

Both `release` and `dist` can also be read from environment variables
`BUGBARN_RELEASE` and `BUGBARN_DIST` automatically when not passed in `init()`.

## 2. Upload source maps after a build

Call `createSourceMapUploader` once to bind your credentials, then call the
returned function for each bundle/map pair produced by your build tool.

```ts
// scripts/upload-source-maps.ts
import { createSourceMapUploader } from '@bugbarn/typescript';

const upload = createSourceMapUploader({
  endpoint: process.env.BUGBARN_ENDPOINT!,    // e.g. https://bugbarn.example.com/api/v1/source-maps
  apiKey: process.env.BUGBARN_API_KEY!,
  release: process.env.BUGBARN_RELEASE!,      // must match what you passed to init()
  dist: 'web',
});

// After webpack / esbuild / vite outputs dist/bundle.js + dist/bundle.js.map:
await upload({
  bundleUrl: 'https://cdn.example.com/bundle.js',  // public URL of the minified file
  sourceMap: './dist/bundle.js.map',               // local path — Node.js fs.readFile used internally
  sourceMapName: 'bundle.js.map',                  // stored filename (optional)
});
```

You can also pass a `Blob` or `ArrayBuffer` directly when already in memory:

```ts
import { readFile } from 'node:fs/promises';

const mapContents = await readFile('./dist/bundle.js.map');
await upload({
  bundleUrl: 'https://cdn.example.com/bundle.js',
  sourceMap: new Blob([mapContents], { type: 'application/json' }),
});
```

## 3. GitHub Actions step

```yaml
jobs:
  build-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install dependencies
        run: npm ci

      - name: Build
        run: npm run build
        env:
          BUGBARN_RELEASE: ${{ github.sha }}

      - name: Upload source maps
        run: npx tsx scripts/upload-source-maps.ts
        env:
          BUGBARN_ENDPOINT: ${{ secrets.BUGBARN_ENDPOINT }}
          BUGBARN_API_KEY: ${{ secrets.BUGBARN_API_KEY }}
          BUGBARN_RELEASE: ${{ github.sha }}
```

Store `BUGBARN_ENDPOINT` and `BUGBARN_API_KEY` as repository secrets. Set
`BUGBARN_RELEASE` to a value that matches what the running application passes
to `init()` — a commit SHA, a semantic version tag, or a custom string.

## Backend contract

The upload endpoint expects `POST multipart/form-data` with:

| Field             | Type   | Required | Description                         |
|-------------------|--------|----------|-------------------------------------|
| `release`         | string | yes      | Release identifier                  |
| `dist`            | string | no       | Distribution/variant                |
| `bundle_url`      | string | yes      | Public URL of the minified bundle   |
| `source_map_name` | string | no       | Filename hint for the stored map    |
| `source_map`      | file   | yes      | Source map file content             |

Authentication is via the `x-bugbarn-api-key` request header.
