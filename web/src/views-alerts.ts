// Alerts view: list loading, rendering, the create-alert form, and deletion.
import { renderAlertsViewMarkup } from "./components.js";
import { normalizeList } from "./data.js";
import { errorMessage } from "./format.js";
import type { ApiAlert } from "./types.js";
import { elements, setActiveView, setStatus, state } from "./core.js";
import { deleteJson, fetchJson, postJson } from "./http.js";

export async function loadAlerts(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/alerts", true);
    state.alerts = payload ? normalizeList<ApiAlert>(payload, "alerts") : [];
    if (state.currentRoute === "alerts") renderAlertsView();
  } catch (error) {
    state.alerts = [];
    if (state.currentRoute === "alerts") renderAlertsView(error);
  }
}

export function renderAlertsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Alerts";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderAlertsViewMarkup(state.alerts, error);
  wireAlertActions();
}

function wireAlertActions(): void {
  const form = elements.overviewView.querySelector<HTMLFormElement>("#alert-form");
  const conditionSelect = form?.querySelector<HTMLSelectElement>("#alert-condition-select");
  const thresholdField = form?.querySelector<HTMLElement>(".alert-threshold-field");
  const paramField = form?.querySelector<HTMLElement>(".alert-param-field");

  function updateAlertFormFields(): void {
    const cond = conditionSelect?.value;
    if (thresholdField) thresholdField.hidden = cond !== "event_count_exceeds";
    if (paramField) paramField.hidden = cond !== "message_contains";
  }

  conditionSelect?.addEventListener("change", updateAlertFormFields);
  updateAlertFormFields();

  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitAlertForm(form);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-action='delete-alert']").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset["id"];
      if (id) void deleteAlert(id);
    });
  });
}

async function deleteAlert(id: string): Promise<void> {
  try {
    await deleteJson(`/api/v1/alerts/${encodeURIComponent(id)}`);
    setStatus("Alert deleted.");
    await loadAlerts();
  } catch (error) {
    setStatus(`Failed to delete alert: ${errorMessage(error)}`);
  }
}

async function submitAlertForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  const cooldownRaw = data.get("cooldown_minutes");
  const thresholdRaw = data.get("threshold");
  const param = String(data.get("param") || "").trim();
  try {
    await postJson("/api/v1/alerts", {
      name: String(data.get("name") || ""),
      condition: String(data.get("condition") || ""),
      param: param || undefined,
      webhook_url: String(data.get("webhook_url") || ""),
      threshold: thresholdRaw ? Number(thresholdRaw) : undefined,
      cooldown_minutes: cooldownRaw ? Number(cooldownRaw) : undefined,
      enabled: data.get("enabled") !== null,
    });
    setStatus("Alert saved.");
    await loadAlerts();
  } catch (error) {
    setStatus(`Alert unavailable: ${errorMessage(error)}`);
  }
}
