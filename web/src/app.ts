type RawRecord = Record<string, unknown>;

interface ApiIssue extends RawRecord {
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

interface ApiEvent extends RawRecord {
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

interface IssueListResponse extends RawRecord {
  issues?: ApiIssue[];
  items?: ApiIssue[];
  data?: ApiIssue[];
}

interface EventListResponse extends RawRecord {
  events?: ApiEvent[];
  items?: ApiEvent[];
  data?: ApiEvent[];
}

interface AppState {
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

interface AppElements {
  apiBase: HTMLInputElement;
  saveApi: HTMLButtonElement;
  refreshAll: HTMLButtonElement;
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

const state: AppState = {
  apiBase: readApiBase(),
  authChecked: false,
  authRequired: false,
  authenticated: false,
  username: "",
  issues: [],
  issueQuery: "",
  selectedIssueId: null,
  selectedEventId: null,
  liveEvents: [],
  liveError: null,
  liveTimer: null,
  inFlight: new Map<string, Promise<unknown>>(),
};

const httpUnauthorized = 401;

const elements: AppElements = {
  apiBase: byId<HTMLInputElement>("api-base"),
  saveApi: byId<HTMLButtonElement>("save-api"),
  refreshAll: byId<HTMLButtonElement>("refresh-all"),
  issueCount: byId<HTMLElement>("issue-count"),
  issueFilter: byId<HTMLInputElement>("issue-filter"),
  issueList: byId<HTMLElement>("issue-list"),
  detailTitle: byId<HTMLElement>("detail-title"),
  detailBody: byId<HTMLElement>("detail-body"),
  liveList: byId<HTMLElement>("live-list"),
  liveStatus: byId<HTMLElement>("live-status"),
  routeChip: byId<HTMLElement>("route-chip"),
  statusText: byId<HTMLElement>("status-text"),
};

elements.apiBase.value = state.apiBase;
elements.issueFilter.value = state.issueQuery;

elements.saveApi.addEventListener("click", () => {
  state.apiBase = normalizeBase(elements.apiBase.value);
  persistApiBase(state.apiBase);
  setStatus(`API base saved: ${state.apiBase || "same origin"}`);
  void start();
});

elements.refreshAll.addEventListener("click", () => {
  void refreshAll();
});

elements.issueFilter.addEventListener("input", () => {
  state.issueQuery = elements.issueFilter.value.trim().toLowerCase();
  renderIssueList();
});

window.addEventListener("hashchange", route);
window.addEventListener("beforeunload", stopLivePolling);

void start();

async function start(): Promise<void> {
  route();
  await loadSession();
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }
  await refreshAll();
}

function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element as T;
}

function readApiBase(): string {
  const params = new URLSearchParams(location.search);
  const fromQuery = params.get("api");
  if (fromQuery) {
    return normalizeBase(fromQuery);
  }
  return normalizeBase(localStorage.getItem("bugbarn.apiBase") || "");
}

function persistApiBase(value: string): void {
  localStorage.setItem("bugbarn.apiBase", value);
}

function normalizeBase(value: string): string {
  return String(value || "")
    .trim()
    .replace(/\/+$/, "");
}

function apiUrl(path: string): string {
  return `${state.apiBase}${path}`;
}

function setStatus(message: string): void {
  elements.statusText.textContent = message;
}

function setRouteChip(message: string, tone = ""): void {
  elements.routeChip.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.routeChip.textContent = message;
}

function setLiveStatus(message: string, tone = ""): void {
  elements.liveStatus.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.liveStatus.textContent = message;
}

function route(): void {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const [kind, id] = parts;
  state.selectedIssueId = null;
  state.selectedEventId = null;

  if (kind === "issues" && id) {
    state.selectedIssueId = decodeURIComponent(id);
    setRouteChip(`Issue ${state.selectedIssueId}`);
  } else if (kind === "events" && id) {
    state.selectedEventId = decodeURIComponent(id);
    setRouteChip(`Event ${state.selectedEventId}`);
  } else {
    setRouteChip("Issues");
  }

  renderIssueList();
  renderDetail();
}

async function refreshAll(): Promise<void> {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }
  await Promise.all([loadIssues(), loadLiveEvents(), loadActiveRoute()]);
}

async function loadSession(): Promise<void> {
  try {
    const response = await fetch(apiUrl("/api/v1/me"), {
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    state.authChecked = true;
    if (response.status === httpUnauthorized) {
      state.authRequired = true;
      state.authenticated = false;
      return;
    }
    if (!response.ok) {
      state.authRequired = false;
      state.authenticated = true;
      return;
    }
    const payload = normalizeObject<RawRecord>(await response.json());
    state.authRequired = Boolean(payload.authEnabled);
    state.authenticated = Boolean(payload.authenticated);
    state.username = readString(payload, ["username"]);
  } catch {
    state.authChecked = true;
    state.authRequired = false;
    state.authenticated = true;
  }
}

async function loadActiveRoute(): Promise<void> {
  if (state.selectedIssueId) {
    await loadIssueDetail(state.selectedIssueId);
    return;
  }

  if (state.selectedEventId) {
    await loadEventDetail(state.selectedEventId);
    return;
  }

  if (state.issues.length && !location.hash) {
    const firstId = firstIdentifier(state.issues[0]);
    if (firstId) {
      location.hash = `#/issues/${encodeURIComponent(firstId)}`;
    }
  }
}

async function loadIssues(): Promise<void> {
  elements.issueCount.textContent = "Loading";
  try {
    const payload = await fetchJson("/api/v1/issues");
    state.issues = normalizeList<ApiIssue>(payload, "issues");
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
    elements.issueCount.textContent = `${state.issues.length} issues`;
    renderIssueList();

    if (!location.hash && state.issues.length) {
      const firstId = firstIdentifier(state.issues[0]);
      if (firstId) {
        location.hash = `#/issues/${encodeURIComponent(firstId)}`;
      }
    }
  } catch (error) {
    state.issues = [];
    elements.issueCount.textContent = "Unavailable";
    renderIssueList(error);
    setStatus(`Issues unavailable: ${errorMessage(error)}`);
  }
}

async function loadIssueDetail(issueId: string): Promise<void> {
  setDetailLoading(`Issue ${issueId}`);
  try {
    const [issuePayload, eventsPayload] = await Promise.all([
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events`),
    ]);
    const issue = normalizeObject<ApiIssue>(issuePayload, "issue");
    const events = normalizeList<ApiEvent>(eventsPayload, "events");
    renderIssueDetail(issue, events);
  } catch (error) {
    renderErrorDetail(`Issue ${issueId}`, error);
  }
}

async function loadEventDetail(eventId: string): Promise<void> {
  setDetailLoading(`Event ${eventId}`);
  try {
    const eventPayload = await fetchJson(`/api/v1/events/${encodeURIComponent(eventId)}`);
    const event = normalizeObject<ApiEvent>(eventPayload, "event");
    let issue: ApiIssue | null = null;
    let issueEvents: ApiEvent[] = [];

    const relatedIssueId = eventIssueId(event);
    if (relatedIssueId) {
      const issueId = String(relatedIssueId);
      try {
        const [issuePayload, eventsPayload] = await Promise.all([
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events`),
        ]);
        issue = normalizeObject<ApiIssue>(issuePayload, "issue");
        issueEvents = normalizeList<ApiEvent>(eventsPayload, "events");
      } catch {
        issueEvents = [];
      }
    }

    renderEventDetail(event, issue, issueEvents);
  } catch (error) {
    renderErrorDetail(`Event ${eventId}`, error);
  }
}

