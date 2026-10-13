import type { BugBarnEnvelope, CheckInBody, FlushOptions, Transport } from "./types.js";

const DEFAULT_ENDPOINT = "/api/v1/events";
const DEFAULT_FLUSH_TIMEOUT_MS = 2000;

function resolveUrl(endpoint: string): string {
  if (endpoint.startsWith("http://") || endpoint.startsWith("https://")) {
    if (!endpoint.includes("/api/")) {
      return endpoint.replace(/\/+$/, "") + DEFAULT_ENDPOINT;
    }
    return endpoint;
  }

  return `http://127.0.0.1${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
}

function resolveCheckInUrl(endpoint: string, slug: string): string {
  const url = resolveUrl(endpoint);
  const base = url.includes("/api/") ? url.split("/api/")[0] : url.replace(/\/+$/, "");
  return `${base}/api/v1/monitors/${encodeURIComponent(slug)}/check-ins`;
}

export function createTransport(apiKey: string, endpoint = DEFAULT_ENDPOINT, project?: string): Transport {
  const queue: BugBarnEnvelope[] = [];
  let flushScheduled = false;
  let flushInFlight: Promise<void> | null = null;
  const checkIns = new Set<Promise<void>>();

  function requestHeaders(): Record<string, string> {
    const headers: Record<string, string> = {
      "content-type": "application/json",
      "x-bugbarn-api-key": apiKey,
    };
    if (project) {
      headers["x-bugbarn-project"] = project;
    }
    return headers;
  }

  // Check-ins start at once and flush() waits for them. A failed one is dropped
  // like an event the server could not take, and never thrown into the caller's job.
  async function sendCheckIn(slug: string, body: CheckInBody): Promise<void> {
    const request: Promise<void> = fetch(resolveCheckInUrl(endpoint, slug), {
      method: "POST",
      headers: requestHeaders(),
      body: JSON.stringify(body),
    })
      .then(() => undefined)
      .catch(() => undefined)
      .finally(() => {
        checkIns.delete(request);
      });
    checkIns.add(request);
  }

  async function send(event: BugBarnEnvelope): Promise<void> {
    queue.push(event);
    if (!flushScheduled) {
      flushScheduled = true;
      setTimeout(() => {
        void drain().catch(() => {});
      }, 0);
    }
  }

  async function drain(): Promise<void> {
    if (flushInFlight) {
      await flushInFlight;
      if (queue.length > 0) {
        return drain();
      }
      return;
    }

    flushScheduled = false;
    const batch = queue.splice(0, queue.length);
    if (batch.length === 0) {
      return;
    }

    flushInFlight = (async () => {
      for (const event of batch) {
        const response = await fetch(resolveUrl(endpoint), {
          method: "POST",
          headers: requestHeaders(),
          body: JSON.stringify(event),
        });

        if (!response.ok) {
          throw new Error(`BugBarn transport failed with ${response.status}`);
        }
      }
    })();

    try {
      await flushInFlight;
    } finally {
      flushInFlight = null;
      if (queue.length > 0) {
        void drain();
      }
    }
  }

  async function flush(options: FlushOptions = {}): Promise<boolean> {
    const timeoutMs = options.timeoutMs ?? DEFAULT_FLUSH_TIMEOUT_MS;
    const drainAll = async (): Promise<boolean> => {
      await drain();
      await Promise.all([...checkIns]);
      return true;
    };
    if (timeoutMs <= 0) {
      return drainAll();
    }

    let timeout: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        drainAll(),
        new Promise<boolean>((resolve) => {
          timeout = setTimeout(() => resolve(false), timeoutMs);
        }),
      ]);
    } finally {
      if (timeout) {
        clearTimeout(timeout);
      }
    }
  }

  return { send, sendCheckIn, flush };
}
