import {
  filteredIssueCount,
  renderAlertsViewMarkup,
  renderEmptyIssues,
  renderErrorDetailMarkup,
  renderEventDetailMarkup,
  renderIssueDetailMarkup,
  renderIssueListMarkup,
  renderLiveListMarkup,
  renderReleasesViewMarkup,
  renderSettingsViewMarkup,
  renderSetupGuide,
} from "./components.js";
import { normalizeList, normalizeObject, readString } from "./data.js";
import { eventIssueId, eventTimestamp, eventTitle, firstIdentifier, issueTitle } from "./domain.js";
import { escapeHtml, errorMessage } from "./format.js";
import type { ApiAlert, ApiEvent, ApiIssue, ApiRelease, ApiSettings, AppElements, AppState, RawRecord } from "./types.js";

const httpUnauthorized = 401;
const liveWindowMinutes = 15;

const state: AppState = {
  authChecked: false,
  authRequired: false,
  authenticated: false,
  username: "",
  currentRoute: "issues",
  issues: [],
  issueQuery: "",
  selectedIssueId: null,
  selectedEventId: null,
  releases: [],
  alerts: [],
  settings: null,
  liveEvents: [],
  liveError: null,
  liveTimer: null,
  inFlight: new Map<string, Promise<unknown>>(),
};

const elements: AppElements = {
  refreshAll: byId<HTMLButtonElement>("refresh-all"),
  overviewView: byId<HTMLElement>("overview-view"),
  detailView: byId<HTMLElement>("detail-view"),
  navLinks: document.querySelectorAll<HTMLAnchorElement>(".side-nav a"),
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

elements.issueFilter.value = state.issueQuery;

elements.refreshAll.addEventListener("click", () => {
  void refreshAll();
});

window.addEventListener("hashchange", () => {
  route();
  void refreshAll();
});
window.addEventListener("beforeunload", stopLivePolling);

void start();

async function start(): Promise<void> {
  await loadSession();
  route();
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

function apiUrl(path: string): string {
  return path;
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

function setActiveNav(): void {
  const routeGroup = state.currentRoute;
  elements.navLinks.forEach((link) => {
    const target = link.getAttribute("data-route") || "";
    link.classList.toggle("active", target === routeGroup);
  });
}

function route(): void {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const [kind, id] = parts;
  state.selectedIssueId = null;
  state.selectedEventId = null;

  if (kind === "issues" && id) {
    state.currentRoute = "issues";
    state.selectedIssueId = decodeURIComponent(id);
    setRouteChip(`Issue ${state.selectedIssueId}`);
  } else if (kind === "events" && id) {
    state.currentRoute = "issues";
    state.selectedEventId = decodeURIComponent(id);
    setRouteChip(`Event ${state.selectedEventId}`);
  } else if (kind === "releases") {
    state.currentRoute = "releases";
    setRouteChip("Releases");
  } else if (kind === "alerts") {
    state.currentRoute = "alerts";
    setRouteChip("Alerts");
  } else if (kind === "settings") {
    state.currentRoute = "settings";
    setRouteChip("Settings");
  } else {
    state.currentRoute = "issues";
    setRouteChip("Issues");
  }

  setActiveNav();
}

async function refreshAll(): Promise<void> {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }

  await Promise.all([loadIssues(), loadLiveEvents(), loadCurrentRouteData()]);
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

async function loadCurrentRouteData(): Promise<void> {
  if (state.currentRoute === "releases") {
    await loadReleases();
    return;
  }
  if (state.currentRoute === "alerts") {
    await loadAlerts();
    return;
  }
  if (state.currentRoute === "settings") {
    await loadSettings();
    return;
  }
  if (state.selectedEventId) {
    await loadEventDetail(state.selectedEventId);
    return;
  }
  if (state.selectedIssueId) {
    await loadIssueDetail(state.selectedIssueId);
    return;
  }
  renderIssuesView();
}

async function loadIssues(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/issues");
    state.issues = normalizeList<ApiIssue>(payload, "issues");
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      renderIssuesView();
    }
  } catch (error) {
    state.issues = [];
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      renderIssuesView(error);
    }
    setStatus(`Issues unavailable: ${errorMessage(error)}`);
  }
}

async function loadReleases(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/releases", true);
    state.releases = payload ? normalizeList<ApiRelease>(payload, "releases") : [];
    renderReleasesView();
  } catch (error) {
    state.releases = [];
    renderReleasesView(error);
  }
}

