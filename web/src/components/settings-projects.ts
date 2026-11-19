// The settings Projects page: the project list with usage counts, the project
// group editor and the alias editor.
import { escapeAttr, escapeHtml, formatAge } from "../format.js";
import type { ApiAlias, ApiEnvironment, ApiProject, ApiProjectGroup } from "../types.js";
import { renderField } from "./event-detail.js";

// usageCount formats a per-project count for display. An absent count means the
// server could not produce the usage aggregate, which must not be shown as "0"
// — that would read as "this project has no data" when the truth is "we don't
// know right now".
function usageCount(n: number | undefined): string {
  return n === undefined ? "—" : String(n);
}

/** Tooltip for a project's issue count: how the issues split by status. */
function issueBreakdownTitle(p: ApiProject): string {
  const s = p.issue_status;
  if (!s) return "Issues";
  return `${s.open} open (${s.regressed} regressed), ${s.resolved} resolved, ${s.muted} muted`;
}

function compareProjects(a: ApiProject, b: ApiProject): number {
  const aPending = (a.status ?? a.Status) === "pending" ? 0 : 1;
  const bPending = (b.status ?? b.Status) === "pending" ? 0 : 1;
  if (aPending !== bPending) return aPending - bPending;
  const aName = String(a.name ?? a.Name ?? a.slug ?? a.Slug ?? "").toLowerCase();
  const bName = String(b.name ?? b.Name ?? b.slug ?? b.Slug ?? "").toLowerCase();
  return aName.localeCompare(bName);
}

function renderFunnelbarnForm(p: ApiProject, slug: string, status: string): string {
  if (status === 'pending') return "";
  return `<form class="funnelbarn-form" data-funnelbarn-form="${escapeAttr(slug)}">
                <input type="url" name="funnelbarn_url" maxlength="2048" value="${escapeAttr(String(p.funnelbarn_url ?? ""))}"
                  placeholder="FunnelBarn funnel URL, {surface} is replaced per row"
                  aria-label="FunnelBarn URL for ${escapeAttr(slug)}">
                <button type="submit" class="btn-sm">Save link</button>
              </form>`;
}

function renderProjectUsage(p: ApiProject): string {
  // Absent counts mean the usage aggregate was unavailable, which is not
  // the same as zero — show a dash rather than claiming the project is
  // empty.
  const issues = p.issue_count;
  const events = p.event_count;
  const logs = p.log_count;
  return `<div class="project-usage">
                <span class="usage-stat" title="${escapeAttr(issueBreakdownTitle(p))}"><span class="usage-icon">◆</span>${escapeHtml(usageCount(issues))}<span class="usage-label">${issues === 1 ? "issue" : "issues"}</span></span>
                <span class="usage-stat" title="Retained events (older ones are expired by the retention window)"><span class="usage-icon">▸</span>${escapeHtml(usageCount(events))}<span class="usage-label">${events === 1 ? "event" : "events"}</span></span>
                <span class="usage-stat" title="Retained log lines"><span class="usage-icon">≡</span>${escapeHtml(usageCount(logs))}<span class="usage-label">${logs === 1 ? "log" : "logs"}</span></span>
              </div>`;
}

function renderProjectRow(p: ApiProject, groups: ApiProjectGroup[]): string {
  const slug = String(p.slug ?? p.Slug ?? '');
  const name = String(p.name ?? p.Name ?? slug);
  const status = String(p.status ?? p.Status ?? 'active');
  const setupUrl = `/api/v1/setup/${slug}`;
  // Sort/filter attributes need a number, so absent counts use 0.
  const group = p.group_id != null ? groups.find(g => g.id === p.group_id) : undefined;
  return `
          <div class="project-row" data-slug="${escapeAttr(slug)}" data-name="${escapeAttr(name.toLowerCase())}" data-status="${escapeAttr(status)}" data-issues="${p.issue_count ?? 0}" data-events="${p.event_count ?? 0}" data-logs="${p.log_count ?? 0}">
            <div class="project-info">
              <strong>${escapeHtml(name)}</strong>
              <span class="project-slug">${escapeHtml(slug)}</span>
              ${group ? `<span class="chip" style="font-size:11px">${escapeHtml(group.name)}</span>` : ""}
              ${renderFunnelbarnForm(p, slug, status)}
            </div>
            <div class="project-actions">
              ${renderProjectUsage(p)}
              <span class="chip ${status === 'pending' ? 'warn' : ''}">${escapeHtml(status)}</span>
              <a class="ghost btn-sm" href="${escapeAttr(setupUrl)}" target="_blank">Setup</a>
              ${status === 'pending'
                ? `<button class="btn-sm" data-approve-project="${escapeAttr(slug)}">Approve</button><button class="btn-sm danger" data-delete-project="${escapeAttr(slug)}">Reject</button>`
                : `<button class="btn-sm danger" data-delete-project="${escapeAttr(slug)}">Delete</button>`}
            </div>
          </div>`;
}

function renderProjectList(projects: ApiProject[], groups: ApiProjectGroup[]): string {
  const sortedProjects = [...projects].sort(compareProjects);
  return `
    <div class="section">
      <h3>Projects</h3>
      ${projects.length === 0 ? `<p class="muted">No projects yet. Use the Quick Setup URL on the <a href="#/settings/overview">Settings overview</a>.</p>` : `
      <div class="project-controls">
        <input type="search" id="project-filter" placeholder="Filter projects…" autocomplete="off" />
        <select id="project-sort">
          <option value="default">Pending first, then A–Z</option>
          <option value="name-asc">Name A–Z</option>
          <option value="name-desc">Name Z–A</option>
          <option value="issues-desc">Most issues</option>
          <option value="events-desc">Most events</option>
          <option value="logs-desc">Most logs</option>
          <option value="status">Status</option>
        </select>
        <select id="project-status-filter">
          <option value="all">All statuses</option>
          <option value="pending">Pending</option>
          <option value="active">Active</option>
        </select>
      </div>
      <div id="project-list">
      ${sortedProjects.map(p => renderProjectRow(p, groups)).join('')}
      </div>`}
    </div>`;
}

