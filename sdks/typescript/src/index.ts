import { createTransport } from "./transport.js";
import type { BugBarnClientOptions, BugBarnEnvelope, CaptureOptions, StackFrame, Transport } from "./types.js";

const SDK_NAME = "bugbarn.typescript";
const SDK_VERSION = "0.1.0";

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

function parseStacktrace(stack?: string): StackFrame[] | undefined {
  if (!stack) {
    return undefined;
  }

  const frames: StackFrame[] = [];
  const lines = stack.split("\n").map((line) => line.trim()).filter(Boolean);

  for (const line of lines.slice(1)) {
    const callMatch = /^at (?:(?<function>.+?) )?\((?<file>.+?):(?<line>\d+):(?<column>\d+)\)$/.exec(line);
    const bareMatch = /^at (?<file>.+?):(?<line>\d+):(?<column>\d+)$/.exec(line);
    const match = callMatch ?? bareMatch;

    if (!match?.groups) {
      continue;
    }

    const file = match.groups.file;
    const frame: StackFrame = {
      file,
      line: Number(match.groups.line),
      column: Number(match.groups.column),
    };

    if (match.groups.function) {
      frame.function = match.groups.function;
    }

    const moduleName = file.includes("/") ? file.split("/").slice(-1)[0] : undefined;
    if (moduleName) {
      frame.module = moduleName;
    }

    frames.push(frame);
  }

  return frames.length > 0 ? frames : undefined;
}

function buildEnvelope(error: unknown, options?: CaptureOptions): BugBarnEnvelope {
  const normalized = normalizeError(error);
  return {
    timestamp: new Date().toISOString(),
    severityText: "ERROR",
    body: normalized.message,
    exception: {
      type: normalized.name || "Error",
      message: normalized.message,
      stacktrace: parseStacktrace(normalized.stack),
    },
    attributes: options?.attributes,
    tags: options?.tags,
    extra: options?.extra,
    sender: {
      sdk: {
        name: SDK_NAME,
        version: SDK_VERSION,
      },
    },
  };
}

function installDefaultHandlers(): void {
  if (handlersInstalled) {
    return;
  }
  handlersInstalled = true;

  process.on("uncaughtException", (error: Error) => {
    void captureException(error).finally(() => {
      process.exit(1);
    });
  });

  process.on("unhandledRejection", (reason: unknown) => {
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

  await transport.send(buildEnvelope(error, options));
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
