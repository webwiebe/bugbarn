// Unit tests for the SPA's pure helpers: formatting, record readers, event and
// issue field accessors, the line chart and the key request list.
import "./support/dom-env.mjs";
import test from "node:test";
import assert from "node:assert/strict";
const { errorMessage, escapeAttr, escapeHtml, formatAge, formatTime } = await import("../src/format.ts");
const { collectKeyValues, hasKeys, isRecord, normalizeList, normalizeObject, readFirst, readNumber, readRecord, readString } =
  await import("../src/records.ts");
const domain = await import("../src/domain.ts");
const { renderLineChart } = await import("../src/components/chart.ts");
const { renderKeyRequests } = await import("../src/components/key-requests.ts");
const { isStale } = await import("../src/telemetry-api.ts");

const minutesAgo = (n) => new Date(Date.now() - n * 60000).toISOString();

test("formatAge picks minutes, hours, days and months", () => {
  assert.equal(formatAge(""), "");
  assert.equal(formatAge(null), "");
  assert.equal(formatAge("garbage"), "garbage");
  assert.equal(formatAge(new Date(Date.now() + 60000).toISOString()), "now");
  assert.equal(formatAge(minutesAgo(0)), "1m");
  assert.equal(formatAge(minutesAgo(59)), "59m");
  assert.equal(formatAge(minutesAgo(180)), "3h");
  assert.equal(formatAge(minutesAgo(60 * 24 * 5)), "5d");
  assert.equal(formatAge(minutesAgo(60 * 24 * 95)), "3mo");
});

test("formatTime formats valid dates and passes garbage through", () => {
  assert.equal(formatTime(undefined), "");
  assert.equal(formatTime("nope"), "nope");
  assert.match(formatTime("2026-01-02T03:04:05Z"), /2026/);
});

test("escapeHtml and escapeAttr neutralise markup, quotes and backticks", () => {
  assert.equal(escapeHtml(`<a href="x">&'`), "&lt;a href=&quot;x&quot;&gt;&amp;&#39;");
  assert.equal(escapeAttr("a`b"), "a&#96;b");
});

test("errorMessage reads Error messages and stringifies the rest", () => {
  assert.equal(errorMessage(new Error("boom")), "boom");
  assert.equal(errorMessage(42), "42");
});

test("normalizeList accepts arrays, keyed, items and data envelopes, and nothing", () => {
  assert.deepEqual(normalizeList([{ a: 1 }], "x"), [{ a: 1 }]);
  assert.deepEqual(normalizeList({ x: [{ a: 1 }] }, "x"), [{ a: 1 }]);
  assert.deepEqual(normalizeList({ items: [{ a: 2 }] }, "x"), [{ a: 2 }]);
  assert.deepEqual(normalizeList({ data: [{ a: 3 }] }, "x"), [{ a: 3 }]);
  assert.deepEqual(normalizeList({ other: 1 }, "x"), []);
  assert.deepEqual(normalizeList(null, "x"), []);
  assert.deepEqual(normalizeList(["s"], "x"), [{ value: "s" }]);
});

test("normalizeObject unwraps a key and wraps scalars", () => {
  assert.deepEqual(normalizeObject({ issue: { id: 1 } }, "issue"), { id: 1 });
  assert.deepEqual(normalizeObject({ id: 1 }, "issue"), { id: 1 });
  assert.deepEqual(normalizeObject(5), { value: 5 });
  assert.equal(isRecord([]), false);
  assert.equal(isRecord(null), false);
});

test("the read helpers take the first non-empty key and coerce by type", () => {
  const src = { a: "", b: null, c: "x", n: "12", m: " ", r: { k: 1 }, num: 4 };
  assert.equal(readFirst(src, ["a", "b", "c"]), "x");
  assert.equal(readFirst(src, ["a", "b"]), "");
  assert.equal(readString(src, ["num"]), "4");
  assert.equal(readString(src, ["r"]), "");
  assert.equal(readNumber(src, ["n"]), 12);
  assert.equal(readNumber(src, ["m"]), 0);
  assert.equal(readNumber(src, ["c"]), 0);
  assert.equal(readNumber(src, ["num"]), 4);
  assert.deepEqual(readRecord(src, ["r"]), { k: 1 });
  assert.deepEqual(readRecord(src, ["c"]), {});
  assert.equal(hasKeys({}), false);
  assert.equal(hasKeys({ a: 1 }), true);
  assert.deepEqual(collectKeyValues({ a: 1, b: null, c: 3, d: undefined }, ["c"]), { a: 1 });
});