async function loadAlerts(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/alerts", true);
    state.alerts = payload ? normalizeList<ApiAlert>(payload, "alerts") : [];
    renderAlertsView();
  } catch (error) {
    state.alerts = [];
    renderAlertsView(error);
  }
}

async function loadSettings(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/settings", true);
    state.settings = payload ? normalizeObject<ApiSettings>(payload, "settings") : null;
    renderSettingsView();
  } catch (error) {
    state.settings = null;
    renderSettingsView(error);
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
    const payload = await fetchJson("/api/v1/live/events", true);
    const events = payload ? normalizeList<ApiEvent>(payload, "events") : [];
    state.liveEvents = events
      .filter((event) => toTimestampMs(eventTimestamp(event)) >= Date.now() - liveWindowMinutes * 60 * 1000)
      .sort((a, b) => toTimestampMs(eventTimestamp(b)) - toTimestampMs(eventTimestamp(a)))
      .slice(0, 12);
    state.liveError = null;
    renderLiveList();
    setLiveStatus(state.liveEvents.length ? `Live ${state.liveEvents.length}` : "Idle", state.liveEvents.length ? "warn" : "");
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

async function fetchJson(path: string, allowMissing = false): Promise<unknown> {
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
    if (allowMissing && response.status === 404) {
      return null;
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

async function postJson(path: string, body: unknown): Promise<unknown> {
  const response = await fetch(apiUrl(path), {
    method: "POST",
    credentials: "include",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (response.status === httpUnauthorized) {
    state.authRequired = true;
    state.authenticated = false;
    renderLogin();
  }
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

async function postFormData(path: string, formData: FormData): Promise<unknown> {
  const response = await fetch(apiUrl(path), {
    method: "POST",
    credentials: "include",
    body: formData,
  });
  if (response.status === httpUnauthorized) {
    state.authRequired = true;
    state.authenticated = false;
    renderLogin();
  }
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

function renderIssuesView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Select an issue";
  elements.detailBody.innerHTML = "";
  const count = filteredIssueCount(state.issues, state.issueQuery);
  elements.overviewView.innerHTML = `
    <div class="view-head">
      <div>
        <p class="eyebrow">Issues</p>
        <h2 id="issue-count">${escapeHtml(error ? "Unavailable" : `${count} issue${count === 1 ? "" : "s"}`)}</h2>
      </div>
      <div class="view-actions">
        <input id="issue-filter" type="search" placeholder="is unresolved" aria-label="Filter issues" />
      </div>
    </div>
    <div id="issue-list" class="list issue-list" aria-live="polite"></div>
  `;
  elements.issueCount = byId<HTMLElement>("issue-count");
  elements.issueFilter = byId<HTMLInputElement>("issue-filter");
  elements.issueList = byId<HTMLElement>("issue-list");
  elements.issueFilter.value = state.issueQuery;
  elements.issueFilter.addEventListener("input", () => {
    state.issueQuery = elements.issueFilter.value.trim().toLowerCase();
    renderIssuesList(error);
  });
  renderIssuesList(error);
}

function renderIssuesList(error: unknown = null): void {
  if (error) {
    elements.issueCount.textContent = "Unavailable";
    elements.issueList.innerHTML = renderIssueListMarkup([], state.issueQuery, state.selectedIssueId, error);
    return;
  }

  const count = filteredIssueCount(state.issues, state.issueQuery);
  elements.issueCount.textContent = `${count} issue${count === 1 ? "" : "s"}`;

  if (!count) {
    elements.issueList.innerHTML = renderEmptyIssues(renderSetupGuide());
    return;
  }

  elements.issueList.innerHTML = renderIssueListMarkup(state.issues, state.issueQuery, state.selectedIssueId);
  elements.issueList.querySelectorAll("[data-issue-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const issueId = button.getAttribute("data-issue-id");
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });
}

function renderReleasesView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Releases";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderReleasesViewMarkup(state.releases, error);
  wireReleaseActions();
}

function renderAlertsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Alerts";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderAlertsViewMarkup(state.alerts, error);
  wireAlertActions();
}

function renderSettingsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Settings";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderSettingsViewMarkup(state.settings, state.username, error);
  wireSettingsActions();
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

  setActiveView("overview");
}

function renderLogin(error = ""): void {
  stopLivePolling();
  setActiveView("detail");
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
  setActiveView("detail");
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[]): void {
  setActiveView("detail");
  elements.detailTitle.textContent = issueTitle(issue);
  elements.detailBody.innerHTML = renderIssueDetailMarkup(issue, events);
  wireIssueDetailActions(firstIdentifier(issue));
}

function renderEventDetail(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[]): void {
  setActiveView("detail");
  const issueId = issue ? firstIdentifier(issue) : eventIssueId(event);
  elements.detailTitle.textContent = eventTitle(event);
  elements.detailBody.innerHTML = renderEventDetailMarkup(event, issue, issueEvents);
  wireEventDetailActions(issueId);
}

function renderErrorDetail(title: string, error: unknown): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = renderErrorDetailMarkup(error);
}

function wireIssueDetailActions(issueId: string): void {
  wireCopyButtons();

  elements.detailBody.querySelectorAll("[data-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });

  elements.detailBody.querySelectorAll("[data-resolve-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      const target = button.getAttribute("data-resolve-issue") || issueId;
      if (target) {
        void toggleIssueStatus(target, "resolved");
      }
    });
  });

  elements.detailBody.querySelectorAll("[data-reopen-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      const target = button.getAttribute("data-reopen-issue") || issueId;
      if (target) {
        void toggleIssueStatus(target, "unresolved");
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

function wireReleaseActions(): void {
  const form = elements.overviewView.querySelector<HTMLFormElement>("#release-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitReleaseForm(form);
  });
}

function wireAlertActions(): void {
  const form = elements.overviewView.querySelector<HTMLFormElement>("#alert-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitAlertForm(form);
  });
}

