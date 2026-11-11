// Filter state of the Logs view and its URL form. Pure data so a node test can
// load it without the DOM: the hash `#/logs?range=24h&level=error&attr=k=v` is
// the single source of truth, which is what makes a filtered view shareable.

export type LogRange = "1h" | "24h" | "7d";

export const logRanges: LogRange[] = ["1h", "24h", "7d"];

const rangeMs: Record<LogRange, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
};

export interface LogFilters {
  range: LogRange;
  /** from/to narrow the range to a clicked histogram bucket (RFC 3339). */
  from: string;
  to: string;
  level: string;
  q: string;
  /** Attribute filters as "key=value", all must match. */
  attrs: string[];
  /** The attribute key whose top values the facet panel shows. */
  facetKey: string;
}

export function defaultLogFilters(): LogFilters {
  return { range: "24h", from: "", to: "", level: "", q: "", attrs: [], facetKey: "" };
}

function isRange(value: string | null): value is LogRange {
  return value !== null && (logRanges as string[]).includes(value);
}

/** parseLogFilters reads the filters out of a location hash. Unknown or broken
 * values fall back to the defaults. */
export function parseLogFilters(hash: string): LogFilters {
  const filters = defaultLogFilters();
  const queryStart = hash.indexOf("?");
  if (queryStart < 0) return filters;
  const params = new URLSearchParams(hash.slice(queryStart + 1));
  const range = params.get("range");
  if (isRange(range)) filters.range = range;
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  if (!Number.isNaN(Date.parse(from)) && !Number.isNaN(Date.parse(to)) && Date.parse(from) < Date.parse(to)) {
    filters.from = from;
    filters.to = to;
  }
  filters.level = params.get("level") ?? "";
  filters.q = params.get("q") ?? "";
  filters.attrs = params.getAll("attr").filter((a) => a.indexOf("=") > 0);
  filters.facetKey = params.get("facet") ?? "";
  return filters;
}

/** logFiltersHash is the inverse of parseLogFilters. Default values are left
 * out so the plain Logs link stays `#/logs`. */
export function logFiltersHash(filters: LogFilters): string {
  const params = new URLSearchParams();
  if (filters.range !== "24h") params.set("range", filters.range);
  if (filters.from && filters.to) {
    params.set("from", filters.from);
    params.set("to", filters.to);
  }
  if (filters.level) params.set("level", filters.level);
  if (filters.q) params.set("q", filters.q);
  for (const attr of filters.attrs) params.append("attr", attr);
  if (filters.facetKey) params.set("facet", filters.facetKey);
  const qs = params.toString();
  return qs ? `#/logs?${qs}` : "#/logs";
}

export interface LogWindow {
  from: string;
  to: string;
}

/** logWindow is the time range every request of the view uses: the clicked
 * bucket when there is one, else the last range ending now. */
export function logWindow(filters: LogFilters, nowMs: number): LogWindow {
  if (filters.from && filters.to) return { from: filters.from, to: filters.to };
  return {
    from: new Date(nowMs - rangeMs[filters.range]).toISOString(),
    to: new Date(nowMs + 1000).toISOString(),
  };
}

/** logQueryParams are the query parameters shared by the list, histogram and
 * facets endpoints. */
export function logQueryParams(filters: LogFilters, nowMs: number): URLSearchParams {
  const params = new URLSearchParams();
  const win = logWindow(filters, nowMs);
  params.set("from", win.from);
  params.set("to", win.to);
  if (filters.level) params.set("level", filters.level);
  if (filters.q) params.set("q", filters.q);
  for (const attr of filters.attrs) params.append("attr", attr);
  return params;
}

/** withAttr adds a key=value filter once; a repeat click changes nothing. */
export function withAttr(filters: LogFilters, key: string, value: string): LogFilters {
  const attr = `${key}=${value}`;
  if (filters.attrs.includes(attr)) return filters;
  return { ...filters, attrs: [...filters.attrs, attr] };
}

export function withoutAttr(filters: LogFilters, attr: string): LogFilters {
  return { ...filters, attrs: filters.attrs.filter((a) => a !== attr) };
}

/** liveCompatible is true when the SSE stream can be filtered client side to
 * the same rows the list shows: it carries level and text only. */
export function liveCompatible(filters: LogFilters): boolean {
  return filters.attrs.length === 0 && !(filters.from && filters.to);
}

/** splitAttr splits "key=value" at the first "=". */
export function splitAttr(attr: string): { key: string; value: string } {
  const at = attr.indexOf("=");
  return at < 0 ? { key: attr, value: "" } : { key: attr.slice(0, at), value: attr.slice(at + 1) };
}
