// Layer rules for the web app, read by scripts/boundaries-ratchet.mjs. The layering is the
// one the imports already follow:
//
//   app, bootstrap                      entrypoints, compose everything
//   views-*, router, http, live         screens and the plumbing they share
//   components, components/*            rendering helpers
//   types, data, domain, format, core,  shared vocabulary and pure helpers
//   and the small pure modules
//
// Dependencies point down. Test files are not part of src/, so these rules judge
// production code only.

// Modules every layer may import. They import only each other.
const BOTTOM =
  "(types|data|domain|format|core|infinite-scroll|timeline-stack|project-filter|release-rows|surfaces)";

/** @type {import('dependency-cruiser').IConfiguration} */
module.exports = {
  forbidden: [
    {
      name: "bottom-layer-imports-nothing-above",
      comment: "The shared vocabulary and pure helpers import only each other.",
      severity: "error",
      from: { path: `^src/${BOTTOM}\\.ts$` },
      to: { path: "^src/", pathNot: `^src/${BOTTOM}\\.ts$` },
    },
    {
      name: "components-stay-presentational",
      comment:
        "Rendering helpers take data and return markup. They do not fetch (http), navigate (router) or know a screen (views-*).",
      severity: "error",
      from: { path: "^src/components(/|\\.ts$)" },
      to: {
        path: "^src/",
        pathNot: `^src/(${BOTTOM}\\.ts|components(/|\\.ts$))`,
      },
    },
    {
      name: "only-entrypoints-import-the-entrypoints",
      comment: "app and bootstrap compose the app. Nothing below them reaches back up.",
      severity: "error",
      from: { path: "^src/", pathNot: "^src/(app|bootstrap)\\.ts$" },
      to: { path: "^src/(app|bootstrap)\\.ts$" },
    },
    {
      name: "no-circular",
      severity: "error",
      from: {},
      to: { circular: true },
    },
    {
      name: "production-code-does-not-import-tests",
      severity: "error",
      from: { path: "^src/" },
      to: { path: "(^|/)(e2e|scripts)/|\\.(test|spec)\\.[cm]?[jt]s$" },
    },
  ],
  options: {
    // swc parses the TypeScript: dependency-cruiser's own TypeScript support stops at
    // typescript 6, and the app builds with 7. Under 7 it cruises zero modules and says nothing.
    parser: "swc",
    tsConfig: { fileName: "tsconfig.json" },
    doNotFollow: { path: "node_modules" },
    exclude: { path: "\\.d\\.ts$" },
  },
};
