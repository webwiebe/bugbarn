// Settings view: settings/projects/groups/aliases/keys/system loading,
// rendering, action wiring, and all the project/group/alias mutations.
import { wireProjectList } from "./project-filter.js";
import { renderSettingsViewMarkup } from "./components.js";
import { fetchSystemHealth, normalizeList, normalizeObject } from "./data.js";
import { errorMessage, escapeHtml } from "./format.js";
import type { ApiAlias, ApiApiKey, ApiKeyRequest, ApiProject, ApiProjectGroup, ApiSettings } from "./types.js";
import { elements, liveWindowMinutes, setActiveView, setStatus, showFlash, state } from "./core.js";
import { apiFetch, fetchJson, logout, postFormData, postJson } from "./http.js";
import { checkPendingProjects, renderProjectSwitcher } from "./views-projects.js";

export async function loadSettings(): Promise<void> {
  try {
    const [settingsPayload, keysPayload, projectsPayload, groupsPayload, aliasesPayload, keyRequestsPayload] = await Promise.all([
      fetchJson("/api/v1/settings", true),
      fetchJson("/api/v1/apikeys", true).catch(() => null),
      // ?usage=true: the settings screen is the only view that renders the
      // per-project counts, and computing them is expensive enough that every
      // other caller opts out.
      fetchJson("/api/v1/projects?usage=true", true).catch(() => null),
      fetchJson("/api/v1/groups", true).catch(() => null),
      fetchJson("/api/v1/aliases", true).catch(() => null),
      fetchJson("/api/v1/key-requests", true).catch(() => null),
    ]);
    state.settings = settingsPayload ? normalizeObject<ApiSettings>(settingsPayload, "settings") : null;
    state.apiKeys = keysPayload ? normalizeList<ApiApiKey>(keysPayload as Record<string, unknown>, "apiKeys") : [];
    if (projectsPayload) state.projects = normalizeList<ApiProject>(projectsPayload, "projects");
    state.groups = groupsPayload ? ((groupsPayload as Record<string, unknown>)["groups"] as ApiProjectGroup[] ?? []) : [];
    state.aliases = aliasesPayload ? ((aliasesPayload as Record<string, unknown>)["aliases"] as ApiAlias[] ?? []) : [];
    state.keyRequests = keyRequestsPayload ? normalizeList<ApiKeyRequest>(keyRequestsPayload as Record<string, unknown>, "keyRequests") : [];
    renderProjectSwitcher();
    if (state.currentRoute === "settings") renderSettingsView();
  } catch (error) {
    state.settings = null;
    if (state.currentRoute === "settings") renderSettingsView(error);
  }
}

export async function loadSdkInfo(): Promise<void> {
  const el = document.getElementById("sdk-info");
  if (!el) return;
  try {
    const res = await fetch("/packages/typescript/latest.json");
    if (!res.ok) throw new Error(`${res.status}`);
    const info = await res.json() as { version: string; filename: string; url: string };
    const absUrl = `${window.location.origin}${info.url}`;
    el.innerHTML = `
      <div class="kv"><span>Version</span><span>${escapeHtml(info.version)}</span></div>
      <div class="kv"><span>Tarball URL</span><code class="sdk-url">${escapeHtml(absUrl)}</code></div>
      <div class="kv"><span>Install</span><code class="sdk-url">pnpm add ${escapeHtml(absUrl)}</code></div>
    `;
  } catch {
    el.innerHTML = `<div class="kv"><span>Status</span><span>Package not yet published — deploy the web container first.</span></div>`;
  }
}

let systemHealthLoading = false;

export function renderSettingsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Settings";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderSettingsViewMarkup(state.settings, state.username, state.apiKeys, error, state.projects, state.groups, state.aliases, state.settingsTab, state.systemHealth, state.keyRequests);
  wireSettingsActions();
  if (state.settingsTab === "system") {
    void loadSystemHealth();
  }
}

// loadSystemHealth fetches the detailed health endpoint and re-renders the
// System tab. It is best-effort: a fetch failure leaves the existing snapshot in
// place rather than blanking the panel.
async function loadSystemHealth(): Promise<void> {
  if (systemHealthLoading) return;
  systemHealthLoading = true;
  try {
    state.systemHealth = await fetchSystemHealth();
  } catch {
    // Leave state.systemHealth as-is; the panel shows the last good value.
  } finally {
    systemHealthLoading = false;
  }
  if (state.currentRoute === "settings" && state.settingsTab === "system") {
    elements.overviewView.innerHTML = renderSettingsViewMarkup(state.settings, state.username, state.apiKeys, null, state.projects, state.groups, state.aliases, state.settingsTab, state.systemHealth, state.keyRequests);
    wireSettingsActions();
  }
}

