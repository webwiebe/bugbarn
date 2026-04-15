import { createTransport } from "./transport.ts";
import type { BugBarnClientOptions, BugBarnEvent, CaptureOptions, Transport } from "./types.ts";

const SDK_NAME = "bugbarn.typescript";

let transport: Transport | null = null;
let currentApiKey = "";
let handlersInstalled = false;

function normalizeError(error: unknown): Error {
  if (error instanceof Error) {
    return error;
  }

  if (typeof error === "string") {
    return new Error(error);
  }

  return new Error("Unknown error");
}

function buildEvent(error: unknown, options?: CaptureOptions): BugBarnEvent {
  const normalized = normalizeError(error);
  return {
    sdk: SDK_NAME,
    message: normalized.message,
    exception: {
      type: normalized.name || "Error",
      value: normalized.message,
      stack: normalized.stack,
    },
    timestamp: new Date().toISOString(),
    tags: options?.tags,
    extra: options?.extra,
  };
}

function installDefaultHandlers(): void {
  if (handlersInstalled) {
    return;
  }
  handlersInstalled = true;

  process.on("uncaughtException", (error) => {
    void captureException(error).finally(() => {
      process.exit(1);
    });
  });

  process.on("unhandledRejection", (reason) => {
    void captureException(reason).finally(() => {
      process.exit(1);
    });
  });
}

export function init(options: BugBarnClientOptions): void {
  currentApiKey = options.apiKey;
  transport = options.transport ?? createTransport(options.apiKey, options.endpoint);

  if (options.installDefaultHandlers !== false) {
    installDefaultHandlers();
  }
}

export async function captureException(error: unknown, options?: CaptureOptions): Promise<void> {
  if (!transport) {
    return;
  }

  await transport.send(buildEvent(error, options));
}

export async function flush(): Promise<void> {
  if (!transport) {
    return;
  }

  await transport.flush();
}

export function getApiKey(): string {
  return currentApiKey;
}

export { createTransport };