test("firstIdentifier reads id keys, nested records and nested issue/event objects", () => {
  assert.equal(domain.firstIdentifier({ id: 7 }), "7");
  assert.equal(domain.firstIdentifier({ ID: { id: "n" } }), "n");
  assert.equal(domain.firstIdentifier({ id: "a", ID: "b" }, ["id"]), "b");
  assert.equal(domain.firstIdentifier({}), "");
  assert.equal(domain.firstIdentifier({ issue: { id: "deep" } }), "deep");
  assert.equal(domain.firstIdentifier({ id: "", Event: { event_id: "ev" } }), "ev");
  assert.equal(domain.firstIdentifier({ id: { id: "" }, issue: { IssueID: "x" } }), "x");
});

test("issue accessors read every spelling and default sensibly", () => {
  assert.equal(domain.issueTitle({}), "Untitled issue");
  assert.equal(domain.issueTitle({ NormalizedTitle: "T" }), "T");
  assert.equal(domain.issueNormalizedTitle({ normalized_title: "n" }), "n");
  assert.equal(domain.issueExceptionType({ exception_type: "E" }), "E");
  assert.equal(domain.issueFingerprint({ Fingerprint: "f" }), "f");
  assert.equal(domain.issueStatus({}), "unresolved");
  assert.equal(domain.issueStatus({ Status: "muted" }), "muted");
  assert.equal(domain.issueLastSeen({ LastSeen: 5 }), 5);
  assert.equal(domain.issueFirstSeen({ first_seen: 6 }), 6);
  assert.equal(domain.issueEventCount({ count: 3 }), 3);
  assert.equal(domain.issueEventCount({}, 9), 9);
  assert.equal(domain.issueSampleRate({ sample_rate: 100 }), 100);
  assert.equal(domain.issueSampleRate({}), 1);
});

test("issueFingerprintMaterial reads a record, a JSON string, and returns {} otherwise", () => {
  assert.deepEqual(domain.issueFingerprintMaterial({ fingerprintMaterial: { a: 1 } }), { a: 1 });
  assert.deepEqual(domain.issueFingerprintMaterial({ fingerprint_debug: '{"b":2}' }), { b: 2 });
  assert.deepEqual(domain.issueFingerprintMaterial({ fingerprint_debug: "not json" }), {});
  assert.deepEqual(domain.issueFingerprintMaterial({ fingerprint_debug: "[1]" }), {});
  assert.deepEqual(domain.issueFingerprintMaterial({}), {});
});

test("sampledChipMarkup renders only for sampled issues", () => {
  assert.equal(domain.sampledChipMarkup(1), "");
  assert.match(domain.sampledChipMarkup(10), /1 in 10/);
  assert.match(domain.sampledChipMarkup(10, "1rem"), /font-size:1rem/);
});

test("issueSeverity prefers the representative event and falls back to the issue", () => {
  assert.equal(domain.issueSeverity({ RepresentativeEvent: { severityText: "ERROR" } }), "ERROR");
  assert.equal(domain.issueSeverity({ representative_event: { Severity: "WARN" } }), "WARN");
  assert.equal(domain.issueSeverity({ RepresentativeEvent: {}, severity: "INFO" }), "INFO");
  assert.equal(domain.issueSeverity({}), "");
});

const rich = {
  id: "e1",
  timestamp: "2026-01-01T00:00:00Z",
  severity: "ERROR",
  payload: {
    ingestId: "ing",
    sdk_name: "bugbarn.js",
    trace_id: "t1",
    span_id: "s1",
    resource: { host: "h" },
    attributes: { url: "https://x.test/p" },
    exception: { type: "TypeError", message: "bad", stacktrace: [{ file: "a.js" }] },
    spans: [{ name: "s" }],
    rawScrubbed: { tags: { env: "prod" }, sender: { sdk: { name: "raw-sdk" } } },
  },
};

test("event accessors read the payload, the scrubbed raw payload and the exception", () => {
  assert.equal(domain.eventIssueId({ issue: { id: "i9" } }), "i9");
  assert.equal(domain.eventIssueId({ issue_id: "i1" }), "i1");
  assert.equal(domain.eventTitle({ Body: "b" }), "b");
  assert.equal(domain.eventTitle({ exception: { message: "m" } }), "m");
  assert.equal(domain.eventTitle({}), "Event");
  assert.equal(domain.eventTimestamp(rich), "2026-01-01T00:00:00Z");
  assert.equal(domain.eventSeverity(rich), "ERROR");
  assert.equal(domain.eventTraceId(rich), "t1");
  assert.equal(domain.eventSpanId(rich), "s1");
  assert.equal(domain.eventTraceId({ payload: { rawScrubbed: { traceId: "r" } } }), "r");
  assert.equal(domain.eventSpanId({ payload: { rawScrubbed: { spanId: "r" } } }), "r");
  assert.equal(domain.eventUrl(rich), "https://x.test/p");
  assert.equal(domain.eventUrl({ payload: { rawScrubbed: { requestUrl: "https://r" } } }), "https://r");
  assert.equal(domain.eventUrl({ payload: { rawScrubbed: { request: { url: "https://q" } } } }), "https://q");
  assert.deepEqual(domain.eventRawScrubbed({ payload: { raw_scrubbed: { a: 1 } } }), { a: 1 });
  assert.equal(domain.eventException(rich).type, "TypeError");
  assert.equal(domain.eventException({ exception: { type: "Direct" }, payload: rich.payload }).type, "Direct");
});

