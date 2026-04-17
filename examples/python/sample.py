from __future__ import annotations

import os

from bugbarn.client import capture_exception, init, shutdown


def main() -> None:
    api_key = os.environ.get("BUGBARN_API_KEY", "bb_live_example")
    endpoint = os.environ.get("BUGBARN_ENDPOINT", "http://127.0.0.1:8080/api/v1/events")
    mode = os.environ.get("BUGBARN_SAMPLE_MODE", "manual")

    init(api_key=api_key, endpoint=endpoint, install_excepthook=True)

    if mode == "uncaught":
        raise RuntimeError("BugBarn Python sample uncaught exception")

    capture_exception(
        RuntimeError("BugBarn Python sample manual error"),
        attributes={
            "service.name": "bugbarn-python-sample",
            "runtime.name": "python",
        },
        tags={"sample": "python"},
        extra={"mode": "manual"},
    )
    shutdown(timeout=2.0)


if __name__ == "__main__":
    main()
