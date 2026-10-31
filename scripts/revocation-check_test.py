#!/usr/bin/env python3
"""Run revocation-check.py against a local stand-in, one broken step at a time.

Every response carries a marker. It must never reach the output, and neither may
the password or the setup key.
"""
import http.server
import json
import os
import re
import secrets
import subprocess
import sys
import threading

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(REPO, "scripts", "revocation-check.py")
MARKER = "response-body-marker"
PASSWORD = secrets.token_hex(16)
SETUP_KEY = secrets.token_hex(20)
SESSION = secrets.token_hex(16)
USER = "e2e-check@e2e.wiebe.xyz"
OK_LINES = [
    "PASS target is testing",
    "PASS deployed build",
    "PASS login",
    "PASS setup key",
    "PASS event accepted",
    "PASS revocation not listed before",
    "PASS revoke",
    "PASS revocation listed",
    "PASS event refused",
    "PASS project removed",
    "PASS logout",
]


class StandIn(http.server.BaseHTTPRequestHandler):
    broken = None
    revoked = set()
    project_404s = 0

    def log_message(self, *_args):
        pass

    def answer(self, status, body=None, cookies=()):
        data = (body if body is not None else MARKER).encode()
        self.send_response(status)
        for cookie in cookies:
            self.send_header("Set-Cookie", cookie)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def signed_in(self):
        cookie = self.headers.get("Cookie", "")
        return "bugbarn_session=" + SESSION in cookie and self.headers.get("X-BugBarn-CSRF") == "csrf-" + SESSION

    def fail(self, step):
        return type(self).broken == step

    def do_GET(self):
        if self.path == "/api/v1/version":
            return self.answer(500 if self.fail("deployed build") else 200, json.dumps({"commit": "abc", "m": MARKER}))
        if self.path.startswith("/api/v1/setup/"):
            self.server.last_slug = self.path.rsplit("/", 1)[-1]
            if self.fail("setup key"):
                return self.answer(200, "no key here " + MARKER)
            return self.answer(200, f"| API Key   | {SETUP_KEY} |\n{MARKER}")
        if self.path == "/api/v1/apikeys":
            rows = [] if self.fail("revocation listed") else [{"slug": s} for s in type(self).revoked]
            if self.fail("revocation not listed before"):
                # A row from before the revoke: every slug counts as already listed.
                rows = [{"slug": self.server.last_slug}]
            return self.answer(200 if self.signed_in() else 401, json.dumps({"revokedSetupKeys": rows}))
        return self.answer(404)

    def read_body(self):
        return self.rfile.read(int(self.headers.get("Content-Length", 0)))

    def do_POST(self):
        body = self.read_body()
        if self.path == "/api/v1/login":
            sent = json.loads(body)
            if self.fail("login") or sent != {"username": USER, "password": PASSWORD}:
                return self.answer(401)
            cookies = [f"bugbarn_session={SESSION}; Path=/; HttpOnly", f"bugbarn_csrf=csrf-{SESSION}; Path=/"]
            return self.answer(200, '{"authenticated":true}', cookies)
        if self.path == "/api/v1/events":
            if self.headers.get("Cookie"):
                return self.answer(500)
            slug = self.headers.get("X-BugBarn-Project")
            good = self.headers.get("X-BugBarn-Api-Key") == SETUP_KEY and slug not in type(self).revoked
            if self.fail("event accepted") or (self.fail("event refused") and slug in type(self).revoked):
                return self.answer(503)
            return self.answer(202 if good or self.fail("event refused") else 401)
        if self.path == "/api/v1/apikeys/revoke-setup":
            if self.fail("revoke") or not self.signed_in():
                return self.answer(403)
            type(self).revoked.add(json.loads(body)["slug"])
            return self.answer(200, '{"revoked":true}')
        if self.path == "/api/v1/logout":
            return self.answer(500 if self.fail("logout") else 200)
        return self.answer(404)

    def do_DELETE(self):
        if self.fail("project removed") or not self.signed_in():
            return self.answer(403)
        if type(self).project_404s:
            type(self).project_404s -= 1
            return self.answer(404)
        return self.answer(200, '{"ok":true}')


failures = 0


def check(name, passed):
    global failures
    print(("ok   " if passed else "FAIL ") + name)
    failures += 0 if passed else 1


def run(url, password=PASSWORD, commit=""):
    env = {
        "PATH": os.environ.get("PATH", ""),
        "REVOCATION_CHECK_URL": url,
        "REVOCATION_CHECK_DELAY": "0",
        "E2E_TESTING_USER": USER,
        "E2E_TESTING_PASSWORD": password,
        "CI_COMMIT_SHA": commit,
    }
    return subprocess.run([sys.executable, "-I", SCRIPT], env=env, capture_output=True, text=True, timeout=60, check=False)


def clean(result):
    text = result.stdout + result.stderr
    lines_ok = all(re.fullmatch(r"(PASS|FAIL) [a-z ]+", line) for line in text.splitlines())
    return lines_ok and not any(value in text for value in (MARKER, PASSWORD, SETUP_KEY, SESSION))


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), StandIn)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{server.server_port}"

    StandIn.project_404s = 2
    result = run(url)
    check("every step passes", result.returncode == 0 and result.stdout.splitlines() == OK_LINES)
    check("a passing run prints only step names", clean(result))
    check("a project that appears late still gets removed", StandIn.project_404s == 0)

    for step in [line[5:] for line in OK_LINES[1:]]:
        StandIn.broken = step
        result = run(url)
        check(f"{step}: reported and exits 1", result.returncode == 1 and "FAIL " + step in result.stdout)
        check(f"{step}: prints only step names", clean(result))
    StandIn.broken = None

    result = run(url, commit="abc")
    check("the commit under test is the deployed one", result.returncode == 0)
    result = run(url, commit="other")
    check("another deployed commit stops the run", result.stdout.splitlines()[-1] == "FAIL deployed build")

    result = run(url, password="wrong")
    check("a wrong password stops at login", result.returncode == 1 and result.stdout.splitlines()[-1] == "FAIL login")
    check("a wrong password prints only step names", clean(result))

    server.shutdown()
    result = run(url)
    check("an unreachable server is a clean failure", result.returncode == 1 and clean(result))

    result = run("https://bugbarn.wiebe.xyz")
    check("any other host is refused", result.returncode == 1 and result.stdout.splitlines() == ["FAIL target is testing"])
    result = run("http://bugbarn.test.wiebe.xyz")
    check("testing without TLS is refused", result.returncode == 1 and result.stdout.splitlines() == ["FAIL target is testing"])

    if failures:
        print(f"{failures} check(s) failed")
        return 1
    print("all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
