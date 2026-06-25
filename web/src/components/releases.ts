import { readFirst, readNumber, readString } from "../data.js";
import { issueTitle } from "../domain.js";
import { escapeAttr, escapeHtml, errorMessage, formatTime } from "../format.js";
import type { ApiIssue, ApiRelease } from "../types.js";
import { renderField } from "./shared.js";

export function renderReleasesViewMarkup(releases: ApiRelease[], error: unknown = null, envFilter = ""): string {
  const envs = Array.from(new Set(releases.map(r => readString(r, ["environment", "Environment"]) || "").filter(Boolean))).sort();
  const filtered = envFilter ? releases.filter(r => (readString(r, ["environment", "Environment"]) || "") === envFilter) : releases;
  const filterBar = envs.length > 1 ? `
    <div class="env-filter-bar">
      <button class="env-filter-btn${envFilter === "" ? " active" : ""}" data-env="">All</button>
      ${envs.map(e => `<button class="env-filter-btn${envFilter === e ? " active" : ""}" data-env="${escapeAttr(e)}">${escapeHtml(e)}</button>`).join("")}
    </div>` : "";
  return `
    <div class="view-head">
      <h2>Release markers</h2>
      <span class="chip">${escapeHtml(String(filtered.length))}</span>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Recent release markers</h3>
        ${error ? `<div class="error">Unable to load releases. ${escapeHtml(errorMessage(error))}</div>` : filterBar + renderReleaseList(filtered)}
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