async function loadLiveEvents(): Promise<void> {
  setLiveStatus("Polling");
  try {
    const payload = await fetchJson("/api/v1/live/events");
    state.liveEvents = normalizeList<ApiEvent>(payload, "events");
    state.liveError = null;
    renderLiveList();
    setLiveStatus(`Live ${state.liveEvents.length}`, "warn");
  } catch (error) {
    state.liveEvents = [];
    state.liveError = error instanceof Error ? error : new Error(errorMessage(error));
    renderLiveList();
    setLiveStatus("Unavailable", "bad");
  }

  stopLivePolling();
  state.liveTimer = window.setInterval(() => {
    void loadLiveEvents();
  }, 10000);
}

function stopLivePolling(): void {
  if (state.liveTimer) {
    window.clearInterval(state.liveTimer);
    state.liveTimer = null;
  }
}

async function fetchJson(path: string): Promise<unknown> {
  const url = apiUrl(path);
  const existing = state.inFlight.get(url);
  if (existing) {
    return existing;
  }

  const request = fetch(url, { credentials: "include", headers: { Accept: "application/json" } }).then(async (response) => {
    if (response.status === httpUnauthorized) {
      state.authRequired = true;
      state.authenticated = false;
      renderLogin();
    }
    if (!response.ok) {
      throw new Error(`${response.status} ${response.statusText}`.trim());
    }

    const text = await response.text();
    if (!text) {
      return null;
    }

    try {
      return JSON.parse(text) as unknown;
    } catch {
      return text;
    }
  });

  state.inFlight.set(url, request);
  try {
    return await request;
  } finally {
    state.inFlight.delete(url);
  }
}

