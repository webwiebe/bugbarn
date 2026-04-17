import { createTransport } from "./transport.js";
import { uploadSourceMap, createSourceMapUploader } from "./source-maps.js";
import type { BugBarnClientOptions, BugBarnEnvelope, CaptureOptions, StackFrame, Transport } from "./types.js";
import { setUser, clearUser, getUser } from "./user.js";
import { addBreadcrumb, getBreadcrumbs, clearBreadcrumbs } from "./breadcrumbs.js";
import { installAutoInterceptors } from "./interceptors.js";

const SDK_NAME = "bugbarn.typescript";
const SDK_VERSION = "0.1.0";
const DEFAULT_SHUTDOWN_TIMEOUT_MS = 2000;

let transport: Transport | null = null;
let currentApiKey = "";
let currentRelease: string | undefined;
let currentDist: string | undefined;
let currentProject: string | undefined;
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
  const crumbs = getBreadcrumbs();
  return {
    timestamp: new Date().toISOString(),
    severityText: "ERROR",
    body: normalized.message,
    release: options?.release ?? currentRelease,
    dist: options?.dist ?? currentDist,
    exception: {
      type: normalized.name || "Error",
      message: normalized.message,
      stacktrace: parseStacktrace(normalized.stack),
    },
    attributes: options?.attributes,
    tags: options?.tags,
    extra: options?.extra,
    user: getUser() ?? undefined,
    breadcrumbs: crumbs.length > 0 ? crumbs : undefined,
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
      void shutdown(DEFAULT_SHUTDOWN_TIMEOUT_MS).finally(() => {
        process.exit(1);
      });
    });
  });

  process.on("unhandledRejection", (reason: unknown) => {
    void captureException(reason).finally(() => {
      void shutdown(DEFAULT_SHUTDOWN_TIMEOUT_MS).finally(() => {
        process.exit(1);
      });
    });
  });

  process.on("beforeExit", () => {
    void flush(DEFAULT_SHUTDOWN_TIMEOUT_MS);
  });
}

function normalizeFlushTimeout(timeoutMs?: number): number {
  return timeoutMs ?? DEFAULT_SHUTDOWN_TIMEOUT_MS;
}

async function withTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T | false> {
  if (timeoutMs <= 0) {
    return promise;
  }

  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<false>((resolve) => {
        timeout = setTimeout(() => resolve(false), timeoutMs);
      }),
    ]);
  } finally {
    if (timeout) {
      clearTimeout(timeout);
    }
  }
}

export function init(options: BugBarnClientOptions): void {
  currentApiKey = options.apiKey;
  currentRelease = options.release ?? process.env.BUGBARN_RELEASE ?? undefined;
  currentDist = options.dist ?? process.env.BUGBARN_DIST ?? undefined;
  currentProject = options.project ?? process.env.BUGBARN_PROJECT ?? undefined;
  transport = options.transport ?? createTransport(options.apiKey, options.endpoint, currentProject);

  if (options.installDefaultHandlers !== false) {
    installDefaultHandlers();
  }

  if (options.autoBreadcrumbs !== false) {
    installAutoInterceptors();
  }
}

export async function captureException(error: unknown, options?: CaptureOptions): Promise<void> {
  if (!transport) {
    return;
  }

  await transport.send(buildEnvelope(error, options));
}

export async function flush(timeoutMs?: number): Promise<boolean> {
  if (!transport) {
    return true;
  }

  const normalizedTimeout = normalizeFlushTimeout(timeoutMs);
  const result = await withTimeout(transport.flush({ timeoutMs: normalizedTimeout }), normalizedTimeout);
  return result !== false;
}

export async function shutdown(timeoutMs?: number): Promise<boolean> {
  const drained = await flush(timeoutMs);
  transport = null;
  return drained;
}

export function getApiKey(): string {
  return currentApiKey;
}

export { createTransport };
export { uploadSourceMap, createSourceMapUploader };
export { setUser, clearUser } from "./user.js";
export { addBreadcrumb, clearBreadcrumbs } from "./breadcrumbs.js";
