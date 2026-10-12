// SpanBarn browser tracing. The server advertises the relay in
// /api/v1/runtime-config (`spanbarn`); spans go to that same-origin relay, which
// adds the real SpanBarn key. The SDK insists on a key, so it gets a placeholder
// the relay ignores.
import { SpanBarn, instrumentBrowser } from "@spanbarn/js";

export interface SpanBarnRuntime {
  enabled: boolean;
  endpoint?: string;
  service?: string;
}

const RELAY_KEY_PLACEHOLDER = "relay";

export async function fetchSpanBarnRuntime(): Promise<SpanBarnRuntime | undefined> {
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return undefined;
    const cfg = await res.json() as { spanbarn?: SpanBarnRuntime };
    return cfg?.spanbarn;
  } catch {
    return undefined;
  }
}

/**
 * Starts the SDK when the runtime config enables it. Returns true when the SDK
 * is running, so the caller can skip the built-in instrumentation.
 */
export function startSpanBarn(runtime: SpanBarnRuntime | undefined, errorEndpoint: string): boolean {
  if (!runtime?.enabled || !runtime.endpoint || !runtime.service) return false;
  try {
    SpanBarn.init({
      endpoint: runtime.endpoint,
      apiKey: RELAY_KEY_PLACEHOLDER,
      service: runtime.service,
    });
    instrumentBrowser({
      webVitals: true,
      fetch: true,
      navigation: true,
      errors: true,
      errorEndpoint,
    });
    return true;
  } catch {
    return false;
  }
}
