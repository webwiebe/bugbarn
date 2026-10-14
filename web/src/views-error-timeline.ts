// Loading and display state for the error timeline on the issues page.
import { bindErrorTimeline, renderErrorTimeline, TIMELINE_RANGES, type TimelineRange } from "./components/error-timeline.js";
import type { TimelineData } from "./timeline-stack.js";
import { fetchJson } from "./http.js";

const RANGE_KEY = "bugbarn.errorTimelineRange";
const REFRESH_AFTER_MS = 30_000;

let range: TimelineRange = readStoredRange();
let data: TimelineData | null = null;
let failed = false;
let loadedAt = 0;
let generation = 0;

function readStoredRange(): TimelineRange {
  try {
    const stored = localStorage.getItem(RANGE_KEY);
    return TIMELINE_RANGES.find((r) => r === stored) ?? "24h";
  } catch {
    return "24h";
  }
}

function storeRange(next: TimelineRange): void {
  try {
    localStorage.setItem(RANGE_KEY, next);
  } catch {
    // The range just resets on the next visit.
  }
}

/** Draws the chart from what is cached into the slot on the issues page. */
export function renderErrorTimelineSlot(): void {
  const slot = document.getElementById("error-timeline-slot");
  if (!slot) return;
  slot.innerHTML = renderErrorTimeline(data, failed, range);
  bindErrorTimeline(slot, setRange);
}

function setRange(next: TimelineRange): void {
  if (next === range) return;
  range = next;
  storeRange(next);
  data = null;
  failed = false;
  renderErrorTimelineSlot();
  void loadErrorTimeline(true);
}

/** Fetches the chart data across all projects, skipping a recent load of the same range. */
export async function loadErrorTimeline(force = false): Promise<void> {
  if (!force && data?.range === range && Date.now() - loadedAt < REFRESH_AFTER_MS) return;
  const mine = ++generation;
  const wanted = range;
  try {
    const payload = await fetchJson(`/api/v1/events/timeline?range=${wanted}`, false, true);
    if (mine !== generation) return;
    data = payload as TimelineData;
    failed = false;
    loadedAt = Date.now();
  } catch {
    if (mine !== generation) return;
    data = null;
    failed = true;
  }
  renderErrorTimelineSlot();
}
