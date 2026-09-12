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


def drift(api):
    populate(api)
    env = api.request("POST", "/api/v1/environments",
                      {"id": "integration", "name": "集成环境", "roots": {"render-engine": "1.0.0"}}, 201)
    require(env["revision"] == 1 and env["resolved"] == {"render-engine": "1.0.0", "atlas-core": "1.0.0"},
            "initial environment resolution failed")
    check_path = "/api/v1/environments/integration/drift-checks"

    def check(installed, expected=201):
        return api.request("POST", check_path, {"environment_revision": 1, "installed": installed}, expected)

    def by_id(items):
        return {item["component_id"]: item for item in items}

    # Exact installed set matches the resolved set, including dependency edges.
    ok = check({"render-engine": "1.0.0", "atlas-core": "1.0.0"})
    require(ok["conformant"] is True, "identical installed set must be conformant")
    require(ok["environment_revision"] == 1 and ok["catalog_revision"] >= 1, "basis revisions not recorded")

    # Missing component, extra component, version mismatch, all in one report.
    drifted = check({"render-engine": "2.0.0", "panel-shell": "1.0.0"})
    require(drifted["conformant"] is False, "drifted set must not be conformant")
    missing = by_id(drifted["missing"])
    require("atlas-core" in missing, "missing component not detected")
    require(by_id(drifted["extra"])["panel-shell"]["actual"] == "1.0.0", "extra component not detected")
    mismatch = by_id(drifted["version_mismatches"])["render-engine"]
    require(mismatch["expected"] == "1.0.0" and mismatch["actual"] == "2.0.0", "version mismatch not detected")
    # render-engine 2.0.0 requires atlas-core ^2.0.0, but atlas-core is absent:
    # that is an internal dependency violation reported separately from drift.
    require(any(v["requires"] == "atlas-core" for v in drifted["violations"]),
            "internal dependency violation on missing dependency not detected")

    # Installed dependency violates a registered constraint.
    violated = check({"render-engine": "1.0.0", "atlas-core": "2.0.0"})
    edge_violations = [v for v in violated["violations"] if v["requires"] == "atlas-core" and v["actual"] == "2.0.0"]
    require(edge_violations and edge_violations[0]["constraint"] == "^1.0.0", "constraint violation not detected")
    require(by_id(violated["version_mismatches"])["atlas-core"]["status"] == "version_mismatch",
            "the same component is also a version mismatch")

    # An unregistered actual version is unverifiable, never silently compatible.
    unknown = check({"render-engine": "1.0.0", "atlas-core": "9.9.9"})
    unverifiable = {item["component_id"]: item for item in unknown["unverifiable"]}
    require("atlas-core" in unverifiable and unverifiable["atlas-core"]["version"] == "9.9.9",
            "unregistered release must be reported as unverifiable")
    require(unknown["conformant"] is False, "unverifiable release must block conformant result")
    require(not any(v["requires"] == "atlas-core" for v in unknown["violations"]),
            "an edge to an unregistered release cannot be proven violated")

    # Unregistered version of a component that a registered release depends on:
    # the edge is recorded as unverifiable rather than assumed satisfied.
    panel = api.request("POST", "/api/v1/environments",
                        {"id": "panel-lab", "name": "面板实验", "roots": {"panel-shell": "*"}}, 201)
    panel_drift = api.request("POST", "/api/v1/environments/panel-lab/drift-checks",
                              {"environment_revision": panel["revision"],
                               "installed": {"panel-shell": "1.0.0", "render-engine": "7.7.7",
                                             "atlas-core": "2.0.0"}}, 201)
    edges = [item for item in panel_drift["unverifiable"] if item.get("edge")]
    require(any(item["edge"]["from"] == "panel-shell" and item["edge"]["to"] == "render-engine" for item in edges),
            "dependency edge on unregistered release must be unverifiable")

    # Stale environment revision is rejected without creating a record.
    api.request("POST", check_path,
                {"environment_revision": 2, "installed": {"render-engine": "1.0.0", "atlas-core": "1.0.0"}}, 409)

    # Old records keep their original basis after the environment changes.
    old_id = ok["id"]
    body = {"environment_id": "integration", "base_revision": 1,
            "roots": {"render-engine": "2.0.0"}, "reason": "升级以验证漂移依据保留"}
    plan = api.request("POST", "/api/v1/plans", body, 201)
    ready = api.request("POST", f"/api/v1/plans/{plan['id']}/validate", {"revision": 1})
    applied = api.request("POST", f"/api/v1/plans/{plan['id']}/apply", {"revision": ready["revision"]})
    require(applied["environment"]["revision"] == 2, "environment did not advance")
    stored = api.request("GET", "/api/v1/drift-checks/" + old_id)
    require(stored["environment_revision"] == 1 and stored["conformant"] is True,
            "old drift record lost its original basis")
    listing = api.request("GET", "/api/v1/drift-checks?environment_id=integration")
    require(listing["total"] >= 4 and all(item["environment_id"] == "integration" for item in listing["items"]),
            "drift check listing or filter is wrong")
    api.stop()
    api.start()
    require(api.request("GET", "/api/v1/drift-checks/" + old_id)["conformant"] is True,
            "drift record lost after restart")


