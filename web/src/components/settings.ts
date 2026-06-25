import { readFirst } from "../data.js";
import { escapeAttr, escapeHtml, errorMessage } from "../format.js";
import type { ApiApiKey, ApiProject, ApiSettings } from "../types.js";
import { renderField } from "./shared.js";

export function renderSettingsViewMarkup(
  settings: ApiSettings | null,
  username: string,
  apiKeys: ApiApiKey[] = [],
  error: unknown = null,
  projects: ApiProject[] = [],
  groups: import("../types.js").ApiProjectGroup[] = [],
  aliases: import("../types.js").ApiAlias[] = [],
  tab: import("../types.js").SettingsTab = "overview",
  systemHealth: import("../types.js").SystemHealth | null = null,
): string {
  const pendingProjects = projects.filter(p => (p.status ?? p.Status) === "pending");
  const activeProjects = projects.filter(p => (p.status ?? p.Status) !== "pending");

  const subPageTitles: Record<string, string> = { projects: "Projects", preferences: "Preferences", keys: "API Keys", system: "System" };
  const subTitle = subPageTitles[tab] ?? "";
  const headContent = subTitle
    ? `<a href="#/settings/overview" class="back-link">← Settings</a><h2>${escapeHtml(subTitle)}${tab === "projects" && pendingProjects.length > 0 ? ` <span class="nav-badge">${pendingProjects.length}</span>` : ""}</h2>`
    : `<h2>Settings</h2><span class="chip">${escapeHtml(username || "signed in")}</span>`;

  let content = "";
  if (tab === "overview") {
    content = renderSettingsOverview(settings, username, activeProjects, groups, apiKeys, pendingProjects, error);
  } else if (tab === "projects") {
    content = renderSettingsProjects(projects, groups, aliases);
  } else if (tab === "preferences") {
    content = renderSettingsPreferences(settings);
  } else if (tab === "system") {
    content = renderSettingsSystem(systemHealth);
  } else {
    content = renderSettingsKeys(apiKeys);
  }

  return `
    <div class="view-head">
      ${headContent}
    </div>
    <div class="detail-main">${content}</div>
  `;
}

function renderSettingsOverview(
  settings: ApiSettings | null,
  username: string,
  activeProjects: ApiProject[],
  groups: import("../types.js").ApiProjectGroup[],
  apiKeys: ApiApiKey[],
  pendingProjects: ApiProject[],
  error: unknown,
): string {
  const statsBar = `
    <div class="settings-stats">
      <a href="#/settings/projects" class="settings-stat">
        <span class="stat-value">${activeProjects.length}</span>
        <span class="stat-label">project${activeProjects.length !== 1 ? "s" : ""}</span>
      </a>
      <a href="#/settings/projects" class="settings-stat">
        <span class="stat-value">${groups.length}</span>
        <span class="stat-label">group${groups.length !== 1 ? "s" : ""}</span>
      </a>
      <a href="#/settings/keys" class="settings-stat">
        <span class="stat-value">${apiKeys.length}</span>
        <span class="stat-label">API key${apiKeys.length !== 1 ? "s" : ""}</span>
      </a>
    </div>`;

  const errorBanner = error ? `<div class="callout callout-error">Unable to load settings — ${escapeHtml(errorMessage(error))}</div>` : "";

  const pendingBanner = pendingProjects.length > 0 ? `
    <div class="callout callout-warn">
      <strong>${pendingProjects.length} project${pendingProjects.length > 1 ? "s" : ""} awaiting approval</strong>
      <span>${pendingProjects.map(p => escapeHtml(String(p.slug ?? p.Slug ?? ""))).join(", ")}</span>
      <a href="#/settings/projects" class="btn-sm">Review</a>
    </div>` : "";

  const noProjectsBanner = activeProjects.length === 0 ? `
    <div class="callout callout-info">
      <strong>No projects yet</strong>
      <span>Use the Quick Setup URL below to add your first project in seconds.</span>
    </div>` : "";

  const setupCard = `
    <div class="section quick-setup-card">
      <h3>Quick Setup</h3>
      <p class="muted">Point an LLM or developer at the setup page to auto-configure a project with an ingest API key. Returns a markdown guide with SDK examples.</p>
      <div class="setup-url-box">
        <code id="setup-url">${escapeHtml(`${window.location.origin}/api/v1/setup/`)}your-project-slug</code>
        <button class="btn-sm ghost" id="copy-setup-url" title="Copy URL">⧉</button>
      </div>
      <p class="muted" style="margin-top:6px">Replace <code>your-project-slug</code> with the desired name. Approve the project once it appears in <a href="#/settings/projects">Projects</a>.</p>
    </div>`;

  const navItems = `
    <div class="settings-nav">
      <a href="#/settings/projects" class="settings-nav-item">
        <span class="settings-nav-label">Projects${pendingProjects.length > 0 ? ` <span class="nav-badge">${pendingProjects.length}</span>` : ""}</span>
        <span class="settings-nav-desc">Manage projects, groups, and slug aliases</span>
        <span class="settings-nav-arrow">›</span>
      </a>
      <a href="#/settings/preferences" class="settings-nav-item">
        <span class="settings-nav-label">Preferences</span>
        <span class="settings-nav-desc">Display settings, SDK info, source map uploads</span>
        <span class="settings-nav-arrow">›</span>
      </a>
      <a href="#/settings/keys" class="settings-nav-item">
        <span class="settings-nav-label">API Keys</span>
        <span class="settings-nav-desc">View ingest and full-access API keys</span>
        <span class="settings-nav-arrow">›</span>
      </a>
      <a href="#/settings/system" class="settings-nav-item">
        <span class="settings-nav-label">System health</span>
        <span class="settings-nav-desc">Ingest liveness, write-queue backlog, WAL size</span>
        <span class="settings-nav-arrow">›</span>
      </a>
      <button type="button" id="settings-logout" class="settings-nav-item settings-signout">
        <span class="settings-nav-label">Sign out</span>
        <span class="settings-nav-desc">${escapeHtml(username || "signed in")}</span>
        <span class="settings-nav-arrow">›</span>
      </button>
    </div>`;

  return errorBanner + pendingBanner + noProjectsBanner + statsBar + navItems + setupCard;
}

