export type BugBarnClientOptions = {
  apiKey: string;
  endpoint?: string;
  installDefaultHandlers?: boolean;
  release?: string;
  dist?: string;
  /** Project slug within this BugBarn instance. Events and source maps are routed to this project. */
  project?: string;
  transport?: Transport;
};

export type FlushOptions = {
  timeoutMs?: number;
};

export type CaptureOptions = {
  attributes?: Record<string, unknown>;
  release?: string;
  dist?: string;
  tags?: Record<string, string | number | boolean | null>;
  extra?: Record<string, unknown>;
};

export type StackFrame = {
  function?: string;
  file?: string;
  line?: number;
  column?: number;
  module?: string;
};

export type BugBarnEnvelope = {
  timestamp: string;
  severityText: "ERROR";
  body: string;
  release?: string;
  dist?: string;
  exception: {
    type: string;
    message: string;
    stacktrace?: StackFrame[];
  };
  attributes?: Record<string, unknown>;
  tags?: Record<string, string | number | boolean | null>;
  extra?: Record<string, unknown>;
  sender: {
    sdk: {
      name: string;
      version: string;
    };
  };
};

export type Transport = {
  send(event: BugBarnEnvelope): Promise<void>;
  flush(options?: FlushOptions): Promise<boolean | void>;
};

export type SourceMapUploadOptions = {
  apiKey: string;
  endpoint?: string;
  release: string;
  dist?: string;
  /** Project slug — routes the source map to a specific project within the BugBarn instance. */
  project?: string;
  bundleUrl: string;
  /** Source map content as a string, Blob, or ArrayBuffer. Required when sourceMapPath is not set. */
  sourceMap?: string | ArrayBuffer | Blob;
  /** Node.js file path to read the source map from (alternative to sourceMap) */
  sourceMapPath?: string;
  /** Preferred name for the stored source map file */
  sourceMapName?: string;
  /** @deprecated Use sourceMapName instead */
  sourceMapFilename?: string;
};

export type SourceMapUploaderConfig = {
  apiKey: string;
  endpoint?: string;
  release: string;
  dist?: string;
  project?: string;
};
