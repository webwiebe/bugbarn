// App-level actions that low layers (http, the project views) need but cannot import:
// refreshAll and setRouteStatus live in the router, which imports every view, and
// loadProjects lives in views-projects, which imports http. bootstrap registers the
// real functions at startup; until then each action is a no-op, so a module loaded
// in a test without the app wired up does nothing instead of throwing.

type Action = () => Promise<void>;

const noop: Action = async () => {};

let refreshAllAction: Action = noop;
let loadProjectsAction: Action = noop;
let setRouteStatusAction: () => void = () => {};

export function registerAppActions(actions: {
  refreshAll: Action;
  loadProjects: Action;
  setRouteStatus: () => void;
}): void {
  refreshAllAction = actions.refreshAll;
  loadProjectsAction = actions.loadProjects;
  setRouteStatusAction = actions.setRouteStatus;
}

/** Re-fetches whatever the current route shows. */
export function refreshAll(): Promise<void> {
  return refreshAllAction();
}

/** Re-fetches the project list. */
export function loadProjects(): Promise<void> {
  return loadProjectsAction();
}

/** Rewrites the status line for the current route. */
export function setRouteStatus(): void {
  setRouteStatusAction();
}
