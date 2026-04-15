const state = {
  apiBase: readApiBase(),
  issues: [],
  issueQuery: "",
  selectedIssueId: null,
  selectedEventId: null,
  liveEvents: [],
  liveError: null,
  liveTimer: null,
  inFlight: new Map(),
};

const elements = {
  apiBase: document.getElementById("api-base"),
  saveApi: document.getElementById("save-api"),
  refreshAll: document.getElementById("refresh-all"),
  issueCount: document.getElementById("issue-count"),
  issueFilter: document.getElementById("issue-filter"),
  issueList: document.getElementById("issue-list"),
  detailTitle: document.getElementById("detail-title"),
  detailBody: document.getElementById("detail-body"),
  liveList: document.getElementById("live-list"),
  liveStatus: document.getElementById("live-status"),
  routeChip: document.getElementById("route-chip"),
  statusText: document.getElementById("status-text"),
};

elements.apiBase.value = state.apiBase;
elements.issueFilter.value = state.issueQuery;

elements.saveApi.addEventListener("click", () => {
  state.apiBase = normalizeBase(elements.apiBase.value);
  persistApiBase(state.apiBase);
  setStatus(`API base saved: ${state.apiBase || "same origin"}`);
  refreshAll();
});

elements.refreshAll.addEventListener("click", refreshAll);
elements.issueFilter.addEventListener("input", () => {
  state.issueQuery = elements.issueFilter.value.trim().toLowerCase();
  renderIssueList();
});

window.addEventListener("hashchange", route);
window.addEventListener("beforeunload", stopLivePolling);

route();
refreshAll();

function readApiBase() {
  const params = new URLSearchParams(location.search);
  const fromQuery = params.get("api");
  if (fromQuery) {
    return normalizeBase(fromQuery);
  }
  return normalizeBase(localStorage.getItem("bugbarn.apiBase") || "");
}

function persistApiBase(value) {
  localStorage.setItem("bugbarn.apiBase", value);
}

function normalizeBase(value) {
  return String(value || "")
    .trim()
    .replace(/\/+$/, "");
}

function apiUrl(path) {
  return `${state.apiBase}${path}`;
}

function setStatus(message) {
  elements.statusText.textContent = message;
}

function setRouteChip(message, tone = "") {
  elements.routeChip.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.routeChip.textContent = message;
}

function setLiveStatus(message, tone = "") {
  elements.liveStatus.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.liveStatus.textContent = message;
}

function route() {
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

async function refreshAll() {
  await Promise.all([loadIssues(), loadLiveEvents(), loadActiveRoute()]);
}

async function loadActiveRoute() {
  if (state.selectedIssueId) {
    await loadIssueDetail(state.selectedIssueId);
  } else if (state.selectedEventId) {
    await loadEventDetail(state.selectedEventId);
  } else if (state.issues.length) {
    if (!location.hash) {
      location.hash = `#/issues/${encodeURIComponent(state.issues[0].id)}`;
    }
  }
}

async function loadIssues() {
  elements.issueCount.textContent = "Loading";
  try {
    const payload = await fetchJson("/api/v1/issues");
    state.issues = normalizeList(payload, "issues");
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
    elements.issueCount.textContent = `${state.issues.length} issues`;
    renderIssueList();
    if (!location.hash && state.issues.length) {
      const firstId = state.issues[0].id ?? state.issues[0].issueId ?? state.issues[0].issue_id;
      if (firstId) {
        location.hash = `#/issues/${encodeURIComponent(firstId)}`;
      }
    }
  } catch (error) {
    state.issues = [];
    elements.issueCount.textContent = "Unavailable";
    renderIssueList(error);
    setStatus(`Issues unavailable: ${error.message}`);
  }
}

async function loadIssueDetail(issueId) {
  setDetailLoading(`Issue ${issueId}`);
  try {
    const [issuePayload, eventsPayload] = await Promise.all([
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events`),
    ]);
    const issue = normalizeObject(issuePayload);
    const events = normalizeList(eventsPayload, "events");
    renderIssueDetail(issue, events);
  } catch (error) {
    renderErrorDetail(`Issue ${issueId}`, error);
  }
}

async function loadEventDetail(eventId) {
  setDetailLoading(`Event ${eventId}`);
  try {
    const eventPayload = await fetchJson(`/api/v1/events/${encodeURIComponent(eventId)}`);
    const event = normalizeObject(eventPayload);
    let issue = null;
    let issueEvents = [];

    if (event.issueId || event.issue_id) {
      const issueId = event.issueId || event.issue_id;
      try {
        const [issuePayload, eventsPayload] = await Promise.all([
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events`),
        ]);
        issue = normalizeObject(issuePayload);
        issueEvents = normalizeList(eventsPayload, "events");
      } catch {
        issueEvents = [];
      }
    }

    renderEventDetail(event, issue, issueEvents);
  } catch (error) {
    renderErrorDetail(`Event ${eventId}`, error);
  }
}

