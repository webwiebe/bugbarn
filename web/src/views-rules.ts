// Detection rules: the built-in rules with any stored overrides, plus custom
// rules. Each rule is edited as JSON, the same shape the API stores, so a new
// rule field needs no form change. Saving goes to the writer, which reloads
// the detection engine at once.
import { escapeAttr, escapeHtml, errorMessage } from "./format.js";
import { elements, setActiveView, setStatus, state } from "./core.js";
import { apiFetch } from "./http.js";
import { telemetryGet } from "./telemetry-api.js";

interface Rule {
  id: string;
  name: string;
  enabled: boolean;
  severity: string;
  track: string;
  metric?: string;
  op?: string;
  per_core?: boolean;
  threshold: number;
  window?: string;
  for?: string;
  cooldown?: string;
  group_by?: string[];
  builtin: boolean;
  overridden: boolean;
  updated_at?: string;
}

const rulesPath = "/api/v1/detection/rules";

const rv = {
  rules: [] as Rule[],
  error: "",
  editing: null as string | null, // rule id, or "" for a new rule
  draft: "",
  editError: "",
  saving: false,
};

const newRuleTemplate = {
  id: "my-rule",
  name: "Describe what this catches",
  enabled: true,
  severity: "WARNING",
  track: "security",
  match: [{ field: "source", op: "eq", value: "traefik" }, { field: "status", op: "in", value: [404] }],
  group_by: ["src_ip"],
  threshold: 300,
  window: "5m",
  cooldown: "1h",
};

export async function loadRules(): Promise<void> {
  try {
    const res = await telemetryGet<{ rules: Rule[] }>(rulesPath);
    rv.rules = res.rules ?? [];
    rv.error = "";
  } catch (error) {
    rv.rules = [];
    rv.error = errorMessage(error);
  }
  renderIfActive();
}

export function rulesStatus(): string {
  const on = rv.rules.filter(r => r.enabled).length;
  return `${rv.rules.length} detection rule${rv.rules.length === 1 ? "" : "s"}, ${on} enabled.`;
}

function renderIfActive(): void {
  if (state.currentRoute === "rules") renderRulesView();
}

/** ruleCondition summarises when a rule fires, in one line. */
export function ruleCondition(r: Rule): string {
  if (r.track === "heartbeat") return `no metrics for ${r.window ?? "?"}`;
  if (r.track === "metrics") {
    const limit = r.per_core ? `${r.threshold} × cores` : String(r.threshold);
    return `${r.metric ?? "?"} ${r.op === "lt" ? "<" : ">"} ${limit} for ${r.for ?? "0s"}`;
  }
  const per = r.group_by?.length ? ` per ${r.group_by.join(", ")}` : "";
  return `${r.threshold} matching lines${per} in ${r.window ?? "?"}`;
}

function origin(r: Rule): string {
  if (!r.builtin) return `<span class="chip">custom</span>`;
  return r.overridden ? `<span class="chip warn">built-in, changed</span>` : `<span class="chip">built-in</span>`;
}

function renderRow(r: Rule): string {
  const removeLabel = r.builtin ? "Reset" : "Delete";
  const remove = r.builtin && !r.overridden ? "" :
    `<button type="button" class="btn-sm" data-rule-remove="${escapeAttr(r.id)}">${removeLabel}</button>`;
  return `
    <tr class="${r.enabled ? "" : "muted"}">
      <td><strong>${escapeHtml(r.name)}</strong><div class="muted sec-mono">${escapeHtml(r.id)}</div></td>
      <td>${escapeHtml(r.track)}</td>
      <td>${escapeHtml(r.severity)}</td>
      <td>${escapeHtml(ruleCondition(r))}</td>
      <td>${r.enabled ? "on" : "off"}</td>
      <td>${origin(r)}</td>
      <td class="rule-actions">
        <button type="button" class="btn-sm" data-rule-toggle="${escapeAttr(r.id)}">${r.enabled ? "Disable" : "Enable"}</button>
        <button type="button" class="btn-sm" data-rule-edit="${escapeAttr(r.id)}">Edit</button>
        ${remove}
      </td>
    </tr>`;
}

function renderEditor(): string {
  if (rv.editing === null) return "";
  const title = rv.editing ? `Edit ${escapeHtml(rv.editing)}` : "New rule";
  const err = rv.editError ? `<div class="callout callout-error"><span>${escapeHtml(rv.editError)}</span></div>` : "";
  return `
    <div class="section">
      <h3>${title}</h3>
      ${err}
      <textarea id="rule-json" class="rule-json" rows="18" spellcheck="false">${escapeHtml(rv.draft)}</textarea>
      <div class="sec-form-actions">
        <button type="button" class="btn-sm" id="rule-save"${rv.saving ? " disabled" : ""}>Save</button>
        <button type="button" class="btn-sm" id="rule-cancel">Cancel</button>
        <span class="muted">Fields are listed in the API docs under Detection Rules.</span>
      </div>
    </div>`;
}

