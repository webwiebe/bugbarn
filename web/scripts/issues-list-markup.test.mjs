// Markup of the issues list, the empty state, the setup guide and the live list.
import "./support/dom-env.mjs";
import test from "node:test";
import assert from "node:assert/strict";
const { renderIssueListMarkup, renderEmptyIssues, renderSetupGuide, renderLiveListMarkup } = await import("../src/components/issues-list.ts");

const issue = (id, extra = {}) => ({ id, title: `Title ${id}`, exceptionType: "TypeError", status: "unresolved", event_count: 5, ...extra });
const parse = (html) => {
  const root = document.createElement("div");
  root.innerHTML = html;
  return root;
};

test("an error replaces the list with its message", () => {
  assert.match(renderIssueListMarkup([issue("a")], "", null, new Error("db down")), /Issues unavailable\. db down/);
});

test("no issues, or no match for the query, renders nothing", () => {
  assert.equal(renderIssueListMarkup([], "", null), "");
  assert.equal(renderIssueListMarkup([issue("a")], "zzz", null), "");
});

test("the query matches the id, title, exception type and fingerprint without regard to case", () => {
  const issues = [issue("a"), issue("b", { title: "Network timeout", exceptionType: "NetError" }), issue("c", { fingerprint: "FP-SECRET" })];
  const ids = (q) => [...parse(renderIssueListMarkup(issues, q, null)).querySelectorAll("[data-issue-id]")].map((b) => b.dataset.issueId);
  assert.deepEqual(ids("NETWORK"), ["b"]);
  assert.deepEqual(ids("neterror"), ["b"]);
  assert.deepEqual(ids("fp-secret"), ["c"]);
  assert.deepEqual(ids("  title a "), ["a"]);
});

test("rows carry status classes and chips, the selected row is active, long titles are cut", () => {
  const issues = [
    issue("r", { status: "regressed", project_slug: "web", severity: "error" }),
    issue("m", { status: "muted" }),
    issue("x", { status: "resolved", title: "T".repeat(400), severity: "warning" }),
  ];
  const root = parse(renderIssueListMarkup(issues, "", "m"));
  const rows = [...root.querySelectorAll(".issue-row")];
  assert.deepEqual(rows.map((r) => r.classList.contains("regressed")), [true, false, false]);
  assert.ok(rows[1].classList.contains("active") && rows[1].classList.contains("muted"));
  assert.match(rows[0].textContent, /Regressed/);
  assert.match(rows[0].textContent, /web/);
  assert.match(rows[2].textContent, /Resolved/);
  assert.equal(rows[2].querySelector(".issue-link").textContent.length, 301);
  assert.ok(rows[0].querySelector(".chip.bad"));
  assert.ok(rows[2].querySelector(".chip.warn"));
  assert.equal(root.querySelectorAll(".issue-section-divider").length, 1, "a divider follows the regressed block");
});

test("the count meter scales to the busiest issue and the sparkline draws one bar per hour", () => {
  const root = parse(renderIssueListMarkup([issue("a", { event_count: 100, hourly_counts: [0, 5, 10] }), issue("b", { event_count: 1 })], "", null));
  const widths = [...root.querySelectorAll(".count-meter-fill")].map((el) => el.getAttribute("style"));
  assert.deepEqual(widths, ["width:100%", "width:4%"]);
  const bars = root.querySelectorAll(".issue-row")[0].querySelectorAll(".spark-bar");
  assert.equal(bars.length, 3);
  assert.equal(root.querySelectorAll(".issue-row")[0].querySelectorAll(".spark-bar--active").length, 2);
  assert.ok(root.querySelectorAll(".issue-row")[1].querySelector(".sparkline--empty"));
});

test("the empty state embeds the setup guide with the endpoint and SDK snippets", () => {
  const guide = renderSetupGuide();
  const html = renderEmptyIssues(guide);
  assert.match(html, /No issues yet/);
  assert.match(html, /https:\/\/bugbarn\.test\/api\/v1\/events/);
  for (const heading of ["API key", "SDK package", "TypeScript", "Python", "Smoke test"]) assert.match(html, new RegExp(`<h3>${heading}</h3>`));
});

test("the live list renders an error, an empty state, rows and release dividers between events", () => {
  assert.match(renderLiveListMarkup([], new Error("x")), /Live stream unavailable/);
  assert.match(renderLiveListMarkup([], null), /No live events yet/);
  const now = Date.now();
  const events = [
    { id: "e2", title: "Second", severityText: "fatal", timestamp: new Date(now - 60_000).toISOString() },
    { id: "e1", title: "First", severityText: "warning", timestamp: new Date(now - 3_600_000).toISOString() },
  ];
  const releases = [{ name: "v2", environment: "prod", observedAt: new Date(now - 1_800_000).toISOString() }, { name: "ancient", observedAt: "not a date" }];
  const root = parse(renderLiveListMarkup(events, null, releases));
  assert.deepEqual([...root.children].map((el) => el.className.split(" ")[0]), ["item", "release-divider", "item"]);
  assert.match(root.querySelector(".release-divider").textContent, /v2.*prod.*ago/s);
  assert.ok(root.querySelector(".chip.bad"), "fatal reads as bad");
  assert.ok(root.querySelector(".chip.warn"), "warning reads as warn");
  assert.match(root.textContent, /No issue/);
});
