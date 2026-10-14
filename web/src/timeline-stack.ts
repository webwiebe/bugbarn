// Stacking for the error timeline chart. No imports, so the unit test can load
// it straight from source.

export interface TimelineBucket {
  start: string;
  counts: Record<string, number>;
}

export interface TimelineProject {
  slug: string;
  name: string;
  total: number;
}

export interface TimelineData {
  range: string;
  bucket_seconds: number;
  buckets: TimelineBucket[];
  projects: TimelineProject[];
}

export interface Series {
  key: string;
  name: string;
  total: number;
  color: string;
  isOther: boolean;
}

export interface Segment {
  key: string;
  name: string;
  count: number;
}

export interface Bar {
  start: string;
  total: number;
  segments: Segment[];
}

export interface StackedTimeline {
  series: Series[];
  bars: Bar[];
  max: number;
}

/** Projects beyond this many are folded into one "other" series. */
export const MAX_SERIES = 8;
export const OTHER_KEY = "__other";

/** A colour derived from the slug, so a project keeps it between reloads. */
export function projectColor(slug: string): string {
  let hash = 2166136261;
  for (let i = 0; i < slug.length; i++) {
    hash ^= slug.charCodeAt(i);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  return `hsl(${hash % 360}, 62%, 52%)`;
}

/**
 * Turns the API answer into series and stacked bars. `projects` arrives sorted
 * by total, so the first MAX_SERIES keep their own series (largest at the
 * bottom of each bar) and the rest are summed into "other".
 */
export function stackTimeline(data: TimelineData, maxSeries: number = MAX_SERIES): StackedTimeline {
  const own = data.projects.slice(0, maxSeries);
  const rest = data.projects.slice(maxSeries);
  const ownKeys = new Set(own.map((p) => p.slug));

  const series: Series[] = own.map((p) => ({
    key: p.slug,
    name: p.name || p.slug,
    total: p.total,
    color: projectColor(p.slug),
    isOther: false,
  }));
  if (rest.length > 0) {
    series.push({
      key: OTHER_KEY,
      name: `Other (${rest.length})`,
      total: rest.reduce((sum, p) => sum + p.total, 0),
      color: "hsl(220, 8%, 55%)",
      isOther: true,
    });
  }

  let max = 0;
  const bars: Bar[] = data.buckets.map((bucket) => {
    const counts = bucket.counts ?? {};
    let otherCount = 0;
    for (const [slug, count] of Object.entries(counts)) {
      if (!ownKeys.has(slug)) otherCount += count;
    }
    const segments: Segment[] = [];
    for (const s of series) {
      const count = s.isOther ? otherCount : (counts[s.key] ?? 0);
      if (count > 0) segments.push({ key: s.key, name: s.name, count });
    }
    const total = segments.reduce((sum, seg) => sum + seg.count, 0);
    if (total > max) max = total;
    return { start: bucket.start, total, segments };
  });

  return { series, bars, max };
}
