import { escapeAttr, escapeHtml } from "../format.js";
import type { ApiProject, VolumeDefaults } from "../types.js";

// Volume settings: how much of the database each project is allowed to occupy.
//
// Two controls per project, both overrides of a deployment-wide default:
//   * a retention window, which may only be shorter than the global one
//   * event sampling, which bounds what one runaway fingerprint can cost
//
// The numbers shown are true event counts, not stored rows — a sampled event
// still counts, it just is not kept individually.

/** formatCount renders a count with thousands separators, or an em dash when
 * the server could not produce it. Absent must not read as zero. */
function formatCount(n: number | undefined): string {
  return n === undefined ? "—" : n.toLocaleString();
}

function projectSlug(p: ApiProject): string {
  return String(p.slug ?? p.Slug ?? "");
}

/** samplingLabel describes what an issue in this project is subject to. */
function samplingLabel(mode: string, defaultAfter: number): string {
  if (mode === "off") return "Never sampled";
  if (mode === "on") return `Sampled past ${defaultAfter.toLocaleString()}`;
  return defaultAfter > 0 ? `Inherited — past ${defaultAfter.toLocaleString()}` : "Inherited — off";
}

function renderLadder(sampleAfter: number): string {
  if (sampleAfter <= 0) {
    return `<p class="muted">Sampling is switched off for this deployment, so every event of every issue is stored.</p>`;
  }
  const rows = [1, 10, 100, 1000].map((k, i) => {
    const from = i === 0 ? sampleAfter : sampleAfter * Math.pow(10, i);
    const to = sampleAfter * Math.pow(10, i + 1);
    return `<tr><td>${from.toLocaleString()} – ${to.toLocaleString()}</td><td>1 in ${(k * 10).toLocaleString()}</td></tr>`;
  });
  return `
    <table class="volume-ladder">
      <thead><tr><th>Events on one issue</th><th>Stored</th></tr></thead>
      <tbody>
        <tr><td>up to ${sampleAfter.toLocaleString()}</td><td>every one</td></tr>
        ${rows.join("")}
      </tbody>
    </table>`;
}

function renderProjectRow(p: ApiProject, defaults: VolumeDefaults): string {
  const slug = projectSlug(p);
  const retention = p.retention_days ?? null;
  const mode = String(p.sampling_mode ?? "");
  const inherited = retention === null;
  return `
    <tr data-volume-row="${escapeAttr(slug)}">
      <td>
        <span class="volume-project">${escapeHtml(slug)}</span>
        <span class="volume-sub">${escapeHtml(samplingLabel(mode, defaults.sample_after))}</span>
      </td>
      <td class="volume-num">${formatCount(p.event_count)}</td>
      <td>
        <input type="number" min="1" max="${defaults.retention_days}" class="volume-days"
          data-volume-days="${escapeAttr(slug)}"
          value="${inherited ? "" : String(retention)}"
          placeholder="${defaults.retention_days}"
          aria-label="Retention days for ${escapeAttr(slug)}">
      </td>
      <td>
        <select data-volume-sampling="${escapeAttr(slug)}" aria-label="Sampling for ${escapeAttr(slug)}">
          <option value=""${mode === "" ? " selected" : ""}>Inherit</option>
          <option value="on"${mode === "on" ? " selected" : ""}>On</option>
          <option value="off"${mode === "off" ? " selected" : ""}>Off</option>
        </select>
      </td>
      <td class="volume-actions">
        <button type="button" class="btn-sm" data-volume-save="${escapeAttr(slug)}">Save</button>
      </td>
    </tr>`;
}

export function renderVolumeViewMarkup(
  projects: ApiProject[],
  defaults: VolumeDefaults,
  error: unknown = null,
): string {
  const active = projects.filter(p => (p.status ?? p.Status) !== "pending");
  const totalEvents = active.reduce((sum, p) => sum + (p.event_count ?? 0), 0);
  const overrides = active.filter(p => p.retention_days != null || String(p.sampling_mode ?? "") !== "").length;
  const loudest = [...active].sort((a, b) => (b.event_count ?? 0) - (a.event_count ?? 0))[0];

  const errorBanner = error
    ? `<div class="callout callout-error">Unable to load volume settings — ${escapeHtml(String(error))}</div>`
    : "";

  const stats = `
    <div class="settings-stats">
      <div class="settings-stat">
        <span class="stat-value">${formatCount(totalEvents)}</span>
        <span class="stat-label">events retained</span>
      </div>
      <div class="settings-stat">
        <span class="stat-value">${defaults.retention_days}d</span>
        <span class="stat-label">default window</span>
      </div>
      <div class="settings-stat">
        <span class="stat-value">${overrides}</span>
        <span class="stat-label">project${overrides !== 1 ? "s" : ""} overridden</span>
      </div>
    </div>`;

  const loudestNote = loudest && (loudest.event_count ?? 0) > 0
    ? `<p class="muted">Largest project right now: <strong>${escapeHtml(projectSlug(loudest))}</strong>,
       ${formatCount(loudest.event_count)} of ${formatCount(totalEvents)} retained events
       (${Math.round(((loudest.event_count ?? 0) / Math.max(totalEvents, 1)) * 100)}%).</p>`
    : "";

  const explainer = `
    <div class="section">
      <h3>How sampling works</h3>
      <p class="muted">Every event under one issue is the same error. Past a threshold BugBarn keeps one
        in every few and records how many each stored event stands for, so counts still report real volume.</p>
      ${renderLadder(defaults.sample_after)}
      <p class="muted">Issue event counts stay exact, alerts are unaffected, and every issue keeps a full
        example payload — so a sampled issue still shows you the error, the stack trace and which
        environments and hosts it was seen on.</p>
    </div>`;

  const table = `
    <div class="section">
      <h3>Per project</h3>
      <p class="muted">A retention window may only be shorter than the deployment default of
        ${defaults.retention_days} days; longer values are capped, because the global sweep expires
        anything older regardless. Leave it blank to inherit.</p>
      <div class="table-scroll">
        <table class="volume-table">
          <thead>
            <tr><th>Project</th><th class="volume-num">Events</th><th>Keep for</th><th>Sampling</th><th></th></tr>
          </thead>
          <tbody>
            ${active.length === 0
              ? `<tr><td colspan="5" class="muted">No projects yet.</td></tr>`
              : active.map(p => renderProjectRow(p, defaults)).join("")}
          </tbody>
        </table>
      </div>
    </div>`;

  return errorBanner + stats + loudestNote + explainer + table;
}
