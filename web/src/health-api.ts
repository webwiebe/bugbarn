import type { RawRecord, SystemHealth } from "./types.js";

// fetchSystemHealth reads the detailed health endpoint. Unlike the other fetchers it
// does not throw on a non-2xx status: a stalled ingest pipeline deliberately
// returns 503 with the health body, which is exactly what we want to display.
export async function fetchSystemHealth(): Promise<SystemHealth> {
  const response = await fetch("/api/v1/health?detail=true", {
    credentials: "include",
    headers: { Accept: "application/json" },
  });
  const text = await response.text();
  const data = (text ? JSON.parse(text) : {}) as RawRecord;
  return data as unknown as SystemHealth;
}
