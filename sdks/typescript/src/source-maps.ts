import type { SourceMapUploadOptions } from "./types.js";

const DEFAULT_ENDPOINT = "/api/v1/source-maps";

function resolveUrl(endpoint: string): string {
  if (endpoint.startsWith("http://") || endpoint.startsWith("https://")) {
    return endpoint;
  }

  return `http://127.0.0.1${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
}

function toBlob(sourceMap: SourceMapUploadOptions["sourceMap"]): Blob {
  if (sourceMap instanceof Blob) {
    return sourceMap;
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

  formData.set("source_map", toBlob(options.sourceMap), options.sourceMapFilename ?? "source.map");

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