function normalizeList<T extends RawRecord = RawRecord>(payload: unknown, key: string): T[] {
  if (!payload) {
    return [];
  }
  if (Array.isArray(payload)) {
    return payload.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload[key])) {
    return payload[key].map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.items)) {
    return payload.items.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.data)) {
    return payload.data.map((item) => normalizeObject<T>(item));
  }
  return [];
}

function normalizeObject<T extends RawRecord = RawRecord>(value: unknown, key = ""): T {
  if (!isRecord(value)) {
    return { value } as unknown as T;
  }
  if (key && isRecord(value[key])) {
    return value[key] as T;
  }
  return value as T;
}

function isRecord(value: unknown): value is RawRecord {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function readFirst(source: RawRecord, keys: string[]): unknown {
  for (const key of keys) {
    const value = source[key];
    if (value !== null && value !== undefined && value !== "") {
      return value;
    }
  }
  return "";
}

function readString(source: RawRecord, keys: string[]): string {
  const value = readFirst(source, keys);
  return typeof value === "string" || typeof value === "number" ? String(value) : "";
}

function readNumber(source: RawRecord, keys: string[]): number {
  const value = readFirst(source, keys);
  if (typeof value === "number") {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (!Number.isNaN(parsed)) {
      return parsed;
    }
  }
  return 0;
}

function readRecord(source: RawRecord, keys: string[]): RawRecord {
  const value = readFirst(source, keys);
  return isRecord(value) ? value : {};
}

function hasKeys(source: RawRecord): boolean {
  return Object.keys(source).length > 0;
}

function issueTitle(issue: ApiIssue): string {
  return readString(issue, ["title", "Title", "normalizedTitle", "NormalizedTitle", "normalized_title"]) || "Untitled issue";
}

function issueNormalizedTitle(issue: ApiIssue): string {
  return readString(issue, ["normalizedTitle", "NormalizedTitle", "normalized_title"]);
}

function issueExceptionType(issue: ApiIssue): string {
  return readString(issue, ["exceptionType", "ExceptionType", "exception_type"]);
}

function issueFingerprint(issue: ApiIssue): string {
  return readString(issue, ["fingerprint", "Fingerprint"]);
}

function issueLastSeen(issue: ApiIssue): unknown {
  return readFirst(issue, ["lastSeen", "LastSeen", "last_seen"]);
}

function issueFirstSeen(issue: ApiIssue): unknown {
  return readFirst(issue, ["firstSeen", "FirstSeen", "first_seen"]);
}

function issueEventCount(issue: ApiIssue, fallback = 0): number {
  return readNumber(issue, ["eventCount", "EventCount", "event_count", "count"]) || fallback;
}

function eventIssueId(event: ApiEvent): string {
  return readString(event, ["issueId", "IssueID", "issue_id"]);
}

function eventTitle(event: ApiEvent): string {
  return (
    readString(event, ["title", "Title", "body", "Body", "message", "Message"]) ||
    readNestedMessage(readFirst(event, ["exception", "Exception"])) ||
    "Event"
  );
}

function eventTimestamp(event: ApiEvent): unknown {
  return readFirst(event, ["timestamp", "Timestamp", "createdAt", "CreatedAt", "created_at", "receivedAt", "ReceivedAt", "observedAt", "ObservedAt"]);
}

function eventSeverity(event: ApiEvent): string {
  return readString(event, ["severityText", "SeverityText", "severity_text", "severity", "Severity"]);
}

function eventPayload(event: ApiEvent): RawRecord {
  return readRecord(event, ["payload", "Payload"]);
}

function eventException(event: ApiEvent): RawRecord {
  const payload = eventPayload(event);
  const direct = readRecord(event, ["exception", "Exception"]);
  return hasKeys(direct) ? direct : readRecord(payload, ["exception", "Exception"]);
}

function eventRawScrubbed(event: ApiEvent): RawRecord {
  return readRecord(eventPayload(event), ["rawScrubbed", "raw_scrubbed", "RawScrubbed"]);
}

function eventSdkName(event: ApiEvent): string {
  const payload = eventPayload(event);
  const sender = readRecord(eventRawScrubbed(event), ["sender", "Sender"]);
  const sdk = readRecord(sender, ["sdk", "SDK"]);
  return readString(payload, ["sdkName", "SDKName", "sdk_name"]) || readString(sdk, ["name", "Name"]);
}

function eventTraceId(event: ApiEvent): string {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  return readString(payload, ["traceId", "trace_id", "TraceID"]) || readString(raw, ["traceId", "trace_id", "TraceID"]);
}

function eventContext(event: ApiEvent): RawRecord {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  const context: RawRecord = {};
  for (const [label, value] of [
    ["Ingest id", readString(payload, ["ingestId", "ingest_id", "IngestID"])],
    ["SDK", eventSdkName(event)],
    ["Trace id", eventTraceId(event)],
    ["Exception type", readString(eventException(event), ["type", "Type"])],
    ["Exception message", readString(eventException(event), ["message", "Message"])],
  ] as const) {
    if (value) {
      context[label] = value;
    }
  }
  for (const [label, record] of [
    ["Resource", readRecord(payload, ["resource", "Resource"])],
    ["Attributes", readRecord(payload, ["attributes", "Attributes"])],
    ["Tags", readRecord(raw, ["tags", "Tags"])],
  ] as const) {
    if (Object.keys(record).length) {
      context[label] = record;
    }
  }
  return context;
}

function eventStacktrace(event: ApiEvent): unknown[] {
  const exception = eventException(event);
  const direct = readFirst(eventPayload(event), ["stacktrace", "Stacktrace", "stackTrace", "StackTrace"]);
  const nested = readFirst(exception, ["stacktrace", "Stacktrace", "stackTrace", "StackTrace"]);
  if (Array.isArray(direct)) {
    return direct;
  }
  if (Array.isArray(nested)) {
    return nested;
  }
  return [];
}

function eventSpans(event: ApiEvent): unknown[] {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  const direct = readFirst(payload, ["spans", "Spans"]);
  const rawSpans = readFirst(raw, ["spans", "Spans"]);
  if (Array.isArray(direct)) {
    return direct;
  }
  if (Array.isArray(rawSpans)) {
    return rawSpans;
  }
  return [];
}

function renderIssueList(error: unknown = null): void {
  const filtered = state.issues.filter((issue) => {
    if (!state.issueQuery) {
      return true;
    }
    const text = [
      firstIdentifier(issue),
      issueTitle(issue),
      issueExceptionType(issue),
      issueNormalizedTitle(issue),
      issueFingerprint(issue),
      issueLastSeen(issue),
    ]
      .filter(Boolean)
      .map((value) => String(value))
      .join(" ")
      .toLowerCase();
    return text.includes(state.issueQuery);
  });

  if (error) {
    elements.issueList.innerHTML = `<div class="error">Issues unavailable. ${escapeHtml(errorMessage(error))}</div>`;
    return;
  }

  elements.issueCount.textContent = `${filtered.length} issue${filtered.length === 1 ? "" : "s"}`;

  if (!filtered.length) {
    elements.issueList.innerHTML = renderEmptyIssues();
    return;
  }

  elements.issueList.innerHTML = `
    <div class="issue-table-head">
      <span>Issue</span>
      <span>Last seen</span>
      <span>Events</span>
    </div>
    ${filtered
      .map((issue) => {
      const id = firstIdentifier(issue);
      const title = issueTitle(issue);
      const count = issueEventCount(issue);
      const lastSeen = formatTime(issueLastSeen(issue));
      const active = id && String(id) === String(state.selectedIssueId) ? "active" : "";
      return `
        <button class="item issue-row ${active}" type="button" data-issue-id="${escapeAttr(id)}">
          <div class="item-title"><span class="status-dot"></span>${escapeHtml(title)}</div>
          <span class="issue-cell">${escapeHtml(lastSeen || "No timestamp")}</span>
          <span class="issue-cell">${escapeHtml(String(count))}</span>
          <div class="item-meta">
            <span>${escapeHtml(issueExceptionType(issue) || "Error")}</span>
            <span>${escapeHtml(id)}</span>
            <span>Ongoing</span>
          </div>
        </button>
      `;
    })
      .join("")}
  `;

  elements.issueList.querySelectorAll("[data-issue-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const issueId = button.getAttribute("data-issue-id");
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });
}

