import { collectKeyValues, hasKeys, isRecord, readFirst, readNumber, readString } from "./data.js";
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
import type { AnalyticsBucket, AnalyticsOverview, AnalyticsPage, AnalyticsReferrer, AnalyticsSegmentBucket, DropoutStat, FlowEntry, PageFlowResult, ScrollDepthResult, ApiAlert, ApiApiKey, ApiEvent, ApiIssue, ApiLogEntry, ApiProject, ApiRelease, ApiSettings, BreadcrumbEntry, RawRecord } from "./types.js";

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
      <span>24h</span>
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
        const projectSlug = issue.project_slug ? String(issue.project_slug) : "";
        const status = issueStatus(issue);
        const statusClass = status === "resolved" ? "resolved" : status === "muted" ? "muted" : status === "regressed" ? "regressed" : "";
        const statusLabel = status === "resolved" ? "Resolved" : status === "muted" ? "Muted" : status === "regressed" ? "Regressed" : "";
        return `
          <button class="item issue-row ${active} ${statusClass}" type="button" data-issue-id="${escapeAttr(id)}">
            <div class="item-title"><span class="status-dot ${statusClass}"></span>${escapeHtml(title)}</div>
            <span class="issue-cell"><span class="chip ${severityClass}" style="font-size:0.7rem">${escapeHtml(severity || "n/a")}</span></span>
            <span class="issue-cell">${escapeHtml(lastSeen || "No timestamp")}</span>
            <span class="issue-cell">${escapeHtml(firstSeen || "n/a")}</span>
            <span class="issue-cell">${renderIssueCountMeter(count, maxEvents)}</span>
            <span class="issue-cell">${escapeHtml(String(count))}</span>
            <span class="issue-cell">${renderSparkline(issue.hourly_counts)}</span>
            <div class="item-meta">
              <span>${escapeHtml(issueExceptionType(issue) || "Error")}</span>
              ${statusLabel ? `<span class="chip issue-status-chip ${statusClass}" style="font-size:0.65rem">${escapeHtml(statusLabel)}</span>` : ""}
              ${projectSlug ? `<span class="chip" style="font-size:0.65rem;opacity:0.7">${escapeHtml(projectSlug)}</span>` : ""}
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
        ${renderMuteButton(issue)}
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
  const _spans = eventSpans(event);
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
      ${renderUserSection(event)}
      ${renderBreadcrumbsSection(event)}
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

function renderMuteButton(issue: ApiIssue): string {
  const status = issue.Status || issue.status || '';
  const muteMode = issue.mute_mode || '';

  if (status === 'muted') {
    return `<button class="btn btn--secondary" data-action="unmute-issue" data-issue-id="${issue.ID || issue.id}">Unmute</button>`;
  }

  void muteMode;
  return `
    <span class="mute-group">
      <select id="mute-mode-select" aria-label="Mute mode">
        <option value="until_regression">Until regression</option>
        <option value="forever">Forever</option>
      </select>
      <button class="btn btn--secondary" data-action="mute-issue" data-issue-id="${issue.ID || issue.id}">Mute</button>
    </span>`;
}

function renderUserSection(event: ApiEvent): string {
  const user = event.User || event.user;
  if (!user) return '';
  const id = (event.User?.ID ?? event.User?.id) || event.user?.id;
  const email = (event.User?.Email ?? event.User?.email) || event.user?.email;
  const username = (event.User?.Username ?? event.User?.username) || event.user?.username;
  if (!id && !email && !username) return '';
  return `
    <section class="detail-section">
      <h4>User</h4>
      <table class="kv-table">
        ${id ? `<tr><th>ID</th><td>${escapeHtml(String(id))}</td></tr>` : ''}
        ${email ? `<tr><th>Email</th><td>${escapeHtml(String(email))}</td></tr>` : ''}
        ${username ? `<tr><th>Username</th><td>${escapeHtml(String(username))}</td></tr>` : ''}
      </table>
    </section>`;
}

function renderBreadcrumbsSection(event: ApiEvent): string {
  const crumbs = event.breadcrumbs || event.Breadcrumbs;
  if (!crumbs || crumbs.length === 0) return '';

  const rows = crumbs.map((crumb: BreadcrumbEntry) => {
    const cat = crumb.category || 'manual';
    const icon = cat === 'console' ? '▸' : cat === 'http' ? '⇄' : cat === 'navigation' ? '→' : '•';
    const time = crumb.timestamp ? new Date(crumb.timestamp).toISOString().slice(11, 23) : '';
    const level = crumb.level || '';
    const levelClass = level === 'error' ? 'bad' : level === 'warn' || level === 'warning' ? 'warn' : '';
    return `
      <div class="breadcrumb-row">
        <span class="breadcrumb-icon">${icon}</span>
        <span class="breadcrumb-time muted">${escapeHtml(time)}</span>
        <span class="breadcrumb-cat chip ${levelClass}">${escapeHtml(cat)}</span>
        <span class="breadcrumb-msg">${escapeHtml(crumb.message || '')}</span>
      </div>`;
  }).join('');

  return `
    <section class="detail-section">
      <h4>Breadcrumbs</h4>
      <div class="breadcrumb-list">${rows}</div>
    </section>`;
}

function renderSparkline(hourlyCounts: number[] | undefined): string {
  if (!hourlyCounts || hourlyCounts.length === 0) {
    return '<span class="sparkline sparkline--empty"></span>';
  }
  const max = Math.max(...hourlyCounts, 1);
  const bars = hourlyCounts.map((count) => {
    const pct = Math.round((count / max) * 100);
    const hasEvents = count > 0;
    return `<span class="spark-bar${hasEvents ? ' spark-bar--active' : ''}" style="height:${pct}%" title="${count} events"></span>`;
  });
  return `<span class="sparkline">${bars.join('')}</span>`;
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
        <p class="muted">Alerts fire when a condition is met. Paste a Slack or Discord webhook URL and it will auto-detect the format.</p>
        <form class="form-grid" id="alert-form">
          ${renderField("Name", "name", "text", "New errors on checkout")}
          <label class="field">
            <span>Condition</span>
            <select name="condition">
              <option value="new_issue">New issue created</option>
              <option value="regression">Issue regressed</option>
            </select>
          </label>
          ${renderField("Webhook URL", "webhook_url", "url", "https://hooks.slack.com/…")}
          ${renderField("Cooldown (minutes)", "cooldown_minutes", "number", "60")}
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

export function renderSettingsViewMarkup(settings: ApiSettings | null, username: string, apiKeys: ApiApiKey[] = [], error: unknown = null, projects: ApiProject[] = []): string {
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
        <h3>Projects</h3>
        ${projects.map(p => {
          const slug = String(p.slug ?? p.Slug ?? '');
          const name = String(p.name ?? p.Name ?? slug);
          const status = String(p.status ?? p.Status ?? 'active');
          const setupUrl = `/api/v1/setup/${slug}`;
          return `
            <div style="display:flex;align-items:center;gap:10px;padding:8px 0;border-bottom:1px solid var(--line)">
              <div style="flex:1;min-width:0">
                <strong>${escapeHtml(name)}</strong>
                <span style="margin-left:8px;font-size:11px;color:var(--muted)">${escapeHtml(slug)}</span>
              </div>
              <span class="chip ${status === 'pending' ? 'warn' : ''}">${escapeHtml(status)}</span>
              <a class="ghost btn-sm" href="${escapeAttr(setupUrl)}" target="_blank">Setup page</a>
              ${status === 'pending' ? `<button class="btn-sm" data-approve-project="${escapeAttr(slug)}">Approve</button>` : ''}
            </div>
          `;
        }).join('')}
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
          const id = readString(release, ["id", "ID"]);
          const title = readString(release, ["name", "Name"]) || "Untitled release";
          const environment = readString(release, ["environment", "Environment"]) || "n/a";
          const observedAt = formatTime(readFirst(release, ["observedAt", "observed_at", "ObservedAt", "createdAt", "created_at", "CreatedAt"])) || "n/a";
          const version = readString(release, ["version", "Version"]);
          const commitSha = readString(release, ["commitSha", "commit_sha", "CommitSHA"]);
          const url = readString(release, ["url", "URL"]);
          const notes = readString(release, ["notes", "Notes"]);
          return `
            <article class="route-item" role="button" tabindex="0" style="cursor:pointer" data-release-id="${escapeAttr(id)}">
              <div class="route-item-head">
                <strong>${escapeHtml(title)}</strong>
                <span class="chip">${escapeHtml(environment)}</span>
              </div>
              <div class="route-item-meta">
                <span>${escapeHtml(observedAt)}</span>
                ${version ? `<span>${escapeHtml(version)}</span>` : ""}
                ${commitSha ? `<code style="font-size:11px;opacity:.7">${escapeHtml(commitSha.slice(0, 8))}</code>` : ""}
              </div>
              ${notes ? `<p class="muted" style="margin:4px 0 0">${escapeHtml(notes)}</p>` : ""}
              ${url ? `<p style="margin:2px 0 0;font-size:12px"><a href="${escapeAttr(url)}" target="_blank" rel="noreferrer" onclick="event.stopPropagation()">${escapeHtml(url)}</a></p>` : ""}
            </article>
          `;
        })
        .join("")}
    </div>
  `;
}

