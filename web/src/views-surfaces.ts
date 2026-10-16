// "By surface" panel on the issues page: issues seen per sign-up surface.
import { state } from "./core.js";
import { escapeHtml } from "./format.js";
import { fetchJson } from "./http.js";
import { surfaceRows, surfacesApply, type SurfaceActivity } from "./surfaces.js";

let rows: SurfaceActivity[] = [];
let generation = 0;

/** Draws the panel from what is cached into the slot on the issues page. */
export function renderSurfacesSlot(): void {
  const slot = document.getElementById("surfaces-slot");
  if (!slot) return;
  if (rows.length === 0) {
    slot.innerHTML = "";
    return;
  }
  const body = rows
    .map(
      (r) =>
        `<tr><td>${escapeHtml(r.surface)}</td><td>${r.issues_24h}</td><td>${r.issues_7d}</td><td>${r.open}</td></tr>`,
    )
    .join("");
  slot.innerHTML = `<section class="surface-panel" aria-label="Issues by surface">
    <h3>By surface</h3>
    <table><thead><tr><th>Surface</th><th>Issues 24h</th><th>Issues 7d</th><th>Open</th></tr></thead><tbody>${body}</tbody></table>
  </section>`;
}

/** Fetches the surfaces of the current project; clears the panel outside a single project. */
export async function loadSurfaces(): Promise<void> {
  const mine = ++generation;
  if (!surfacesApply(state.currentProject, state.currentGroup)) {
    rows = [];
    renderSurfacesSlot();
    return;
  }
  try {
    const payload = await fetchJson("/api/v1/surfaces");
    if (mine !== generation) return;
    rows = surfaceRows(payload);
  } catch {
    if (mine !== generation) return;
    rows = [];
  }
  renderSurfacesSlot();
}