function renderSettingsProjects(
  projects: ApiProject[],
  groups: import("../types.js").ApiProjectGroup[],
  aliases: import("../types.js").ApiAlias[],
): string {
  const sortedProjects = [...projects].sort((a, b) => {
    const aPending = (a.status ?? a.Status) === "pending" ? 0 : 1;
    const bPending = (b.status ?? b.Status) === "pending" ? 0 : 1;
    if (aPending !== bPending) return aPending - bPending;
    const aName = String(a.name ?? a.Name ?? a.slug ?? a.Slug ?? "").toLowerCase();
    const bName = String(b.name ?? b.Name ?? b.slug ?? b.Slug ?? "").toLowerCase();
    return aName.localeCompare(bName);
  });

  const projectList = `
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
      ${sortedProjects.map(p => {
        const slug = String(p.slug ?? p.Slug ?? '');
        const name = String(p.name ?? p.Name ?? slug);
        const status = String(p.status ?? p.Status ?? 'active');
        const setupUrl = `/api/v1/setup/${slug}`;
        const issues = p.issue_count ?? 0;
        const events = p.event_count ?? 0;
        const logs = p.log_count ?? 0;
        const group = p.group_id != null ? groups.find(g => g.id === p.group_id) : undefined;
        return `
          <div class="project-row" data-slug="${escapeAttr(slug)}" data-name="${escapeAttr(name.toLowerCase())}" data-status="${escapeAttr(status)}" data-issues="${issues}" data-events="${events}" data-logs="${logs}">
            <div class="project-info">
              <strong>${escapeHtml(name)}</strong>
              <span class="project-slug">${escapeHtml(slug)}</span>
              ${group ? `<span class="chip" style="font-size:11px">${escapeHtml(group.name)}</span>` : ""}
            </div>
            <div class="project-actions">
              <div class="project-usage">
                <span class="usage-stat" title="Open issues"><span class="usage-icon">◆</span>${escapeHtml(String(issues))}<span class="usage-label">${issues === 1 ? "issue" : "issues"}</span></span>
                <span class="usage-stat" title="Total ingested events"><span class="usage-icon">▸</span>${escapeHtml(String(events))}<span class="usage-label">${events === 1 ? "event" : "events"}</span></span>
                <span class="usage-stat" title="Total ingested log lines"><span class="usage-icon">≡</span>${escapeHtml(String(logs))}<span class="usage-label">${logs === 1 ? "log" : "logs"}</span></span>
              </div>
              <span class="chip ${status === 'pending' ? 'warn' : ''}">${escapeHtml(status)}</span>
              <a class="ghost btn-sm" href="${escapeAttr(setupUrl)}" target="_blank">Setup</a>
              ${status === 'pending'
                ? `<button class="btn-sm" data-approve-project="${escapeAttr(slug)}">Approve</button><button class="btn-sm danger" data-delete-project="${escapeAttr(slug)}">Reject</button>`
                : `<button class="btn-sm danger" data-delete-project="${escapeAttr(slug)}">Delete</button>`}
            </div>
          </div>`;
      }).join('')}
      </div>`}
    </div>`;

  const groupsSection = `
    <div class="section">
      <h3>Project groups</h3>
      <p class="muted">Group related projects so you can filter issues across all of them at once.</p>
      ${groups.length === 0 ? `<p class="muted">No groups yet.</p>` : groups.map(g => {
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
      }).join("")}
      <form id="create-group-form" class="form-grid" style="margin-top:12px">
        ${renderField("Group name", "name", "text", "")}
        <div class="link-row form-actions"><button type="submit">Create group</button></div>
      </form>
    </div>`;

  const aliasesSection = `
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

  return projectList + groupsSection + aliasesSection;
}

