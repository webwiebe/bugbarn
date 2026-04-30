export type RawRecord = Record<string, unknown>;

export interface BreadcrumbEntry {
  timestamp?: string;
  category?: string;
  message?: string;
  level?: string;
  data?: Record<string, unknown>;
}

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
  hourly_counts?: number[];
  mute_mode?: string;
  project_slug?: string;
  last_regressed_at?: string | number;
  LastRegressedAt?: string | number;
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
  User?: { ID?: string; Email?: string; Username?: string; id?: string; email?: string; username?: string };
  user?: { id?: string; email?: string; username?: string };
  Breadcrumbs?: BreadcrumbEntry[];
  breadcrumbs?: BreadcrumbEntry[];
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
  ObservedAt?: string | number;
  version?: string;
  Version?: string;
  commitSha?: string;
  commit_sha?: string;
  CommitSHA?: string;
  url?: string;
  URL?: string;
  notes?: string;
  Notes?: string;
  createdAt?: string | number;
  created_at?: string | number;
  CreatedAt?: string | number;
  createdBy?: string;
  created_by?: string;
  CreatedBy?: string;
}

export interface ApiAlert extends RawRecord {
  id?: string;
  name?: string;
  enabled?: boolean;
  condition?: string;
  webhook_url?: string;
  threshold?: number;
  cooldown_minutes?: number;
  last_fired_at?: string;
  created_at?: string;
  project_slug?: string;
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
  status?: string;
  Status?: string;
}

export interface ApiLogEntry {
  id: number
  received_at: string
  level_num: number
  level: string
  message: string
  data?: Record<string, unknown>
  project_slug?: string
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

export interface AnalyticsOverview {
  pageviews: number;
  sessions: number;
  pages: number;
  avgDurationMs: number;
}

export interface AnalyticsPage extends RawRecord { pathname: string; pageviews: number; sessions: number; }
export interface AnalyticsBucket extends RawRecord { date: string; pageviews: number; sessions: number; }
export interface AnalyticsReferrer extends RawRecord { host: string; pageviews: number; sessions: number; }
export interface AnalyticsSegmentBucket extends RawRecord { value: string; pageviews: number; sessions: number; }

export type IssueSort = "last_seen" | "first_seen" | "event_count";
export type IssueStatus = "all" | "open" | "resolved" | "muted";

export interface AppState {
  authChecked: boolean;
  authRequired: boolean;
  authenticated: boolean;
  username: string;
  projects: ApiProject[];
  currentProject: string;
  currentEnv: string;
  currentRoute: "issues" | "releases" | "alerts" | "settings" | "logs" | "analytics";
  issues: ApiIssue[];
  issueQuery: string;
  issueSort: IssueSort;
  issueStatus: IssueStatus;
  selectedIssueId: string | null;
  selectedEventId: string | null;
  selectedReleaseId: string | null;
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
  logs: ApiLogEntry[];
  logLevel: string;
  logSearch: string;
  logSSE: EventSource | null;
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
