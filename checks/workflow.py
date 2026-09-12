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


def batch_imports(api):
    manifest = {"entries": [
        {"component_id": "compute-core", "name": "计算核心", "version": "1.0.0", "requires": {}},
        {"component_id": "render-unit", "name": "渲染单元", "version": "1.0.0",
         "requires": {"compute-core": "^1.0.0"}},
        {"component_id": "render-unit", "version": "1.1.0",
         "requires": {"compute-core": ">=1.0.0 <2.0.0"}},
    ]}

    # A preview never mutates the catalog.
    preview = api.request("POST", "/api/v1/imports", manifest, 201)
    import_id = preview["import"]["id"]
    require(preview["created"] is True and preview["import"]["state"] == "pending", "import was not pending")
    require(preview["import"]["summary"] == {"entries": 3, "create_components": 2,
             "add_releases": 1, "already_present": 0, "errors": 0}, "import summary is wrong")
    actions = [(e["index"], e["action"]) for e in preview["import"]["entries"]]
    require(actions == [(0, "create_component_and_release"), (1, "create_component_and_release"),
                        (2, "add_release")], "preview actions are wrong")
    catalog = api.request("GET", "/api/v1/components")
    require(catalog["total"] == 0 and catalog["catalog_revision"] == 0, "preview changed the catalog")
    api.request("GET", f"/api/v1/imports/{import_id}")

    # Restart between preview and confirm: status must survive.
    api.stop()
    api.start()
    require(api.request("GET", f"/api/v1/imports/{import_id}")["state"] == "pending",
            "pending import did not survive restart")

    # Idempotent resubmission (reordered entries and requirement keys) before confirm.
    reordered = {"entries": [manifest["entries"][2], manifest["entries"][0], manifest["entries"][1]]}
    replay = api.request("POST", "/api/v1/imports", reordered)
    require(replay["created"] is False and replay["import"]["id"] == import_id,
            "identical pending manifest created a second import")

    completed = api.request("POST", f"/api/v1/imports/{import_id}/confirm", {})
    require(completed["state"] == "completed", "confirm did not complete the import")
    require(completed["created_components"] == ["compute-core", "render-unit"], "created components wrong")
    require(completed["added_releases"] == ["compute-core@1.0.0", "render-unit@1.0.0",
                                            "render-unit@1.1.0"], "added releases wrong")
    releases = api.request("GET", "/api/v1/components/render-unit/releases")["items"]
    require([r["version"] for r in releases] == ["1.1.0", "1.0.0"], "batch releases missing")

    # Repeating the completed manifest or its confirmation creates nothing.
    again = api.request("POST", "/api/v1/imports", manifest)
    require(again["created"] is False and again["import"]["id"] == import_id,
            "completed manifest was reimported")
    require(api.request("POST", f"/api/v1/imports/{import_id}/confirm", {})["state"] == "completed",
            "repeat confirmation was not idempotent")

    # Invalid batch: every error is attached to its original entry index.
    bad = api.request("POST", "/api/v1/imports", {"entries": [
        {"component_id": "BadID", "name": "x", "version": "1.0.0", "requires": {}},
        {"component_id": "compute-core", "version": "nope", "requires": {}},
        {"component_id": "new-lib", "name": "新库", "version": "1.0.0",
         "requires": {"ghost-lib": "*"}},
        {"component_id": "compute-core", "name": "改名", "version": "1.0.0", "requires": {}},
    ]}, 201)
    bad_id = bad["import"]["id"]
    require(bad["import"]["state"] == "failed" and bad["import"]["summary"]["errors"] == 4,
            "invalid batch was not stored as failed")
    indexed = {e["index"]: [x["field"] for x in e["errors"]]
               for e in bad["import"]["entries"] if e["errors"]}
    require(indexed == {0: ["component_id"], 1: ["version"], 2: ["requires.ghost-lib"],
                        3: ["name"]}, f"errors are not attributed per entry: {indexed}")
    # Nothing from the failed batch reached the catalog.
    api.request("GET", "/api/v1/components/new-lib", expected=404)
    rejected = api.request("POST", f"/api/v1/imports/{bad_id}/confirm", {}, expected=409)
    require(rejected["error"]["code"] == "conflict", "failed import could be confirmed")

    # A valid preview can be rejected at confirm by a concurrent catalog
    # change; the rejection is atomic (no half batch) and durable.
    racy = api.request("POST", "/api/v1/imports", {"entries": [
        {"component_id": "render-unit", "version": "2.0.0", "requires": {"compute-core": "^2.0.0"}},
    ]}, 201)
    racy_id = racy["import"]["id"]
    require(racy["import"]["state"] == "pending", "racy import was not pending")
    release(api, "render-unit", "2.0.0", {"compute-core": "^1.0.0"})
    rejected_confirm = api.request("POST", f"/api/v1/imports/{racy_id}/confirm", {}, expected=422)
    require(rejected_confirm["error"]["code"] == "import_failed", "racy confirm was not rejected")
    require(rejected_confirm["import"]["state"] == "failed"
            and rejected_confirm["import"]["entries"][0]["errors"][0]["field"] == "requires",
            "racy confirm did not report the immutable release conflict")
    stored = api.request("GET", f"/api/v1/imports/{racy_id}")
    require(stored["state"] == "failed", "racy failure did not persist")

    # Restart keeps every terminal state and imported catalog data.
    api.stop()
    api.start()
    states = {item["id"]: item["state"] for item in api.request("GET", "/api/v1/imports")["items"]}
    require(states[import_id] == "completed" and states[bad_id] == "failed"
            and states[racy_id] == "failed", "import states were lost across restart")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"render-unit": "*"}})
    require(result["resolved"]["render-unit"] == "2.0.0", "imported catalog does not solve after restart")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "imports"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade,
             "imports": batch_imports}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
