import { addBreadcrumb } from "./breadcrumbs.js";

let installed = false;

export function installAutoInterceptors(): void {
  if (installed) return;
  installed = true;
  installConsoleInterceptor();
  installFetchInterceptor();
  installXHRInterceptor();
  installNavigationInterceptor();
}

export function resetInterceptors(): void {
  installed = false;
  // For tests: allow re-installation
}

function installConsoleInterceptor(): void {
  if (typeof console === "undefined") return;
  const methods = ["log", "warn", "error", "info", "debug"] as const;
  for (const method of methods) {
    const original = console[method].bind(console);
    console[method] = (...args: unknown[]) => {
      addBreadcrumb({
        timestamp: new Date().toISOString(),
        category: "console",
        message: args.map(String).join(" "),
        level: method === "log" ? "log" : method,
      });
      original(...args);
    };
  }
}

function installFetchInterceptor(): void {
  if (typeof globalThis.fetch === "undefined") return;
  const original = globalThis.fetch.bind(globalThis);
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
    const method = (init?.method ?? "GET").toUpperCase();
    const response = await original(input, init);
    addBreadcrumb({
      timestamp: new Date().toISOString(),
      category: "http",
      message: `${method} ${url}`,
      data: { method, url, status_code: response.status },
    });
    return response;
  };
}

function installXHRInterceptor(): void {
  if (typeof XMLHttpRequest === "undefined") return;
  const OriginalXHR = XMLHttpRequest;
  const OrigXHRProto = OriginalXHR.prototype;
  const origOpen = OrigXHRProto.open;
  const origSend = OrigXHRProto.send;

  OrigXHRProto.open = function (method: string, url: string, ...rest: unknown[]) {
    (this as unknown as Record<string, unknown>).__bb_method = method;
    (this as unknown as Record<string, unknown>).__bb_url = url;
    return origOpen.apply(this, [method, url, ...rest] as Parameters<typeof origOpen>);
  };

  OrigXHRProto.send = function (...args: unknown[]) {
    const self = this as unknown as Record<string, unknown> & XMLHttpRequest;
    self.addEventListener("loadend", () => {
      addBreadcrumb({
        timestamp: new Date().toISOString(),
        category: "http",
        message: `${self.__bb_method ?? "XHR"} ${self.__bb_url ?? ""}`,
        data: {
          method: self.__bb_method,
          url: self.__bb_url,
          status_code: self.status,
        },
      });
    });
    return origSend.apply(this, args as Parameters<typeof origSend>);
  };
}

function installNavigationInterceptor(): void {
  if (typeof window === "undefined") return;
  let lastLocation = window.location?.href ?? "";

  window.addEventListener("hashchange", () => {
    const from = lastLocation;
    const to = window.location.href;
    lastLocation = to;
    addBreadcrumb({
      timestamp: new Date().toISOString(),
      category: "navigation",
      message: `Navigated to ${to}`,
      data: { from, to },
    });
  });

  for (const method of ["pushState", "replaceState"] as const) {
    const original = history[method].bind(history);
    history[method] = function (...args: Parameters<typeof history.pushState>) {
      original(...args);
      const to = window.location.href;
      addBreadcrumb({
        timestamp: new Date().toISOString(),
        category: "navigation",
        message: `Navigated to ${to}`,
        data: { from: lastLocation, to },
      });
      lastLocation = to;
    };
  }
}
