export type BugBarnClientOptions = {
  apiKey: string;
  endpoint?: string;
  installDefaultHandlers?: boolean;
  transport?: Transport;
};

export type CaptureOptions = {
  attributes?: Record<string, unknown>;
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