function renderDetail(): void {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }
  if (state.selectedEventId) {
    void loadEventDetail(state.selectedEventId);
    return;
  }
  if (state.selectedIssueId) {
    void loadIssueDetail(state.selectedIssueId);
    return;
  }

  if (state.issues.length) {
    const issue = state.issues[0];
    renderIssueDetail(issue, []);
    return;
  }

  elements.detailTitle.textContent = "Start sending errors";
  elements.detailBody.innerHTML = renderSetupGuide();
}

function renderLogin(error = ""): void {
  stopLivePolling();
  setRouteChip("Login", "warn");
  elements.issueCount.textContent = "Locked";
  elements.issueList.innerHTML = `<div class="empty">Log in to view issues.</div>`;
  elements.liveStatus.textContent = "Locked";
  elements.liveList.innerHTML = `<div class="empty">Live events require a session.</div>`;
  elements.detailTitle.textContent = "Log in";
  elements.detailBody.innerHTML = `
    <form class="section login-form" id="login-form">
      <p class="muted">Use the admin credentials configured for this BugBarn instance.</p>
      ${error ? `<div class="error">${escapeHtml(error)}</div>` : ""}
      <label class="field">
        <span>Username</span>
        <input name="username" type="text" autocomplete="username" required />
      </label>
      <label class="field">
        <span>Password</span>
        <input name="password" type="password" autocomplete="current-password" required />
      </label>
      <div class="link-row">
        <button type="submit">Log in</button>
      </div>
    </form>
  `;
  const form = document.getElementById("login-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    const formData = new FormData(form as HTMLFormElement);
    void login(String(formData.get("username") || ""), String(formData.get("password") || ""));
  });
}

