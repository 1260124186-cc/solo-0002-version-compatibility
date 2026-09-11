#!/usr/bin/env python3
"""Bounded operational smoke checks through the running HTTP service."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parent.parent


class RunningService:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.process = None
        self.log = None
        self.base = ""

    def start(self):
        self.log = (self.directory / "server.log").open("w+")
        env = dict(os.environ, COMPAT_ADDRESS="127.0.0.1:0",
                   COMPAT_DATA_DIR=str(self.directory / "state"),
                   COMPAT_MAX_STEPS="50000", COMPAT_REQUEST_TIMEOUT="10s")
        self.process = subprocess.Popen([str(ROOT / "build/compat-server")],
                                        env=env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError("service exited during startup")
            self.log.flush()
            text = (self.directory / "server.log").read_text()
            for line in text.splitlines():
                try:
                    item = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if item.get("msg") == "server ready":
                    self.base = "http://" + item["address"]
                    self.request("GET", "/healthz")
                    return
            time.sleep(0.03)
        raise RuntimeError("service readiness deadline exceeded")

    def stop(self):
        if self.process is not None:
            self.process.terminate()
            try:
                self.process.wait(timeout=12)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=3)
                raise RuntimeError("service did not stop gracefully")
            finally:
                self.process = None
        if self.log is not None:
            self.log.close()
            self.log = None

    def request(self, method, path, data=None, expected=200, raw=None):
        body = raw if raw is not None else (json.dumps(data).encode() if data is not None else None)
        request = urllib.request.Request(self.base + path, data=body, method=method,
                                         headers={"Content-Type": "application/json"})
        try:
            response = urllib.request.urlopen(request, timeout=12)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            result = json.loads(response.read())
            if response.status != expected:
                raise RuntimeError(f"{method} {path}: expected {expected}, got {response.status}: {result}")
            return result


def require(condition, detail):
    if not condition:
        raise RuntimeError(detail)


def component(api, name):
    return api.request("POST", "/api/v1/components", {"id": name, "name": name, "description": ""}, 201)


def release(api, name, version, requires=None, expected=201):
    return api.request("POST", f"/api/v1/components/{name}/releases",
                       {"version": version, "requires": requires or {}}, expected)


def populate(api):
    for name in ("atlas-core", "render-engine", "panel-shell"):
        component(api, name)
    release(api, "atlas-core", "1.0.0")
    release(api, "atlas-core", "2.0.0")
    release(api, "render-engine", "1.0.0", {"atlas-core": "^1.0.0"})
    release(api, "render-engine", "2.0.0", {"atlas-core": "^2.0.0"})
    release(api, "panel-shell", "1.0.0", {"render-engine": "*"})


def catalog(api):
    component(api, "atlas-core")
    release(api, "atlas-core", "1.0.0")
    release(api, "atlas-core", "1.0.0", expected=409)
    release(api, "atlas-core", "01.0.0", expected=400)
    release(api, "atlas-core", "2.0.0", {"missing-core": "*"}, expected=404)
    api.request("POST", "/api/v1/components", expected=400,
                raw=b'{"id":"ab","id":"cd","name":"duplicate"}')
    api.request("POST", "/api/v1/components", {"id": "ab", "name": "x", "unexpected": 1}, 400)
    result = api.request("POST", "/api/v1/components/atlas-core/releases/1.0.0/withdraw", {})
    require(result["state"] == "withdrawn", "release did not become withdrawn")
    api.request("POST", "/api/v1/resolve", {"roots": {"atlas-core": "*"}}, 422)
    api.stop()
    api.start()
    result = api.request("GET", "/api/v1/components/atlas-core/releases")
    require(len(result["items"]) == 1 and result["items"][0]["state"] == "withdrawn", "durable version state changed after restart")
    events = api.request("GET", "/api/v1/events")["items"]
    require([event["action"] for event in events] == ["created", "added", "withdrawn"], "failed writes altered events")


def resolve(api):
    populate(api)
    result = api.request("POST", "/api/v1/resolve", {"roots": {"panel-shell": "*"}})
    require(result["resolved"] == {"panel-shell": "1.0.0", "render-engine": "2.0.0", "atlas-core": "2.0.0"}, "transitive selection is wrong")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"render-engine": "*", "atlas-core": "1.0.0"}})
    require(result["resolved"]["render-engine"] == "1.0.0", "search did not backtrack")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"render-engine": "2.0.0", "atlas-core": "^1.0.0"}}, 422)
    require(result["error"]["code"] == "no_solution" and result["error"]["conflicts"], "missing conflict evidence")
    for name in ("cycle-alpha", "cycle-beta"):
        component(api, name)
    release(api, "cycle-alpha", "1.0.0", {"cycle-beta": "~1.0.0"})
    release(api, "cycle-beta", "1.0.0", {"cycle-alpha": ">=1.0.0 <2.0.0"})
    result = api.request("POST", "/api/v1/resolve", {"roots": {"cycle-alpha": "*"}})
    require(len(result["resolved"]) == 2 and len(result["edges"]) == 2, "compatible dependency cycle failed")


def upgrade(api):
    populate(api)
    env = api.request("POST", "/api/v1/environments", {"id": "integration", "name": "集成环境", "roots": {"render-engine": "1.0.0"}}, 201)
    require(env["resolved"]["atlas-core"] == "1.0.0", "initial environment resolution failed")
    body = {"environment_id": "integration", "base_revision": 1, "roots": {"render-engine": "2.0.0"}, "reason": "验证新版兼容集合"}
    first = api.request("POST", "/api/v1/plans", body, 201)
    second = api.request("POST", "/api/v1/plans", body, 201)
    path = "/api/v1/plans/" + first["id"]
    ready = api.request("POST", path + "/validate", {"revision": 1})
    require(ready["state"] == "ready" and len(ready["changes"]) == 2, "plan validation did not generate expected changes")
    release(api, "atlas-core", "3.0.0")
    api.request("POST", path + "/apply", {"revision": ready["revision"]}, 409)
    require(api.request("GET", "/api/v1/environments/integration")["revision"] == 1, "stale plan mutated environment")
    ready = api.request("POST", path + "/validate", {"revision": ready["revision"]})
    applied = api.request("POST", path + "/apply", {"revision": ready["revision"]})
    require(applied["environment"]["revision"] == 2 and applied["environment"]["resolved"]["atlas-core"] == "2.0.0", "plan application failed")
    api.request("POST", path + "/apply", {"revision": applied["plan"]["revision"]}, 409)
    other = "/api/v1/plans/" + second["id"]
    api.request("POST", other + "/validate", {"revision": 1}, 409)
    cancelled = api.request("POST", other + "/cancel", {"revision": 1})
    require(cancelled["state"] == "cancelled", "plan cancellation failed")
    api.request("POST", "/api/v1/components/atlas-core/releases/2.0.0/withdraw", {}, 409)
    api.stop()
    api.start()
    require(api.request("GET", path)["state"] == "applied", "applied state lost after restart")
    require(api.request("GET", "/api/v1/environments/integration")["revision"] == 2, "environment revision lost after restart")


def matrix(api):
    populate(api)
    axes = {
        "first": {"component": "render-engine", "versions": ["2.0.0", "1.0.0"]},
        "second": {"component": "atlas-core", "versions": ["2.0.0", "1.0.0"]},
        "roots": {"panel-shell": "*"},
    }
    result = api.request("POST", "/api/v1/matrices", axes)
    require(not result["saved"] and "id" not in result, "unsaved matrix gained an identity")
    order = [(cell["first_version"], cell["second_version"]) for cell in result["cells"]]
    require(order == [("2.0.0", "2.0.0"), ("2.0.0", "1.0.0"), ("1.0.0", "2.0.0"), ("1.0.0", "1.0.0")],
            "matrix cells do not follow input version order")
    cells = {(cell["first_version"], cell["second_version"]): cell for cell in result["cells"]}
    require(cells[("2.0.0", "2.0.0")]["compatible"] and cells[("1.0.0", "1.0.0")]["compatible"], "aligned versions rejected")
    require(not cells[("2.0.0", "1.0.0")]["compatible"] and not cells[("1.0.0", "2.0.0")]["compatible"], "mismatched versions accepted")
    require(cells[("2.0.0", "1.0.0")]["detail"] and cells[("2.0.0", "1.0.0")]["conflicts"], "incompatible cell lacks a reason")
    require(cells[("1.0.0", "1.0.0")]["resolved"] == {"panel-shell": "1.0.0", "render-engine": "1.0.0", "atlas-core": "1.0.0"},
            "compatible cell resolution is wrong")
    require(result["compatible"] == 2 and result["incompatible"] == 2 and result["steps"] > 0, "matrix tally is wrong")
    api.request("POST", "/api/v1/matrices", {"first": axes["first"], "second": axes["first"]}, 400)
    api.request("POST", "/api/v1/matrices", {"first": {"component": "render-engine", "versions": []}, "second": axes["second"]}, 400)
    api.request("POST", "/api/v1/matrices", {"first": {"component": "render-engine", "versions": ["1.0.0", "1.0.0"]}, "second": axes["second"]}, 400)
    api.request("POST", "/api/v1/matrices", {"first": {"component": "missing-one", "versions": ["1.0.0"]}, "second": axes["second"]}, 404)
    api.request("POST", "/api/v1/matrices", {**axes, "roots": {"render-engine": "*"}}, 400)
    crowded = {"first": {"component": "render-engine", "versions": [f"1.0.{n}" for n in range(13)]}, "second": {"component": "atlas-core", "versions": ["1.0.0"]}}
    api.request("POST", "/api/v1/matrices", crowded, 400)
    result = api.request("POST", "/api/v1/matrices", {"first": {"component": "render-engine", "versions": ["9.9.9"]},
                                                      "second": {"component": "atlas-core", "versions": ["1.0.0"]}})
    require(not result["cells"][0]["compatible"] and "9.9.9" in result["cells"][0]["detail"], "unknown candidate version not reported")
    saved = api.request("POST", "/api/v1/matrices", {**axes, "save": True}, 201)
    require(saved["saved"] and saved["id"].startswith("matrix-"), "matrix was not saved")
    require(saved["catalog_revision"] == result["catalog_revision"], "matrix did not reuse one catalog view")
    require(api.request("GET", "/api/v1/matrices/" + saved["id"])["cells"] == saved["cells"], "saved matrix cells changed")
    listing = api.request("GET", "/api/v1/matrices")
    require(listing["total"] == 1 and listing["items"][0]["id"] == saved["id"], "saved matrix not listed")
    api.request("GET", "/api/v1/matrices/matrix-absent", expected=404)
    events = api.request("GET", "/api/v1/events?entity_id=" + saved["id"])["items"]
    require([event["action"] for event in events] == ["computed"], "matrix event missing")
    api.request("POST", "/api/v1/components/atlas-core/releases/1.0.0/withdraw", {})
    result = api.request("POST", "/api/v1/matrices", {"first": {"component": "render-engine", "versions": ["1.0.0"]},
                                                      "second": {"component": "atlas-core", "versions": ["1.0.0"]}})
    require(not result["cells"][0]["compatible"] and "withdrawn" in result["cells"][0]["detail"], "withdrawn candidate not reported")
    api.stop()
    api.start()
    require(api.request("GET", "/api/v1/matrices/" + saved["id"])["id"] == saved["id"], "saved matrix lost after restart")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "matrix"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "matrix": matrix}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
