export type RawRecord = Record<string, unknown>;

export interface ApiIssue extends RawRecord {
  ID?: string | number;
  IssueID?: string | number;
  id?: string | number;
  issueId?: string | number;
  issue_id?: string | number;
  Title?: string;
  title?: string;
  NormalizedTitle?: string;
  normalizedTitle?: string;
  normalized_title?: string;
  ExceptionType?: string;
  exceptionType?: string;
  exception_type?: string;
  Fingerprint?: string;
  fingerprint?: string;
  FirstSeen?: string | number;
  firstSeen?: string | number;
  first_seen?: string | number;
  LastSeen?: string | number;
  lastSeen?: string | number;
  last_seen?: string | number;
  EventCount?: number;
  eventCount?: number;
  event_count?: number;
  count?: number;
  Status?: string;
  status?: string;
  fingerprintMaterial?: RawRecord;
  fingerprint_material?: RawRecord;
  fingerprintParts?: RawRecord;
  fingerprint_parts?: RawRecord;
  fingerprintInputs?: RawRecord;
  fingerprint_inputs?: RawRecord;
  fingerprintDebug?: RawRecord;
  fingerprint_debug?: RawRecord;
}

export interface ApiEvent extends RawRecord {
  ID?: string | number;
  EventID?: string | number;
  IssueID?: string | number;
  id?: string | number;
  eventId?: string | number;
  event_id?: string | number;
  issueId?: string | number;
  issue_id?: string | number;
  Title?: string;
  title?: string;
  Body?: string;
  body?: string;
  Message?: string;
  message?: string;
  Timestamp?: string | number;
  timestamp?: string | number;
  CreatedAt?: string | number;
  createdAt?: string | number;
  created_at?: string | number;
  ReceivedAt?: string | number;
  ObservedAt?: string | number;
  Severity?: string;
  SeverityText?: string;
  Payload?: RawRecord;
  payload?: RawRecord;
  severityText?: string;
  severity_text?: string;
  Exception?: RawRecord | { message?: string; Message?: string };
  exception?: RawRecord | { message?: string };
}

export interface ApiRelease extends RawRecord {
  id?: string | number;
  ID?: string | number;
  name?: string;
  Name?: string;
  environment?: string;
  Environment?: string;
  observedAt?: string | number;
  observed_at?: string | number;
  version?: string;
  Version?: string;
  commitSha?: string;
  commit_sha?: string;
  url?: string;
  notes?: string;
  createdAt?: string | number;
  created_at?: string | number;
}

export interface ApiAlert extends RawRecord {
  id?: string | number;
  ID?: string | number;
  name?: string;
  Name?: string;
  enabled?: boolean;
  Enabled?: boolean;
  query?: string;
  Query?: string;
  condition?: string;
  Condition?: string;
  target?: string;
  Target?: string;
  lastTriggeredAt?: string | number;
  last_triggered_at?: string | number;
  createdAt?: string | number;
  created_at?: string | number;
}

export interface ApiApiKey extends RawRecord {
  id?: string | number;
  ID?: string | number;
  name?: string;
  Name?: string;
  projectId?: string | number;
  ProjectID?: string | number;
  scope?: string;
  Scope?: string;
  createdAt?: string;
  CreatedAt?: string;
  lastUsedAt?: string;
  LastUsedAt?: string;
}

export interface ApiProject extends RawRecord {
  id?: string | number;
  ID?: string | number;
  slug?: string;
  Slug?: string;
  name?: string;
  Name?: string;
}

export interface ApiSettings extends RawRecord {
  username?: string;
  Username?: string;
  displayName?: string;
  display_name?: string;
  email?: string;
  Email?: string;
  timezone?: string;
  timezoneName?: string;
  defaultEnvironment?: string;
  default_environment?: string;
  liveWindowMinutes?: number;
  live_window_minutes?: number;
  stacktraceContextLines?: number;
  stacktrace_context_lines?: number;
}

export type IssueSort = "last_seen" | "first_seen" | "event_count";
export type IssueStatus = "all" | "open" | "resolved";

export interface AppState {
  authChecked: boolean;
  authRequired: boolean;
  authenticated: boolean;
  username: string;
  projects: ApiProject[];
  currentProject: string;
  currentEnv: string;
  currentRoute: "issues" | "releases" | "alerts" | "settings";
  issues: ApiIssue[];
  issueQuery: string;
  issueSort: IssueSort;
  issueStatus: IssueStatus;
  selectedIssueId: string | null;
  selectedEventId: string | null;
  releases: ApiRelease[];
  alerts: ApiAlert[];
  settings: ApiSettings | null;
  apiKeys: ApiApiKey[];
  liveEvents: ApiEvent[];
  liveError: Error | null;
  liveTimer: number | null;
  liveSource: EventSource | null;
  liveReconnectDelay: number;
  liveConnected: boolean;
  inFlight: Map<string, Promise<unknown>>;
}

export interface AppElements {
  refreshAll: HTMLButtonElement;
  overviewView: HTMLElement;
  detailView: HTMLElement;
  navLinks: NodeListOf<HTMLAnchorElement>;
  issueCount: HTMLElement;
  issueFilter: HTMLInputElement;
  issueList: HTMLElement;
  detailTitle: HTMLElement;
  detailBody: HTMLElement;
  liveList: HTMLElement;
  liveStatus: HTMLElement;
  routeChip: HTMLElement;
  statusText: HTMLElement;
}
