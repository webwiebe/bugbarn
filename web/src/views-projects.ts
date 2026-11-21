// Project scope: project list loading, the desktop project picker dropdown,
// the mobile scope bottom-sheet, the environment switcher, and the pending
// projects banner/badge.
import { normalizeList } from "./records.js";
import { escapeHtml } from "./format.js";
import type { ApiProject, ApiProjectGroup } from "./types.js";
import {
  apiUrl,
  envKey,
  envSelect,
  groupKey,
  pickerBtn,
  pickerDropdown,
  pickerFilter,
  pickerList,
  projectKey,
  scopeBackdrop,
  scopeBody,
  scopeSheet,
  state,
} from "./core.js";
import { fetchJson } from "./http.js";
import { refreshAll } from "./app-actions.js";
import { sortedOptionsHtml } from "./select-options.js";

export async function loadProjects(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/projects", true);
    state.projects = payload ? normalizeList<ApiProject>(payload, "projects") : [];
  } catch {
    state.projects = [];
  }
  renderProjectPicker();
  updateScopeBtn();
}

export function renderProjectSwitcher(): void {
  renderProjectPicker();
  updateScopeBtn();
}

export function openPicker(): void {
  if (!pickerDropdown || !pickerFilter) return;
  pickerDropdown.hidden = false;
  pickerFilter.value = "";
  renderPickerList("");
  pickerFilter.focus();
}

export function closePicker(): void {
  if (pickerDropdown) pickerDropdown.hidden = true;
}

function renderProjectPicker(): void {
  updatePickerBtn();
  if (pickerDropdown && !pickerDropdown.hidden) renderPickerList(pickerFilter?.value ?? "");
}

function scopeLabelHtml(): string {
  let label: string;
  if (state.currentGroup) {
    const g = state.groups.find(g => g.slug === state.currentGroup);
    label = g ? g.name : state.currentGroup;
  } else {
    const proj = state.projects.find(p => String(p.slug ?? p.Slug ?? "") === state.currentProject);
    label = proj ? String(proj.name ?? proj.Name ?? state.currentProject) : "All projects";
  }
  if (state.currentEnv) label += ` / ${state.currentEnv}`;
  return `${escapeHtml(label)} <span class="scope-chevron">▾</span>`;
}

function updatePickerBtn(): void {
  if (!pickerBtn) return;
  pickerBtn.innerHTML = scopeLabelHtml();
}

function pickerAllHtml(f: string): string {
  const allSel = !state.currentGroup && (!state.currentProject || state.currentProject === "__all");
  if (f && !"all projects".includes(f)) return "";
  return `<button class="picker-item${allSel ? " selected" : ""}" data-pick-project="__all">All projects</button>`;
}

function pickerGroupsHtml(visibleGroups: ApiProjectGroup[]): string {
  if (visibleGroups.length === 0) return "";
  let html = `<div class="picker-section-label">Groups</div>`;
  for (const g of visibleGroups) {
    const sel = state.currentGroup === g.slug;
    const count = state.projects.filter(p => p.group_id === g.id).length;
    html += `<button class="picker-item${sel ? " selected" : ""}" data-pick-group="${escapeHtml(g.slug)}">${escapeHtml(g.name)}<span class="picker-item-badge">${count}</span></button>`;
  }
  return html;
}