async function login(username: string, password: string): Promise<void> {
  try {
    const response = await fetch(apiUrl("/api/v1/login"), {
      method: "POST",
      credentials: "include",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ username, password }),
    });
    if (!response.ok) {
      renderLogin("Invalid username or password.");
      return;
    }
    const payload = normalizeObject<RawRecord>(await response.json());
    state.authRequired = Boolean(payload.authEnabled);
    state.authenticated = Boolean(payload.authenticated);
    state.username = readString(payload, ["username"]);
    setStatus(state.username ? `Logged in as ${state.username}.` : "Logged in.");
    await refreshAll();
  } catch (error) {
    renderLogin(errorMessage(error));
  }
}

function setDetailLoading(title: string): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="loading">Loading.</div>`;
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[]): void {
  const id = firstIdentifier(issue);
  const title = issueTitle(issue);
  const normalizedTitle = issueNormalizedTitle(issue);
  const exceptionType = issueExceptionType(issue);
  const fingerprint = issueFingerprint(issue);
  const firstSeen = formatTime(issueFirstSeen(issue));
  const lastSeen = formatTime(issueLastSeen(issue));
  const eventCount = issueEventCount(issue, events.length);
  const fields = collectKeyValues(issue, [
    "id",
    "ID",
    "issueId",
    "IssueID",
    "issue_id",
    "title",
    "Title",
    "normalizedTitle",
    "NormalizedTitle",
    "normalized_title",
    "exceptionType",
    "ExceptionType",
    "exception_type",
    "fingerprint",
    "Fingerprint",
    "firstSeen",
    "FirstSeen",
    "first_seen",
    "lastSeen",
    "LastSeen",
    "last_seen",
    "eventCount",
    "EventCount",
    "event_count",
  ]);

  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `
    <div class="section">
      <div class="link-row">
        <button type="button" data-copy-id="${escapeAttr(id)}">Copy issue id</button>
      </div>
      <div class="grid">
        <div class="kv"><span>Issue id</span><span>${escapeHtml(String(id || "n/a"))}</span></div>
        <div class="kv"><span>Title</span><span>${escapeHtml(title)}</span></div>
        <div class="kv"><span>Exception</span><span>${escapeHtml(exceptionType || "n/a")}</span></div>
        <div class="kv"><span>Fingerprint</span><span>${escapeHtml(fingerprint || "n/a")}</span></div>
        <div class="kv"><span>First seen</span><span>${escapeHtml(firstSeen || "n/a")}</span></div>
        <div class="kv"><span>Last seen</span><span>${escapeHtml(lastSeen || "n/a")}</span></div>
        <div class="kv"><span>Events</span><span>${escapeHtml(String(eventCount))}</span></div>
      </div>
    </div>
    <div class="section">
      <h3>Events</h3>
      ${renderEventButtons(events)}
    </div>
    ${renderDataSection("Representative event", readRecord(issue, ["representativeEvent", "RepresentativeEvent"]))}
    <div class="section">
      <h3>Issue data</h3>
      <pre class="pre">${escapeHtml(JSON.stringify(fields, null, 2))}</pre>
    </div>
  `;

  wireIssueDetailActions();
}

function renderEventDetail(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[]): void {
  const id = firstIdentifier(event);
  const issueId = issue ? firstIdentifier(issue) : eventIssueId(event);
  const title = eventTitle(event);
  const timestamp = formatTime(eventTimestamp(event));
  const payload = eventPayload(event);
  const exception = eventException(event);
  const rawScrubbed = eventRawScrubbed(event);
  const context = eventContext(event);
  const stacktrace = eventStacktrace(event);
  const spans = eventSpans(event);
  const fields = collectKeyValues(event, [
    "id",
    "ID",
    "eventId",
    "EventID",
    "event_id",
    "issueId",
    "IssueID",
    "issue_id",
    "title",
    "Title",
    "body",
    "Body",
    "message",
    "Message",
    "timestamp",
    "Timestamp",
    "createdAt",
    "CreatedAt",
    "created_at",
    "receivedAt",
    "ReceivedAt",
    "observedAt",
    "ObservedAt",
  ]);

  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `
    <div class="section">
      <div class="link-row">
        <button type="button" data-open-issue="${escapeAttr(issueId)}" ${issueId ? "" : "disabled"}>Open issue</button>
        <button type="button" data-copy-id="${escapeAttr(id)}" ${id ? "" : "disabled"}>Copy event id</button>
        ${renderEventNavigation(issueEvents, id)}
      </div>
      <div class="grid">
        <div class="kv"><span>Event id</span><span>${escapeHtml(String(id || "n/a"))}</span></div>
        <div class="kv"><span>Issue id</span><span>${escapeHtml(String(issueId || "n/a"))}</span></div>
        <div class="kv"><span>Timestamp</span><span>${escapeHtml(timestamp || "n/a")}</span></div>
        <div class="kv"><span>Severity</span><span>${escapeHtml(eventSeverity(event) || "n/a")}</span></div>
      </div>
    </div>
    ${renderDataSection("Exception", exception)}
    ${renderDataSection("Context", context)}
    ${renderStacktrace(stacktrace)}
    ${renderSpans(spans)}
    <div class="section">
      <h3>Issue events</h3>
      ${renderEventButtons(issueEvents, id)}
    </div>
    ${renderDataSection("Scrubbed payload", rawScrubbed)}
    ${renderDataSection("Normalized payload", payload)}
    <div class="section">
      <h3>Event data</h3>
      <pre class="pre">${escapeHtml(JSON.stringify(fields, null, 2))}</pre>
    </div>
  `;

  wireEventDetailActions(issueId);
}

function renderDataSection(title: string, data: RawRecord): string {
  if (!hasKeys(data)) {
    return "";
  }
  return `
    <div class="section">
      <h3>${escapeHtml(title)}</h3>
      ${renderRecord(data)}
    </div>
  `;
}

function renderRecord(data: RawRecord): string {
  const entries = Object.entries(data).filter(([, value]) => value !== null && value !== undefined && value !== "");
  if (!entries.length) {
    return `<div class="empty">No data returned.</div>`;
  }
  return `
    <div class="grid">
      ${entries
        .map(([key, value]) => {
          const rendered = isRecord(value) || Array.isArray(value) ? `<pre class="pre compact">${escapeHtml(JSON.stringify(value, null, 2))}</pre>` : `<span>${escapeHtml(String(value))}</span>`;
          return `<div class="kv"><span>${escapeHtml(key)}</span>${rendered}</div>`;
        })
        .join("")}
    </div>
  `;
}

function renderStacktrace(stacktrace: unknown[]): string {
  if (!stacktrace.length) {
    return "";
  }
  return `
    <div class="section">
      <h3>Stacktrace</h3>
      <div class="stacktrace">
        ${stacktrace
          .map((frame, index) => {
            if (!isRecord(frame)) {
              return `<div class="frame"><span>#${index + 1}</span><code>${escapeHtml(String(frame))}</code></div>`;
            }
            const fn = readString(frame, ["function", "Function", "name", "Name"]) || "<anonymous>";
            const file = readString(frame, ["file", "File", "filename", "Filename", "path", "Path"]);
            const line = readString(frame, ["line", "Line", "lineno", "Lineno"]);
            const column = readString(frame, ["column", "Column", "colno", "Colno"]);
            const location = [file, line ? `:${line}` : "", column ? `:${column}` : ""].join("");
            return `
              <div class="frame">
                <span>#${index + 1}</span>
                <div>
                  <code>${escapeHtml(fn)}</code>
                  <small>${escapeHtml(location || "unknown source")}</small>
                </div>
              </div>
            `;
          })
          .join("")}
      </div>
    </div>
  `;
}

function renderSpans(spans: unknown[]): string {
  if (!spans.length) {
    return "";
  }
  return `
    <div class="section">
      <h3>Spans</h3>
      <pre class="pre">${escapeHtml(JSON.stringify(spans, null, 2))}</pre>
    </div>
  `;
}

function renderEventNavigation(events: ApiEvent[], activeId: string): string {
  if (!events.length || !activeId) {
    return "";
  }
  const index = events.findIndex((event) => firstIdentifier(event) === activeId);
  if (index < 0) {
    return "";
  }
  const previousId = index > 0 ? firstIdentifier(events[index - 1]) : "";
  const nextId = index < events.length - 1 ? firstIdentifier(events[index + 1]) : "";
  return `
    <button type="button" data-event-id="${escapeAttr(previousId)}" ${previousId ? "" : "disabled"}>Previous event</button>
    <button type="button" data-event-id="${escapeAttr(nextId)}" ${nextId ? "" : "disabled"}>Next event</button>
  `;
}

function renderErrorDetail(title: string, error: unknown): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="error">Unable to load detail. ${escapeHtml(errorMessage(error))}</div>`;
}