function wireSettingsActions(): void {
  const settingsForm = elements.overviewView.querySelector<HTMLFormElement>("#settings-form");
  const sourceMapForm = elements.overviewView.querySelector<HTMLFormElement>("#source-map-form");

  settingsForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSettingsForm(settingsForm);
  });

  sourceMapForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSourceMapsForm(sourceMapForm);
  });

  const copySetupBtn = elements.overviewView.querySelector<HTMLButtonElement>("#copy-setup-url");
  copySetupBtn?.addEventListener("click", () => {
    const urlEl = elements.overviewView.querySelector<HTMLElement>("#setup-url");
    if (urlEl) {
      void navigator.clipboard.writeText(urlEl.textContent || "");
      copySetupBtn.textContent = "✓";
      setTimeout(() => { copySetupBtn.textContent = "⧉"; }, 1500);
    }
  });

  const logoutBtn = elements.overviewView.querySelector<HTMLButtonElement>("#settings-logout");
  logoutBtn?.addEventListener("click", () => { void logout(); });

  wireProjectListControls();

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-approve-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["approveProject"];
      if (slug) void approveProject(slug, btn);
    });
  });

  wireKeyRequestActions();

  wireFunnelbarnForms();

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteProject"];
      if (slug) void deleteProject(slug, btn);
    });
  });

  // Group actions
  const createGroupForm = elements.overviewView.querySelector<HTMLFormElement>("#create-group-form");
  createGroupForm?.addEventListener("submit", (e) => {
    e.preventDefault();
    const data = new FormData(createGroupForm);
    const name = String(data.get("name") || "").trim();
    if (name) void createGroup(name);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-group]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteGroup"];
      if (slug) void deleteGroup(slug);
    });
  });

  elements.overviewView.querySelectorAll<HTMLFormElement>("[data-add-to-group]").forEach((form) => {
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const groupSlug = form.dataset["addToGroup"]!;
      const select = form.querySelector<HTMLSelectElement>("select");
      const projectSlug = select?.value;
      if (projectSlug) void addProjectToGroup(groupSlug, projectSlug);
    });
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-remove-from-group]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const projectSlug = btn.dataset["removeFromGroup"];
      if (projectSlug) void removeProjectFromGroup(projectSlug);
    });
  });

  // Alias actions
  const createAliasForm = elements.overviewView.querySelector<HTMLFormElement>("#create-alias-form");
  createAliasForm?.addEventListener("submit", (e) => {
    e.preventDefault();
    const data = new FormData(createAliasForm);
    const alias = String(data.get("alias") || "").trim();
    const project = String(data.get("project") || "").trim();
    if (alias && project) void createAlias(alias, project);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-alias]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteAlias"];
      if (slug) void deleteAlias(slug);
    });
  });
}

function wireProjectListControls(): void {
  const list = elements.overviewView.querySelector<HTMLElement>("#project-list");
  if (!list) return;
  wireProjectList({
    list,
    filter: elements.overviewView.querySelector<HTMLInputElement>("#project-filter"),
    sort: elements.overviewView.querySelector<HTMLSelectElement>("#project-sort"),
    status: elements.overviewView.querySelector<HTMLSelectElement>("#project-status-filter"),
  });
}

async function approveProject(slug: string, btn?: HTMLButtonElement): Promise<void> {
  const label = btn?.textContent ?? "";
  if (btn) { btn.disabled = true; btn.textContent = "…"; }
  try {
    await postJson(`/api/v1/projects/${encodeURIComponent(slug)}/approve`, {});
    window.funnelbarn?.track("project_approved", { slug });
    showFlash(`Project "${slug}" approved.`, "success");
    await loadSettings();
    void checkPendingProjects();
  } catch (error) {
    if (btn) { btn.disabled = false; btn.textContent = label; }
    showFlash(`Approve failed: ${errorMessage(error)}`, "error", 8000);
  }
}

function wireKeyRequestActions(): void {
  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-approve-key-request], [data-deny-key-request]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const approve = btn.dataset["approveKeyRequest"];
      const deny = btn.dataset["denyKeyRequest"];
      if (approve) void resolveKeyRequest(approve, "approve", btn);
      else if (deny) void resolveKeyRequest(deny, "deny", btn);
    });
  });
}

async function resolveKeyRequest(id: string, action: "approve" | "deny", btn: HTMLButtonElement): Promise<void> {
  const label = btn.textContent ?? "";
  btn.disabled = true;
  btn.textContent = "…";
  try {
    await postJson(`/api/v1/key-requests/${encodeURIComponent(id)}/${action}`, {});
    showFlash(action === "approve" ? "Key request approved. The agent can now collect its key." : "Key request denied.", "success");
    await loadSettings();
  } catch (error) {
    btn.disabled = false;
    btn.textContent = label;
    showFlash(`Key request ${action} failed: ${errorMessage(error)}`, "error", 8000);
  }
}