export function renderReleaseDetailMarkup(
  release: ApiRelease,
  newIssues: ApiIssue[],
  regressions: ApiIssue[],
  nextRelease: ApiRelease | null,
): string {
  const title = readString(release, ["name", "Name"]) || "Untitled release";
  const environment = readString(release, ["environment", "Environment"]);
  const observedAt = formatTime(readFirst(release, ["observedAt", "observed_at", "ObservedAt"]));
  const version = readString(release, ["version", "Version"]);
  const commitSha = readString(release, ["commitSha", "commit_sha", "CommitSHA"]);
  const url = readString(release, ["url", "URL"]);
  const notes = readString(release, ["notes", "Notes"]);
  const createdBy = readString(release, ["createdBy", "created_by", "CreatedBy"]);
  const nextTitle = nextRelease ? readString(nextRelease, ["name", "Name"]) : null;

  const renderIssueRow = (issue: ApiIssue) => {
    const id = readString(issue, ["id", "ID"]) || "";
    const title = issueTitle(issue);
    const count = readNumber(issue, ["event_count", "EventCount"]);
    const slug = issue.project_slug ? String(issue.project_slug) : "";
    return `
      <button class="item" type="button" data-issue-id="${escapeAttr(id)}" style="width:100%;text-align:left">
        <div class="item-title">${escapeHtml(title)}</div>
        <div class="item-meta">
          ${slug ? `<span class="chip" style="font-size:0.65rem;opacity:.7">${escapeHtml(slug)}</span>` : ""}
          <span>${escapeHtml(String(count))} events</span>
          <span>${escapeHtml(id)}</span>
        </div>
      </button>`;
  };

  return `
    <div class="detail-section">
      <div class="route-item-head" style="margin-bottom:12px">
        <strong style="font-size:1.1rem">${escapeHtml(title)}</strong>
        ${environment ? `<span class="chip">${escapeHtml(environment)}</span>` : ""}
      </div>
      <dl class="kv-grid">
        ${observedAt ? `<dt>Deployed</dt><dd>${escapeHtml(observedAt)}</dd>` : ""}
        ${version ? `<dt>Version</dt><dd>${escapeHtml(version)}</dd>` : ""}
        ${commitSha ? `<dt>Commit</dt><dd><code>${escapeHtml(commitSha.slice(0, 12))}</code></dd>` : ""}
        ${createdBy ? `<dt>By</dt><dd>${escapeHtml(createdBy)}</dd>` : ""}
        ${nextTitle ? `<dt>Next</dt><dd>${escapeHtml(nextTitle)}</dd>` : ""}
      </dl>
      ${notes ? `<p style="margin:12px 0;color:#8b949e">${escapeHtml(notes)}</p>` : ""}
      ${url ? `<p style="margin:8px 0"><a href="${escapeAttr(url)}" target="_blank" rel="noreferrer">View release on GitHub →</a></p>` : ""}
    </div>

    <div class="detail-section" style="margin-top:20px">
      <h3 style="margin:0 0 8px;font-size:.75rem;text-transform:uppercase;letter-spacing:.08em;color:#8b949e">
        New issues in this release ${newIssues.length ? `<span class="chip bad" style="font-size:11px">${newIssues.length}</span>` : ""}
      </h3>
      ${newIssues.length
        ? `<div class="list">${newIssues.map(renderIssueRow).join("")}</div>`
        : `<p class="muted">No new issues ${nextTitle ? `before ${escapeHtml(nextTitle)}` : "yet"}</p>`}
    </div>

    <div class="detail-section" style="margin-top:20px">
      <h3 style="margin:0 0 8px;font-size:.75rem;text-transform:uppercase;letter-spacing:.08em;color:#8b949e">
        Regressions in this release ${regressions.length ? `<span class="chip bad" style="font-size:11px">${regressions.length}</span>` : ""}
      </h3>
      ${regressions.length
        ? `<div class="list">${regressions.map(renderIssueRow).join("")}</div>`
        : `<p class="muted">No regressions ${nextTitle ? `before ${escapeHtml(nextTitle)}` : "yet"}</p>`}
    </div>
  `;
}

