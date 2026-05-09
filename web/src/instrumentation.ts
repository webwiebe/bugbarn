const TELEMETRY_URL = "/api/v1/telemetry";
const CLIENT_ERRORS_URL = "/api/v1/client-errors";
const SERVICE_NAME = "bugbarn-web";
const FLUSH_INTERVAL_MS = 5000;
const MAX_BATCH = 50;
const AUTO_FLUSH_AT = 25;

interface SpanPayload {
  traceId: string;
  spanId: string;
  parentSpanId?: string;
  name: string;
  service: string;
  kind: string;
  status: string;
  startTime: number;
  duration: number;
  attributes?: Record<string, string | number | boolean>;
}

let spanQueue: SpanPayload[] = [];
let flushTimer: ReturnType<typeof setInterval> | null = null;
let pageTraceId = hex(16);
let pageSpanId = hex(8);
let originalFetch: typeof window.fetch;

function hex(bytes: number): string {
  const arr = new Uint8Array(bytes);
  crypto.getRandomValues(arr);
  return Array.from(arr, (b) => b.toString(16).padStart(2, "0")).join("");
}

function nowUs(): number {
  return Math.round((performance.timeOrigin + performance.now()) * 1000);
}

function traceparent(traceId: string, spanId: string): string {
  return `00-${traceId}-${spanId}-01`;
}

function isInstrumentationUrl(url: string): boolean {
  return url.includes(TELEMETRY_URL) || url.includes(CLIENT_ERRORS_URL);
}

function enqueueSpan(span: SpanPayload): void {
  spanQueue.push(span);
  if (spanQueue.length >= AUTO_FLUSH_AT) {
    flushSpans();
  }
}

function flushSpans(): void {
  if (!spanQueue.length) return;
  const batch = spanQueue.splice(0, MAX_BATCH);
  try {
    originalFetch(TELEMETRY_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ spans: batch }),
      keepalive: true,
      credentials: "same-origin",
    }).catch(() => {});
  } catch {
    // Silently drop on failure — telemetry is best-effort.
  }
}

function reportError(message: string, type: string, stack: string): void {
  try {
    originalFetch(CLIENT_ERRORS_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, type, stack, url: location.href }),
      keepalive: true,
      credentials: "same-origin",
    }).catch(() => {});
  } catch {
    // Best-effort.
  }
}

function instrumentFetch(): void {
  originalFetch = window.fetch;
  window.fetch = async function (input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;

    if (isInstrumentationUrl(url)) {
      return originalFetch.call(window, input, init);
    }

    const spanId = hex(8);
    const startUs = nowUs();
    const method = init?.method?.toUpperCase() || "GET";

    const headers = new Headers(init?.headers);
    headers.set("traceparent", traceparent(pageTraceId, spanId));

    try {
      const response = await originalFetch.call(window, input, { ...init, headers });
      const durationUs = nowUs() - startUs;

      enqueueSpan({
        traceId: pageTraceId,
        spanId,
        parentSpanId: pageSpanId,
        name: `${method} ${new URL(url, location.origin).pathname}`,
        service: SERVICE_NAME,
        kind: "CLIENT",
        status: response.status >= 500 ? "ERROR" : "OK",
        startTime: startUs,
        duration: durationUs,
        attributes: {
          "http.method": method,
          "http.url": new URL(url, location.origin).pathname,
          "http.status_code": response.status,
        },
      });

      if (response.status >= 500) {
        reportError(`HTTP ${response.status} ${method} ${url}`, "FetchError", "");
      }

      return response;
    } catch (err) {
      const durationUs = nowUs() - startUs;
      const message = err instanceof Error ? err.message : String(err);

      enqueueSpan({
        traceId: pageTraceId,
        spanId,
        parentSpanId: pageSpanId,
        name: `${method} ${new URL(url, location.origin).pathname}`,
        service: SERVICE_NAME,
        kind: "CLIENT",
        status: "ERROR",
        startTime: startUs,
        duration: durationUs,
        attributes: {
          "http.method": method,
          "http.url": new URL(url, location.origin).pathname,
          "error.message": message,
        },
      });

      throw err;
    }
  };
}

function instrumentNavigation(): void {
  let currentPath = location.pathname;

  const onNavigate = (): void => {
    const newPath = location.pathname;
    if (newPath === currentPath) return;

    flushSpans();

    const prevPath = currentPath;
    currentPath = newPath;
    pageTraceId = hex(16);
    pageSpanId = hex(8);

    enqueueSpan({
      traceId: pageTraceId,
      spanId: pageSpanId,
      name: `navigation ${newPath}`,
      service: SERVICE_NAME,
      kind: "INTERNAL",
      status: "OK",
      startTime: nowUs(),
      duration: 0,
      attributes: {
        "navigation.from": prevPath,
        "navigation.to": newPath,
      },
    });
  };

  const origPushState = history.pushState.bind(history);
  history.pushState = function (...args: Parameters<typeof history.pushState>) {
    origPushState(...args);
    onNavigate();
  };

  const origReplaceState = history.replaceState.bind(history);
  history.replaceState = function (...args: Parameters<typeof history.replaceState>) {
    origReplaceState(...args);
    onNavigate();
  };

  window.addEventListener("popstate", onNavigate);
}

function installErrorHandlers(): void {
  window.addEventListener("error", (event) => {
    if (event.error instanceof Error) {
      reportError(event.error.message, event.error.name, event.error.stack || "");
    }
  });

  window.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason;
    if (reason instanceof Error) {
      reportError(reason.message, reason.name, reason.stack || "");
    } else {
      reportError(String(reason), "UnhandledRejection", "");
    }
  });
}

export function initInstrumentation(): void {
  instrumentFetch();
  instrumentNavigation();
  installErrorHandlers();
  flushTimer = setInterval(flushSpans, FLUSH_INTERVAL_MS);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flushSpans();
  });
}

export function shutdownInstrumentation(): void {
  if (flushTimer) {
    clearInterval(flushTimer);
    flushTimer = null;
  }
  flushSpans();
}
