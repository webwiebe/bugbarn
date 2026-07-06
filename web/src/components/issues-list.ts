import { readFirst, readString } from "../data.js";
import {
  eventIssueId,
  eventSeverity,
  eventTimestamp,
  eventTitle,
  firstIdentifier,
  issueEventCount,
  issueExceptionType,
  issueFingerprint,
  issueFirstSeen,
  issueLastSeen,
  issueNormalizedTitle,
  issueSeverity,
  issueStatus,
  issueTitle,
} from "../domain.js";
import { escapeAttr, escapeHtml, errorMessage, formatAge, formatTime } from "../format.js";
import type { ApiEvent, ApiIssue, ApiRelease } from "../types.js";
import { toTimestampMs } from "./shared.js";

export function renderIssueListMarkup(issues: ApiIssue[], query: string, selectedIssueId: string | null, error: unknown = null): string {
  if (error) {
    return `<div class="error">Issues unavailable. ${escapeHtml(errorMessage(error))}</div>`;
  }

  const filtered = query ? filterIssues(issues, query) : issues;
  if (!filtered.length) {
    return "";
  }
  const maxEvents = filtered.reduce((max, issue) => Math.max(max, issueEventCount(issue)), 1);

  const hasRegressed = filtered.some((i) => issueStatus(i) === "regressed");
  let passedRegressed = false;

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
        let sectionHeader = "";
        const st = issueStatus(issue);
        if (hasRegressed && !passedRegressed && st !== "regressed") {
          passedRegressed = true;
          sectionHeader = `<div class="issue-section-divider"></div>`;
        }
        const id = firstIdentifier(issue);
        const rawTitle = issueTitle(issue);
        const title = rawTitle.length > 300 ? rawTitle.slice(0, 300) + "…" : rawTitle;
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
        return `${sectionHeader}
          <button class="item issue-row ${active} ${statusClass}" type="button" data-issue-id="${escapeAttr(id)}">
            <div class="item-title"><span class="status-dot ${statusClass}"></span><a href="#/issues/${escapeAttr(id)}" class="issue-link" onclick="event.stopPropagation()">${escapeHtml(title)}</a></div>
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
          <span class="item-title-text">${escapeHtml(title)}</span>
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