function renderList(): string {
  if (rv.error) return `<div class="callout callout-error"><span>${escapeHtml(rv.error)}</span></div>`;
  if (!rv.rules.length) return `<div class="empty">Loading rules…</div>`;
  return `
    <div class="table-scroll">
      <table class="volume-table">
        <thead><tr><th>Rule</th><th>Track</th><th>Severity</th><th>Fires when</th><th>State</th><th>Origin</th><th></th></tr></thead>
        <tbody>${rv.rules.map(renderRow).join("")}</tbody>
      </table>
    </div>`;
}

export function renderRulesView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Detection rules";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = `
    <div class="view-head">
      <h2>Detection rules</h2>
      <button type="button" class="btn-sm" id="rule-new">New rule</button>
    </div>
    <p class="muted">Each detection becomes an issue in the infrastructure project. Changes apply immediately and restart every rule's count.</p>
    ${renderEditor()}
    <div class="section">${renderList()}</div>`;
  wireRulesView();
}

/** editable strips the read-only fields the API adds to a listed rule. */
export function editable(r: Rule): Record<string, unknown> {
  const { builtin: _b, overridden: _o, updated_at: _u, ...rule } = r;
  return rule;
}

function startEdit(id: string): void {
  const rule = rv.rules.find(r => r.id === id);
  rv.editing = id;
  rv.draft = JSON.stringify(rule ? editable(rule) : newRuleTemplate, null, 2);
  rv.editError = "";
  renderRulesView();
}

async function putRule(rule: Record<string, unknown>): Promise<void> {
  const id = String(rule["id"] ?? "").trim();
  if (!id) throw new Error("The rule needs an id.");
  const res = await apiFetch(`${rulesPath}/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(rule) });
  if (!res.ok) throw new Error((await res.text().catch(() => "")).trim() || `${res.status} ${res.statusText}`);
}

async function saveDraft(): Promise<void> {
  const textarea = elements.overviewView.querySelector<HTMLTextAreaElement>("#rule-json");
  rv.draft = textarea?.value ?? rv.draft;
  rv.saving = true;
  try {
    await putRule(JSON.parse(rv.draft) as Record<string, unknown>);
    rv.editing = null;
    setStatus("Rule saved; detections use it now.");
    await loadRules();
  } catch (error) {
    rv.editError = errorMessage(error);
    renderRulesView();
  } finally {
    rv.saving = false;
  }
}

async function toggleRule(id: string): Promise<void> {
  const rule = rv.rules.find(r => r.id === id);
  if (!rule) return;
  try {
    await putRule({ ...editable(rule), enabled: !rule.enabled });
    setStatus(`Rule ${rule.enabled ? "disabled" : "enabled"}.`);
  } catch (error) {
    setStatus(`Could not change the rule: ${errorMessage(error)}`);
  }
  await loadRules();
}

async function removeRule(id: string): Promise<void> {
  const rule = rv.rules.find(r => r.id === id);
  const question = rule?.builtin ? `Reset "${id}" to the built-in definition?` : `Delete the rule "${id}"?`;
  if (!window.confirm(question)) return;
  const res = await apiFetch(`${rulesPath}/${encodeURIComponent(id)}`, { method: "DELETE" });
  setStatus(res.ok ? "Rule removed." : `Could not remove the rule: ${(await res.text()).trim()}`);
  await loadRules();
}

function onClick(selector: string, attr: string, fn: (id: string) => void | Promise<void>): void {
  elements.overviewView.querySelectorAll<HTMLButtonElement>(`[${selector}]`).forEach(btn => {
    btn.addEventListener("click", () => {
      const id = btn.getAttribute(attr);
      if (id !== null) void fn(id);
    });
  });
}

function wireRulesView(): void {
  const root = elements.overviewView;
  root.querySelector("#rule-new")?.addEventListener("click", () => startEdit(""));
  root.querySelector("#rule-save")?.addEventListener("click", () => void saveDraft());
  root.querySelector("#rule-cancel")?.addEventListener("click", () => {
    rv.editing = null;
    renderRulesView();
  });
  onClick("data-rule-edit", "data-rule-edit", startEdit);
  onClick("data-rule-toggle", "data-rule-toggle", toggleRule);
  onClick("data-rule-remove", "data-rule-remove", removeRule);
}
