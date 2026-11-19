import { readFirst } from "../records.js";
import { escapeAttr, escapeHtml, errorMessage } from "../format.js";
import type { ApiApiKey, ApiKeyRequest, ApiProject, ApiSettings, IngestHealth } from "../types.js";
import { renderKeyRequests } from "./key-requests.js";
import { renderField } from "./event-detail.js";
import { renderSettingsProjects } from "./settings-projects.js";

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
  keyRequests: ApiKeyRequest[] = [],
  environments: import("../types.js").ApiEnvironment[] = [],
  currentProject = "__all",
): string {
  const pendingProjects = projects.filter(p => (p.status ?? p.Status) === "pending");
  const activeProjects = projects.filter(p => (p.status ?? p.Status) !== "pending");
  const headContent = renderSettingsHead(tab, username, pendingProjects.length);

  let content: string;
  if (tab === "overview") {
    content = renderSettingsOverview(settings, username, activeProjects, groups, apiKeys, pendingProjects, error);
  } else if (tab === "projects") {
    content = renderKeyRequests(keyRequests) + renderSettingsProjects(projects, groups, aliases, environments, currentProject);
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

function renderSettingsHead(tab: string, username: string, pendingCount: number): string {
  const subPageTitles: Record<string, string> = { projects: "Projects", volume: "Volume", preferences: "Preferences", keys: "API Keys", system: "System" };
  const subTitle = subPageTitles[tab] ?? "";
  return subTitle
    ? `<a href="#/settings/overview" class="back-link">← Settings</a><h2>${escapeHtml(subTitle)}${tab === "projects" && pendingCount > 0 ? ` <span class="nav-badge">${pendingCount}</span>` : ""}</h2>`
    : `<h2>Settings</h2><span class="chip">${escapeHtml(username || "signed in")}</span>`;
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

  const navItems = renderSettingsNav(username, pendingProjects.length);

  return errorBanner + pendingBanner + noProjectsBanner + statsBar + navItems + setupCard;
}

// renderSettingsNav is the settings landing page's link list. It lives outside
// renderSettingsOverview so that function stays inside the 80-line budget the
// TS gate enforces.
function renderSettingsNav(username: string, pendingCount: number): string {
  return `
    <div class="settings-nav">
      <a href="#/settings/projects" class="settings-nav-item">
        <span class="settings-nav-label">Projects${pendingCount > 0 ? ` <span class="nav-badge">${pendingCount}</span>` : ""}</span>
        <span class="settings-nav-desc">Manage projects, groups, and slug aliases</span>
        <span class="settings-nav-arrow">›</span>
      </a>
      <a href="#/settings/volume" class="settings-nav-item">
        <span class="settings-nav-label">Volume</span>
        <span class="settings-nav-desc">Retention windows and event sampling per project</span>
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
}

function readTextPreferences(settings: ApiSettings | null): { displayName: string; timezone: string; defaultEnvironment: string } {
  return {
    displayName: settings?.displayName || settings?.display_name || "",
    timezone: settings?.timezone || settings?.timezoneName || "",
    defaultEnvironment: settings?.defaultEnvironment || settings?.default_environment || "",
  };
}

function readNumberPreferences(settings: ApiSettings | null): { liveWindowMinutes: number; stacktraceContextLines: number } {
  return {
    liveWindowMinutes: settings?.liveWindowMinutes ?? settings?.live_window_minutes ?? 15,
    stacktraceContextLines: settings?.stacktraceContextLines ?? settings?.stacktrace_context_lines ?? 3,
  };
}

function renderSettingsPreferences(settings: ApiSettings | null): string {
  const { displayName, timezone, defaultEnvironment } = readTextPreferences(settings);
  const { liveWindowMinutes, stacktraceContextLines } = readNumberPreferences(settings);
  return renderPreferencesForm(displayName, timezone, defaultEnvironment, liveWindowMinutes, stacktraceContextLines) +
    renderSdkSection() +
    renderSourceMapSection(defaultEnvironment);
}

function renderPreferencesForm(
  displayName: string,
  timezone: string,
  defaultEnvironment: string,
  liveWindowMinutes: number,
  stacktraceContextLines: number,
): string {
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
    </div>`;
}

function renderSdkSection(): string {
  return `
    <div class="section">
      <h3>TypeScript SDK</h3>
      <p class="muted">Install the SDK to capture errors automatically from browser and Node.js apps.</p>
      <div id="sdk-info" class="grid">
        <div class="kv"><span>Status</span><span>Loading…</span></div>
      </div>
    </div>`;
}

function renderSourceMapSection(defaultEnvironment: string): string {
  return `
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

// spoolBacklogLabel shows the writer spool's unprocessed bytes, or "n/a" when
// this instance cannot see the writer's spool.
function spoolBacklogLabel(ingest: IngestHealth | null, fmtBytes: (n: number) => string): string {
  if (!ingest || !ingest.spoolBacklogKnown) return "n/a";
  return fmtBytes(ingest.spoolBacklogBytes ?? 0);
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
  const spool = spoolBacklogLabel(ingest, fmtBytes);

  const stats = ingest ? `
    <div class="stats-bar">
      <div class="stat"><span class="stat-value">${escapeHtml(lastEvent)}</span><span class="stat-label">Last event ingested</span></div>
      <div class="stat"><span class="stat-value">${escapeHtml(backlog)}</span><span class="stat-label">Write-queue backlog</span></div>
      <div class="stat"><span class="stat-value">${escapeHtml(spool)}</span><span class="stat-label">Spool backlog</span></div>
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
