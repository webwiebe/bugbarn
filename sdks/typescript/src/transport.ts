import type { BugBarnEvent, Transport } from "./types.ts";

const DEFAULT_ENDPOINT = "/api/v1/events";

function resolveUrl(endpoint: string): string {
  if (endpoint.startsWith("http://") || endpoint.startsWith("https://")) {
    return endpoint;
  }

  return `http://127.0.0.1${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
}

export function createTransport(apiKey: string, endpoint = DEFAULT_ENDPOINT): Transport {
  const queue: BugBarnEvent[] = [];
  let flushScheduled = false;
  let flushInFlight: Promise<void> | null = null;

  async function send(event: BugBarnEvent): Promise<void> {
    queue.push(event);
    if (!flushScheduled) {
      flushScheduled = true;
      setTimeout(() => {
        void flush();
      }, 0);
    }
  }

  async function flush(): Promise<void> {
    if (flushInFlight) {
      await flushInFlight;
      if (queue.length > 0) {
        return flush();
      }
      return;
    }

    flushScheduled = false;
    const batch = queue.splice(0, queue.length);
    if (batch.length === 0) {
      return;
    }

    flushInFlight = (async () => {
      const response = await fetch(resolveUrl(endpoint), {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-bugbarn-api-key": apiKey,
        },
        body: JSON.stringify({ events: batch }),
      });

      if (!response.ok) {
        throw new Error(`BugBarn transport failed with ${response.status}`);
      }
    })();

    try {
      await flushInFlight;
    } finally {
      flushInFlight = null;
      if (queue.length > 0) {
        void flush();
      }
    }
  }

  return { send, flush };
}
