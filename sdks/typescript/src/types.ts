export type BugBarnClientOptions = {
  apiKey: string;
  endpoint?: string;
  installDefaultHandlers?: boolean;
  transport?: Transport;
};

export type CaptureOptions = {
  tags?: Record<string, string | number | boolean | null>;
  extra?: Record<string, unknown>;
};

export type BugBarnEvent = {
  sdk: "bugbarn.typescript";
  message: string;
  exception: {
    type: string;
    value: string;
    stack?: string;
  };
  timestamp: string;
  tags?: Record<string, string | number | boolean | null>;
  extra?: Record<string, unknown>;
};

export type Transport = {
  send(event: BugBarnEvent): Promise<void>;
  flush(): Promise<void>;
};
