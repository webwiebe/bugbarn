import type { AnalyticsBucket, AnalyticsOverview, AnalyticsPage, AnalyticsReferrer, AnalyticsSegmentBucket, DropoutStat, PageFlowResult, RawRecord, ScrollDepthResult } from "./types.js";
import { isRecord, normalizeList } from "./records.js";

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