function renderEventButtons(events: ApiEvent[], activeId = ""): string {
  if (!events.length) {
    return `<div class="empty">No events returned.</div>`;
  }

  return `
    <div class="grid">
      ${events
        .map((event) => {
          const id = firstIdentifier(event);
          const title = eventTitle(event);
          const timestamp = formatTime(eventTimestamp(event));
          const active = activeId && String(activeId) === String(id) ? "active" : "";
          return `
            <button class="item ${active}" type="button" data-event-id="${escapeAttr(id)}">
              <div class="item-title">${escapeHtml(title)}</div>
              <div class="item-meta">
                <span>${escapeHtml(String(id || "n/a"))}</span>
                <span>${escapeHtml(timestamp || "No timestamp")}</span>
              </div>
            </button>
          `;
        })
        .join("")}
    </div>
  `;
}

function wireIssueDetailActions(): void {
  wireCopyButtons();

  elements.detailBody.querySelectorAll("[data-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });
}

function wireEventDetailActions(issueId: string): void {
  elements.detailBody.querySelectorAll("[data-open-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });

  wireCopyButtons();

  elements.detailBody.querySelectorAll("[data-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const id = button.getAttribute("data-event-id");
      if (id) {
        location.hash = `#/events/${encodeURIComponent(id)}`;
      }
    });
  });
}