test("eventContext collects the labelled facts and records", () => {
  const context = domain.eventContext(rich);
  assert.equal(context["Ingest id"], "ing");
  assert.equal(context["SDK"], "bugbarn.js");
  assert.equal(context["Trace id"], "t1");
  assert.equal(context["Exception type"], "TypeError");
  assert.equal(context["Exception message"], "bad");
  assert.deepEqual(context["Resource"], { host: "h" });
  assert.deepEqual(context["Attributes"], { url: "https://x.test/p" });
  assert.deepEqual(context["Tags"], { env: "prod" });
  assert.equal(domain.eventContext({ payload: { rawScrubbed: { sender: { sdk: { name: "only-raw" } } } } })["SDK"], "only-raw");
  assert.deepEqual(domain.eventContext({}), {});
});

test("eventStacktrace and eventSpans read payload, exception and raw sources", () => {
  assert.deepEqual(domain.eventStacktrace(rich), [{ file: "a.js" }]);
  assert.deepEqual(domain.eventStacktrace({ payload: { stacktrace: [1] } }), [1]);
  assert.deepEqual(domain.eventStacktrace({}), []);
  assert.deepEqual(domain.eventSpans(rich), [{ name: "s" }]);
  assert.deepEqual(domain.eventSpans({ payload: { rawScrubbed: { spans: [2] } } }), [2]);
  assert.deepEqual(domain.eventSpans({}), []);
});

test("renderLineChart returns nothing for no data and a polyline otherwise", () => {
  assert.equal(renderLineChart([], { ariaLabel: "x" }), "");
  const svg = renderLineChart([1, 2, 3], { ariaLabel: "cpu <chart>", startLabel: "a", endLabel: "b" });
  assert.match(svg, /<polyline points="0,\d+ 400,\d+ 800,\d+"/);
  assert.match(svg, /aria-label="cpu &lt;chart&gt;"/);
  assert.match(svg, /<span>a<\/span>/);
});

test("renderLineChart spaces points by x, thins the grid for long series and honours maxValue", () => {
  const timed = renderLineChart([1, 1, 1], { ariaLabel: "t", xs: [0, 100, 1000], maxValue: 100 });
  assert.match(timed, /points="0,\d+ 80,\d+ 800,\d+"/);
  const long = renderLineChart(Array.from({ length: 30 }, (_, i) => i), { ariaLabel: "l" });
  assert.equal(long.match(/<line /g).length, 6);
  assert.match(renderLineChart([5], { ariaLabel: "one" }), /points="0,/);
});

test("renderKeyRequests lists approve and deny buttons and says how long ago", () => {
  assert.equal(renderKeyRequests([]), "");
  const html = renderKeyRequests([
    { id: "k1", project: "web", label: "laptop <1>", createdAt: minutesAgo(0), expiresAt: "x" },
    { id: "k2", project: "web", label: "b", createdAt: minutesAgo(30), expiresAt: "x" },
    { id: "k3", project: "web", label: "c", createdAt: minutesAgo(180), expiresAt: "x" },
    { id: "k4", project: "web", label: "d", createdAt: "bad", expiresAt: "x" },
  ]);
  assert.match(html, /laptop &lt;1&gt;/);
  assert.match(html, /data-approve-key-request="k1"/);
  assert.match(html, /data-deny-key-request="k4"/);
  assert.match(html, /just now/);
  assert.match(html, /30 min ago/);
  assert.match(html, /3 h ago/);
});

test("isStale marks hosts silent for over five minutes and unparseable times", () => {
  const now = Date.parse("2026-01-01T00:10:00Z");
  assert.equal(isStale("2026-01-01T00:08:00Z", now), false);
  assert.equal(isStale("2026-01-01T00:04:00Z", now), true);
  assert.equal(isStale("garbage", now), true);
});
