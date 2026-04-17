export interface Breadcrumb {
  timestamp: string; // ISO 8601
  category: string; // "console" | "http" | "navigation" | "manual"
  message: string;
  level?: string; // "log" | "warn" | "error" | "info" | "debug"
  data?: Record<string, unknown>;
}

const MAX_BREADCRUMBS = 100;
const buffer: Breadcrumb[] = [];

export function addBreadcrumb(crumb: Breadcrumb): void {
  buffer.push(crumb);
  if (buffer.length > MAX_BREADCRUMBS) {
    buffer.shift();
  }
}

export function getBreadcrumbs(): Breadcrumb[] {
  return [...buffer];
}

export function clearBreadcrumbs(): void {
  buffer.length = 0;
}