function wireSettingsActions(): void {
  const settingsForm = elements.overviewView.querySelector<HTMLFormElement>("#settings-form");
  const sourceMapForm = elements.overviewView.querySelector<HTMLFormElement>("#source-map-form");

  settingsForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSettingsForm(settingsForm);
  });

  sourceMapForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSourceMapsForm(sourceMapForm);
  });
}

async function submitReleaseForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/releases", {
      name: String(data.get("name") || ""),
      environment: String(data.get("environment") || ""),
      observedAt: String(data.get("observedAt") || ""),
      version: String(data.get("version") || ""),
      commitSha: String(data.get("commitSha") || ""),
      url: String(data.get("url") || ""),
      notes: String(data.get("notes") || ""),
    });
    setStatus("Release marker saved.");
    await loadReleases();
  } catch (error) {
    setStatus(`Release marker unavailable: ${errorMessage(error)}`);
  }
}

async function submitAlertForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/alerts", {
      name: String(data.get("name") || ""),
      condition: String(data.get("condition") || ""),
      query: String(data.get("query") || ""),
      target: String(data.get("target") || ""),
      enabled: data.get("enabled") !== null,
    });
    setStatus("Alert saved.");
    await loadAlerts();
  } catch (error) {
    setStatus(`Alert unavailable: ${errorMessage(error)}`);
  }
}

async function submitSettingsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/settings", {
      displayName: String(data.get("displayName") || ""),
      timezone: String(data.get("timezone") || ""),
      defaultEnvironment: String(data.get("defaultEnvironment") || ""),
      liveWindowMinutes: Number(data.get("liveWindowMinutes") || liveWindowMinutes),
      stacktraceContextLines: Number(data.get("stacktraceContextLines") || 3),
    });
    setStatus("Settings saved.");
    await loadSettings();
  } catch (error) {
    setStatus(`Settings unavailable: ${errorMessage(error)}`);
  }
}

async function submitSourceMapsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postFormData("/api/v1/source-maps", data);
    setStatus("Source maps uploaded.");
  } catch (error) {
    setStatus(`Source map upload unavailable: ${errorMessage(error)}`);
  }
}

async function toggleIssueStatus(issueId: string, status: "resolved" | "unresolved"): Promise<void> {
  try {
    await postJson(`/api/v1/issues/${encodeURIComponent(issueId)}/${status === "resolved" ? "resolve" : "reopen"}`, {});
    setStatus(`Issue ${issueId} marked ${status}.`);
    await loadIssueDetail(issueId);
    await loadIssues();
  } catch (error) {
    setStatus(`Issue status unavailable: ${errorMessage(error)}`);
  }
}

function renderLiveList(): void {
  elements.liveList.innerHTML = renderLiveListMarkup(state.liveEvents, state.liveError);
  elements.liveList.querySelectorAll("[data-live-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-live-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });
}

function setActiveView(view: "overview" | "detail"): void {
  elements.overviewView.classList.toggle("hidden", view !== "overview");
  elements.detailView.classList.toggle("hidden", view !== "detail");
}

function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}
