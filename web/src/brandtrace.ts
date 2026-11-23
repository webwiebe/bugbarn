// BrandTrace site SDK for the dashboard. The site key is public and allow-listed
// per origin (brandtrace#2121). It comes from GET /api/v1/runtime-config, so the
// SDK stays off wherever the server has no key (staging, testing, local).

const BRANDTRACE_SDK_URL = "https://brandtrace.net/sdk.js";

type BrandTraceRuntime = { enabled?: boolean; siteKey?: string };

export async function fetchBrandTraceRuntime(): Promise<BrandTraceRuntime | undefined> {
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return undefined;
    const cfg = (await res.json()) as { brandtrace?: BrandTraceRuntime };
    return cfg?.brandtrace;
  } catch {
    return undefined;
  }
}

// startBrandTrace injects the SDK tag once. It reports whether a tag was added.
export function startBrandTrace(runtime: BrandTraceRuntime | undefined): boolean {
  if (!runtime?.enabled || !runtime.siteKey) return false;
  if (document.querySelector(`script[src="${BRANDTRACE_SDK_URL}"]`)) return false;
  const script = document.createElement("script");
  script.src = BRANDTRACE_SDK_URL;
  script.async = true;
  script.setAttribute("data-site-key", runtime.siteKey);
  document.head.appendChild(script);
  return true;
}

export async function initBrandTrace(): Promise<void> {
  startBrandTrace(await fetchBrandTraceRuntime());
}
