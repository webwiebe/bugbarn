// Module resolution hook for the SPA tests. The sources import siblings as
// "./x.js" (the bundler convention); on disk they are .ts files. Node loads
// .ts directly, so a relative ".js" specifier that has no file maps to ".ts".
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith(".") && specifier.endsWith(".js") && context.parentURL) {
    const target = new URL(specifier, context.parentURL);
    if (!existsSync(fileURLToPath(target))) {
      return nextResolve(specifier.replace(/\.js$/, ".ts"), context);
    }
  }
  return nextResolve(specifier, context);
}
