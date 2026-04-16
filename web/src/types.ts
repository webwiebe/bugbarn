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

export interface AppState {
  apiBase: string;
  authChecked: boolean;
  authRequired: boolean;
  authenticated: boolean;
  username: string;
  issues: ApiIssue[];
  issueQuery: string;
  selectedIssueId: string | null;
  selectedEventId: string | null;
  liveEvents: ApiEvent[];
  liveError: Error | null;
  liveTimer: number | null;
  inFlight: Map<string, Promise<unknown>>;
}

export interface AppElements {
  apiBase: HTMLInputElement;
  saveApi: HTMLButtonElement;
  refreshAll: HTMLButtonElement;
  overviewView: HTMLElement;
  detailView: HTMLElement;
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