async function loadLiveEvents() {
  setLiveStatus("Polling");
  try {
    const payload = await fetchJson("/api/v1/live/events");
    state.liveEvents = normalizeList(payload, "events");
    state.liveError = null;
    renderLiveList();
    setLiveStatus(`Live ${state.liveEvents.length}`, "warn");
  } catch (error) {
    state.liveEvents = [];
    state.liveError = error;
    renderLiveList();
    setLiveStatus("Unavailable", "bad");
  }

  stopLivePolling();
  state.liveTimer = window.setInterval(() => {
    loadLiveEvents();
  }, 10000);
}

function stopLivePolling() {
  if (state.liveTimer) {
    window.clearInterval(state.liveTimer);
    state.liveTimer = null;
  }
}

async function fetchJson(path) {
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
      return JSON.parse(text);
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

function normalizeList(payload, key) {
  if (!payload) {
    return [];
  }
  if (Array.isArray(payload)) {
    return payload.map(normalizeObject);
  }
  if (Array.isArray(payload[key])) {
    return payload[key].map(normalizeObject);
  }
  if (Array.isArray(payload.items)) {
    return payload.items.map(normalizeObject);
  }
  if (Array.isArray(payload.data)) {
    return payload.data.map(normalizeObject);
  }
  return [];
}

function normalizeObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { value };
  }
  return value;
}

function renderIssueList(error = null) {
  const filtered = state.issues.filter((issue) => {
    if (!state.issueQuery) {
      return true;
    }
    const text = [
      issue.id,
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
      .join(" ")
      .toLowerCase();
    return text.includes(state.issueQuery);
  });

  if (error) {
    elements.issueList.innerHTML = `<div class="error">Issues unavailable. ${escapeHtml(error.message)}</div>`;
    return;
  }

  elements.issueCount.textContent = `${filtered.length} issue${filtered.length === 1 ? "" : "s"}`;

  if (!filtered.length) {
    elements.issueList.innerHTML = `<div class="empty">No issues returned.</div>`;
    return;
  }

  elements.issueList.innerHTML = filtered
    .map((issue) => {
      const id = issue.id ?? issue.issueId ?? issue.issue_id ?? "";
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
      location.hash = `#/issues/${encodeURIComponent(issueId)}`;
    });
  });
}

function renderDetail() {
  if (state.selectedEventId) {
    loadEventDetail(state.selectedEventId);
    return;
  }
  if (state.selectedIssueId) {
    loadIssueDetail(state.selectedIssueId);
    return;
  }

  if (state.issues.length) {
    const issue = state.issues[0];
    renderIssueDetail(issue, []);
    return;
  }

  elements.detailTitle.textContent = "Select an issue";
  elements.detailBody.innerHTML = `<div class="empty">No issues loaded yet.</div>`;
}

function setDetailLoading(title) {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="loading">Loading.</div>`;
}

function renderIssueDetail(issue, events) {
  const id = issue.id ?? issue.issueId ?? issue.issue_id ?? "";
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

function renderEventDetail(event, issue, issueEvents) {
  const id = event.id ?? event.eventId ?? event.event_id ?? "";
  const issueId = issue?.id ?? issue?.issueId ?? issue?.issue_id ?? event.issueId ?? event.issue_id ?? "";
  const title = event.title ?? event.body ?? event.message ?? event.exception?.message ?? "Event";
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

function renderErrorDetail(title, error) {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="error">Unable to load detail. ${escapeHtml(error.message)}</div>`;
}

function renderEventButtons(events, activeId = "") {
  if (!events.length) {
    return `<div class="empty">No events returned.</div>`;
  }

  return `
    <div class="grid">
      ${events
        .map((event) => {
          const id = event.id ?? event.eventId ?? event.event_id ?? "";
          const title = event.title ?? event.body ?? event.message ?? event.exception?.message ?? "Event";
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

function wireIssueDetailActions() {
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

function wireEventDetailActions(issueId) {
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

function wireCopyButtons() {
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

function renderLiveList() {
  if (state.liveError) {
    elements.liveList.innerHTML = `<div class="empty">Live endpoint unavailable. Polling will keep trying.</div>`;
    return;
  }

  if (!state.liveEvents.length) {
    elements.liveList.innerHTML = `<div class="empty">No live events yet.</div>`;
    return;
  }

  elements.liveList.innerHTML = state.liveEvents
    .map((event) => {
      const id = event.id ?? event.eventId ?? event.event_id ?? "";
      const issueId = event.issueId ?? event.issue_id ?? "";
      const title = event.title ?? event.body ?? event.message ?? event.exception?.message ?? "Event";
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

function collectKeyValues(source, omitKeys = []) {
  const omit = new Set(omitKeys);
  return Object.entries(source || {}).reduce((acc, [key, value]) => {
    if (omit.has(key)) {
      return acc;
    }
    if (value === null || value === undefined) {
      return acc;
    }
    if (typeof value === "object") {
      acc[key] = value;
      return acc;
    }
    acc[key] = value;
    return acc;
  }, {});
}

function formatTime(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function escapeAttr(value) {
  return escapeHtml(value).replaceAll("`", "&#96;");
}