function renderSettingsPreferences(settings: ApiSettings | null): string {
  const displayName = settings?.displayName || settings?.display_name || "";
  const timezone = settings?.timezone || settings?.timezoneName || "";
  const defaultEnvironment = settings?.defaultEnvironment || settings?.default_environment || "";
  const liveWindowMinutes = settings?.liveWindowMinutes ?? settings?.live_window_minutes ?? 15;
  const stacktraceContextLines = settings?.stacktraceContextLines ?? settings?.stacktrace_context_lines ?? 3;

  return `
    <div class="section">
      <h3>Preferences</h3>
      <form class="form-grid" id="settings-form">
        ${renderField("Display name", "displayName", "text", displayName)}
        ${renderField("Timezone", "timezone", "text", timezone || "Europe/Amsterdam")}
        ${renderField("Default environment", "defaultEnvironment", "text", defaultEnvironment || "testing")}
        ${renderField("Live window minutes", "liveWindowMinutes", "number", String(liveWindowMinutes))}
        ${renderField("Stacktrace context lines", "stacktraceContextLines", "number", String(stacktraceContextLines))}
        <div class="link-row form-actions"><button type="submit">Save settings</button></div>
      </form>
    </div>
    <div class="section">
      <h3>TypeScript SDK</h3>
      <p class="muted">Install the SDK to capture errors automatically from browser and Node.js apps.</p>
      <div id="sdk-info" class="grid">
        <div class="kv"><span>Status</span><span>Loading…</span></div>
      </div>
    </div>
    <div class="section">
      <h3>Source maps</h3>
      <p class="muted">Upload source maps so stack frames show original source instead of minified output.</p>
      <form class="form-grid" id="source-map-form" enctype="multipart/form-data">
        ${renderField("Release", "release", "text", "")}
        ${renderField("Environment", "environment", "text", defaultEnvironment || "testing")}
        ${renderField("URL prefix", "urlPrefix", "text", "https://app.example.com/static/")}
        <label class="field field-wide">
          <span>Source map files</span>
          <input name="files" type="file" accept=".map,.js,.ts" multiple />
        </label>
        <div class="link-row form-actions"><button type="submit">Upload source maps</button></div>
      </form>
    </div>`;
}

function renderSettingsKeys(apiKeys: ApiApiKey[]): string {
  return `
    <div class="section">
      <h3>API keys</h3>
      <p class="muted">
        <strong>ingest</strong> keys are safe to embed in browser bundles — they can only POST events.
        <strong>full</strong> keys grant full API access; keep them server-side only.
        Create keys with <code>bugbarn apikey create --scope ingest --name my-frontend</code>.
      </p>
      ${renderApiKeyTable(apiKeys)}
    </div>`;
}

function renderSettingsSystem(health: import("../types.js").SystemHealth | null): string {
  if (!health) {
    return `<div class="section"><h3>System health</h3><p class="muted">Loading…</p></div>`;
  }

  const ingest = health.ingest ?? null;
  const ok = ingest ? ingest.healthy : health.status === "ok";
  const statusChip = ok
    ? `<span class="chip">healthy</span>`
    : `<span class="chip bad">unhealthy</span>`;

  const fmtAge = (secs: number): string => {
    if (!isFinite(secs) || secs < 0) return "—";
    if (secs < 90) return `${Math.round(secs)}s ago`;
    if (secs < 5400) return `${Math.round(secs / 60)}m ago`;
    if (secs < 172800) return `${Math.round(secs / 3600)}h ago`;
    return `${Math.round(secs / 86400)}d ago`;
  };
  const fmtBytes = (n: number): string => {
    if (!n) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let v = n; let i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
  };

  const reasons = ingest && ingest.reasons && ingest.reasons.length
    ? `<div class="callout callout-warn"><strong>Pipeline degraded</strong>${ingest.reasons.map(r => `<span>${escapeHtml(r)}</span>`).join("")}</div>`
    : "";

  const lastEvent = ingest
    ? (ingest.hasEvents ? fmtAge(ingest.lastEventAgeSeconds) : "no events yet")
    : "—";
  const backlog = ingest && ingest.queueDepthKnown ? String(ingest.queueDepth) : "n/a";
  const wal = ingest ? fmtBytes(ingest.walSizeBytes) : "—";

  const stats = ingest ? `
    <div class="stats-bar">
      <div class="stat"><span class="stat-value">${escapeHtml(lastEvent)}</span><span class="stat-label">Last event ingested</span></div>
      <div class="stat"><span class="stat-value">${escapeHtml(backlog)}</span><span class="stat-label">Write-queue backlog</span></div>
      <div class="stat"><span class="stat-value">${escapeHtml(wal)}</span><span class="stat-label">WAL size</span></div>
    </div>` : `<p class="muted">Ingest health is reported by reader and writer instances; no data available.</p>`;

  return `
    <div class="section">
      <h3>Ingest pipeline ${statusChip}</h3>
      <p class="muted">Liveness of the write path that turns received events into stored issues. A stall here is what caused the 5-day silent outage; this panel and the <code>/api/v1/health?detail=true</code> probe now surface it.</p>
      ${reasons}
      ${stats}
    </div>`;
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
