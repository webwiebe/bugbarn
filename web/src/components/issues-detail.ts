import { collectKeyValues, hasKeys, isRecord, readFirst, readString } from "../data.js";
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
  issueStatus,
  issueTitle,
} from "../domain.js";
import { escapeAttr, escapeHtml, errorMessage, formatAge, formatTime } from "../format.js";
import type { ApiEvent, ApiIssue, ApiRelease, BreadcrumbEntry, RawRecord } from "../types.js";
import {
  renderDataSection,
  renderEmptySection,
  renderEventButtons,
  renderEventNavigation,
  renderRecord,
  renderStacktrace,
  toTimestampMs,
} from "./shared.js";

const nearbyReleaseWindowMs = 72 * 60 * 60 * 1000; // 72 hours
const maxNearbyReleases = 5;
const occurrenceBucketCount = 24;

export function renderIssueDetailMarkup(issue: ApiIssue, events: ApiEvent[], releases: ApiRelease[] = [], hasMore = false): string {
  const id = firstIdentifier(issue);
  const rawTitle = issueTitle(issue);
  const exceptionType = issueExceptionType(issue);
  const title = exceptionType && rawTitle.startsWith(exceptionType + ": ") ? rawTitle.slice(exceptionType.length + 2) : rawTitle;
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
        <p class="muted">${escapeHtml(fingerprint || id || "No fingerprint")}</p>
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
  const stacktrace = lastEvent ? eventStacktrace(lastEvent) : [];

  material["normalized exception type"] = issueExceptionType(issue) || readString(exception, ["type", "Type"]) || "n/a";
  material["normalized message"] = issueNormalizedTitle(issue) || readString(exception, ["message", "Message"]) || "n/a";
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