function wireFunnelbarnForms(): void {
  elements.overviewView.querySelectorAll<HTMLFormElement>("[data-funnelbarn-form]").forEach((form) => {
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const slug = form.dataset["funnelbarnForm"];
      if (slug) void saveFunnelbarnUrl(slug, form);
    });
  });
}

async function saveFunnelbarnUrl(slug: string, form: HTMLFormElement): Promise<void> {
  const input = form.querySelector<HTMLInputElement>("input[name=funnelbarn_url]");
  const btn = form.querySelector<HTMLButtonElement>("button[type=submit]");
  if (!input) return;
  const label = btn?.textContent ?? "";
  if (btn) { btn.disabled = true; btn.textContent = "…"; }
  try {
    const res = await apiFetch(`/api/v1/projects/${encodeURIComponent(slug)}/funnelbarn`, {
      method: "PUT",
      body: JSON.stringify({ funnelbarn_url: input.value.trim() }),
    });
    if (!res.ok) {
      const text = (await res.text().catch(() => "")).trim();
      throw new Error(text || res.statusText);
    }
    showFlash(input.value.trim() === "" ? `FunnelBarn link cleared for ${slug}.` : `FunnelBarn link saved for ${slug}.`, "success");
    await loadSettings();
  } catch (error) {
    if (btn) { btn.disabled = false; btn.textContent = label; }
    showFlash(`Saving the FunnelBarn link failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteProject(slug: string, btn?: HTMLButtonElement): Promise<void> {
  if (!confirm(`Delete project "${slug}"? This will remove all its issues and events.`)) return;
  const label = btn?.textContent ?? "";
  if (btn) { btn.disabled = true; btn.textContent = "…"; }
  try {
    const res = await apiFetch(`/api/v1/projects/${encodeURIComponent(slug)}`, { method: "DELETE" });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as Record<string, string>).error || res.statusText);
    }
    window.funnelbarn?.track("project_deleted", { slug });
    showFlash(`Project "${slug}" deleted.`, "success");
    await loadSettings();
    void checkPendingProjects();
  } catch (error) {
    if (btn) { btn.disabled = false; btn.textContent = label; }
    showFlash(`Delete failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function createGroup(name: string): Promise<void> {
  try {
    await postJson("/api/v1/groups", { name });
    window.funnelbarn?.track("project_group_created", { name });
    showFlash(`Group "${name}" created.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Create group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteGroup(slug: string): Promise<void> {
  if (!confirm(`Delete group "${slug}"? Projects will be ungrouped but not deleted.`)) return;
  try {
    const res = await apiFetch(`/api/v1/groups/${encodeURIComponent(slug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`Group "${slug}" deleted.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Delete group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function addProjectToGroup(groupSlug: string, projectSlug: string): Promise<void> {
  try {
    await postJson(`/api/v1/groups/${encodeURIComponent(groupSlug)}/projects`, { project: projectSlug });
    showFlash(`"${projectSlug}" added to group.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Add to group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function removeProjectFromGroup(projectSlug: string): Promise<void> {
  try {
    const group = state.groups.find(g => state.projects.find(p => (p.slug ?? p.Slug) === projectSlug && p.group_id === g.id));
    if (!group) throw new Error("project not in a group");
    const res = await apiFetch(`/api/v1/groups/${encodeURIComponent(group.slug)}/projects/${encodeURIComponent(projectSlug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`"${projectSlug}" removed from group.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Remove from group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function createAlias(alias: string, project: string): Promise<void> {
  try {
    await postJson("/api/v1/aliases", { alias, project });
    window.funnelbarn?.track("project_alias_created", { alias, project });
    showFlash(`Alias "${alias}" → "${project}" created.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Create alias failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteAlias(aliasSlug: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/aliases/${encodeURIComponent(aliasSlug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`Alias "${aliasSlug}" deleted.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Delete alias failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function submitSettingsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/settings", {
      displayName: String(data.get("displayName") || ""),
      timezone: String(data.get("timezone") || ""),
      defaultEnvironment: String(data.get("defaultEnvironment") || ""),
      liveWindowMinutes: Number(data.get("liveWindowMinutes") || liveWindowMinutes),
      stacktraceContextLines: Number(data.get("stacktraceContextLines") || 3),
    });
    setStatus("Settings saved.");
    await loadSettings();
  } catch (error) {
    setStatus(`Settings unavailable: ${errorMessage(error)}`);
  }
}

async function submitSourceMapsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postFormData("/api/v1/source-maps", data);
    window.funnelbarn?.track("sourcemaps_uploaded");
    setStatus("Source maps uploaded.");
  } catch (error) {
    setStatus(`Source map upload unavailable: ${errorMessage(error)}`);
  }
}
