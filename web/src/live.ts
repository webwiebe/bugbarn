// Live event stream (SSE), the live-events list rendering, and regression
// desktop notifications.
import { renderLiveListMarkup } from "./components.js";
import type { ApiEvent } from "./types.js";
import {
  elements,
  liveWindowMinutes,
  notifiedRegressionKey,
  setLiveStatus,
  state,
} from "./core.js";

export async function loadLiveEvents(): Promise<void> {
  startLiveStream();
}

export function startLiveStream(): void {
  stopLiveStream();

  const since = new Date(Date.now() - liveWindowMinutes * 60 * 1000).toISOString();
  const url = `/api/v1/events/stream?since=${encodeURIComponent(since)}`;

  const source = new EventSource(url);
  state.liveSource = source;

  source.onopen = () => {
    state.liveConnected = true;
    state.liveReconnectDelay = 3000;
    state.liveError = null;
    setLiveStatus("Connected", "ok");
  };

  source.onmessage = (ev: MessageEvent) => {
    try {
      const event = JSON.parse(ev.data as string) as ApiEvent;
      state.liveEvents = [event, ...state.liveEvents].slice(0, 200);
      state.liveError = null;
      renderLiveList();
      setLiveStatus(`Live ${state.liveEvents.length}`, "warn");
      notifyRegression(event);
    } catch {
      // malformed event data — skip
    }
  };

  source.onerror = () => {
    state.liveConnected = false;
    source.close();
    state.liveSource = null;
    setLiveStatus("Reconnecting", "warn");

    const delay = state.liveReconnectDelay;
    state.liveReconnectDelay = Math.min(delay * 2, 30000);
    state.liveTimer = window.setTimeout(() => {
      state.liveTimer = null;
      startLiveStream();
    }, delay);
  };
}

function _stopLivePolling(): void {
  stopLiveStream();
}

export function stopLiveStream(): void {
  if (state.liveSource) {
    state.liveSource.close();
    state.liveSource = null;
  }
  if (state.liveTimer) {
    window.clearTimeout(state.liveTimer);
    state.liveTimer = null;
  }
  state.liveConnected = false;
}

function notifyRegression(event: ApiEvent): void {
  const regressed = (event as Record<string, unknown>)["Regressed"] ?? (event as Record<string, unknown>)["regressed"];
  if (!regressed) return;
  if (!("Notification" in window) || Notification.permission !== "granted") return;

  const issueId = String(event.IssueID ?? event.issueId ?? event.issue_id ?? "");
  if (!issueId) return;

  const notified: string[] = JSON.parse(localStorage.getItem(notifiedRegressionKey) || "[]");
  if (notified.includes(issueId)) return;

  notified.push(issueId);
  if (notified.length > 200) notified.splice(0, notified.length - 200);
  localStorage.setItem(notifiedRegressionKey, JSON.stringify(notified));

  const title = String(event.Message ?? event.message ?? event.Title ?? event.title ?? "Issue regressed");
  if ("serviceWorker" in navigator && navigator.serviceWorker.controller) {
    navigator.serviceWorker.ready.then((reg) => {
      reg.showNotification("BugBarn: Issue Regressed", {
        body: title,
        icon: "/icons/icon-192.png",
        tag: `regressed-${issueId}`,
        data: { url: `${location.origin}/#/issues/${encodeURIComponent(issueId)}` },
      });
    });
  } else {
    new Notification("BugBarn: Issue Regressed", {
      body: title,
      icon: "/icons/icon-192.png",
      tag: `regressed-${issueId}`,
    });
  }
}

export function requestNotificationPermission(): void {
  if (!("Notification" in window)) return;
  if (Notification.permission !== "default") return;
  Notification.requestPermission();
}

export function renderLiveList(): void {
  elements.liveList.innerHTML = renderLiveListMarkup(state.liveEvents, state.liveError, state.releases);
  elements.liveList.querySelectorAll("[data-live-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-live-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });
}