function pickerProjectsHtml(visibleProjects: ApiProject[]): string {
  if (visibleProjects.length === 0) return "";
  let html = `<div class="picker-section-label">Projects</div>`;
  for (const p of visibleProjects) {
    const slug = String(p.slug ?? p.Slug ?? "");
    const name = String(p.name ?? p.Name ?? slug);
    const sel = !state.currentGroup && slug === state.currentProject;
    html += `<button class="picker-item${sel ? " selected" : ""}" data-pick-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
  }
  return html;
}

function pickerListHtml(f: string): string {
  const matchProject = (p: ApiProject) => {
    if (!f) return true;
    return String(p.name ?? p.Name ?? "").toLowerCase().includes(f) ||
           String(p.slug ?? p.Slug ?? "").toLowerCase().includes(f);
  };
  const matchGroup = (g: ApiProjectGroup) => !f ||
    g.name.toLowerCase().includes(f) || g.slug.toLowerCase().includes(f) ||
    state.projects.some(p => p.group_id === g.id && matchProject(p));

  const html = pickerAllHtml(f) +
    pickerGroupsHtml(state.groups.filter(matchGroup)) +
    pickerProjectsHtml(state.projects.filter(matchProject));
  return html || `<p class="picker-empty">No matches</p>`;
}

export function renderPickerList(filter: string): void {
  if (!pickerList) return;
  const f = filter.trim().toLowerCase();
  pickerList.innerHTML = pickerListHtml(f);

  pickerList.querySelectorAll<HTMLButtonElement>("[data-pick-group]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["pickGroup"] ?? "";
      state.currentGroup = slug; localStorage.setItem(groupKey, slug);
      state.currentProject = "__all"; localStorage.removeItem(projectKey);
      state.currentEnv = ""; localStorage.removeItem(envKey);
      renderEnvSwitcher([]); updatePickerBtn(); updateScopeBtn(); closePicker();
      void refreshAll();
    });
  });

  pickerList.querySelectorAll<HTMLButtonElement>("[data-pick-project]").forEach(btn => {
    btn.addEventListener("click", () => {
      selectProject(btn.dataset["pickProject"] ?? "__all");
      closePicker();
    });
  });
}

/** Scopes the app to one project, as the picker does. */
function selectProject(slug: string): void {
  state.currentGroup = null; localStorage.removeItem(groupKey);
  state.currentProject = slug; localStorage.setItem(projectKey, slug);
  state.currentEnv = ""; localStorage.removeItem(envKey);
  updatePickerBtn(); updateScopeBtn();
  if (slug === "__all") { renderEnvSwitcher([]); void refreshAll(); }
  else { void Promise.all([loadEnvironments(), refreshAll()]); }
}

// The error timeline legend asks for a project through this event, which keeps
// the chart component free of an import on this module.
document.addEventListener("bugbarn:select-project", (ev) => {
  const slug = (ev as CustomEvent<string>).detail;
  if (typeof slug === "string" && slug) selectProject(slug);
});

function updateScopeBtn(): void {
  const btn = document.getElementById("scope-btn");
  if (!btn) return;
  updatePickerBtn();
  btn.innerHTML = scopeLabelHtml();
}

export async function checkPendingProjects(): Promise<void> {
  const banner = document.getElementById("pending-banner");
  if (!banner) return;
  try {
    const res = await fetch(apiUrl("/api/v1/projects/pending-count"), {
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) return;
    const data = (await res.json()) as { count: number; slugs: string[] };
    if (data.count === 0) {
      banner.hidden = true;
      updatePendingBadge(0);
      return;
    }
    const slugList = (data.slugs ?? []).map((s: string) => escapeHtml(s)).join(", ");
    banner.innerHTML =
      `<span>${data.count} project${data.count > 1 ? "s" : ""} awaiting approval: <strong>${slugList}</strong></span>` +
      `<a href="#/settings" class="pending-banner-link">Review in Settings</a>`;
    banner.hidden = false;
    updatePendingBadge(data.count);
  } catch {
    // Silently ignore — non-critical.
  }
}

function updatePendingBadge(count: number): void {
  const settingsLink = document.querySelector<HTMLAnchorElement>('.side-nav a[data-route="settings"]');
  if (!settingsLink) return;
  let badge = settingsLink.querySelector<HTMLElement>(".nav-badge");
  if (count === 0) {
    badge?.remove();
    return;
  }
  if (!badge) {
    badge = document.createElement("span");
    badge.className = "nav-badge";
    settingsLink.appendChild(badge);
  }
  badge.textContent = String(count);
}

export async function loadEnvironments(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/facets/attributes.environment", true);
    const raw = payload as Record<string, unknown>;
    const envs = Array.isArray(raw?.["values"]) ? (raw["values"] as string[]) : [];
    renderEnvSwitcher(envs);
  } catch {
    renderEnvSwitcher([]);
  }
}

export function renderEnvSwitcher(envs: string[]): void {
  if (!envSelect) return;
  const current = state.currentEnv;
  envSelect.innerHTML = `<option value="">All environments</option>` +
    sortedOptionsHtml(envs.map((e) => ({ value: e, label: e })), current);
  envSelect.hidden = envs.length === 0;
  updateScopeBtn();
}

export function openScopeSheet(): void {
  if (!scopeSheet || !scopeBackdrop || !scopeBody) return;
  renderScopeSheetBody();
  scopeSheet.hidden = false;
  scopeBackdrop.hidden = false;
}

export function closeScopeSheet(): void {
  if (scopeSheet) scopeSheet.hidden = true;
  if (scopeBackdrop) scopeBackdrop.hidden = true;
}

type ScopeMatcher = (name: string, slug: string) => boolean;

function scopeGroupSectionHtml(f: string, current: string, visGroups: ApiProjectGroup[]): string {
  let html = `<div class="scope-section-label">Group</div>`;
  if (!f) html += `<button class="scope-item${state.currentGroup === null && (current === "__all" || !current) ? " selected" : ""}" data-scope-project="__all">All projects</button>`;
  for (const g of visGroups) {
    const selected = state.currentGroup === g.slug;
    html += `<button class="scope-item${selected ? " selected" : ""}" data-scope-group="${escapeHtml(g.slug)}">${escapeHtml(g.name)}</button>`;
  }
  return html + `<div class="scope-divider"></div>`;
}

function scopeGroupedHtml(f: string, current: string, matchesFilter: ScopeMatcher): string {
  let html = "";
  const visGroups = state.groups.filter(g => matchesFilter(g.name, g.slug));
  if (visGroups.length > 0 || !f) html += scopeGroupSectionHtml(f, current, visGroups);
  html += `<div class="scope-section-label">Project</div>`;
  if (f && "all projects".includes(f)) html += `<button class="scope-item" data-scope-project="__all">All projects</button>`;
  for (const p of state.projects) {
    const slug = String(p.slug ?? p.Slug ?? "");
    const name = String(p.name ?? p.Name ?? slug);
    if (!matchesFilter(name, slug)) continue;
    const selected = !state.currentGroup && slug === current;
    html += `<button class="scope-item${selected ? " selected" : ""}" data-scope-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
  }
  return html;
}

