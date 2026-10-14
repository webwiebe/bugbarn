// Infinite scroll for paged lists. No imports, so the unit test can load it
// straight from source.

/** Distance below the viewport at which the next page starts loading. */
export const SCROLL_ROOT_MARGIN = "400px";

export interface PageLoader {
  /** Runs the load unless one is already in flight. Resolves to false when skipped. */
  run(): Promise<boolean>;
  inFlight(): boolean;
}

/** Wraps a page load so a second request cannot start while one is running. */
export function createPageLoader(load: () => Promise<unknown>): PageLoader {
  let running = false;
  return {
    inFlight: () => running,
    async run() {
      if (running) return false;
      running = true;
      try {
        await load();
      } finally {
        running = false;
      }
      return true;
    },
  };
}

export interface SentinelOptions {
  sentinel: Element;
  /** Whether another page exists and loading is allowed (no earlier failure). */
  canLoad: () => boolean;
  loader: PageLoader;
  /** Defaults to the global IntersectionObserver. */
  observerCtor?: typeof IntersectionObserver;
}

/**
 * Loads the next page when the sentinel comes within SCROLL_ROOT_MARGIN of the
 * viewport. Returns the observer so the caller can disconnect it before the
 * list is re-rendered, or null when the browser has no IntersectionObserver
 * (the caller then shows the button).
 */
export function observeSentinel(opts: SentinelOptions): IntersectionObserver | null {
  const Ctor = opts.observerCtor ?? (typeof IntersectionObserver === "undefined" ? undefined : IntersectionObserver);
  if (!Ctor) return null;
  const observer = new Ctor(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      if (!opts.canLoad() || opts.loader.inFlight()) return;
      void opts.loader.run();
    },
    { rootMargin: SCROLL_ROOT_MARGIN },
  );
  observer.observe(opts.sentinel);
  return observer;
}
