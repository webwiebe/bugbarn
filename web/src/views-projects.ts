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
  favouritesKey,
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
import { sortedOptionsHtml } from "./components/select-options.js";
import { compareByName, matchesSearch, orderProjects, parseFavourites, toggleFavourite, type SwitcherProject } from "./components/project-order.js";

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
  for (const g of [...visibleGroups].sort((a, b) => compareByName({ slug: a.slug, name: a.name }, { slug: b.slug, name: b.name }))) {
    const sel = state.currentGroup === g.slug;
    const count = state.projects.filter(p => p.group_id === g.id).length;
    html += `<button class="picker-item${sel ? " selected" : ""}" data-pick-group="${escapeHtml(g.slug)}">${escapeHtml(g.name)}<span class="picker-item-badge">${count}</span></button>`;
  }
  return html;
}

// Favourites live in this browser: BugBarn keeps its settings per project, not
// per user, so there is no server-side place that follows a user across devices.
function loadFavourites(): Set<string> {
  try {
    return new Set(parseFavourites(localStorage.getItem(favouritesKey)));
  } catch {
    return new Set();
  }
}

function flipFavourite(slug: string): void {
  try {
    localStorage.setItem(favouritesKey, JSON.stringify(toggleFavourite([...loadFavourites()], slug)));
  } catch {
    // Storage can be blocked; the star then does nothing.
  }
}

function switcherProjects(): SwitcherProject[] {
  return state.projects.map(switcherProjectOf);
}

function starHtml(slug: string, name: string, starred: boolean, cls: string): string {
  return `<button type="button" class="${cls}${starred ? " starred" : ""}" data-favourite="${escapeHtml(slug)}" aria-pressed="${starred}" aria-label="${starred ? "Remove" : "Add"} ${escapeHtml(name)} ${starred ? "from" : "to"} favourites">${starred ? "★" : "☆"}</button>`;
}

function pickerRowHtml(p: SwitcherProject, starred: boolean): string {
  const sel = !state.currentGroup && p.slug === state.currentProject;
  return `<div class="picker-row"><button class="picker-item${sel ? " selected" : ""}"${sel ? ' aria-current="true"' : ""} data-pick-project="${escapeHtml(p.slug)}">${escapeHtml(p.name)}</button>${starHtml(p.slug, p.name, starred, "picker-star")}</div>`;
}

function pickerProjectsHtml(search: string): string {
  const { favourites, others } = orderProjects(switcherProjects(), loadFavourites(), search);
  let html = "";
  if (favourites.length > 0) html += `<div class="picker-section-label">Favourites</div>` + favourites.map((p) => pickerRowHtml(p, true)).join("");
  if (others.length > 0) html += `<div class="picker-section-label">Projects</div>` + others.map((p) => pickerRowHtml(p, false)).join("");
  return html;
}

function pickerListHtml(f: string): string {
  const matchGroup = (g: ApiProjectGroup) => matchesSearch({ slug: g.slug, name: g.name }, f) ||
    state.projects.some(p => p.group_id === g.id && matchesSearch(switcherProjectOf(p), f));

  const html = pickerAllHtml(f) +
    pickerGroupsHtml(state.groups.filter(matchGroup)) +
    pickerProjectsHtml(f);
  return html || `<p class="picker-empty">No projects match</p>`;
}

function switcherProjectOf(p: ApiProject): SwitcherProject {
  const slug = String(p.slug ?? p.Slug ?? "");
  return { slug, name: String(p.name ?? p.Name ?? slug) };
}

export function renderPickerList(filter: string): void {
  if (!pickerList) return;
  const f = filter.trim().toLowerCase();
  pickerList.innerHTML = pickerListHtml(f);

  pickerList.querySelectorAll<HTMLButtonElement>("[data-favourite]").forEach(btn => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      flipFavourite(btn.dataset["favourite"] ?? "");
      renderPickerList(pickerFilter?.value ?? "");
    });
  });

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

function scopeRowHtml(p: SwitcherProject, starred: boolean, selected: boolean): string {
  return `<div class="scope-row"><button class="scope-item${selected ? " selected" : ""}" data-scope-project="${escapeHtml(p.slug)}">${escapeHtml(p.name)}</button>${starHtml(p.slug, p.name, starred, "scope-star")}</div>`;
}

function scopeProjectsHtml(f: string, current: string): string {
  const { favourites, others } = orderProjects(switcherProjects(), loadFavourites(), f);
  const row = (starred: boolean) => (p: SwitcherProject) => scopeRowHtml(p, starred, !state.currentGroup && p.slug === current);
  let html = "";
  if (favourites.length > 0) html += `<div class="scope-section-label">Favourites</div>` + favourites.map(row(true)).join("");
  if (others.length > 0) html += `<div class="scope-section-label">Project</div>` + others.map(row(false)).join("");
  if (favourites.length + others.length === 0) html += `<p class="picker-empty">No projects match</p>`;
  return html;
}

function scopeGroupedHtml(f: string, current: string, matchesFilter: ScopeMatcher): string {
  let html = "";
  const visGroups = state.groups.filter(g => matchesFilter(g.name, g.slug))
    .sort((a, b) => compareByName({ slug: a.slug, name: a.name }, { slug: b.slug, name: b.name }));
  if (visGroups.length > 0 || !f) html += scopeGroupSectionHtml(f, current, visGroups);
  if (f && "all projects".includes(f)) html += `<button class="scope-item" data-scope-project="__all">All projects</button>`;
  return html + scopeProjectsHtml(f, current);
}

function scopeFlatHtml(f: string, current: string): string {
  let html = "";
  if (!f || "all projects".includes(f)) html += `<button class="scope-item${current === "__all" || !current ? " selected" : ""}" data-scope-project="__all">All projects</button>`;
  return html + scopeProjectsHtml(f, current);
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
    : scopeFlatHtml(f, current);
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
  scopeBody.querySelectorAll<HTMLButtonElement>("[data-favourite]").forEach(btn => {
    btn.addEventListener("click", () => {
      flipFavourite(btn.dataset["favourite"] ?? "");
      renderScopeSheetBody(scopeBody?.querySelector<HTMLInputElement>(".scope-sheet-filter")?.value ?? "");
    });
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
