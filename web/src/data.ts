import type { AnalyticsBucket, AnalyticsOverview, AnalyticsPage, AnalyticsReferrer, AnalyticsSegmentBucket, DropoutStat, PageFlowResult, ScrollDepthResult, RawRecord } from "./types.js";

export function normalizeList<T extends RawRecord = RawRecord>(payload: unknown, key: string): T[] {
  if (!payload) {
    return [];
  }
  if (Array.isArray(payload)) {
    return payload.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload[key])) {
    return payload[key].map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.items)) {
    return payload.items.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.data)) {
    return payload.data.map((item) => normalizeObject<T>(item));
  }
  return [];
}

export function normalizeObject<T extends RawRecord = RawRecord>(value: unknown, key = ""): T {
  if (!isRecord(value)) {
    return { value } as unknown as T;
  }
  if (key && isRecord(value[key])) {
    return value[key] as T;
  }
  return value as T;
}

export function isRecord(value: unknown): value is RawRecord {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function readFirst(source: RawRecord, keys: string[]): unknown {
  for (const key of keys) {
    const value = source[key];
    if (value !== null && value !== undefined && value !== "") {
      return value;
    }
  }
  return "";
}

export function readString(source: RawRecord, keys: string[]): string {
  const value = readFirst(source, keys);
  return typeof value === "string" || typeof value === "number" ? String(value) : "";
}

export function readNumber(source: RawRecord, keys: string[]): number {
  const value = readFirst(source, keys);
  if (typeof value === "number") {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (!Number.isNaN(parsed)) {
      return parsed;
    }
  }
  return 0;
}

export function readRecord(source: RawRecord, keys: string[]): RawRecord {
  const value = readFirst(source, keys);
  return isRecord(value) ? value : {};
}

export function hasKeys(source: RawRecord): boolean {
  return Object.keys(source).length > 0;
}

export function collectKeyValues(source: RawRecord, omitKeys: string[] = []): RawRecord {
  const omit = new Set(omitKeys);
  return Object.entries(source || {}).reduce<RawRecord>((acc, [key, value]) => {
    if (omit.has(key) || value === null || value === undefined) {
      return acc;
    }
    acc[key] = value;
    return acc;
  }, {});
}

async function apiFetchJson(url: string, project: string): Promise<unknown> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (project && project !== "default" && project !== "__all") {
    headers["X-BugBarn-Project"] = project;
  }
  const response = await fetch(url, { credentials: "include", headers });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

export async function fetchAnalyticsOverview(project: string, start: string, end: string): Promise<AnalyticsOverview> {
  const qs = new URLSearchParams({ start, end }).toString();
  const data = await apiFetchJson(`/api/v1/analytics/overview?${qs}`, project);
  const r = isRecord(data) ? data : {};
  return {
    pageviews: typeof r["pageviews"] === "number" ? r["pageviews"] : 0,
    sessions: typeof r["sessions"] === "number" ? r["sessions"] : 0,
    pages: typeof r["pages"] === "number" ? r["pages"] : 0,
    avgDurationMs: typeof r["avgDurationMs"] === "number" ? r["avgDurationMs"] : 0,
  };
}

export async function fetchAnalyticsPages(project: string, start: string, end: string): Promise<AnalyticsPage[]> {
  const qs = new URLSearchParams({ start, end }).toString();
  const data = await apiFetchJson(`/api/v1/analytics/pages?${qs}`, project);
  return normalizeList<AnalyticsPage>(data, "pages");
}

export async function fetchAnalyticsTimeline(project: string, start: string, end: string): Promise<AnalyticsBucket[]> {
  const qs = new URLSearchParams({ start, end }).toString();
  const data = await apiFetchJson(`/api/v1/analytics/timeline?${qs}`, project);
  return normalizeList<AnalyticsBucket>(data, "buckets");
}

export async function fetchAnalyticsReferrers(project: string, start: string, end: string): Promise<AnalyticsReferrer[]> {
  const qs = new URLSearchParams({ start, end }).toString();
  const data = await apiFetchJson(`/api/v1/analytics/referrers?${qs}`, project);
  return normalizeList<AnalyticsReferrer>(data, "referrers");
}

export async function fetchAnalyticsSegments(project: string, start: string, end: string, dim: string): Promise<AnalyticsSegmentBucket[]> {
  const qs = new URLSearchParams({ start, end, dim }).toString();
  const data = await apiFetchJson(`/api/v1/analytics/segments?${qs}`, project);
  return normalizeList<AnalyticsSegmentBucket>(data, "buckets");
}

export async function fetchAnalyticsFlow(project: string, start: string, end: string, pathname: string): Promise<PageFlowResult> {
  const qs = new URLSearchParams({ start, end, pathname }).toString();
  const raw = await apiFetchJson(`/api/v1/analytics/flow?${qs}`, project);
  const data = isRecord(raw) ? raw : {};
  const cameFrom = Array.isArray(data["cameFrom"]) ? (data["cameFrom"] as RawRecord[]).map((e) => ({ pathname: String(e["pathname"] ?? ""), count: Number(e["count"] ?? 0), pct: Number(e["pct"] ?? 0) })) : [];
  const wentTo = Array.isArray(data["wentTo"]) ? (data["wentTo"] as RawRecord[]).map((e) => ({ pathname: String(e["pathname"] ?? ""), count: Number(e["count"] ?? 0), pct: Number(e["pct"] ?? 0) })) : [];
  return { pathname: String(data["pathname"] ?? pathname), cameFrom, wentTo };
}

export async function fetchAnalyticsScroll(project: string, start: string, end: string, pathname: string): Promise<ScrollDepthResult> {
  const qs = new URLSearchParams({ start, end, pathname }).toString();
  const raw = await apiFetchJson(`/api/v1/analytics/scroll?${qs}`, project);
  const data = isRecord(raw) ? raw : {};
  const buckets = Array.isArray(data["buckets"]) ? (data["buckets"] as RawRecord[]).map((b) => ({ label: String(b["label"] ?? ""), count: Number(b["count"] ?? 0), pct: Number(b["pct"] ?? 0) })) : [];
  return { pathname: String(data["pathname"] ?? pathname), buckets };
}

export async function fetchAnalyticsDropout(project: string, start: string, end: string): Promise<DropoutStat[]> {
  const qs = new URLSearchParams({ start, end }).toString();
  const raw = await apiFetchJson(`/api/v1/analytics/dropout?${qs}`, project);
  const data = isRecord(raw) ? raw : {};
  const pages = Array.isArray(data["pages"]) ? data["pages"] as RawRecord[] : [];
  return pages.map((p) => ({ pathname: String(p["pathname"] ?? ""), pageviews: Number(p["pageviews"] ?? 0), bouncedSessions: Number(p["bouncedSessions"] ?? 0), bounceRate: Number(p["bounceRate"] ?? 0) }));
}