function renderGroupRow(g: ApiProjectGroup, projects: ApiProject[]): string {
  const members = projects.filter(p => p.group_id === g.id);
  const ungrouped = projects.filter(p => !p.group_id && (p.status ?? p.Status) !== "pending");
  return `
          <div class="project-row" style="flex-direction:column;align-items:flex-start;gap:8px">
            <div style="display:flex;align-items:center;gap:8px;width:100%">
              <strong>${escapeHtml(g.name)}</strong>
              <span class="project-slug">${escapeHtml(g.slug)}</span>
              <button class="btn-sm danger" style="margin-left:auto" data-delete-group="${escapeAttr(g.slug)}">Delete</button>
            </div>
            <div style="display:flex;flex-wrap:wrap;gap:6px;align-items:center">
              ${members.map(p => {
                const slug = String(p.slug ?? p.Slug ?? "");
                return `<span class="chip">${escapeHtml(String(p.name ?? p.Name ?? slug))}<button class="btn-inline" data-remove-from-group="${escapeAttr(slug)}" title="Remove" style="margin-left:4px;opacity:.6;font-size:11px">×</button></span>`;
              }).join("")}
              ${ungrouped.length > 0 ? `
              <form data-add-to-group="${escapeAttr(g.slug)}" style="display:flex;gap:4px">
                <select name="project" style="font-size:12px">
                  ${ungrouped.map(p => { const s = String(p.slug ?? p.Slug ?? ""); return `<option value="${escapeAttr(s)}">${escapeHtml(String(p.name ?? p.Name ?? s))}</option>`; }).join("")}
                </select>
                <button type="submit" class="btn-sm">Add</button>
              </form>` : ""}
            </div>
          </div>`;
}

function renderGroupsSection(projects: ApiProject[], groups: ApiProjectGroup[]): string {
  return `
    <div class="section">
      <h3>Project groups</h3>
      <p class="muted">Group related projects so you can filter issues across all of them at once.</p>
      ${groups.length === 0 ? `<p class="muted">No groups yet.</p>` : groups.map(g => renderGroupRow(g, projects)).join("")}
      <form id="create-group-form" class="form-grid" style="margin-top:12px">
        ${renderField("Group name", "name", "text", "")}
        <div class="link-row form-actions"><button type="submit">Create group</button></div>
      </form>
    </div>`;
}

function renderAliasesSection(projects: ApiProject[], aliases: ApiAlias[]): string {
  return `
    <div class="section">
      <h3>Project aliases</h3>
      <p class="muted">An alias slug transparently routes events to an existing project — useful when an SDK is reporting under an old name.</p>
      ${aliases.length === 0 ? `<p class="muted">No aliases yet.</p>` : `
        <div class="grid">
          ${aliases.map(a => `
            <div class="kv">
              <span><code>${escapeHtml(a.alias_slug)}</code> → <code>${escapeHtml(a.project_slug)}</code></span>
              <button class="btn-sm danger" data-delete-alias="${escapeAttr(a.alias_slug)}">Delete</button>
            </div>`).join("")}
        </div>`}
      <form id="create-alias-form" class="form-grid" style="margin-top:12px">
        ${renderField("Alias slug", "alias", "text", "")}
        <label class="field">
          <span>Target project</span>
          <select name="project">
            ${projects.filter(p => (p.status ?? p.Status) !== "pending").map(p => {
              const slug = String(p.slug ?? p.Slug ?? "");
              return `<option value="${escapeAttr(slug)}">${escapeHtml(String(p.name ?? p.Name ?? slug))}</option>`;
            }).join("")}
          </select>
        </label>
        <div class="link-row form-actions"><button type="submit">Create alias</button></div>
      </form>
    </div>`;
}

/** The environments one project reports from, with their issue counts. */
function renderEnvironmentsSection(environments: ApiEnvironment[], currentProject: string): string {
  if (currentProject === "__all") {
    return `
    <div class="section">
      <h3>Environments</h3>
      <p class="muted">Select a project in the scope picker to see the environments it reports from.</p>
    </div>`;
  }
  const rows = environments.map((e) => `
        <div class="kv" data-environment="${escapeAttr(e.environment)}">
          <span><code>${escapeHtml(e.environment)}</code></span>
          <span>${escapeHtml(String(e.open))} open of ${escapeHtml(String(e.issues))} ${e.issues === 1 ? "issue" : "issues"}${e.last_seen ? `, last seen ${escapeHtml(formatAge(e.last_seen))}` : ""}</span>
        </div>`).join("");
  return `
    <div class="section">
      <h3>Environments of ${escapeHtml(currentProject)}</h3>
      <p class="muted">One project holds every environment of an app. Pick one in the environment switcher to filter issues, logs and releases.</p>
      ${environments.length === 0 ? `<p class="muted">No issue carries an environment yet. Send it as the <code>environment</code> attribute from the SDK.</p>` : `<div class="grid">${rows}</div>`}
    </div>`;
}

export function renderSettingsProjects(
  projects: ApiProject[],
  groups: ApiProjectGroup[],
  aliases: ApiAlias[],
  environments: ApiEnvironment[] = [],
  currentProject = "__all",
): string {
  return renderProjectList(projects, groups) +
    renderEnvironmentsSection(environments, currentProject) +
    renderGroupsSection(projects, groups) +
    renderAliasesSection(projects, aliases);
}
