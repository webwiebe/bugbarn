/**
 * Source map upload helper.
 *
 * Backend upload contract — POST multipart/form-data to /api/v1/source-maps:
 *   release        (string, required)  — release identifier, e.g. "1.2.3"
 *   dist           (string, optional)  — distribution/variant, e.g. "web"
 *   bundle_url     (string, required)  — public URL of the minified bundle
 *   source_map_name (string, optional) — filename hint for the stored map
 *   source_map     (file, required)    — the source map file content
 *
 * Authentication: x-bugbarn-api-key header.
 */

import type { SourceMapUploadOptions, SourceMapUploaderConfig } from "./types.js";

const DEFAULT_ENDPOINT = "/api/v1/source-maps";

function resolveUrl(endpoint: string): string {
  if (endpoint.startsWith("http://") || endpoint.startsWith("https://")) {
    return endpoint;
  }

  return `http://127.0.0.1${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
}

async function toBlob(sourceMap: SourceMapUploadOptions["sourceMap"]): Promise<Blob> {
  if (sourceMap instanceof Blob) {
    return sourceMap;
  }

  // Node.js file path string
  if (typeof sourceMap === "string") {
    const { readFile } = await import("node:fs/promises");
    const contents = await readFile(sourceMap);
    return new Blob([contents], { type: "application/json" });
  }

  return new Blob([sourceMap], { type: "application/json" });
}

export async function uploadSourceMap(options: SourceMapUploadOptions): Promise<void> {
  const formData = new FormData();
  formData.set("release", options.release);
  formData.set("bundle_url", options.bundleUrl);

  if (options.dist) {
    formData.set("dist", options.dist);
  }

  if (options.sourceMapName ?? options.sourceMapFilename) {
    formData.set("source_map_name", (options.sourceMapName ?? options.sourceMapFilename)!);
  }

  const blob = await toBlob(options.sourceMap);
  formData.set("source_map", blob, options.sourceMapName ?? options.sourceMapFilename ?? "source.map");

  const response = await fetch(resolveUrl(options.endpoint ?? DEFAULT_ENDPOINT), {
    method: "POST",
    headers: {
      "x-bugbarn-api-key": options.apiKey,
    },
    body: formData,
  });

  if (!response.ok) {
    throw new Error(`BugBarn source map upload failed with ${response.status}`);
  }
}

/**
 * Factory that returns an upload function pre-filled with endpoint, apiKey,
 * release, and dist. Convenient for use in build scripts:
 *
 * ```ts
 * import { createSourceMapUploader } from '@bugbarn/typescript';
 * const upload = createSourceMapUploader({ endpoint, apiKey, release, dist });
 * await upload({ bundleUrl: 'https://example.com/bundle.js', sourceMapPath: './dist/bundle.js.map' });
 * ```
 */
export function createSourceMapUploader(config: SourceMapUploaderConfig) {
  return async function upload(params: {
    bundleUrl: string;
    /** File path (Node.js) or Blob/ArrayBuffer (browser) */
    sourceMap: string | Blob | ArrayBuffer;
    sourceMapName?: string;
  }): Promise<void> {
    await uploadSourceMap({
      apiKey: config.apiKey,
      endpoint: config.endpoint,
      release: config.release,
      dist: config.dist,
      bundleUrl: params.bundleUrl,
      sourceMap: params.sourceMap,
      sourceMapName: params.sourceMapName,
    });
  };
}
