// Order and filter for the project switcher: alphabetical by name, starred
// projects first in their own group, a search over name and slug.

export interface SwitcherProject {
  slug: string;
  name: string;
}

export interface OrderedProjects {
  favourites: SwitcherProject[];
  others: SwitcherProject[];
}

const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });

/** Compares by name, then by slug, so equal names keep a stable order. */
export function compareByName(a: SwitcherProject, b: SwitcherProject): number {
  return collator.compare(a.name, b.name) || collator.compare(a.slug, b.slug);
}

/** True when the search text appears in the name or the slug, ignoring case. */
export function matchesSearch(p: SwitcherProject, search: string): boolean {
  const f = search.trim().toLowerCase();
  return !f || p.name.toLowerCase().includes(f) || p.slug.toLowerCase().includes(f);
}

/** Filters, sorts and splits the projects into starred and the rest. */
export function orderProjects(projects: readonly SwitcherProject[], favourites: ReadonlySet<string>, search: string): OrderedProjects {
  const matching = projects.filter((p) => matchesSearch(p, search)).sort(compareByName);
  return {
    favourites: matching.filter((p) => favourites.has(p.slug)),
    others: matching.filter((p) => !favourites.has(p.slug)),
  };
}

/** Adds the slug to the favourites, or removes it when it is there already. */
export function toggleFavourite(favourites: readonly string[], slug: string): string[] {
  return favourites.includes(slug) ? favourites.filter((s) => s !== slug) : [...favourites, slug];
}

/** Reads the stored favourites; anything that is not a list of strings gives an empty list. */
export function parseFavourites(raw: string | null): string[] {
  if (!raw) return [];
  try {
    const value: unknown = JSON.parse(raw);
    return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
  } catch {
    return [];
  }
}
