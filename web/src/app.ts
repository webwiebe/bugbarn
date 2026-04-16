import {
  filteredIssueCount,
  renderEmptyIssues,
  renderErrorDetailMarkup,
  renderEventDetailMarkup,
  renderIssueDetailMarkup,
  renderIssueListMarkup,
  renderLiveListMarkup,
  renderSetupGuide,
} from "./components.js";
import { normalizeList, normalizeObject, readString } from "./data.js";
import { eventIssueId, eventTitle, firstIdentifier, issueTitle } from "./domain.js";
import { escapeHtml, errorMessage } from "./format.js";
import type { ApiEvent, ApiIssue, AppElements, AppState, RawRecord } from "./types.js";

const httpUnauthorized = 401;

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

const elements: AppElements = {
  apiBase: byId<HTMLInputElement>("api-base"),
  saveApi: byId<HTMLButtonElement>("save-api"),
  refreshAll: byId<HTMLButtonElement>("refresh-all"),
  overviewView: byId<HTMLElement>("overview-view"),
  detailView: byId<HTMLElement>("detail-view"),
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

  setActiveView(state.selectedIssueId || state.selectedEventId ? "detail" : "overview");
  renderIssueList();
  renderDetail();
}

function setActiveView(view: "overview" | "detail"): void {
  elements.overviewView.classList.toggle("hidden", view !== "overview");
  elements.detailView.classList.toggle("hidden", view !== "detail");
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
  }
}

async function loadIssues(): Promise<void> {
  elements.issueCount.textContent = "Loading";
  try {
    const payload = await fetchJson("/api/v1/issues");
    state.issues = normalizeList<ApiIssue>(payload, "issues");
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
    renderIssueList();
  } catch (error) {
    state.issues = [];
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

function renderIssueList(error: unknown = null): void {
  if (error) {
    elements.issueCount.textContent = "Unavailable";
    elements.issueList.innerHTML = renderIssueListMarkup([], state.issueQuery, state.selectedIssueId, error);
    return;
  }

  const count = filteredIssueCount(state.issues, state.issueQuery);
  elements.issueCount.textContent = `${count} issue${count === 1 ? "" : "s"}`;

  if (!count) {
    elements.issueList.innerHTML = renderEmptyIssues(renderSetupGuide(state.apiBase));
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
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[]): void {
  setActiveView("detail");
  elements.detailTitle.textContent = issueTitle(issue);
  elements.detailBody.innerHTML = renderIssueDetailMarkup(issue, events);
  wireIssueDetailActions();
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
