#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path

import yaml


def fail(message: str) -> None:
    print(f"openapi validation failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def main() -> None:
    path = Path("specs/001-personal-error-tracker/contracts/ingest-api.yaml")
    document = yaml.safe_load(path.read_text())

    if document.get("openapi") != "3.1.0":
        fail("expected OpenAPI 3.1.0")

    paths = document.get("paths") or {}
    ingest = paths.get("/api/v1/events", {}).get("post")
    if not ingest:
        fail("missing POST /api/v1/events")

    responses = ingest.get("responses") or {}
    for status in ("202", "400", "401", "413", "429", "503"):
        if status not in responses:
            fail(f"missing ingest response {status}")

    schemes = (document.get("components") or {}).get("securitySchemes") or {}
    api_key = schemes.get("ApiKeyAuth") or {}
    if api_key.get("name") != "x-bugbarn-api-key":
        fail("ApiKeyAuth must use x-bugbarn-api-key")

    schemas = (document.get("components") or {}).get("schemas") or {}
    for schema in ("IngestEnvelope", "Exception", "StackFrame", "IngestAccepted"):
        if schema not in schemas:
            fail(f"missing schema {schema}")

    print("openapi ok")


if __name__ == "__main__":
    main()

