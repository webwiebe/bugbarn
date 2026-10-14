// Filtering, sorting and the empty state of the project list on Settings.
// No imports, so the unit test can load it straight from source.

type RowCompare = (a: HTMLElement, b: HTMLElement) => number;

const num = (row: HTMLElement, key: string): number => Number(row.dataset[key] ?? 0);
const byName = (a: HTMLElement, b: HTMLElement, dir: 1 | -1): number =>
  (a.dataset["name"] ?? "").localeCompare(b.dataset["name"] ?? "") * dir;

const COMPARATORS: Record<string, RowCompare> = {
  "name-asc": (a, b) => byName(a, b, 1),
  "name-desc": (a, b) => byName(a, b, -1),
  "issues-desc": (a, b) => num(b, "issues") - num(a, "issues"),
  "events-desc": (a, b) => num(b, "events") - num(a, "events"),
  "logs-desc": (a, b) => num(b, "logs") - num(a, "logs"),
  status: (a, b) => (a.dataset["status"] ?? "").localeCompare(b.dataset["status"] ?? "") || byName(a, b, 1),
};

const pendingFirst: RowCompare = (a, b) => {
  const ap = a.dataset["status"] === "pending" ? 0 : 1;
  const bp = b.dataset["status"] === "pending" ? 0 : 1;
  return ap - bp || byName(a, b, 1);
};

function matches(row: HTMLElement, query: string, status: string): boolean {
  const name = row.dataset["name"] ?? "";
  const slug = (row.dataset["slug"] ?? "").toLowerCase();
  if (query && !name.includes(query) && !slug.includes(query)) return false;
  return status === "all" || (row.dataset["status"] ?? "") === status;
}

function ensureEmptyState(list: HTMLElement): HTMLElement {
  const existing = list.parentElement?.querySelector<HTMLElement>("#project-empty");
  if (existing) return existing;
  const empty = document.createElement("div");
  empty.id = "project-empty";
  empty.className = "project-empty muted";
  empty.hidden = true;
  list.after(empty);
  return empty;
}

function showEmptyState(empty: HTMLElement, query: string, onClear: () => void): void {
  empty.textContent = "";
  const text = document.createElement("span");
  text.textContent = query ? `No projects match "${query}" ` : "No projects match the filters ";
  const clear = document.createElement("button");
  clear.type = "button";
  clear.id = "project-empty-clear";
  clear.className = "project-empty-clear";
  clear.textContent = "Clear";
  clear.addEventListener("click", onClear);
  empty.append(text, clear);
  empty.hidden = false;
}

export interface ProjectListControls {
  list: HTMLElement;
  filter: HTMLInputElement | null;
  sort: HTMLSelectElement | null;
  status: HTMLSelectElement | null;
}

/** Applies the filter, status and sort controls to the project rows. */
export function applyProjectList(controls: ProjectListControls): void {
  const { list, filter, sort, status } = controls;
  const rows = Array.from(list.querySelectorAll<HTMLElement>(".project-row"));
  const rawQuery = (filter?.value ?? "").trim();
  const query = rawQuery.toLowerCase();
  const statusValue = status?.value ?? "all";

  const visible = rows.filter((row) => matches(row, query, statusValue));
  rows.forEach((row) => { row.hidden = !visible.includes(row); });

  const compare = COMPARATORS[sort?.value ?? "default"] ?? pendingFirst;
  [...visible].sort(compare).forEach((row) => { list.appendChild(row); });

  const empty = ensureEmptyState(list);
  if (rows.length > 0 && visible.length === 0) {
    showEmptyState(empty, rawQuery, () => {
      if (filter) filter.value = "";
      if (status) status.value = "all";
      applyProjectList(controls);
    });
  } else {
    empty.hidden = true;
  }
}

/** Wires the controls to re-apply on change. */
export function wireProjectList(controls: ProjectListControls): void {
  const apply = (): void => applyProjectList(controls);
  controls.filter?.addEventListener("input", apply);
  controls.sort?.addEventListener("change", apply);
  controls.status?.addEventListener("change", apply);
}
