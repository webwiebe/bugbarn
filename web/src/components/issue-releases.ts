import { escapeHtml, formatTime } from "../format.js";
import { releaseRows } from "../release-rows.js";
import type { IssueRelease } from "../release-rows.js";

/**
 * "Releases" block of the issue page: which releases the issue occurs in, counted
 * over the latest events. Empty when no event carries a release, so issues from
 * senders without a version show nothing.
 */
export function renderIssueReleasesMarkup(releases: IssueRelease[], sampled: number, firstRelease = ""): string {
  const { rows, withoutRelease } = releaseRows(releases, sampled);
  if (rows.length === 0) return "";
  const first = firstRelease ? `<p class="muted">First seen in release <code>${escapeHtml(firstRelease)}</code>.</p>` : "";
  const body = rows
    .map(
      (r) => `<tr>
        <td><code>${escapeHtml(r.release)}</code></td>
        <td>${escapeHtml(String(r.events))} (${escapeHtml(String(r.percent))}%)</td>
        <td>${escapeHtml(formatTime(r.firstSeen))}</td>
        <td>${escapeHtml(formatTime(r.lastSeen))}</td>
      </tr>`,
    )
    .join("");
  const note = `Counted over the latest ${sampled} events${withoutRelease > 0 ? `; ${withoutRelease} carry no release` : ""}.`;
  return `
    <div class="section" data-issue-releases>
      <h3>Releases</h3>
      ${first}
      <table class="releases-table">
        <thead><tr><th>Release</th><th>Events</th><th>First seen</th><th>Last seen</th></tr></thead>
        <tbody>${body}</tbody>
      </table>
      <p class="muted">${escapeHtml(note)}</p>
    </div>`;
}