def drift_legacy(api, directory):
    # Regression for upgrading a service whose state was written by the
    # baseline build: its state.json predates drift checks and has no
    # drift_checks collection. Seed exactly that baseline-shaped file.
    state_dir = Path(directory) / "state"
    state_dir.mkdir(parents=True)
    baseline = {
        "schema": 1,
        "revision": 5,
        "catalog": {
            "revision": 4,
            "components": {
                "alpha-core": {"id": "alpha-core", "name": "alpha", "description": "",
                               "created_at": "2026-09-01T00:00:00Z"},
                "beta-tool": {"id": "beta-tool", "name": "beta", "description": "",
                              "created_at": "2026-09-01T00:00:01Z"},
            },
            "releases": {
                "alpha-core": {"1.0.0": {"component_id": "alpha-core", "version": "1.0.0",
                                         "requires": {}, "state": "available",
                                         "created_at": "2026-09-01T00:00:02Z"}},
                "beta-tool": {"1.0.0": {"component_id": "beta-tool", "version": "1.0.0",
                                        "requires": {"alpha-core": "^1.0.0"}, "state": "available",
                                        "created_at": "2026-09-01T00:00:03Z"}},
            },
        },
        "environments": {
            "legacy-env": {"id": "legacy-env", "name": "遗留环境",
                           "roots": {"beta-tool": "1.0.0"},
                           "resolved": {"beta-tool": "1.0.0", "alpha-core": "1.0.0"},
                           "revision": 1,
                           "created_at": "2026-09-01T00:00:04Z",
                           "updated_at": "2026-09-01T00:00:04Z"},
        },
        "plans": {},
        "events": [
            {"sequence": 1, "kind": "component", "entity_id": "alpha-core", "action": "created",
             "at": "2026-09-01T00:00:00Z"},
            {"sequence": 2, "kind": "component", "entity_id": "beta-tool", "action": "created",
             "at": "2026-09-01T00:00:01Z"},
            {"sequence": 3, "kind": "release", "entity_id": "alpha-core@1.0.0", "action": "added",
             "at": "2026-09-01T00:00:02Z"},
            {"sequence": 4, "kind": "release", "entity_id": "beta-tool@1.0.0", "action": "added",
             "at": "2026-09-01T00:00:03Z"},
            {"sequence": 5, "kind": "environment", "entity_id": "legacy-env", "action": "created",
             "at": "2026-09-01T00:00:04Z"},
        ],
    }
    (state_dir / "state.json").write_text(json.dumps(baseline, ensure_ascii=False))
    api.start()
    # Reaching readiness already proves the old state file loaded instead of
    # being rejected for the missing collection.
    env = api.request("GET", "/api/v1/environments/legacy-env")
    require(env["revision"] == 1 and env["resolved"]["alpha-core"] == "1.0.0",
            "legacy environment did not survive the upgrade load")
    listing = api.request("GET", "/api/v1/drift-checks")
    require(listing["total"] == 0, "migrated state must start with no drift records")
    check_path = "/api/v1/environments/legacy-env/drift-checks"
    ok = api.request("POST", check_path,
                     {"environment_revision": 1,
                      "installed": {"beta-tool": "1.0.0", "alpha-core": "1.0.0"}}, 201)
    require(ok["conformant"] is True and ok["environment_revision"] == 1 and ok["catalog_revision"] == 4,
            "drift check against upgraded baseline state has wrong basis")
    drifted = api.request("POST", check_path,
                          {"environment_revision": 1,
                           "installed": {"beta-tool": "1.0.0", "alpha-core": "2.0.0"}}, 201)
    unverifiable = {item["component_id"] for item in drifted["unverifiable"]}
    require(drifted["conformant"] is False and "alpha-core" in unverifiable,
            "unregistered release on upgraded state must be unverifiable")
    require(api.request("GET", "/api/v1/environments/legacy-env")["revision"] == 1,
            "drift check after upgrade must not modify the environment")
    api.stop()
    api.start()
    records = api.request("GET", "/api/v1/drift-checks")
    require(records["total"] == 2, "drift records created on migrated state did not persist")
    stored = api.request("GET", "/api/v1/drift-checks/" + ok["id"])
    require(stored["environment_revision"] == 1 and stored["catalog_revision"] == 4 and stored["conformant"] is True,
            "persisted drift record lost its basis after restart")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "drift", "drift-legacy"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            if args.workflow == "drift-legacy":
                # This workflow seeds a baseline-shaped state.json first and
                # starts the service itself.
                drift_legacy(api, directory)
            else:
                api.start()
                {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "drift": drift}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
