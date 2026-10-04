import { escapeAttr, escapeHtml } from "../format.js";

// A small dependency-free SVG line chart, shared by the analytics timeline
// and the host metric charts.

export interface LineChartOptions {
  ariaLabel: string;
  /** Label under the left edge, usually the first timestamp. */
  startLabel?: string;
  /** Label under the right edge, usually the last timestamp. */
  endLabel?: string;
  height?: number;
  /** Fixed top of the y axis (e.g. 100 for percentages); defaults to the data max. */
  maxValue?: number;
  /** X positions (e.g. unix ms). When given, points are spaced by x, so gaps show as gaps in time. */
  xs?: number[];
}

const chartWidth = 800;
const chartPad = 8;

function xPositions(count: number, xs: number[] | undefined): number[] {
  if (xs && xs.length === count && count > 1) {
    const min = xs[0];
    const span = Math.max(xs[count - 1] - min, 1);
    return xs.map(x => Math.round(((x - min) / span) * chartWidth));
  }
  const step = chartWidth / Math.max(count - 1, 1);
  return Array.from({ length: count }, (_, i) => Math.round(i * step));
}

/** renderLineChart draws values as a polyline with light vertical grid lines.
 * It returns an empty string for no data; callers show their own empty state. */
export function renderLineChart(values: number[], opts: LineChartOptions): string {
  if (!values.length) return "";
  const h = opts.height ?? 120;
  const innerH = h - chartPad * 2;
  const max = Math.max(opts.maxValue ?? 0, ...values, Number.EPSILON);
  const xs = xPositions(values.length, opts.xs);

  const points = values
    .map((v, i) => `${xs[i]},${Math.round(chartPad + innerH - (Math.max(v, 0) / max) * innerH)}`)
    .join(" ");

  // Grid lines: at every point when there are few, else roughly every seventh.
  const gridInterval = values.length > 14 ? Math.ceil(values.length / 7) : 1;
  const gridLines = xs
    .filter((_, i) => i % gridInterval === 0)
    .map(x => `<line x1="${x}" y1="${chartPad}" x2="${x}" y2="${h - chartPad}" stroke="var(--line,#21262d)" stroke-width="1"/>`)
    .join("");

  return `
    <div class="line-chart" style="position:relative">
      <svg viewBox="0 0 ${chartWidth} ${h}" width="100%" height="${h}" preserveAspectRatio="none"
        aria-label="${escapeAttr(opts.ariaLabel)}" role="img" style="display:block;overflow:visible">
        ${gridLines}
        <polyline points="${escapeAttr(points)}" fill="none" stroke="#d4a054" stroke-width="2"
          stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke"/>
      </svg>
      <div class="line-chart-axis">
        <span>${escapeHtml(opts.startLabel ?? "")}</span>
        <span>${escapeHtml(opts.endLabel ?? "")}</span>
      </div>
    </div>`;
}