function wireCopyButtons(): void {
  elements.detailBody.querySelectorAll("[data-copy-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      const id = button.getAttribute("data-copy-id");
      if (!id) {
        return;
      }
      try {
        await navigator.clipboard.writeText(id);
        setStatus(`Copied ${id}.`);
      } catch {
        setStatus(`Could not copy ${id}.`);
      }
    });
  });
}

function renderLiveList(): void {
  if (state.liveError) {
    elements.liveList.innerHTML = `<div class="empty">Live endpoint unavailable. Polling will keep trying.</div>`;
    return;
  }

  if (!state.liveEvents.length) {
    elements.liveList.innerHTML = `
      <div class="empty">
        <strong>No live events yet.</strong>
        <p>Send an exception with one of the SDK snippets and this list will update on the next poll.</p>
      </div>
    `;
    return;
  }

  elements.liveList.innerHTML = state.liveEvents
    .map((event) => {
      const id = firstIdentifier(event);
      const issueId = eventIssueId(event);
      const title = eventTitle(event);
      const timestamp = formatTime(eventTimestamp(event));
      return `
        <button class="item" type="button" data-live-event-id="${escapeAttr(id)}">
          <div class="item-title">${escapeHtml(title)}</div>
          <div class="item-meta">
            <span>${escapeHtml(String(issueId || "No issue"))}</span>
            <span>${escapeHtml(timestamp || "No timestamp")}</span>
          </div>
        </button>
      `;
    })
    .join("");

  elements.liveList.querySelectorAll("[data-live-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-live-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });
}

function collectKeyValues(source: RawRecord, omitKeys: string[] = []): RawRecord {
  const omit = new Set(omitKeys);
  return Object.entries(source || {}).reduce<RawRecord>((acc, [key, value]) => {
    if (omit.has(key)) {
      return acc;
    }
    if (value === null || value === undefined) {
      return acc;
    }
    acc[key] = value;
    return acc;
  }, {});
}

function renderEmptyIssues(): string {
  return `
    <div class="empty">
      <strong>No issues yet.</strong>
      <p>Connect an app with the BugBarn API key. New exceptions will appear here after the background worker processes them.</p>
    </div>
  `;
}

function renderSetupGuide(): string {
  const endpoint = `${state.apiBase || window.location.origin}/api/v1/events`;
  const packageUrl = `${state.apiBase || window.location.origin}/packages/typescript/bugbarn-typescript-0.1.0.tgz`;
  const testApiKeyCommand = "kubectl -n bugbarn-testing get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo";
  const stagingApiKeyCommand = "kubectl -n bugbarn-staging get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo";
  return `
    <div class="section">
      <p class="muted">Use your BugBarn API key and send errors to:</p>
      <pre class="pre">${escapeHtml(endpoint)}</pre>
    </div>
    <div class="section">
      <h3>API key</h3>
      <p class="muted">Read the key from the cluster secret for the environment you are sending to.</p>
      <pre class="pre">${escapeHtml(`# Testing
${testApiKeyCommand}

# Staging
${stagingApiKeyCommand}`)}</pre>
      <p class="muted">If the command prints replace-me-testing or replace-me-staging, rotate the secret before connecting a real application.</p>
    </div>
    <div class="section">
      <h3>SDK package</h3>
      <p class="muted">Install the hosted TypeScript SDK tarball directly from this BugBarn instance.</p>
      <pre class="pre">${escapeHtml(`cd /Users/wiebe/webwiebe/rapid-root
pnpm add ${packageUrl}`)}</pre>
    </div>
    <div class="section">
      <h3>TypeScript</h3>
      <pre class="pre">${escapeHtml(`import { init } from "@bugbarn/typescript";

init({
  apiKey: process.env.BUGBARN_API_KEY ?? "",
  endpoint: "${endpoint}",
});`)}</pre>
    </div>
    <div class="section">
      <h3>Python</h3>
      <pre class="pre">${escapeHtml(`import os
from bugbarn import init

init(
    api_key=os.environ["BUGBARN_API_KEY"],
    endpoint="${endpoint}",
    install_excepthook=True,
)`)}</pre>
    </div>
    <div class="section">
      <h3>Smoke test</h3>
      <pre class="pre">${escapeHtml(`curl -X POST ${endpoint} \\
  -H "content-type: application/json" \\
  -H "x-bugbarn-api-key: $BUGBARN_API_KEY" \\
  --data '{"body":"BugBarn smoke test","exception":{"type":"SmokeError","message":"BugBarn smoke test"}}'`)}</pre>
    </div>
  `;
}

function formatTime(value: unknown): string {
  if (value === null || value === undefined || value === "") {
    return "";
  }
  const date = new Date(value as string | number | Date);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function escapeHtml(value: unknown): string {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function escapeAttr(value: unknown): string {
  return escapeHtml(value).replaceAll("`", "&#96;");
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

function firstIdentifier(source: ApiIssue | ApiEvent, extraOmitKeys: string[] = []): string {
  const omit = new Set(extraOmitKeys);
  const keys = ["id", "ID", "issueId", "IssueID", "issue_id", "eventId", "EventID", "event_id"].filter((key) => !omit.has(key));
  const value = readFirst(source, keys);
  if (value === null || value === undefined || value === "") {
    return "";
  }
  return String(value);
}

function readNestedMessage(value: unknown): string {
  if (!isRecord(value)) {
    return "";
  }
  const message = readFirst(value, ["message", "Message"]);
  return typeof message === "string" ? message : "";
}
