#!/usr/bin/env node
// Serves the built dashboard the way the deployed stack does, for running the
// post-deploy smoke (playwright.smoke.config.ts) against a local build:
//
//   /app/*   web/dist (index.html for any path without a file)
//   /api/*   proxied to BUGBARN_API_URL (default http://localhost:8080)
//
// Usage: node scripts/serve-local.mjs [port]   (default 4173; run `pnpm run build` first)

import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const API = new URL(process.env["BUGBARN_API_URL"] ?? "http://localhost:8080");
const PORT = Number(process.argv[2] ?? 4173);

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".map": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
};

function serveStatic(req, res) {
  // The image copies the bundles to /srv/app/dist/, so /app/dist/x is web/dist/x.
  const rel = decodeURIComponent(new URL(req.url, "http://x").pathname)
    .replace(/^\/app\/?/, "")
    .replace(/^dist\//, "");
  const wanted = path.join(DIST, rel);
  const file = wanted.startsWith(DIST) && fs.existsSync(wanted) && fs.statSync(wanted).isFile() ? wanted : path.join(DIST, "index.html");
  res.writeHead(200, { "content-type": TYPES[path.extname(file)] ?? "application/octet-stream", "cache-control": "no-cache" });
  fs.createReadStream(file).pipe(res);
}

function proxy(req, res) {
  const upstream = http.request(
    { host: API.hostname, port: API.port, path: req.url, method: req.method, headers: { ...req.headers, host: API.host } },
    (up) => {
      res.writeHead(up.statusCode ?? 502, up.headers);
      up.pipe(res);
    },
  );
  upstream.on("error", () => {
    res.writeHead(502).end("bad gateway");
  });
  req.pipe(upstream);
}

http
  .createServer((req, res) => {
    if (req.url.startsWith("/api/")) proxy(req, res);
    else if (req.url === "/" || req.url.startsWith("/app")) serveStatic(req, res);
    else res.writeHead(404).end("not found");
  })
  .listen(PORT, () => console.log(`serving ${DIST} at http://localhost:${PORT}/app/ (api -> ${API.origin})`));
