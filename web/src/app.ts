type RawRecord = Record<string, unknown>;

interface ApiIssue extends RawRecord {
  id?: string | number;
  issueId?: string | number;
  issue_id?: string | number;
  title?: string;
  normalizedTitle?: string;
  normalized_title?: string;
  exceptionType?: string;
  exception_type?: string;
  fingerprint?: string;
  firstSeen?: string | number;
  first_seen?: string | number;
  lastSeen?: string | number;
  last_seen?: string | number;
  eventCount?: number;
  event_count?: number;
  count?: number;
}

interface ApiEvent extends RawRecord {
  id?: string | number;
  eventId?: string | number;
  event_id?: string | number;
  issueId?: string | number;
  issue_id?: string | number;
  title?: string;
  body?: string;
  message?: string;
  timestamp?: string | number;
  createdAt?: string | number;
  created_at?: string | number;
  severityText?: string;
  severity_text?: string;
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
  issues: [],
  issueQuery: "",
  selectedIssueId: null,
  selectedEventId: null,
  liveEvents: [],
  liveError: null,
  liveTimer: null,
  inFlight: new Map<string, Promise<unknown>>(),
};

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
  void refreshAll();
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

route();
void refreshAll();

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
  await Promise.all([loadIssues(), loadLiveEvents(), loadActiveRoute()]);
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
    const issue = normalizeObject<ApiIssue>(issuePayload);
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
    const event = normalizeObject<ApiEvent>(eventPayload);
    let issue: ApiIssue | null = null;
    let issueEvents: ApiEvent[] = [];

    const relatedIssueId = event.issueId ?? event.issue_id;
    if (relatedIssueId !== null && relatedIssueId !== undefined && relatedIssueId !== "") {
      const issueId = String(relatedIssueId);
      try {
        const [issuePayload, eventsPayload] = await Promise.all([
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events`),
        ]);
        issue = normalizeObject<ApiIssue>(issuePayload);
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

  const request = fetch(url, { headers: { Accept: "application/json" } }).then(async (response) => {
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

function normalizeObject<T extends RawRecord = RawRecord>(value: unknown): T {
  if (!isRecord(value)) {
    return { value } as unknown as T;
  }
  return value as T;
}

function isRecord(value: unknown): value is RawRecord {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function renderIssueList(error: unknown = null): void {
  const filtered = state.issues.filter((issue) => {
    if (!state.issueQuery) {
      return true;
    }
    const text = [
      issue.id,
      issue.issueId,
      issue.issue_id,
      issue.title,
      issue.exceptionType,
      issue.exception_type,
      issue.normalizedTitle,
      issue.normalized_title,
      issue.fingerprint,
      issue.lastSeen,
      issue.last_seen,
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

  elements.issueList.innerHTML = filtered
    .map((issue) => {
      const id = firstIdentifier(issue);
      const title = issue.title ?? issue.normalizedTitle ?? issue.normalized_title ?? "Untitled issue";
      const count = issue.eventCount ?? issue.event_count ?? issue.count ?? 0;
      const lastSeen = formatTime(issue.lastSeen ?? issue.last_seen);
      const active = id && String(id) === String(state.selectedIssueId) ? "active" : "";
      return `
        <button class="item ${active}" type="button" data-issue-id="${escapeAttr(id)}">
          <div class="item-title">${escapeHtml(title)}</div>
          <div class="item-meta">
            <span>${escapeHtml(String(count))} events</span>
            <span>${escapeHtml(lastSeen || "No timestamp")}</span>
          </div>
        </button>
      `;
    })
    .join("");

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

function setDetailLoading(title: string): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="loading">Loading.</div>`;
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[]): void {
  const id = firstIdentifier(issue);
  const title = issue.title ?? issue.normalizedTitle ?? issue.normalized_title ?? "Untitled issue";
  const normalizedTitle = issue.normalizedTitle ?? issue.normalized_title ?? "";
  const exceptionType = issue.exceptionType ?? issue.exception_type ?? "";
  const fingerprint = issue.fingerprint ?? "";
  const firstSeen = formatTime(issue.firstSeen ?? issue.first_seen);
  const lastSeen = formatTime(issue.lastSeen ?? issue.last_seen);
  const eventCount = issue.eventCount ?? issue.event_count ?? events.length ?? 0;
  const fields = collectKeyValues(issue, [
    "id",
    "issueId",
    "issue_id",
    "title",
    "normalizedTitle",
    "normalized_title",
    "exceptionType",
    "exception_type",
    "fingerprint",
    "firstSeen",
    "first_seen",
    "lastSeen",
    "last_seen",
    "eventCount",
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
        <div class="kv"><span>Normalized title</span><span>${escapeHtml(normalizedTitle || "n/a")}</span></div>
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
    <div class="section">
      <h3>Issue data</h3>
      <pre class="pre">${escapeHtml(JSON.stringify(fields, null, 2))}</pre>
    </div>
  `;

  wireIssueDetailActions();
}

function renderEventDetail(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[]): void {
  const id = firstIdentifier(event);
  const issueId = issue ? firstIdentifier(issue) : firstIdentifier(event, ["id", "eventId", "event_id"]);
  const title = event.title ?? event.body ?? event.message ?? readNestedMessage(event.exception) ?? "Event";
  const timestamp = formatTime(event.timestamp ?? event.createdAt ?? event.created_at);
  const fields = collectKeyValues(event, [
    "id",
    "eventId",
    "event_id",
    "issueId",
    "issue_id",
    "title",
    "body",
    "message",
    "timestamp",
    "createdAt",
    "created_at",
  ]);

  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `
    <div class="section">
      <div class="link-row">
        <button type="button" data-open-issue="${escapeAttr(issueId)}">Open issue</button>
        <button type="button" data-copy-id="${escapeAttr(id)}">Copy event id</button>
      </div>
      <div class="grid">
        <div class="kv"><span>Event id</span><span>${escapeHtml(String(id || "n/a"))}</span></div>
        <div class="kv"><span>Issue id</span><span>${escapeHtml(String(issueId || "n/a"))}</span></div>
        <div class="kv"><span>Timestamp</span><span>${escapeHtml(timestamp || "n/a")}</span></div>
        <div class="kv"><span>Severity</span><span>${escapeHtml(event.severityText ?? event.severity_text ?? "n/a")}</span></div>
      </div>
    </div>
    <div class="section">
      <h3>Issue events</h3>
      ${renderEventButtons(issueEvents, id)}
    </div>
    <div class="section">
      <h3>Event data</h3>
      <pre class="pre">${escapeHtml(JSON.stringify(fields, null, 2))}</pre>
    </div>
  `;

  wireEventDetailActions(issueId);
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
          const title = event.title ?? event.body ?? event.message ?? readNestedMessage(event.exception) ?? "Event";
          const timestamp = formatTime(event.timestamp ?? event.createdAt ?? event.created_at);
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
      const issueId = event.issueId ?? event.issue_id;
      const title = event.title ?? event.body ?? event.message ?? readNestedMessage(event.exception) ?? "Event";
      const timestamp = formatTime(event.timestamp ?? event.createdAt ?? event.created_at);
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
  return `
    <div class="section">
      <p class="muted">Use your BugBarn API key and send errors to:</p>
      <pre class="pre">${escapeHtml(endpoint)}</pre>
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
  const value = source.id ?? source.issueId ?? source.issue_id ?? source.eventId ?? source.event_id;
  if (value === null || value === undefined || value === "") {
    return "";
  }
  return String(value);
}

function readNestedMessage(value: unknown): string {
  if (!isRecord(value)) {
    return "";
  }
  const message = value.message;
  return typeof message === "string" ? message : "";
}
