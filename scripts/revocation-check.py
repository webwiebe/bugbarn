#!/usr/bin/env python3
"""Check on the deployed testing build that a revoked setup key is refused.

Uses a generated slug per run: its setup key takes one event, gets revoked, and
one more event must be refused. Prints PASS or FAIL per step and nothing from a
response. The password comes from the environment; the session and the setup key
stay in this process, so none of them reach a command line or a file.
"""
import json
import os
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

TESTING_HOST = "bugbarn.test.wiebe.xyz"
STAND_IN_HOST = "127.0.0.1"
KEY_ROW = re.compile(r"\|\s*API Key\s*\|\s*([0-9a-f]{40})\s*\|")
EVENT = json.dumps({"severityText": "info", "body": "setup key revocation check"}).encode()
TIMEOUT_SECONDS = 20
PROJECT_TRIES = 6


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        return None


class Client:
    def __init__(self, base):
        self.base = base
        self.cookies = {}
        self.opener = urllib.request.build_opener(NoRedirect)

    def call(self, method, path, body=None, headers=None, session=True):
        sent = {"User-Agent": "bugbarn-revocation-check", "Content-Type": "application/json"}
        sent.update(headers or {})
        if session and self.cookies:
            sent["Cookie"] = "; ".join(f"{k}={v}" for k, v in self.cookies.items())
            if "bugbarn_csrf" in self.cookies:
                sent["X-BugBarn-CSRF"] = self.cookies["bugbarn_csrf"]
        request = urllib.request.Request(self.base + path, data=body, headers=sent, method=method)
        try:
            response = self.opener.open(request, timeout=TIMEOUT_SECONDS)
        except urllib.error.HTTPError as err:
            response = err
        with response:
            payload = response.read()
            if session:
                for line in response.headers.get_all("Set-Cookie") or []:
                    name, _, rest = line.partition("=")
                    self.cookies[name.strip()] = rest.split(";", 1)[0]
            return response.status, payload


def run_step(name, check):
    try:
        passed = bool(check())
    except Exception:  # A failure prints the step name and nothing else.
        passed = False
    print(("PASS " if passed else "FAIL ") + name, flush=True)
    return passed


def base_url():
    base = os.environ.get("REVOCATION_CHECK_URL", "https://" + TESTING_HOST).rstrip("/")
    parts = urllib.parse.urlsplit(base)
    if (parts.hostname, parts.scheme) in ((TESTING_HOST, "https"), (STAND_IN_HOST, "http")):
        return base
    return None


def main():
    sys.excepthook = lambda *_args: None
    base = base_url()
    if not run_step("target is testing", lambda: base):
        return 1
    user = os.environ.get("E2E_TESTING_USER", "")
    password = os.environ.get("E2E_TESTING_PASSWORD", "")
    delay = float(os.environ.get("REVOCATION_CHECK_DELAY", "5"))
    client = Client(base)
    slug = "e2e-revocation-check-" + secrets.token_hex(4)
    state = {}

    def deployed_build():
        status, payload = client.call("GET", "/api/v1/version", session=False)
        expected = os.environ.get("CI_COMMIT_SHA", "")
        return status == 200 and (not expected or json.loads(payload).get("commit") == expected)

    def login():
        body = json.dumps({"username": user, "password": password}).encode()
        status, _ = client.call("POST", "/api/v1/login", body)
        return status == 200 and "bugbarn_session" in client.cookies and "bugbarn_csrf" in client.cookies

    def setup_key():
        status, payload = client.call("GET", "/api/v1/setup/" + slug, session=False)
        match = KEY_ROW.search(payload.decode("utf-8", "replace"))
        if status != 200 or not match:
            return False
        state["key"] = match.group(1)
        return True

    def send_event():
        headers = {"X-BugBarn-Api-Key": state["key"], "X-BugBarn-Project": slug}
        return client.call("POST", "/api/v1/events", EVENT, headers, session=False)[0]

    def revoke():
        status, payload = client.call("POST", "/api/v1/apikeys/revoke-setup", json.dumps({"slug": slug}).encode())
        return status == 200 and json.loads(payload).get("revoked") is True

    def listed():
        status, payload = client.call("GET", "/api/v1/apikeys")
        if status != 200:
            raise ValueError
        return any(r.get("slug") == slug for r in json.loads(payload).get("revokedSetupKeys", []))

    def remove_project():
        # The project appears when the worker has taken the first event.
        for attempt in range(PROJECT_TRIES):
            if attempt:
                time.sleep(delay)
            status, _ = client.call("DELETE", "/api/v1/projects/" + slug)
            if status != 404:
                return status == 200
        return False

    def logout():
        status, _ = client.call("POST", "/api/v1/logout")
        return status == 200

    if not run_step("deployed build", deployed_build) or not run_step("login", login):
        return 1

    results = []
    if run_step("setup key", setup_key):
        accepted = run_step("event accepted", lambda: send_event() == 202)
        results.append(accepted)
        before = accepted and run_step("revocation not listed before", lambda: not listed())
        results.append(before)
        if before and run_step("revoke", revoke):
            results.append(run_step("revocation listed", listed))
            results.append(run_step("event refused", lambda: send_event() == 401))
        else:
            results.append(False)
        if accepted:
            results.append(run_step("project removed", remove_project))
    else:
        results.append(False)
    results.append(run_step("logout", logout))
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
