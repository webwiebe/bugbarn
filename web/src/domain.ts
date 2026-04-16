import { isRecord, readFirst, readNumber, readRecord, readString } from "./data.js";
import type { ApiEvent, ApiIssue, RawRecord } from "./types.js";

export function firstIdentifier(source: ApiIssue | ApiEvent | RawRecord, extraOmitKeys: string[] = []): string {
  const omit = new Set(extraOmitKeys);
  const keys = ["id", "ID", "issueId", "IssueID", "issue_id", "eventId", "EventID", "event_id"].filter((key) => !omit.has(key));
  const value = readFirst(source, keys);
  if (typeof value === "string" || typeof value === "number") {
    return String(value);
  }
  if (isRecord(value)) {
    const nested = readFirst(value, ["id", "ID"]);
    if (typeof nested === "string" || typeof nested === "number") {
      return String(nested);
    }
  }
  for (const nestedKey of ["issue", "Issue", "event", "Event"]) {
    const nested = source[nestedKey];
    if (isRecord(nested)) {
      const nestedID = firstIdentifier(nested, extraOmitKeys);
      if (nestedID) {
        return nestedID;
      }
    }
  }
  return "";
}

export function issueTitle(issue: ApiIssue): string {
  return readString(issue, ["title", "Title", "normalizedTitle", "NormalizedTitle", "normalized_title"]) || "Untitled issue";
}

export function issueNormalizedTitle(issue: ApiIssue): string {
  return readString(issue, ["normalizedTitle", "NormalizedTitle", "normalized_title"]);
}

export function issueExceptionType(issue: ApiIssue): string {
  return readString(issue, ["exceptionType", "ExceptionType", "exception_type"]);
}

export function issueFingerprint(issue: ApiIssue): string {
  return readString(issue, ["fingerprint", "Fingerprint"]);
}

export function issueLastSeen(issue: ApiIssue): unknown {
  return readFirst(issue, ["lastSeen", "LastSeen", "last_seen"]);
}

export function issueFirstSeen(issue: ApiIssue): unknown {
  return readFirst(issue, ["firstSeen", "FirstSeen", "first_seen"]);
}

export function issueEventCount(issue: ApiIssue, fallback = 0): number {
  return readNumber(issue, ["eventCount", "EventCount", "event_count", "count"]) || fallback;
}

export function eventIssueId(event: ApiEvent): string {
  const direct = readString(event, ["issueId", "IssueID", "issue_id"]);
  if (direct) {
    return direct;
  }
  const issue = readRecord(event, ["issue", "Issue"]);
  return firstIdentifier(issue);
}

export function eventTitle(event: ApiEvent): string {
  return (
    readString(event, ["title", "Title", "body", "Body", "message", "Message"]) ||
    readNestedMessage(readFirst(event, ["exception", "Exception"])) ||
    "Event"
  );
}

export function eventTimestamp(event: ApiEvent): unknown {
  return readFirst(event, ["timestamp", "Timestamp", "createdAt", "CreatedAt", "created_at", "receivedAt", "ReceivedAt", "observedAt", "ObservedAt"]);
}

export function eventSeverity(event: ApiEvent): string {
  return readString(event, ["severityText", "SeverityText", "severity_text", "severity", "Severity"]);
}

export function eventPayload(event: ApiEvent): RawRecord {
  return readRecord(event, ["payload", "Payload"]);
}

export function eventException(event: ApiEvent): RawRecord {
  const payload = eventPayload(event);
  const direct = readRecord(event, ["exception", "Exception"]);
  const nested = readRecord(payload, ["exception", "Exception"]);
  return Object.keys(direct).length ? direct : nested;
}

export function eventRawScrubbed(event: ApiEvent): RawRecord {
  return readRecord(eventPayload(event), ["rawScrubbed", "raw_scrubbed", "RawScrubbed"]);
}

export function eventSdkName(event: ApiEvent): string {
  const payload = eventPayload(event);
  const sender = readRecord(eventRawScrubbed(event), ["sender", "Sender"]);
  const sdk = readRecord(sender, ["sdk", "SDK"]);
  return readString(payload, ["sdkName", "SDKName", "sdk_name"]) || readString(sdk, ["name", "Name"]);
}

export function eventTraceId(event: ApiEvent): string {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  return readString(payload, ["traceId", "trace_id", "TraceID"]) || readString(raw, ["traceId", "trace_id", "TraceID"]);
}

export function eventContext(event: ApiEvent): RawRecord {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  const context: RawRecord = {};
  for (const [label, value] of [
    ["Ingest id", readString(payload, ["ingestId", "ingest_id", "IngestID"])],
    ["SDK", eventSdkName(event)],
    ["Trace id", eventTraceId(event)],
    ["Exception type", readString(eventException(event), ["type", "Type"])],
    ["Exception message", readString(eventException(event), ["message", "Message"])],
  ] as const) {
    if (value) {
      context[label] = value;
    }
  }
  for (const [label, record] of [
    ["Resource", readRecord(payload, ["resource", "Resource"])],
    ["Attributes", readRecord(payload, ["attributes", "Attributes"])],
    ["Tags", readRecord(raw, ["tags", "Tags"])],
  ] as const) {
    if (Object.keys(record).length) {
      context[label] = record;
    }
  }
  return context;
}

export function eventStacktrace(event: ApiEvent): unknown[] {
  const exception = eventException(event);
  const direct = readFirst(eventPayload(event), ["stacktrace", "Stacktrace", "stackTrace", "StackTrace"]);
  const nested = readFirst(exception, ["stacktrace", "Stacktrace", "stackTrace", "StackTrace"]);
  if (Array.isArray(direct)) {
    return direct;
  }
  if (Array.isArray(nested)) {
    return nested;
  }
  return [];
}

export function eventSpans(event: ApiEvent): unknown[] {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  const direct = readFirst(payload, ["spans", "Spans"]);
  const rawSpans = readFirst(raw, ["spans", "Spans"]);
  if (Array.isArray(direct)) {
    return direct;
  }
  if (Array.isArray(rawSpans)) {
    return rawSpans;
  }
  return [];
}

export function eventUrl(event: ApiEvent): string {
  const payload = eventPayload(event);
  const raw = eventRawScrubbed(event);
  const attributes = readRecord(payload, ["attributes", "Attributes"]);
  return (
    readString(attributes, ["url", "http.url", "http.target", "route"]) ||
    readString(raw, ["url", "URL", "requestUrl", "request_url"]) ||
    readString(readRecord(raw, ["request", "Request"]), ["url", "URL"])
  );
}

function readNestedMessage(value: unknown): string {
  if (!isRecord(value)) {
    return "";
  }
  const message = readFirst(value, ["message", "Message"]);
  return typeof message === "string" ? message : "";
}
