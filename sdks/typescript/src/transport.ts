import type { BugBarnEnvelope, Transport } from "./types.js";

const DEFAULT_ENDPOINT = "/api/v1/events";

function resolveUrl(endpoint: string): string {
  if (endpoint.startsWith("http://") || endpoint.startsWith("https://")) {
    return endpoint;
  }

  return `http://127.0.0.1${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
}

export function createTransport(apiKey: string, endpoint = DEFAULT_ENDPOINT, project?: string): Transport {
  const queue: BugBarnEnvelope[] = [];
  let flushScheduled = false;
  let flushInFlight: Promise<void> | null = null;

  async function send(event: BugBarnEnvelope): Promise<void> {
    queue.push(event);
    if (!flushScheduled) {
      flushScheduled = true;
      setTimeout(() => {
        void flush().catch(() => {});
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
      for (const event of batch) {
        const headers: Record<string, string> = {
          "content-type": "application/json",
          "x-bugbarn-api-key": apiKey,
        };
        if (project) {
          headers["x-bugbarn-project"] = project;
        }
        const response = await fetch(resolveUrl(endpoint), {
          method: "POST",
          headers,
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
        void flush();
      }
    }
  }

  return { send, flush };
}