function scopeFlatHtml(f: string, current: string, matchesFilter: ScopeMatcher): string {
  let html = `<div class="scope-section-label">Project</div>`;
  if (!f || "all projects".includes(f)) html += `<button class="scope-item${current === "__all" || !current ? " selected" : ""}" data-scope-project="__all">All projects</button>`;
  for (const p of state.projects) {
    const slug = String(p.slug ?? p.Slug ?? "");
    const name = String(p.name ?? p.Name ?? slug);
    if (!matchesFilter(name, slug)) continue;
    html += `<button class="scope-item${slug === current ? " selected" : ""}" data-scope-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
  }
  return html;
}

function scopeEnvHtml(): string {
  return `<div class="scope-divider"></div>` +
    `<div class="scope-section-label">Environment</div>` +
    `<button class="scope-item scope-item-sub${!state.currentEnv ? " selected" : ""}" data-scope-env="">All environments</button>` +
    `<div id="scope-env-list"></div>`;
}

function scopeSheetHtml(filter: string, f: string, current: string, showEnv: boolean): string {
  const matchesFilter: ScopeMatcher = (name, slug) => !f || name.toLowerCase().includes(f) || slug.toLowerCase().includes(f);
  let html = `<input class="scope-sheet-filter" type="text" placeholder="Filter…" value="${escapeHtml(filter)}" autocomplete="off" />`;
  html += state.groups.length > 0
    ? scopeGroupedHtml(f, current, matchesFilter)
    : scopeFlatHtml(f, current, matchesFilter);
  if (showEnv) html += scopeEnvHtml();
  return html;
}

function onScopeGroupClick(btn: HTMLButtonElement): void {
  const slug = btn.getAttribute("data-scope-group") ?? "";
  state.currentGroup = slug;
  localStorage.setItem(groupKey, slug);
  state.currentProject = "__all";
  localStorage.removeItem(projectKey);
  state.currentEnv = "";
  localStorage.removeItem(envKey);
  renderEnvSwitcher([]);
  renderProjectSwitcher();
  closeScopeSheet();
  void refreshAll();
}

function onScopeProjectClick(btn: HTMLButtonElement): void {
  const slug = btn.getAttribute("data-scope-project") ?? "__all";
  state.currentGroup = null;
  localStorage.removeItem(groupKey);
  state.currentProject = slug;
  localStorage.setItem(projectKey, slug);
  state.currentEnv = "";
  localStorage.removeItem(envKey);
  renderProjectSwitcher();
  if (slug === "__all") {
    renderEnvSwitcher([]);
    closeScopeSheet();
    void refreshAll();
  } else {
    renderScopeSheetBody();
    void Promise.all([loadEnvironments(), refreshAll()]);
  }
}

function onScopeEnvClick(btn: HTMLButtonElement): void {
  const env = btn.getAttribute("data-scope-env") ?? "";
  state.currentEnv = env;
  if (env) localStorage.setItem(envKey, env); else localStorage.removeItem(envKey);
  renderEnvSwitcher([]);
  updateScopeBtn();
  closeScopeSheet();
  void refreshAll();
}

function renderScopeSheetBody(filter = ""): void {
  if (!scopeBody) return;
  const f = filter.trim().toLowerCase();
  const current = state.currentProject;
  const showEnv = !state.currentGroup && !!current && current !== "__all";
  scopeBody.innerHTML = scopeSheetHtml(filter, f, current, showEnv);
  // The list container exists only once the markup is in the DOM.
  if (showEnv) void loadScopeEnvs();

  scopeBody.querySelector<HTMLInputElement>(".scope-sheet-filter")?.addEventListener("input", (e) => {
    renderScopeSheetBody((e.target as HTMLInputElement).value);
  });
  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-group]").forEach(btn => {
    btn.addEventListener("click", () => onScopeGroupClick(btn));
  });
  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-project]").forEach(btn => {
    btn.addEventListener("click", () => onScopeProjectClick(btn));
  });
  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-env]").forEach(btn => {
    btn.addEventListener("click", () => onScopeEnvClick(btn));
  });
}

async function loadScopeEnvs(): Promise<void> {
  const el = document.getElementById("scope-env-list");
  if (!el) return;
  try {
    const payload = await fetchJson("/api/v1/facets/attributes.environment", true);
    const raw = payload as Record<string, unknown>;
    const envs = Array.isArray(raw?.["values"]) ? (raw["values"] as string[]) : [];
    let html = "";
    for (const e of envs) {
      html += `<button class="scope-item scope-item-sub${e === state.currentEnv ? " selected" : ""}" data-scope-env="${escapeHtml(e)}">${escapeHtml(e)}</button>`;
    }
    el.innerHTML = html;
    el.querySelectorAll<HTMLButtonElement>("[data-scope-env]").forEach(btn => {
      btn.addEventListener("click", () => {
        const env = btn.getAttribute("data-scope-env") ?? "";
        state.currentEnv = env;
        if (env) localStorage.setItem(envKey, env); else localStorage.removeItem(envKey);
        updateScopeBtn();
        closeScopeSheet();
        void refreshAll();
      });
    });
  } catch {
    el.innerHTML = "";
  }
}
