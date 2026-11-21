// Option lists for every <select>. Options sort alphabetically by label,
// case-insensitive and in the UI language, with the leading "All ..." / "None"
// style entries kept first. A list whose options have a natural order (time
// ranges, severity) renders with `optionsHtml` unsorted and a comment naming
// that order at the call site.

import { escapeAttr, escapeHtml } from "../format.js";

export interface SelectOption {
  value: string;
  label: string;
}

const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });

/** Returns the options sorted by label; the first `pinned` options stay in front, in order. */
export function sortOptions(options: readonly SelectOption[], pinned = 0): SelectOption[] {
  return [...options.slice(0, pinned), ...options.slice(pinned).sort((a, b) => collator.compare(a.label, b.label))];
}

/** Renders option elements; the one whose value equals `selected` is marked selected. */
export function optionsHtml(options: readonly SelectOption[], selected?: string): string {
  return options
    .map((o) => `<option value="${escapeAttr(o.value)}"${o.value === selected ? " selected" : ""}>${escapeHtml(o.label)}</option>`)
    .join("");
}

/** Sorted options in one step: the common case for a select with no natural order. */
export function sortedOptionsHtml(options: readonly SelectOption[], selected?: string, pinned = 0): string {
  return optionsHtml(sortOptions(options, pinned), selected);
}
