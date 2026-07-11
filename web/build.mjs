// Production build for the dashboard SPA.
//
// esbuild bundles src/app.ts into a single, content-hashed file
// (dist/app-<hash>.js) and rewrites every internal import to that one file, so
// there are no bare, unversioned module URLs left for a CDN to serve stale.
// styles.css is content-hashed the same way. The hashed assets are immutable —
// a new build changes the filename, so browsers and the CDN can cache them
// forever, and only index.html / sw.js (served no-cache) need to update.
//
// index.html and sw.js are templated here with the actual hashed filenames.
import { build } from "esbuild";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";

const OUT = "dist";

rmSync(OUT, { recursive: true, force: true });
mkdirSync(OUT, { recursive: true });

// Bundle the SPA into one content-hashed ESM file. Not minified: keeps
// self-reported (dogfooded) stack traces readable, and the sourcemap covers the
// dashboard's own source-map resolution.
const result = await build({
  entryPoints: ["src/app.ts"],
  bundle: true,
  format: "esm",
  target: "es2021",
  sourcemap: true,
  outdir: OUT,
  entryNames: "app-[hash]",
  metafile: true,
  legalComments: "none",
});

const jsOut = Object.keys(result.metafile.outputs).find(
  (f) => f.endsWith(".js") && !f.endsWith(".map"),
);
if (!jsOut) throw new Error("build: no JS output produced");
const appJs = jsOut.slice(OUT.length + 1); // e.g. app-ABC123.js

// Content-hash styles.css.
const css = readFileSync("styles.css");
const cssHash = createHash("sha256").update(css).digest("hex").slice(0, 8);
const appCss = `styles-${cssHash}.css`;
writeFileSync(`${OUT}/${appCss}`, css);

// The app bundle's hash doubles as the overall build/cache version.
const buildHash = (appJs.match(/^app-([^.]+)\.js$/) ?? [])[1] ?? cssHash;

// Template index.html with the hashed asset paths (served from /app/dist/).
const html = readFileSync("index.html", "utf8")
  .replaceAll("__APP_CSS__", `./dist/${appCss}`)
  .replaceAll("__APP_JS__", `./dist/${appJs}`);
writeFileSync(`${OUT}/index.html`, html);

// Template the service worker: cache version + the precache shell, using the
// real /app/-scoped URLs the browser will request.
const appShell = JSON.stringify([
  "/app/",
  `/app/dist/${appJs}`,
  `/app/dist/${appCss}`,
  "/app/manifest.json",
]);
const sw = readFileSync("sw.js", "utf8")
  .replaceAll("__BUILD_HASH__", buildHash)
  .replaceAll("__APP_SHELL__", appShell);
writeFileSync(`${OUT}/sw.js`, sw);

console.log(`built ${appJs}, ${appCss} (version ${buildHash})`);
