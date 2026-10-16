// Rows of the "By surface" panel on the Issues page. Pure data so a node test
// can load it without the DOM helpers.

export interface SurfaceActivity extends Record<string, unknown> {
  surface: string;
  issues_24h: number;
  issues_7d: number;
  open: number;
}

/** Keeps well-formed rows from the surfaces endpoint, busiest first. */
export function surfaceRows(payload: unknown): SurfaceActivity[] {
  const raw = (payload as { surfaces?: unknown } | null)?.surfaces;
  if (!Array.isArray(raw)) return [];
  return (raw as SurfaceActivity[])
    .filter((r) => typeof r?.surface === "string" && r.surface !== "")
    .sort((a, b) => b.issues_7d - a.issues_7d || a.surface.localeCompare(b.surface));
}

/** The panel is per project: it stays hidden for the all-projects and group scopes. */
export function surfacesApply(currentProject: string, currentGroup: string | null): boolean {
  return !currentGroup && currentProject !== "" && currentProject !== "__all" && currentProject !== "default";
}
