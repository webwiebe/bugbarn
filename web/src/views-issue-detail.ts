// Issue / event detail views: loading, rendering, action wiring (copy, resolve,
// mute, navigation), and the issue status mutations.
import { renderErrorDetailMarkup, renderEventDetailMarkup, renderIssueDetailMarkup } from "./components.js";
import { normalizeList, normalizeObject } from "./data.js";
import { eventIssueId, eventTitle, firstIdentifier, issueTitle } from "./domain.js";
import { errorMessage } from "./format.js";
import type { ApiEvent, ApiIssue } from "./types.js";
import { elements, setActiveView, setStatus, showFlash, state } from "./core.js";
import { apiFetch, fetchJson, postJson } from "./http.js";
import { loadIssues } from "./views-issues.js";

export async function loadIssueDetail(issueId: string): Promise<void> {
  setDetailLoading(`Issue ${issueId}`);
  try {
    const [issuePayload, eventsPayload] = await Promise.all([
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25`),
    ]);
    const issue = normalizeObject<ApiIssue>(issuePayload, "issue");
    const raw = eventsPayload as Record<string, unknown>;
    const events = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    renderIssueDetail(issue, events, hasMore);
  } catch (error) {
    renderErrorDetail(`Issue ${issueId}`, error);
  }
}

export async function loadEventDetail(eventId: string): Promise<void> {
  setDetailLoading(`Event ${eventId}`);
  try {
    const eventPayload = await fetchJson(`/api/v1/events/${encodeURIComponent(eventId)}`);
    const event = normalizeObject<ApiEvent>(eventPayload, "event");
    let issue: ApiIssue | null = null;
    let issueEvents: ApiEvent[] = [];
    let eventsHasMore = false;

    const relatedIssueId = eventIssueId(event);
    if (relatedIssueId) {
      const issueId = String(relatedIssueId);
      try {
        const [issuePayload, eventsPayload] = await Promise.all([
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25`),
        ]);
        issue = normalizeObject<ApiIssue>(issuePayload, "issue");
        const eventsRaw = eventsPayload as Record<string, unknown>;
        issueEvents = normalizeList<ApiEvent>(eventsRaw, "events");
        eventsHasMore = Boolean(eventsRaw?.["hasMore"]);
      } catch {
        issueEvents = [];
      }
    }

    renderEventDetail(event, issue, issueEvents, eventsHasMore);
  } catch (error) {
    renderErrorDetail(`Event ${eventId}`, error);
  }
}

