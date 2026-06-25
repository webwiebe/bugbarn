import { hasKeys, isRecord, readFirst, readString } from "../data.js";
import { eventTimestamp, eventTitle, firstIdentifier } from "../domain.js";
import { escapeAttr, escapeHtml, formatTime } from "../format.js";
import type { ApiEvent, RawRecord } from "../types.js";

export function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}

export function renderField(label: string, name: string, type: "text" | "number" | "url" | "email" | "datetime-local" = "text", value = ""): string {
  return `
    <label class="field">
      <span>${escapeHtml(label)}</span>
      <input name="${escapeAttr(name)}" type="${type}" value="${escapeAttr(value)}" />
    </label>
  `;
}

export function renderEmptySection(title: string, message: string): string {
  return `
    <div class="section">
      <h3>${escapeHtml(title)}</h3>
      <div class="empty">
        <p>${escapeHtml(message)}</p>
      </div>
    </div>
  `;
}

export function renderDataSection(title: string, data: RawRecord): string {
  if (!hasKeys(data)) {
    return "";
  }
  return `
    <div class="section">
      <h3>${escapeHtml(title)}</h3>
      ${renderRecord(data)}
    </div>
  `;
}

export function renderRecord(data: RawRecord, emptyMessage = "No data returned."): string {
  const entries = Object.entries(data).filter(([, value]) => value !== null && value !== undefined && value !== "");
  if (!entries.length) {
    return `<div class="empty">${escapeHtml(emptyMessage)}</div>`;
  }
  return `
    <div class="grid">
      ${entries
        .map(([key, value]) => {
          const rendered = isRecord(value) || Array.isArray(value) ? `<pre class="pre compact">${escapeHtml(JSON.stringify(value, null, 2))}</pre>` : `<span>${escapeHtml(String(value))}</span>`;
          return `<div class="kv"><span>${escapeHtml(key)}</span>${rendered}</div>`;
        })
        .join("")}
    </div>
  `;
}

export function renderStacktrace(stacktrace: unknown[]): string {
  if (!stacktrace.length) {
    return "";
  }
  return `
    <div class="section">
      <h3>Stacktrace</h3>
      <div class="stacktrace">
        ${stacktrace
          .map((frame, index) => renderFrame(frame, index))
          .join("")}
      </div>
    </div>
  `;
}

export function renderFrame(frame: unknown, index: number): string {
  if (!isRecord(frame)) {
    return `<div class="frame"><span>#${index + 1}</span><code>${escapeHtml(String(frame))}</code></div>`;
  }

  const fn = readString(frame, ["function", "Function", "name", "Name"]) || "<anonymous>";
  const file = readString(frame, ["file", "File", "filename", "Filename", "path", "Path"]);
  const line = readString(frame, ["line", "Line", "lineno", "Lineno"]);
  const column = readString(frame, ["column", "Column", "colno", "Colno"]);
  const location = [file, line ? `:${line}` : "", column ? `:${column}` : ""].join("");
  const snippet = readFrameSnippet(frame);

  // Original (symbolicated) position fields — try all common naming variants
  const origFn = readString(frame, ["originalFunction", "original_function", "OriginalFunction"]);
  const origFile = readString(frame, ["originalFile", "original_file", "OriginalFile"]);
  const origLine = readString(frame, ["originalLine", "original_line", "OriginalLine"]);
  const origColumn = readString(frame, ["originalColumn", "original_column", "OriginalColumn"]);

  const hasOriginal = Boolean(origFn ?? origFile ?? origLine);
  const displayFn = origFn ?? fn;
  const origLocation = origFile
    ? [origFile, origLine ? `:${origLine}` : "", origColumn ? `:${origColumn}` : ""].join("")
    : "";

  return `
    <article class="frame${hasOriginal ? " symbolicated" : ""}">
      <span>#${index + 1}</span>
      <div class="frame-body">
        <div class="frame-head">
          <code>${escapeHtml(displayFn)}</code>
          ${hasOriginal && origLocation
            ? `<small style="color:var(--accent)">${escapeHtml(origLocation)}</small>`
            : ""}
          <small style="${hasOriginal ? "color:var(--muted);font-size:11px;opacity:0.7" : ""}">${escapeHtml(location || "unknown source")}</small>
        </div>
        ${snippet ? `<pre class="frame-snippet">${escapeHtml(snippet)}</pre>` : ""}
      </div>
    </article>
  `;
}

export function readFrameSnippet(frame: RawRecord): string {
  const direct = readFirst(frame, [
    "snippet",
    "Snippet",
    "sourceSnippet",
    "source_snippet",
    "contextLine",
    "context_line",
    "lineText",
    "line_text",
  ]);
  if (typeof direct === "string" && direct.trim()) {
    return direct;
  }
  if (Array.isArray(direct)) {
    return direct.map((line) => String(line)).join("\n");
  }

  const source = readFirst(frame, ["source", "Source", "code", "Code"]);
  if (typeof source === "string" && source.trim()) {
    return source;
  }

  const pre = toLines(readFirst(frame, ["preContext", "PreContext", "pre_context"]));
  const post = toLines(readFirst(frame, ["postContext", "PostContext", "post_context"]));
  const context = readString(frame, ["contextLine", "context_line", "ContextLine"]);
  const lines = [...pre, ...(context ? [context] : []), ...post];
  return lines.join("\n");
}

export function toLines(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.map((line) => String(line)).filter(Boolean);
}

export function renderEventNavigation(events: ApiEvent[], activeId: string): string {
  if (!events.length || !activeId) {
    return "";
  }
  const index = events.findIndex((event) => firstIdentifier(event) === activeId);
  if (index < 0) {
    return "";
  }
  const previousId = index > 0 ? firstIdentifier(events[index - 1]) : "";
  const nextId = index < events.length - 1 ? firstIdentifier(events[index + 1]) : "";
  return `
    <button type="button" data-event-id="${escapeAttr(previousId)}" ${previousId ? "" : "disabled"}>Previous event</button>
    <button type="button" data-event-id="${escapeAttr(nextId)}" ${nextId ? "" : "disabled"}>Next event</button>
  `;
}

export function renderEventButtons(events: ApiEvent[], activeId = "", className = ""): string {
  if (!events.length) {
    return `<div class="empty">No events returned.</div>`;
  }

  return `
    <div class="event-list${className ? ` ${className}` : ""}">
      ${events
        .map((event) => {
          const id = firstIdentifier(event);
          const title = eventTitle(event);
          const timestamp = formatTime(eventTimestamp(event));
          const active = activeId && String(activeId) === String(id) ? "active" : "";
          return `
            <button class="item ${active}" type="button" data-event-id="${escapeAttr(id)}">
              <div class="item-title">${escapeHtml(title)}</div>
              <div class="item-meta">
                <span>${escapeHtml(String(id || "n/a"))}</span>
                <span>${escapeHtml(timestamp || "No timestamp")}</span>
              </div>
            </button>
          `;
        })
        .join("")}
    </div>
  `;
}
