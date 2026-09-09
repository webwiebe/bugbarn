// Volume settings view: per-project retention window and event sampling.
import { renderVolumeViewMarkup } from "./components.js";
import { normalizeList } from "./data.js";
import { errorMessage } from "./format.js";
import type { ApiProject, VolumeDefaults } from "./types.js";
import { elements, setActiveView, setStatus, showFlash, state } from "./core.js";
import { apiFetch, fetchJson } from "./http.js";

const DEFAULT_LIMITS: VolumeDefaults = { retention_days: 30, sample_after: 1000 };

/** loadVolume fetches the project list with usage counts and the deployment
 * defaults the per-project controls are relative to. */
export async function loadVolume(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/projects?usage=true", true);
    if (payload) {
      state.projects = normalizeList<ApiProject>(payload, "projects");
      const defaults = (payload as Record<string, unknown>)["defaults"];
      state.volumeDefaults = defaults ? (defaults as VolumeDefaults) : DEFAULT_LIMITS;
    }
    if (state.currentRoute === "settings" && state.settingsTab === "volume") renderVolumeView();
  } catch (error) {
    if (state.currentRoute === "settings" && state.settingsTab === "volume") renderVolumeView(error);
  }
}

export function renderVolumeView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Volume";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderVolumeViewMarkup(
    state.projects,
    state.volumeDefaults ?? DEFAULT_LIMITS,
    error,
  );
  wireVolumeActions();
}

/** wireVolumeActions re-attaches the row handlers after every render, because
 * assigning innerHTML above discarded the previous ones. */
function wireVolumeActions(): void {
  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-volume-save]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.getAttribute("data-volume-save");
      if (slug) void saveVolume(slug, btn);
    });
  });
}

/** readRow pulls the two controls for a project out of the DOM. A blank
 * retention field means "inherit", which the API models as null. */
function readRow(slug: string): { retention_days: number | null; sampling_mode: string } | null {
  const days = elements.overviewView.querySelector<HTMLInputElement>(
    `[data-volume-days="${CSS.escape(slug)}"]`);
  const sampling = elements.overviewView.querySelector<HTMLSelectElement>(
    `[data-volume-sampling="${CSS.escape(slug)}"]`);
  if (!days || !sampling) return null;

  const raw = days.value.trim();
  if (raw === "") return { retention_days: null, sampling_mode: sampling.value };

  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed < 1) return null;
  return { retention_days: Math.floor(parsed), sampling_mode: sampling.value };
}

async function saveVolume(slug: string, btn: HTMLButtonElement): Promise<void> {
  const body = readRow(slug);
  if (!body) {
    showFlash("Keep-for must be a whole number of days, or blank to inherit.", "error", 8000);
    return;
  }

  const label = btn.textContent ?? "Save";
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const res = await apiFetch(`/api/v1/projects/${encodeURIComponent(slug)}/limits`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`.trim());
    const payload = await res.json().catch(() => ({})) as { clamped?: boolean; retention_days?: number | null };
    window.funnelbarn?.track("project_volume_updated", { slug, sampling: body.sampling_mode });
    if (payload.clamped) {
      showFlash(
        `Kept ${slug} at ${payload.retention_days} days — the deployment window is the maximum.`,
        "info", 8000);
    } else {
      setStatus(`Volume settings saved for ${slug}.`);
    }
    await loadVolume();
  } catch (error) {
    btn.disabled = false;
    btn.textContent = label;
    showFlash(`Save failed: ${errorMessage(error)}`, "error", 8000);
  }
}