export function setDetailLoading(title: string): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="loading">Loading.</div>`;
  setActiveView("detail");
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[], hasMore = false): void {
  setActiveView("detail");
  elements.detailTitle.textContent = issueTitle(issue);
  elements.detailBody.innerHTML = renderIssueDetailMarkup(issue, events, state.releases, hasMore);
  wireIssueDetailActions(firstIdentifier(issue));

  if (hasMore) {
    const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
    btn?.addEventListener("click", () => {
      const oldestId = events.length > 0
        ? Number(String(events[0].ID ?? events[0].id ?? "0").replace(/\D/g, "")) || 0
        : 0;
      void loadOlderEvents(firstIdentifier(issue), oldestId, events);
    });
  }
}

async function loadOlderEvents(issueId: string, beforeRowId: number, existing: ApiEvent[]): Promise<void> {
  const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
  if (btn) btn.disabled = true;
  try {
    const payload = await fetchJson(
      `/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25&before=${beforeRowId}`
    );
    const raw = payload as Record<string, unknown>;
    const older = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    const combined = [...older, ...existing];
    renderIssueDetail(
      normalizeObject<ApiIssue>({ ID: issueId }, "issue"),
      combined,
      hasMore,
    );
    // Re-fetch the issue so the header stays accurate — use cached issues list.
    const cached = state.issues.find((i) => String(firstIdentifier(i)) === issueId);
    if (cached) {
      renderIssueDetail(cached, combined, hasMore);
    }
  } catch {
    if (btn) btn.disabled = false;
  }
}

async function loadOlderEventsForEvent(
  currentEvent: ApiEvent,
  issue: ApiIssue | null,
  issueId: string,
  beforeRowId: number,
  existing: ApiEvent[],
): Promise<void> {
  const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
  if (btn) btn.disabled = true;
  try {
    const payload = await fetchJson(
      `/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25&before=${beforeRowId}`
    );
    const raw = payload as Record<string, unknown>;
    const older = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    const combined = [...older, ...existing];
    renderEventDetail(currentEvent, issue, combined, hasMore);
  } catch {
    if (btn) btn.disabled = false;
  }
}

function renderEventDetail(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[], hasMore = false): void {
  setActiveView("detail");
  const issueId = issue ? firstIdentifier(issue) : eventIssueId(event);
  elements.detailTitle.textContent = issueId ? `${issueId} — ${firstIdentifier(event)}` : firstIdentifier(event) || eventTitle(event);
  elements.detailBody.innerHTML = renderEventDetailMarkup(event, issue, issueEvents, hasMore, state.releases);
  wireEventDetailActions(issueId);

  if (hasMore && issueId) {
    const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
    btn?.addEventListener("click", () => {
      const oldestId = issueEvents.length > 0
        ? Number(String(issueEvents[0].ID ?? issueEvents[0].id ?? "0").replace(/\D/g, "")) || 0
        : 0;
      void loadOlderEventsForEvent(event, issue, issueId, oldestId, issueEvents);
    });
  }
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

  elements.detailBody.querySelectorAll("[data-action='mute-issue']").forEach((button) => {
    button.addEventListener("click", () => {
      const id = (button as HTMLElement).dataset.issueId ?? issueId;
      const muteMode = (document.getElementById("mute-mode-select") as HTMLSelectElement | null)?.value ?? "until_regression";
      void muteIssue(id, muteMode);
    });
  });

  elements.detailBody.querySelectorAll("[data-action='unmute-issue']").forEach((button) => {
    button.addEventListener("click", () => {
      const id = (button as HTMLElement).dataset.issueId ?? issueId;
      void unmuteIssue(id);
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

async function muteIssue(id: string, muteMode: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/issues/${encodeURIComponent(id)}/mute`, {
      method: "PATCH",
      body: JSON.stringify({ mute_mode: muteMode }),
    });
    if (res.ok) {
      setStatus(`Issue ${id} muted.`);
      await loadIssues();
      if (state.selectedIssueId === id) {
        await loadIssueDetail(id);
      }
    } else {
      showFlash(`Mute failed: ${res.status} ${res.statusText}`.trim(), "error");
    }
  } catch (error) {
    showFlash(`Mute failed: ${errorMessage(error)}`, "error");
  }
}

async function unmuteIssue(id: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/issues/${encodeURIComponent(id)}/unmute`, { method: "PATCH" });
    if (res.ok) {
      setStatus(`Issue ${id} unmuted.`);
      await loadIssues();
      if (state.selectedIssueId === id) {
        await loadIssueDetail(id);
      }
    } else {
      showFlash(`Unmute failed: ${res.status} ${res.statusText}`.trim(), "error");
    }
  } catch (error) {
    showFlash(`Unmute failed: ${errorMessage(error)}`, "error");
  }
}

async function toggleIssueStatus(issueId: string, status: "resolved" | "unresolved"): Promise<void> {
  try {
    await postJson(`/api/v1/issues/${encodeURIComponent(issueId)}/${status === "resolved" ? "resolve" : "reopen"}`, {});
    showFlash(`Issue ${issueId} marked ${status}.`, "success");
    await loadIssueDetail(issueId);
    await loadIssues();
  } catch (error) {
    showFlash(`Issue status change failed: ${errorMessage(error)}`, "error");
  }
}
