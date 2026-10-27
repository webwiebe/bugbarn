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

/**
 * The FunnelBarn page for one surface row, or null when the project has no
 * link. "{surface}" in the setting is replaced by the encoded surface name; a
 * setting without it is used as is. Only absolute http(s) URLs qualify, so a
 * stored value can never become a javascript: link.
 */
export function surfaceLink(template: string | undefined | null, surface: string): string | null {
  const raw = (template ?? "").trim();
  if (raw === "") return null;
  const filled = raw.split("{surface}").join(encodeURIComponent(surface));
  try {
    const u = new URL(filled);
    return u.protocol === "https:" || u.protocol === "http:" ? u.toString() : null;
  } catch {
    return null;
  }
}

/** The panel is per project: it stays hidden for the all-projects and group scopes. */
export function surfacesApply(currentProject: string, currentGroup: string | null): boolean {
  return !currentGroup && currentProject !== "" && currentProject !== "__all" && currentProject !== "default";
}
