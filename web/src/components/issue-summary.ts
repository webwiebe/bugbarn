import type { IssueCounts, IssueStatus } from "../types.js";

interface CountItem {
  status: IssueStatus;
  label: string;
  key: keyof IssueCounts;
}

const STRIP_ITEMS: CountItem[] = [
  { status: "open", label: "Open", key: "open" },
  { status: "resolved", label: "Resolved", key: "resolved" },
  { status: "regressed", label: "Regressed", key: "regressed" },
  { status: "muted", label: "Muted", key: "muted" },
];

const TAB_ITEMS: CountItem[] = [
  { status: "all", label: "All", key: "all" },
  { status: "open", label: "Open", key: "open" },
  { status: "resolved", label: "Resolved", key: "resolved" },
  { status: "muted", label: "Muted", key: "muted" },
];

function formatCount(n: number): string {
  return n.toLocaleString("en-US");
}

function noun(n: number): string {
  return `issue${n === 1 ? "" : "s"}`;
}

/** "Showing 50 of 1,312 issues" while more exist, "1,312 issues" once all are loaded. */
export function issueHeading(loaded: number, total: number | null, approximate: boolean, hasMore: boolean): string {
  if (approximate) {
    return hasMore ? `${formatCount(loaded)}+ issues` : `${formatCount(loaded)} ${noun(loaded)}`;
  }
  if (total === null || total <= loaded) {
    return `${formatCount(loaded)} ${noun(loaded)}`;
  }
  return `Showing ${formatCount(loaded)} of ${formatCount(total)} ${noun(total)}`;
}

/** Open, resolved, regressed and muted at a glance; each number applies its filter. */
export function renderIssueSummaryStrip(counts: IssueCounts | null, active: IssueStatus): string {
  if (!counts) return "";
  const cells = STRIP_ITEMS.map(
    ({ status, label, key }) =>
      `<button type="button" class="issue-summary-cell ${status}${active === status ? " active" : ""}" data-status="${status}">` +
      `<span class="issue-summary-count">${formatCount(counts[key])}</span>` +
      `<span class="issue-summary-label">${label}</span></button>`,
  );
  return `<div class="issue-summary-strip" aria-label="Issue totals">${cells.join("")}</div>`;
}

/** The status tabs, each carrying its cached count when one is known. */
export function renderIssueStatusTabs(counts: IssueCounts | null, active: IssueStatus): string {
  return TAB_ITEMS.map(({ status, label, key }) => {
    const count = counts ? ` <span class="tab-count">${formatCount(counts[key])}</span>` : "";
    return `<button class="tab${active === status ? " active" : ""}" data-status="${status}" role="tab">${label}${count}</button>`;
  }).join("");
}
