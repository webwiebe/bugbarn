import { collectKeyValues, hasKeys, isRecord, readString } from "./data.js";
import {
  eventContext,
  eventException,
  eventIssueId,
  eventPayload,
  eventRawScrubbed,
  eventSeverity,
  eventSpans,
  eventStacktrace,
  eventTimestamp,
  eventTitle,
  eventTraceId,
  eventUrl,
  firstIdentifier,
  issueEventCount,
  issueExceptionType,
  issueFingerprint,
  issueFirstSeen,
  issueLastSeen,
  issueNormalizedTitle,
  issueTitle,
} from "./domain.js";
import { escapeAttr, escapeHtml, errorMessage, formatAge, formatTime } from "./format.js";
import type { ApiEvent, ApiIssue, RawRecord } from "./types.js";

export function renderIssueListMarkup(issues: ApiIssue[], query: string, selectedIssueId: string | null, error: unknown = null): string {
  if (error) {
    return `<div class="error">Issues unavailable. ${escapeHtml(errorMessage(error))}</div>`;
  }

  const filtered = filterIssues(issues, query);
  if (!filtered.length) {
    return "";
  }

  return `
    <div class="issue-table-head">
      <span>Issue</span>
      <span>Last seen</span>
      <span>Age</span>
      <span>Events</span>
    </div>
    ${filtered
      .map((issue) => {
        const id = firstIdentifier(issue);
        const title = issueTitle(issue);
        const count = issueEventCount(issue);
        const lastSeen = formatTime(issueLastSeen(issue));
        const age = formatAge(issueFirstSeen(issue));
        const active = id && String(id) === String(selectedIssueId) ? "active" : "";
        return `
          <button class="item issue-row ${active}" type="button" data-issue-id="${escapeAttr(id)}">
            <div class="item-title"><span class="status-dot"></span>${escapeHtml(title)}</div>
            <span class="issue-cell">${escapeHtml(lastSeen || "No timestamp")}</span>
            <span class="issue-cell">${escapeHtml(age || "n/a")}</span>
            <span class="issue-cell">${escapeHtml(String(count))}</span>
            <div class="item-meta">
              <span>${escapeHtml(issueExceptionType(issue) || "Error")}</span>
              <span>${escapeHtml(id)}</span>
            </div>
          </button>
        `;
      })
      .join("")}
  `;
}

export function filteredIssueCount(issues: ApiIssue[], query: string): number {
  return filterIssues(issues, query).length;
}

export function renderEmptyIssues(setupGuide: string): string {
  return `
    <div class="empty">
      <strong>No issues yet.</strong>
      <p>Connect an app with the BugBarn API key. New exceptions will appear here after the background worker processes them.</p>
      ${setupGuide}
    </div>
  `;
}

export function renderIssueDetailMarkup(issue: ApiIssue, events: ApiEvent[]): string {
  const id = firstIdentifier(issue);
  const title = issueTitle(issue);
  const normalizedTitle = issueNormalizedTitle(issue);
  const exceptionType = issueExceptionType(issue);
  const fingerprint = issueFingerprint(issue);
  const firstSeen = formatTime(issueFirstSeen(issue));
  const lastSeen = formatTime(issueLastSeen(issue));
  const eventCount = issueEventCount(issue, events.length);
  const lastEvent = events[0];
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

  return `
    <div class="issue-hero">
      <div>
        <p class="eyebrow">${escapeHtml(exceptionType || "Error")}</p>
        <h3>${escapeHtml(title)}</h3>
        <p class="muted">${escapeHtml(normalizedTitle || fingerprint || id || "No fingerprint")}</p>
      </div>
      <div class="link-row">
        <button type="button" data-copy-id="${escapeAttr(id)}" ${id ? "" : "disabled"}>Copy issue id</button>
      </div>
    </div>
    <div class="issue-stats">
      <div><span>Events</span><strong>${escapeHtml(String(eventCount))}</strong></div>
      <div><span>First seen</span><strong>${escapeHtml(firstSeen || "n/a")}</strong></div>
      <div><span>Last seen</span><strong>${escapeHtml(lastSeen || "n/a")}</strong></div>
      <div><span>Fingerprint</span><strong>${escapeHtml(fingerprint || "n/a")}</strong></div>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Events in this issue</h3>
        ${renderEventButtons(events)}
      </div>
      ${lastEvent ? renderDataSection("Latest event context", eventContext(lastEvent)) : ""}
      ${lastEvent ? renderDataSection("Latest exception", eventException(lastEvent)) : ""}
      ${renderDataSection("Issue data", fields)}
    </div>
  `;
}

export function renderEventDetailMarkup(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[]): string {
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

  return `
    <div class="issue-hero">
      <div>
        <p class="eyebrow">${escapeHtml(eventSeverity(event) || "event")}</p>
        <h3>${escapeHtml(title)}</h3>
        <p class="muted">${escapeHtml(eventUrl(event) || eventTraceId(event) || id || "No request context")}</p>
      </div>
      <div class="link-row">
        <button type="button" data-open-issue="${escapeAttr(issueId)}" ${issueId ? "" : "disabled"}>Open issue</button>
        <button type="button" data-copy-id="${escapeAttr(id)}" ${id ? "" : "disabled"}>Copy event id</button>
        ${renderEventNavigation(issueEvents, id)}
      </div>
    </div>
    <div class="issue-stats">
      <div><span>Event id</span><strong>${escapeHtml(String(id || "n/a"))}</strong></div>
      <div><span>Issue id</span><strong>${escapeHtml(String(issueId || "n/a"))}</strong></div>
      <div><span>Timestamp</span><strong>${escapeHtml(timestamp || "n/a")}</strong></div>
      <div><span>Severity</span><strong>${escapeHtml(eventSeverity(event) || "n/a")}</strong></div>
    </div>
    <div class="detail-main">
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
      ${renderDataSection("Event data", fields)}
    </div>
  `;
}

export function renderErrorDetailMarkup(error: unknown): string {
  return `<div class="error">Unable to load detail. ${escapeHtml(errorMessage(error))}</div>`;
}

export function renderLiveListMarkup(events: ApiEvent[], liveError: Error | null): string {
  if (liveError) {
    return `<div class="empty">Live endpoint unavailable. Polling will keep trying.</div>`;
  }

  if (!events.length) {
    return `
      <div class="empty">
        <strong>No live events yet.</strong>
        <p>Send an exception with one of the SDK snippets and this list will update on the next poll.</p>
      </div>
    `;
  }

  return events
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
}

export function renderSetupGuide(apiBase: string): string {
  const endpoint = `${apiBase || window.location.origin}/api/v1/events`;
  const packageUrl = `${apiBase || window.location.origin}/packages/typescript/bugbarn-typescript-0.1.0.tgz`;
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

function filterIssues(issues: ApiIssue[], query: string): ApiIssue[] {
  const normalizedQuery = query.trim().toLowerCase();
  return issues.filter((issue) => {
    if (!normalizedQuery) {
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
    return text.includes(normalizedQuery);
  });
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
