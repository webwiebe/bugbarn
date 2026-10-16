// Rows of the "Releases" block on the issue page. Pure data so a node test can
// load it without the DOM helpers.

export interface IssueRelease extends Record<string, unknown> {
  release: string;
  events: number;
  first_seen: string;
  last_seen: string;
}

export interface ReleaseRow {
  release: string;
  events: number;
  /** Share of the sampled events, 0 to 100, rounded to a whole number. */
  percent: number;
  firstSeen: string;
  lastSeen: string;
}

export interface ReleaseRows {
  rows: ReleaseRow[];
  /** Events in the sample that carry no release. */
  withoutRelease: number;
  sampled: number;
}

/** Turns the releases endpoint response into display rows, newest release first. */
export function releaseRows(releases: IssueRelease[], sampled: number): ReleaseRows {
  const listed = releases.reduce((sum, r) => sum + r.events, 0);
  const rows = releases.map((r) => ({
    release: r.release,
    events: r.events,
    percent: sampled > 0 ? Math.round((r.events / sampled) * 100) : 0,
    firstSeen: r.first_seen,
    lastSeen: r.last_seen,
  }));
  return { rows, withoutRelease: Math.max(sampled - listed, 0), sampled };
}