function webhookBadge(webhookUrl: string | undefined): string {
  if (!webhookUrl) return "";
  if (webhookUrl.includes("hooks.slack.com")) return `<span class="chip chip-slack">Slack</span>`;
  if (webhookUrl.includes("discord.com/api/webhooks")) return `<span class="chip chip-discord">Discord</span>`;
  return `<span class="chip">Webhook</span>`;
}

function conditionLabel(condition: string | undefined): string {
  if (condition === "new_issue") return "New issue";
  if (condition === "regression") return "Regression";
  return condition || "n/a";
}

function renderAlertList(alerts: ApiAlert[]): string {
  if (!alerts.length) {
    return `
      <div class="empty">
        <strong>No alert rules yet.</strong>
        <p>Use the form below to create the first rule.</p>
      </div>
    `;
  }

  return `
    <div class="route-list">
      ${alerts
        .map((alert) => {
          const id = alert.id ?? "";
          const title = alert.name || "Untitled alert";
          const condition = alert.condition ?? "";
          const webhookUrl = alert.webhook_url ?? "";
          const cooldown = alert.cooldown_minutes ?? 0;
          const enabled = Boolean(alert.enabled);
          const lastFiredAt = formatTime(alert.last_fired_at) || "never";
          const projectSlug = alert.project_slug ? String(alert.project_slug) : "";
          return `
            <article class="route-item">
              <div class="route-item-head">
                <strong>${escapeHtml(title)}</strong>
                <span class="chip ${enabled ? "" : "bad"}">${escapeHtml(enabled ? "enabled" : "disabled")}</span>
                ${projectSlug ? `<span class="chip" style="font-size:0.65rem;opacity:0.7">${escapeHtml(projectSlug)}</span>` : ""}
                ${webhookBadge(webhookUrl)}
              </div>
              <div class="route-item-meta">
                <span>${escapeHtml(conditionLabel(condition))}</span>
                ${cooldown ? `<span>cooldown ${escapeHtml(String(cooldown))}m</span>` : ""}
                <span>last fired: ${escapeHtml(lastFiredAt)}</span>
              </div>
              ${webhookUrl ? `<div class="route-item-meta"><span class="muted url-truncate">${escapeHtml(webhookUrl)}</span></div>` : ""}
              <div class="link-row">
                <button class="btn-danger btn-sm" data-action="delete-alert" data-id="${escapeAttr(id)}">Delete</button>
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

function renderLogData(data: Record<string, unknown>): string {
  const keys = Object.keys(data).slice(0, 4);
  return keys
    .map((k) => {
      const raw = String(data[k] ?? "");
      const val = raw.length > 60 ? `${raw.slice(0, 60)}…` : raw;
      return `${escapeHtml(k)}=${escapeHtml(val)}`;
    })
    .join(" ");
}

export function renderLogRow(entry: ApiLogEntry): string {
  const hasData = entry.data && Object.keys(entry.data).length > 0;
  const dataInline = hasData ? `<span class="log-data">${renderLogData(entry.data as Record<string, unknown>)}</span>` : "";
  const dataExpanded = hasData
    ? `<div class="log-data-expanded"><pre>${escapeHtml(JSON.stringify(entry.data, null, 2))}</pre></div>`
    : "";
  const projectBadge = entry.project_slug ? `<span class="log-project-badge">${escapeHtml(entry.project_slug)}</span>` : "";
  return `
    <div class="log-row log-row-${escapeAttr(entry.level)}" data-log-id="${escapeAttr(String(entry.id))}">
      <span class="log-time">${escapeHtml(formatTime(entry.received_at))}</span>
      <span class="log-level log-level-${escapeAttr(entry.level)}">${escapeHtml(entry.level.toUpperCase())}</span>
      <span class="log-msg">${escapeHtml(entry.message)}</span>
      ${projectBadge}
      ${dataInline}
      ${dataExpanded}
    </div>
  `;
}

export function renderLogsViewMarkup(logs: ApiLogEntry[], level: string, search: string): string {
  const count = logs.length;
  const levelOptions = [
    { value: "", label: "All levels" },
    { value: "trace", label: "Trace" },
    { value: "debug", label: "Debug" },
    { value: "info", label: "Info" },
    { value: "warn", label: "Warn" },
    { value: "error", label: "Error" },
    { value: "fatal", label: "Fatal" },
  ];

  const listContent = count
    ? logs.map(renderLogRow).join("")
    : `<div class="empty">No log entries yet. Connect a project to start streaming logs.</div>`;

  return `
    <div class="view-head">
      <div>
        <p class="eyebrow">Logs</p>
        <h2>Log stream</h2>
      </div>
      <span class="chip">${escapeHtml(String(count))}</span>
    </div>
    <div class="log-toolbar">
      <select id="log-level-filter" aria-label="Filter by level">
        ${levelOptions.map((opt) => `<option value="${escapeAttr(opt.value)}"${opt.value === level ? " selected" : ""}>${escapeHtml(opt.label)}</option>`).join("")}
      </select>
      <input id="log-search" type="search" placeholder="Filter by message…" value="${escapeAttr(search)}" />
      <button id="log-clear" type="button">Clear</button>
      <span class="log-live-indicator" id="log-live-dot"></span>
    </div>
    <div id="log-list" class="log-list">
      ${listContent}
    </div>
  `;
}

export function renderAnalyticsViewMarkup(
  overview: AnalyticsOverview | null,
  pages: AnalyticsPage[],
  timeline: AnalyticsBucket[],
  referrers: AnalyticsReferrer[],
  segments: AnalyticsSegmentBucket[],
  rangeDays: number,
  segmentDim: string,
  error: unknown = null,
): string {
  return `
    <div class="view-head">
      <div>
        <p class="eyebrow">Analytics</p>
        <h2>Web analytics</h2>
      </div>
      <div class="view-actions">
        <div class="analytics-range-bar" role="group" aria-label="Date range">
          <button class="tab${rangeDays === 7 ? " active" : ""}" data-analytics-range="7">7d</button>
          <button class="tab${rangeDays === 30 ? " active" : ""}" data-analytics-range="30">30d</button>
          <button class="tab${rangeDays === 90 ? " active" : ""}" data-analytics-range="90">90d</button>
        </div>
      </div>
    </div>
    ${error ? `<div class="error">Analytics unavailable. ${escapeHtml(errorMessage(error))}</div>` : ""}
    ${renderAnalyticsOverviewCards(overview)}
    ${renderAnalyticsTimeline(timeline)}
    <div class="analytics-tables">
      ${renderAnalyticsPagesTable(pages)}
      ${renderAnalyticsReferrersTable(referrers)}
    </div>
    ${renderAnalyticsSegmentSection(segments, segmentDim)}
  `;
}

function renderAnalyticsOverviewCards(overview: AnalyticsOverview | null): string {
  const pv = overview?.pageviews ?? 0;
  const sv = overview?.sessions ?? 0;
  const pg = overview?.pages ?? 0;
  return `
    <div class="analytics-cards">
      <div class="analytics-card">
        <span class="analytics-card-label">Pageviews</span>
        <strong class="analytics-card-value">${escapeHtml(String(pv))}</strong>
      </div>
      <div class="analytics-card">
        <span class="analytics-card-label">Sessions</span>
        <strong class="analytics-card-value">${escapeHtml(String(sv))}</strong>
      </div>
      <div class="analytics-card">
        <span class="analytics-card-label">Pages</span>
        <strong class="analytics-card-value">${escapeHtml(String(pg))}</strong>
      </div>
    </div>
  `;
}

function renderAnalyticsTimeline(buckets: AnalyticsBucket[]): string {
  if (!buckets.length) {
    return `<div class="section"><div class="empty">No timeline data available.</div></div>`;
  }

  const maxPv = buckets.reduce((m, b) => Math.max(m, b.pageviews), 1);
  const w = 800;
  const h = 120;
  const padTop = 8;
  const padBottom = 8;
  const innerH = h - padTop - padBottom;
  const step = w / Math.max(buckets.length - 1, 1);

  const points = buckets.map((b, i) => {
    const x = Math.round(i * step);
    const y = Math.round(padTop + innerH - (b.pageviews / maxPv) * innerH);
    return `${x},${y}`;
  }).join(" ");

  // Vertical grid lines at each bucket (only if few buckets, else every ~7)
  const gridInterval = buckets.length > 14 ? 7 : 1;
  const gridLines = buckets
    .filter((_, i) => i % gridInterval === 0)
    .map((_, idx) => {
      const i = idx * gridInterval;
      const x = Math.round(i * step);
      return `<line x1="${x}" y1="${padTop}" x2="${x}" y2="${h - padBottom}" stroke="var(--line,#21262d)" stroke-width="1"/>`;
    })
    .join("");

  // Date labels — first and last
  const firstLabel = buckets[0]?.date ?? "";
  const lastLabel = buckets[buckets.length - 1]?.date ?? "";
  const _lastX = Math.round((buckets.length - 1) * step);

  return `
    <div class="section analytics-timeline-section">
      <h3>Pageviews over time</h3>
      <div class="analytics-chart" style="position:relative">
        <svg viewBox="0 0 ${w} ${h}" width="100%" height="${h}" aria-label="Pageviews timeline" role="img" style="display:block;overflow:visible">
          ${gridLines}
          <polyline points="${escapeAttr(points)}" fill="none" stroke="#d4a054" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>
        </svg>
        <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--muted,#8b949e);margin-top:2px">
          <span>${escapeHtml(firstLabel)}</span>
          <span>${escapeHtml(lastLabel)}</span>
        </div>
      </div>
    </div>
  `;
}

function renderAnalyticsPagesTable(pages: AnalyticsPage[]): string {
  if (!pages.length) {
    return `
      <div class="section" style="flex:1;min-width:0">
        <h3>Top pages</h3>
        <div class="empty">
          <p>No page data yet.</p>
          <p class="muted">Install the BugBarn snippet on your site to track pageviews.</p>
        </div>
      </div>
    `;
  }
  const rows = pages.map((p) => `
    <tr>
      <td class="url-truncate">${escapeHtml(p.pathname)}</td>
      <td>${escapeHtml(String(p.pageviews))}</td>
      <td>${escapeHtml(String(p.sessions))}</td>
    </tr>
  `).join("");
  return `
    <div class="section" style="flex:1;min-width:0">
      <h3>Top pages</h3>
      <table class="data-table">
        <thead><tr><th>Path</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>
  `;
}

function renderAnalyticsReferrersTable(referrers: AnalyticsReferrer[]): string {
  if (!referrers.length) {
    return `
      <div class="section" style="flex:1;min-width:0">
        <h3>Referrers</h3>
        <div class="empty"><p>No referrer data yet.</p></div>
      </div>
    `;
  }
  const rows = referrers.map((r) => `
    <tr>
      <td class="url-truncate">${escapeHtml(r.host || "(direct)")}</td>
      <td>${escapeHtml(String(r.pageviews))}</td>
      <td>${escapeHtml(String(r.sessions))}</td>
    </tr>
  `).join("");
  return `
    <div class="section" style="flex:1;min-width:0">
      <h3>Referrers</h3>
      <table class="data-table">
        <thead><tr><th>Host</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>
  `;
}

function renderAnalyticsSegmentSection(segments: AnalyticsSegmentBucket[], dim: string): string {
  const dimOptions = [
    { value: "", label: "-- none --" },
    { value: "referrer_host", label: "Referrer host" },
    { value: "screen_width", label: "Screen width" },
  ];
  const selectHtml = `
    <select id="analytics-segment-dim" aria-label="Breakdown dimension">
      ${dimOptions.map((o) => `<option value="${escapeAttr(o.value)}"${o.value === dim ? " selected" : ""}>${escapeHtml(o.label)}</option>`).join("")}
    </select>
  `;
  if (!dim) {
    return `
      <div class="section">
        <h3>Breakdown</h3>
        ${selectHtml}
      </div>
    `;
  }
  let tableHtml = `<div class="empty"><p>No segment data.</p></div>`;
  if (segments.length) {
    const rows = segments.map((s) => `
      <tr>
        <td>${escapeHtml(s.value || "(unknown)")}</td>
        <td>${escapeHtml(String(s.pageviews))}</td>
        <td>${escapeHtml(String(s.sessions))}</td>
      </tr>
    `).join("");
    tableHtml = `
      <table class="data-table">
        <thead><tr><th>${escapeHtml(dim)}</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    `;
  }
  return `
    <div class="section">
      <h3>Breakdown</h3>
      ${selectHtml}
      ${tableHtml}
    </div>
  `;
}

export function renderAnalyticsPagesWithDropout(pages: AnalyticsPage[], dropoutMap: Map<string, DropoutStat>): string {
  if (!pages.length) return `<div class="empty"><p>No page data yet.</p></div>`;
  const rows = pages.map((p) => {
    const d = dropoutMap.get(p.pathname);
    const bouncePct = d ? (d.bounceRate * 100).toFixed(1) + "%" : "—";
    return `<tr class="analytics-page-row" data-pathname="${escapeAttr(p.pathname)}" style="cursor:pointer">
      <td>${escapeHtml(p.pathname)}</td>
      <td style="text-align:right">${escapeHtml(String(p.pageviews))}</td>
      <td style="text-align:right">${escapeHtml(String(p.sessions))}</td>
      <td style="text-align:right">${escapeHtml(bouncePct)}</td>
    </tr>`;
  }).join("");
  return `<table class="data-table" style="width:100%">
    <thead><tr><th>Page</th><th style="text-align:right">Views</th><th style="text-align:right">Sessions</th><th style="text-align:right">Bounce %</th></tr></thead>
    <tbody>${rows}</tbody>
  </table>`;
}

export function renderPageDetail(flow: PageFlowResult, scroll: ScrollDepthResult): string {
  const scrollBars = scroll.buckets.map((b) => {
    const w = Math.max(2, Math.round(b.pct));
    return `<div style="display:flex;align-items:center;gap:8px;margin:3px 0">
      <span style="min-width:60px;font-size:12px">${escapeHtml(b.label)}</span>
      <div style="flex:1;background:var(--line,#21262d);border-radius:3px;height:12px;overflow:hidden">
        <div style="width:${escapeAttr(String(w))}%;background:var(--accent,#d4a054);height:100%"></div>
      </div>
      <span style="min-width:36px;font-size:12px;text-align:right">${escapeHtml(b.pct.toFixed(1))}%</span>
    </div>`;
  }).join("");
  const flowTable = (entries: FlowEntry[], empty: string) =>
    entries.length ? `<table style="width:100%;border-collapse:collapse;font-size:12px">
      <thead><tr><th style="text-align:left">Page</th><th style="text-align:right">Count</th><th style="text-align:right">%</th></tr></thead>
      <tbody>${entries.map((e) => `<tr>
        <td>${escapeHtml(e.pathname)}</td>
        <td style="text-align:right">${escapeHtml(String(e.count))}</td>
        <td style="text-align:right">${escapeHtml(e.pct.toFixed(1))}%</td>
      </tr>`).join("")}</tbody>
    </table>` : `<p class="muted" style="font-size:12px">${escapeHtml(empty)}</p>`;
  return `<div style="padding:12px 0">
    <h4 style="margin:0 0 10px">${escapeHtml(flow.pathname)}</h4>
    <div class="section"><h3>Scroll depth</h3>${scrollBars || `<p class="muted">No data yet.</p>`}</div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:12px">
      <div class="section"><h3>Came from</h3>${flowTable(flow.cameFrom, "No upstream pages.")}</div>
      <div class="section"><h3>Went to</h3>${flowTable(flow.wentTo, "No downstream pages.")}</div>
    </div>
  </div>`;
}
