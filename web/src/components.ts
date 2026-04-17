import { collectKeyValues, hasKeys, isRecord, readFirst, readString } from "./data.js";
import {
  eventContext,
  eventException,
  eventIssueId,
  eventPayload,
  eventRawScrubbed,
  eventSeverity,
  eventSpanId,
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
  issueFingerprintMaterial,
  issueFirstSeen,
  issueLastSeen,
  issueNormalizedTitle,
  issueSeverity,
  issueStatus,
  issueTitle,
} from "./domain.js";
import { escapeAttr, escapeHtml, errorMessage, formatAge, formatTime } from "./format.js";
import type { ApiAlert, ApiApiKey, ApiEvent, ApiIssue, ApiRelease, ApiSettings, RawRecord } from "./types.js";

const nearbyReleaseWindowMs = 72 * 60 * 60 * 1000; // 72 hours
const maxNearbyReleases = 5;
const occurrenceBucketCount = 24;

export function renderIssueListMarkup(issues: ApiIssue[], query: string, selectedIssueId: string | null, error: unknown = null): string {
  if (error) {
    return `<div class="error">Issues unavailable. ${escapeHtml(errorMessage(error))}</div>`;
  }

  const filtered = query ? filterIssues(issues, query) : issues;
  if (!filtered.length) {
    return "";
  }
  const maxEvents = filtered.reduce((max, issue) => Math.max(max, issueEventCount(issue)), 1);

  return `
    <div class="issue-table-head">
      <span>Issue</span>
      <span>Severity</span>
      <span>Last seen</span>
      <span>First seen</span>
      <span>Trend</span>
      <span>Events</span>
    </div>
    ${filtered
      .map((issue) => {
        const id = firstIdentifier(issue);
        const title = issueTitle(issue);
        const count = issueEventCount(issue);
        const lastSeen = formatTime(issueLastSeen(issue));
        const firstSeen = formatTime(issueFirstSeen(issue));
        const active = id && String(id) === String(selectedIssueId) ? "active" : "";
        const severity = issueSeverity(issue);
        const severityClass = severity === "error" || severity === "fatal" ? "bad" : severity === "warning" ? "warn" : "";
        return `
          <button class="item issue-row ${active}" type="button" data-issue-id="${escapeAttr(id)}">
            <div class="item-title"><span class="status-dot"></span>${escapeHtml(title)}</div>
            <span class="issue-cell"><span class="chip ${severityClass}" style="font-size:0.7rem">${escapeHtml(severity || "n/a")}</span></span>
            <span class="issue-cell">${escapeHtml(lastSeen || "No timestamp")}</span>
            <span class="issue-cell">${escapeHtml(firstSeen || "n/a")}</span>
            <span class="issue-cell">${renderIssueCountMeter(count, maxEvents)}</span>
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

export function renderIssueDetailMarkup(issue: ApiIssue, events: ApiEvent[], releases: ApiRelease[] = [], hasMore = false): string {
  const id = firstIdentifier(issue);
  const title = issueTitle(issue);
  const normalizedTitle = issueNormalizedTitle(issue);
  const exceptionType = issueExceptionType(issue);
  const fingerprint = issueFingerprint(issue);
  const firstSeen = formatTime(issueFirstSeen(issue));
  const lastSeen = formatTime(issueLastSeen(issue));
  const status = issueStatus(issue);
  const eventCount = issueEventCount(issue, events.length);
  const lastEvent = events[0] || null;
  const fingerprintMaterial = buildFingerprintMaterial(issue, lastEvent);
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
    "status",
    "Status",
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
        ${status === "resolved" ? `<button type="button" data-reopen-issue="${escapeAttr(id)}" ${id ? "" : "disabled"}>Reopen issue</button>` : `<button type="button" data-resolve-issue="${escapeAttr(id)}" ${id ? "" : "disabled"}>Resolve issue</button>`}
      </div>
    </div>
    <div class="issue-stats">
      <div><span>Events</span><strong>${escapeHtml(String(eventCount))}</strong></div>
      <div><span>First seen</span><strong>${escapeHtml(firstSeen || "n/a")}</strong></div>
      <div><span>Last seen</span><strong>${escapeHtml(lastSeen || "n/a")}</strong></div>
      <div><span>Status</span><strong>${escapeHtml(status)}</strong></div>
    </div>
    <div class="detail-main">
      ${renderOccurrenceTimeline(events, releases, "", eventCount, hasMore)}
      ${renderFingerprintSection(fingerprint, fingerprintMaterial)}
      ${lastEvent ? renderDataSection("Exception", eventException(lastEvent)) : renderEmptySection("Exception", "No exception data returned.")}
      ${lastEvent ? renderDataSection("Context", eventContext(lastEvent)) : renderEmptySection("Context", "No contextual fields were returned for the latest event.")}
      ${renderStacktrace(lastEvent ? eventStacktrace(lastEvent) : [])}
      ${renderNearbyReleasesPanel(releases, issueLastSeen(issue))}
      <div class="section section-scrollable">
        <h3>Events in this issue</h3>
        ${hasMore ? `<button type="button" data-load-older class="load-older-btn">Load older events</button>` : ""}
        ${renderEventButtons(events, "", "scrollable-list")}
      </div>
      ${lastEvent ? renderDataSection("Latest event payload", eventPayload(lastEvent)) : ""}
      ${lastEvent ? renderDataSection("Scrubbed payload", eventRawScrubbed(lastEvent)) : ""}
      ${renderDataSection("Issue data", fields)}
    </div>
  `;
}

export function renderEventDetailMarkup(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[], hasMore = false, releases: ApiRelease[] = []): string {
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
    "traceId",
    "trace_id",
    "TraceID",
    "spanId",
    "span_id",
    "SpanID",
  ]);

  return `
    <div class="issue-hero">
      <div>
        <p class="eyebrow">${escapeHtml(eventSeverity(event) || "event")}</p>
        <h3>${escapeHtml(title)}</h3>
        <p class="muted">${escapeHtml(eventUrl(event) || eventTraceId(event) || eventSpanId(event) || id || "No request context")}</p>
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
      ${renderOccurrenceTimeline(issueEvents, releases, id, issueEvents.length, hasMore)}
      ${renderDataSection("Exception", exception)}
      ${renderDataSection("Context", context)}
      ${renderStacktrace(stacktrace)}
      <div class="section section-scrollable">
        <h3>Issue events</h3>
        ${hasMore ? `<button type="button" data-load-older class="load-older-btn">Load older events</button>` : ""}
        ${renderEventButtons(issueEvents, id, "scrollable-list")}
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

export function renderLiveListMarkup(events: ApiEvent[], liveError: Error | null, releases: ApiRelease[] = []): string {
  if (liveError) {
    return `<div class="empty">Live stream unavailable. Reconnecting…</div>`;
  }

  if (!events.length) {
    return `
      <div class="empty">
        <strong>No live events yet.</strong>
        <p>Send an exception with one of the SDK snippets and new events will appear here in real time.</p>
      </div>
    `;
  }

  // Sort releases descending by observedAt for efficient lookup
  const sortedReleases = releases
    .map((r) => ({ release: r, ts: toTimestampMs(readFirst(r, ["observedAt", "observed_at", "createdAt", "created_at"])) }))
    .filter((r) => r.ts > 0)
    .sort((a, b) => b.ts - a.ts);

  const rows: string[] = [];
  const emittedRelease = new Set<number>();

  for (let i = 0; i < events.length; i++) {
    const event = events[i];
    const eventTs = toTimestampMs(eventTimestamp(event));
    const nextEventTs = i + 1 < events.length ? toTimestampMs(eventTimestamp(events[i + 1])) : 0;

    const id = firstIdentifier(event);
    const issueId = eventIssueId(event);
    const title = eventTitle(event);
    const timestamp = formatTime(eventTimestamp(event));
    const severity = eventSeverity(event) || "info";
    const severityClass = severity === "error" || severity === "fatal" ? "bad" : severity === "warning" ? "warn" : "";

    rows.push(`
      <button class="item" type="button" data-live-event-id="${escapeAttr(id)}">
        <div class="item-title">
          <span class="chip ${severityClass}" style="font-size:0.7rem">${escapeHtml(severity)}</span>
          ${escapeHtml(title)}
        </div>
        <div class="item-meta">
          <span>${escapeHtml(String(issueId || "No issue"))}</span>
          <span>${escapeHtml(timestamp || "No timestamp")}</span>
        </div>
      </button>
    `);

    // After this event, insert release dividers that fall between this event and the next
    if (nextEventTs > 0 && eventTs > 0) {
      for (const { release, ts } of sortedReleases) {
        if (emittedRelease.has(ts)) {
          continue;
        }
        if (ts <= eventTs && ts > nextEventTs) {
          emittedRelease.add(ts);
          rows.push(renderReleaseDivider(release, ts));
        }
      }
    }
  }

  return rows.join("");
}

function renderReleaseDivider(release: ApiRelease, ts: number): string {
  const name = readString(release, ["name", "Name"]) || "Release";
  const environment = readString(release, ["environment", "Environment"]) || "";
  const age = formatAge(ts);
  return `
    <div class="release-divider">
      <span class="release-divider-icon" aria-hidden="true">&#9650;</span>
      <span class="release-divider-name">${escapeHtml(name)}</span>
      ${environment ? `<span class="chip" style="font-size:0.7rem">${escapeHtml(environment)}</span>` : ""}
      <span class="release-divider-time">${escapeHtml(age ? `${age} ago` : "")}</span>
    </div>
  `;
}

function renderNearbyReleasesPanel(releases: ApiRelease[], lastSeen: unknown): string {
  const lastSeenMs = toTimestampMs(lastSeen);
  if (!lastSeenMs) {
    return "";
  }

  const windowStart = lastSeenMs - nearbyReleaseWindowMs;

  const nearby = releases
    .map((r) => ({ release: r, ts: toTimestampMs(readFirst(r, ["observedAt", "observed_at", "createdAt", "created_at"])) }))
    .filter(({ ts }) => ts > 0 && ts >= windowStart && ts <= lastSeenMs)
    .sort((a, b) => b.ts - a.ts)
    .slice(0, maxNearbyReleases);

  if (!nearby.length) {
    return "";
  }

  return `
    <div class="section">
      <h3>Recent Releases</h3>
      <p class="muted">Releases deployed within 72 hours before this issue was last seen.</p>
      <div class="route-list">
        ${nearby
          .map(({ release, ts }) => {
            const name = readString(release, ["name", "Name"]) || "Untitled release";
            const environment = readString(release, ["environment", "Environment"]) || "n/a";
            const version = readString(release, ["version", "Version"]);
            const url = readString(release, ["url"]);
            const age = formatAge(ts);
            return `
              <article class="route-item">
                <div class="route-item-head">
                  <strong>${escapeHtml(name)}</strong>
                  <span class="chip">${escapeHtml(environment)}</span>
                </div>
                <div class="route-item-meta">
                  <span>${escapeHtml(age ? `${age} ago` : formatTime(ts))}</span>
                  ${version ? `<span>${escapeHtml(version)}</span>` : ""}
                  ${url ? `<a href="${escapeAttr(url)}" target="_blank" rel="noreferrer">${escapeHtml(url)}</a>` : ""}
                </div>
              </article>
            `;
          })
          .join("")}
      </div>
    </div>
  `;
}

function renderIssueCountMeter(count: number, maxCount: number): string {
  const width = maxCount > 0 ? Math.max(4, Math.round((count / maxCount) * 100)) : 0;
  return `
    <span class="count-meter" title="${escapeAttr(`${count} events`)}" aria-label="${escapeAttr(`${count} events`)}">
      <span class="count-meter-fill" style="width:${escapeAttr(String(width))}%"></span>
    </span>
  `;
}

function renderOccurrenceTimeline(events: ApiEvent[], releases: ApiRelease[], activeEventId = "", totalCount = events.length, hasMore = false): string {
  const points = events
    .map((event) => ({
      event,
      id: firstIdentifier(event),
      ts: toTimestampMs(eventTimestamp(event)),
    }))
    .filter((point) => point.ts > 0)
    .sort((a, b) => a.ts - b.ts);

  if (!points.length) {
    return `
      <div class="section">
        <h3>Event occurrences</h3>
        <div class="empty">No timestamped events returned.</div>
      </div>
    `;
  }

  const minEventTs = points[0].ts;
  const maxEventTs = points[points.length - 1].ts;
  const span = Math.max(maxEventTs - minEventTs, 60 * 1000);
  const bucketSize = span / occurrenceBucketCount;
  const buckets = Array.from({ length: occurrenceBucketCount }, () => 0);
  for (const point of points) {
    const bucket = Math.min(occurrenceBucketCount - 1, Math.max(0, Math.floor((point.ts - minEventTs) / bucketSize)));
    buckets[bucket] += 1;
  }
  const maxBucket = Math.max(...buckets, 1);
  const active = points.find((point) => activeEventId && String(point.id) === String(activeEventId));
  const releaseMarkers = releases
    .map((release) => ({ release, ts: toTimestampMs(readFirst(release, ["observedAt", "observed_at", "createdAt", "created_at"])) }))
    .filter(({ ts }) => ts >= minEventTs && ts <= maxEventTs)
    .sort((a, b) => a.ts - b.ts);
  const label = hasMore
    ? `Showing ${points.length} recent timestamped events of ${totalCount || points.length}+ loaded/known events`
    : `Showing ${points.length} timestamped events${totalCount > points.length ? ` of ${totalCount}` : ""}`;

  return `
    <div class="section occurrence-section">
      <div class="section-head">
        <div>
          <h3>Event occurrences</h3>
          <p class="muted">${escapeHtml(label)}</p>
        </div>
        <span class="chip">${escapeHtml(formatTime(minEventTs) || "n/a")} - ${escapeHtml(formatTime(maxEventTs) || "n/a")}</span>
      </div>
      <div class="occurrence-chart" role="img" aria-label="${escapeAttr("Event occurrence timeline")}">
        <div class="occurrence-bars">
          ${buckets
            .map((count) => {
              const height = count ? Math.max(12, Math.round((count / maxBucket) * 100)) : 3;
              return `<span class="occurrence-bar${count ? " active" : ""}" style="height:${escapeAttr(String(height))}%" title="${escapeAttr(`${count} events`)}"></span>`;
            })
            .join("")}
        </div>
        ${releaseMarkers
          .map(({ release, ts }) => renderReleaseMarker(release, ((ts - minEventTs) / span) * 100))
          .join("")}
        ${active ? `<span class="event-marker" style="left:${escapeAttr(String(Math.max(0, Math.min(100, ((active.ts - minEventTs) / span) * 100))))}%" title="Selected event"></span>` : ""}
      </div>
      <div class="occurrence-axis">
        <span>${escapeHtml(formatTime(minEventTs) || "Start")}</span>
        <span>${escapeHtml(formatTime(maxEventTs) || "End")}</span>
      </div>
    </div>
  `;
}

function renderReleaseMarker(release: ApiRelease, left: number): string {
  const name = readString(release, ["name", "Name"]) || "Release";
  const environment = readString(release, ["environment", "Environment"]) || "";
  const clampedLeft = Math.max(0, Math.min(100, left));
  const title = environment ? `${name} (${environment})` : name;
  return `
    <span class="release-marker" style="left:${escapeAttr(String(clampedLeft))}%" title="${escapeAttr(title)}">
      <span class="release-marker-pin" aria-hidden="true"></span>
      <span class="release-marker-label">${escapeHtml(name)}</span>
    </span>
  `;
}

function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}

export function renderSetupGuide(): string {
  const endpoint = `${window.location.origin}/api/v1/events`;
  const packageUrl = `${window.location.origin}/packages/typescript/bugbarn-typescript-0.1.0.tgz`;
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

export function renderReleasesViewMarkup(releases: ApiRelease[], error: unknown = null): string {
  return `
    <div class="view-head">
      <div>
        <p class="eyebrow">Releases</p>
        <h2>Release markers</h2>
      </div>
      <span class="chip">${escapeHtml(String(releases.length))}</span>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Recent release markers</h3>
        ${error ? `<div class="error">Unable to load releases. ${escapeHtml(errorMessage(error))}</div>` : renderReleaseList(releases)}
      </div>
      <div class="section">
        <h3>Mark a release</h3>
        <p class="muted">POST /api/v1/releases</p>
        <form class="form-grid" id="release-form">
          ${renderField("Name", "name", "text", "1.4.0")}
          ${renderField("Environment", "environment", "text", "testing")}
          ${renderField("Observed at", "observedAt", "datetime-local")}
          ${renderField("Version", "version", "text", "v1.4.0")}
          ${renderField("Commit sha", "commitSha", "text", "abc123")}
          ${renderField("URL", "url", "url", "https://example.com/release-notes")}
          <label class="field field-wide">
            <span>Notes</span>
            <textarea name="notes" rows="4" placeholder="Deployment note, bug fix, or incident context"></textarea>
          </label>
          <div class="link-row form-actions">
            <button type="submit">Publish release marker</button>
          </div>
        </form>
      </div>
    </div>
  `;
}

export function renderAlertsViewMarkup(alerts: ApiAlert[], error: unknown = null): string {
  return `
    <div class="view-head">
      <div>
        <p class="eyebrow">Alerts</p>
        <h2>Alert rules</h2>
      </div>
      <span class="chip">${escapeHtml(String(alerts.length))}</span>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Configured alerts</h3>
        ${error ? `<div class="error">Unable to load alerts. ${escapeHtml(errorMessage(error))}</div>` : renderAlertList(alerts)}
      </div>
      <div class="section">
        <h3>Create alert</h3>
        <p class="muted">POST /api/v1/alerts</p>
        <form class="form-grid" id="alert-form">
          ${renderField("Name", "name", "text", "500s on checkout")}
          ${renderField("Condition", "condition", "text", "event_count > 10")}
          ${renderField("Query", "query", "text", "environment:testing")}
          ${renderField("Target", "target", "text", "ops@example.com")}
          <label class="field field-wide checkbox-field">
            <input name="enabled" type="checkbox" checked />
            <span>Enabled</span>
          </label>
          <div class="link-row form-actions">
            <button type="submit">Create alert</button>
          </div>
        </form>
      </div>
    </div>
  `;
}

export function renderSettingsViewMarkup(settings: ApiSettings | null, username: string, apiKeys: ApiApiKey[] = [], error: unknown = null): string {
  const displayName = settings?.displayName || settings?.display_name || username || "";
  const timezone = settings?.timezone || settings?.timezoneName || "";
  const defaultEnvironment = settings?.defaultEnvironment || settings?.default_environment || "";
  const liveWindowMinutes = settings?.liveWindowMinutes ?? settings?.live_window_minutes ?? 15;
  const stacktraceContextLines = settings?.stacktraceContextLines ?? settings?.stacktrace_context_lines ?? 3;

  return `
    <div class="view-head">
      <div>
        <p class="eyebrow">Settings</p>
        <h2>Workspace settings</h2>
      </div>
      <span class="chip">${escapeHtml(username || "signed in")}</span>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Session</h3>
        ${error ? `<div class="error">Unable to load settings. ${escapeHtml(errorMessage(error))}</div>` : ""}
        <div class="grid">
          <div class="kv"><span>Username</span><span>${escapeHtml(username || "n/a")}</span></div>
          <div class="kv"><span>Display name</span><span>${escapeHtml(displayName || "n/a")}</span></div>
          <div class="kv"><span>Timezone</span><span>${escapeHtml(timezone || "n/a")}</span></div>
        </div>
      </div>
      <div class="section">
        <h3>Preferences</h3>
        <p class="muted">POST /api/v1/settings</p>
        <form class="form-grid" id="settings-form">
          ${renderField("Display name", "displayName", "text", displayName)}
          ${renderField("Timezone", "timezone", "text", timezone || "Europe/Amsterdam")}
          ${renderField("Default environment", "defaultEnvironment", "text", defaultEnvironment || "testing")}
          ${renderField("Live window minutes", "liveWindowMinutes", "number", String(liveWindowMinutes))}
          ${renderField("Stacktrace context lines", "stacktraceContextLines", "number", String(stacktraceContextLines))}
          <div class="link-row form-actions">
            <button type="submit">Save settings</button>
          </div>
        </form>
      </div>
      <div class="section">
        <h3>SDK</h3>
        <p class="muted">Install the TypeScript SDK in your project to capture errors automatically.</p>
        <div id="sdk-info" class="grid">
          <div class="kv"><span>Status</span><span>Loading…</span></div>
        </div>
      </div>
      <div class="section">
        <h3>Source maps</h3>
        <p class="muted">Upload source maps so frames can show a short source snippet instead of only minified output.</p>
        <form class="form-grid" id="source-map-form" enctype="multipart/form-data">
          ${renderField("Release", "release", "text", "")}
          ${renderField("Environment", "environment", "text", defaultEnvironment || "testing")}
          ${renderField("URL prefix", "urlPrefix", "text", "https://app.example.com/static/")}
          <label class="field field-wide">
            <span>Source map files</span>
            <input name="files" type="file" accept=".map,.js,.ts" multiple />
          </label>
          <div class="link-row form-actions">
            <button type="submit">Upload source maps</button>
          </div>
        </form>
      </div>
      <div class="section">
        <h3>API keys</h3>
        <p class="muted">
          <strong>ingest</strong> keys are safe to embed in browser bundles — they can only POST events.
          <strong>full</strong> keys grant full API access; keep them server-side only.
          Create keys with <code>bugbarn apikey create --scope ingest --name my-frontend</code>.
        </p>
        ${renderApiKeyTable(apiKeys)}
      </div>
    </div>
  `;
}

function renderApiKeyTable(keys: ApiApiKey[]): string {
  if (!keys.length) {
    return `<p class="muted">No API keys found. Use the CLI to create one.</p>`;
  }
  const rows = keys.map((k) => {
    const name = readFirst(k, ["name", "Name"]) ?? "—";
    const scope = String(readFirst(k, ["scope", "Scope"]) || "full");
    const lastUsed = readFirst(k, ["lastUsedAt", "LastUsedAt"]) as string | undefined;
    return `<div class="kv">
      <span>${escapeHtml(String(name))}</span>
      <span><span class="chip chip-${escapeAttr(scope)}">${escapeHtml(scope)}</span>${lastUsed ? ` · last used ${escapeHtml(lastUsed)}` : ""}</span>
    </div>`;
  });
  return `<div class="grid">${rows.join("")}</div>`;
}

function renderReleaseList(releases: ApiRelease[]): string {
  if (!releases.length) {
    return `
      <div class="empty">
        <strong>No release markers yet.</strong>
        <p>Use the form below or wire the release marker endpoint to link deploys with regressions.</p>
      </div>
    `;
  }

  return `
    <div class="route-list">
      ${releases
        .map((release) => {
          const title = readString(release, ["name", "Name"]) || "Untitled release";
          const environment = readString(release, ["environment", "Environment"]) || "n/a";
          const observedAt = formatTime(readFirst(release, ["observedAt", "observed_at", "createdAt", "created_at"])) || "n/a";
          const version = readString(release, ["version", "Version"]);
          const commitSha = readString(release, ["commitSha", "commit_sha"]);
          const url = readString(release, ["url"]);
          const notes = readString(release, ["notes"]);
          return `
            <article class="route-item">
              <div class="route-item-head">
                <strong>${escapeHtml(title)}</strong>
                <span class="chip">${escapeHtml(environment)}</span>
              </div>
              <div class="route-item-meta">
                <span>${escapeHtml(observedAt)}</span>
                ${version ? `<span>${escapeHtml(version)}</span>` : ""}
                ${commitSha ? `<span>${escapeHtml(commitSha)}</span>` : ""}
                ${url ? `<a href="${escapeAttr(url)}" target="_blank" rel="noreferrer">${escapeHtml(url)}</a>` : ""}
              </div>
              ${notes ? `<p class="muted">${escapeHtml(notes)}</p>` : ""}
            </article>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderAlertList(alerts: ApiAlert[]): string {
  if (!alerts.length) {
    return `
      <div class="empty">
        <strong>No alert rules yet.</strong>
        <p>Use the form below to create the first rule once the backend supports it.</p>
      </div>
    `;
  }

  return `
    <div class="route-list">
      ${alerts
        .map((alert) => {
          const title = readString(alert, ["name", "Name"]) || "Untitled alert";
          const condition = readString(alert, ["condition", "Condition", "query", "Query"]) || "n/a";
          const target = readString(alert, ["target", "Target"]) || "n/a";
          const enabled = Boolean(alert.enabled ?? alert.Enabled);
          const lastTriggeredAt = formatTime(readFirst(alert, ["lastTriggeredAt", "last_triggered_at"])) || "never";
          return `
            <article class="route-item">
              <div class="route-item-head">
                <strong>${escapeHtml(title)}</strong>
                <span class="chip ${enabled ? "" : "bad"}">${escapeHtml(enabled ? "enabled" : "disabled")}</span>
              </div>
              <div class="route-item-meta">
                <span>${escapeHtml(condition)}</span>
                <span>${escapeHtml(target)}</span>
                <span>${escapeHtml(lastTriggeredAt)}</span>
              </div>
            </article>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderField(label: string, name: string, type: "text" | "number" | "url" | "datetime-local" = "text", value = ""): string {
  return `
    <label class="field">
      <span>${escapeHtml(label)}</span>
      <input name="${escapeAttr(name)}" type="${type}" value="${escapeAttr(value)}" />
    </label>
  `;
}

function renderEmptySection(title: string, message: string): string {
  return `
    <div class="section">
      <h3>${escapeHtml(title)}</h3>
      <div class="empty">
        <p>${escapeHtml(message)}</p>
      </div>
    </div>
  `;
}

function renderFingerprintSection(fingerprint: string, material: RawRecord): string {
  const fields = hasKeys(material) ? material : {};
  return `
    <div class="section">
      <h3>Fingerprint</h3>
      <p class="muted">BugBarn groups events by a stable fingerprint derived from normalized exception data, message text, stack frames, and selected stable context.</p>
      <div class="grid">
        <div class="kv">
          <span>Fingerprint hash</span>
          <span>${escapeHtml(fingerprint || "n/a")}</span>
        </div>
      </div>
      ${renderRecord(fields, "No fingerprint material fields were returned by the backend.")}
    </div>
  `;
}

function buildFingerprintMaterial(issue: ApiIssue, lastEvent: ApiEvent | null): RawRecord {
  const direct = issueFingerprintMaterial(issue);
  if (hasKeys(direct)) {
    return direct;
  }

  const material: RawRecord = {};
  const exception = lastEvent ? eventException(lastEvent) : {};
  const context = lastEvent ? eventContext(lastEvent) : {};
  const stacktrace = lastEvent ? eventStacktrace(lastEvent) : [];

  material["normalized exception type"] = issueExceptionType(issue) || readString(exception, ["type", "Type"]) || "n/a";
  material["normalized message"] = issueNormalizedTitle(issue) || readString(exception, ["message", "Message"]) || "n/a";
  if (hasKeys(context)) {
    material["stable context"] = context;
  }
  if (stacktrace.length) {
    material["stack frames"] = stacktrace.slice(0, 5).map((frame) => {
      if (!isRecord(frame)) {
        return String(frame);
      }
      const fn = readString(frame, ["function", "Function", "name", "Name"]) || "<anonymous>";
      const file = readString(frame, ["file", "File", "filename", "Filename", "path", "Path"]);
      const line = readString(frame, ["line", "Line", "lineno", "Lineno"]);
      return `${fn}${file ? ` @ ${file}${line ? `:${line}` : ""}` : ""}`;
    });
  }

  return material;
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

function renderRecord(data: RawRecord, emptyMessage = "No data returned."): string {
  const entries = Object.entries(data).filter(([, value]) => value !== null && value !== undefined && value !== "");
  if (!entries.length) {
    return `<div class="empty">${escapeHtml(emptyMessage)}</div>`;
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
          .map((frame, index) => renderFrame(frame, index))
          .join("")}
      </div>
    </div>
  `;
}

function renderFrame(frame: unknown, index: number): string {
  if (!isRecord(frame)) {
    return `<div class="frame"><span>#${index + 1}</span><code>${escapeHtml(String(frame))}</code></div>`;
  }

  const fn = readString(frame, ["function", "Function", "name", "Name"]) || "<anonymous>";
  const file = readString(frame, ["file", "File", "filename", "Filename", "path", "Path"]);
  const line = readString(frame, ["line", "Line", "lineno", "Lineno"]);
  const column = readString(frame, ["column", "Column", "colno", "Colno"]);
  const location = [file, line ? `:${line}` : "", column ? `:${column}` : ""].join("");
  const snippet = readFrameSnippet(frame);

  // Original (symbolicated) position fields — try all common naming variants
  const origFn = readString(frame, ["originalFunction", "original_function", "OriginalFunction"]);
  const origFile = readString(frame, ["originalFile", "original_file", "OriginalFile"]);
  const origLine = readString(frame, ["originalLine", "original_line", "OriginalLine"]);
  const origColumn = readString(frame, ["originalColumn", "original_column", "OriginalColumn"]);

  const hasOriginal = Boolean(origFn ?? origFile ?? origLine);
  const displayFn = origFn ?? fn;
  const origLocation = origFile
    ? [origFile, origLine ? `:${origLine}` : "", origColumn ? `:${origColumn}` : ""].join("")
    : "";

  return `
    <article class="frame${hasOriginal ? " symbolicated" : ""}">
      <span>#${index + 1}</span>
      <div class="frame-body">
        <div class="frame-head">
          <code>${escapeHtml(displayFn)}</code>
          ${hasOriginal && origLocation
            ? `<small style="color:var(--accent)">${escapeHtml(origLocation)}</small>`
            : ""}
          <small style="${hasOriginal ? "color:var(--muted);font-size:11px;opacity:0.7" : ""}">${escapeHtml(location || "unknown source")}</small>
        </div>
        ${snippet ? `<pre class="frame-snippet">${escapeHtml(snippet)}</pre>` : ""}
      </div>
    </article>
  `;
}

function readFrameSnippet(frame: RawRecord): string {
  const direct = readFirst(frame, [
    "snippet",
    "Snippet",
    "sourceSnippet",
    "source_snippet",
    "contextLine",
    "context_line",
    "lineText",
    "line_text",
  ]);
  if (typeof direct === "string" && direct.trim()) {
    return direct;
  }
  if (Array.isArray(direct)) {
    return direct.map((line) => String(line)).join("\n");
  }

  const source = readFirst(frame, ["source", "Source", "code", "Code"]);
  if (typeof source === "string" && source.trim()) {
    return source;
  }

  const pre = toLines(readFirst(frame, ["preContext", "PreContext", "pre_context"]));
  const post = toLines(readFirst(frame, ["postContext", "PostContext", "post_context"]));
  const context = readString(frame, ["contextLine", "context_line", "ContextLine"]);
  const lines = [...pre, ...(context ? [context] : []), ...post];
  return lines.join("\n");
}

function toLines(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.map((line) => String(line)).filter(Boolean);
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

function renderEventButtons(events: ApiEvent[], activeId = "", className = ""): string {
  if (!events.length) {
    return `<div class="empty">No events returned.</div>`;
  }

  return `
    <div class="event-list${className ? ` ${className}` : ""}">
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
