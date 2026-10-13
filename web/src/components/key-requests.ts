import { escapeAttr, escapeHtml } from "../format.js";
import type { ApiKeyRequest } from "../types.js";

// renderKeyRequests lists the open agent key requests with Approve and Deny.
// The request carries no key: the agent receives it on its next poll after the
// approval.
export function renderKeyRequests(requests: ApiKeyRequest[]): string {
  if (requests.length === 0) return "";
  const rows = requests.map((r) => `
    <div class="project-row" data-key-request="${escapeAttr(r.id)}">
      <div class="project-info">
        <strong>${escapeHtml(r.label)}</strong>
        <span class="project-slug">${escapeHtml(r.project)}</span>
      </div>
      <div class="project-actions">
        <span class="muted" title="${escapeAttr(r.expiresAt)}">asked ${escapeHtml(formatAsked(r.createdAt))}</span>
        <button class="btn-sm" data-approve-key-request="${escapeAttr(r.id)}">Approve</button>
        <button class="btn-sm danger" data-deny-key-request="${escapeAttr(r.id)}">Deny</button>
      </div>
    </div>`).join("");
  return `
    <div class="section" id="key-requests">
      <h3>Key requests <span class="nav-badge">${requests.length}</span></h3>
      <p class="muted">An agent asked for an ingest key for an existing project. Approving lets that agent collect one key, once. Requests expire after 24 hours.</p>
      ${rows}
    </div>`;
}

function formatAsked(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const minutes = Math.max(0, Math.round((Date.now() - t) / 60000));
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  return `${Math.round(minutes / 60)} h ago`;
}
