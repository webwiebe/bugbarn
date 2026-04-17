export type BugBarnClientOptions = {
  apiKey: string;
  endpoint?: string;
  installDefaultHandlers?: boolean;
  release?: string;
  dist?: string;
  transport?: Transport;
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
  flush(): Promise<void>;
};

export type SourceMapUploadOptions = {
  apiKey: string;
  endpoint?: string;
  release: string;
  dist?: string;
  bundleUrl: string;
  sourceMap: string | ArrayBuffer | Blob;
  sourceMapFilename?: string;
};
