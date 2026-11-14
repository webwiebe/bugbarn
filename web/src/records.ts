import type { RawRecord } from "./types.js";

export function normalizeList<T extends RawRecord = RawRecord>(payload: unknown, key: string): T[] {
  if (!payload) {
    return [];
  }
  if (Array.isArray(payload)) {
    return payload.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload[key])) {
    return payload[key].map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.items)) {
    return payload.items.map((item) => normalizeObject<T>(item));
  }
  if (isRecord(payload) && Array.isArray(payload.data)) {
    return payload.data.map((item) => normalizeObject<T>(item));
  }
  return [];
}

export function normalizeObject<T extends RawRecord = RawRecord>(value: unknown, key = ""): T {
  if (!isRecord(value)) {
    return { value } as unknown as T;
  }
  if (key && isRecord(value[key])) {
    return value[key] as T;
  }
  return value as T;
}

export function isRecord(value: unknown): value is RawRecord {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function readFirst(source: RawRecord, keys: string[]): unknown {
  for (const key of keys) {
    const value = source[key];
    if (value !== null && value !== undefined && value !== "") {
      return value;
    }
  }
  return "";
}

export function readString(source: RawRecord, keys: string[]): string {
  const value = readFirst(source, keys);
  return typeof value === "string" || typeof value === "number" ? String(value) : "";
}

export function readNumber(source: RawRecord, keys: string[]): number {
  const value = readFirst(source, keys);
  if (typeof value === "number") {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (!Number.isNaN(parsed)) {
      return parsed;
    }
  }
  return 0;
}

export function readRecord(source: RawRecord, keys: string[]): RawRecord {
  const value = readFirst(source, keys);
  return isRecord(value) ? value : {};
}

export function hasKeys(source: RawRecord): boolean {
  return Object.keys(source).length > 0;
}

export function collectKeyValues(source: RawRecord, omitKeys: string[] = []): RawRecord {
  const omit = new Set(omitKeys);
  return Object.entries(source || {}).reduce<RawRecord>((acc, [key, value]) => {
    if (omit.has(key) || value === null || value === undefined) {
      return acc;
    }
    acc[key] = value;
    return acc;
  }, {});
}
