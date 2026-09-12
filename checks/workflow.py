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


def lockfile(api):
    populate(api)
    env = api.request("POST", "/api/v1/environments",
                      {"id": "integration", "name": "集成环境", "roots": {"render-engine": "1.0.0"}}, 201)
    require(env["resolved"] == {"render-engine": "1.0.0", "atlas-core": "1.0.0"}, "initial resolution changed")
    catalog_revision = api.request("GET", "/api/v1/components")["catalog_revision"]

    first = api.request("POST", "/api/v1/environments/integration/lockfiles", {}, 201)
    require(first["format"] == 1, "lockfile format mismatch")
    require(first["environment_revision"] == 1 and first["environment_id"] == "integration", "lockfile does not name the environment revision")
    require(first["catalog_revision"] == catalog_revision, "lockfile does not record the catalog revision")
    require(first["roots"] == {"render-engine": "1.0.0"}, "lockfile roots mismatch")
    require(first["resolved"] == env["resolved"], "lockfile resolved set mismatch")
    releases = {(r["component_id"], r["version"]): r["requires"] for r in first["releases"]}
    require(releases[("render-engine", "1.0.0")] == {"atlas-core": "^1.0.0"}, "locked release definition missing")
    require(releases[("atlas-core", "1.0.0")] == {}, "locked leaf definition missing")
    sources = {name: [(s["from"], s["constraint"]) for s in reason["sources"]]
               for name, reason in first["reasons"].items()}
    require(sources["render-engine"] == [("root", "1.0.0")], "root selection rationale wrong")
    require(sources["atlas-core"] == [("render-engine@1.0.0", "^1.0.0")], "transitive selection rationale wrong")
    lock_id = first["id"]
    require(len(lock_id) > 20 and first["digest"] and len(first["digest"]) == 64, "lockfile identity or digest missing")

    # The same revision cannot be locked twice: a lockfile is immutable.
    api.request("POST", "/api/v1/environments/integration/lockfiles", {}, 409)

    # Verify, GET and export all return the identical unchanged document.
    report = api.request("GET", f"/api/v1/lockfiles/{lock_id}/verify")
    drift = report["drift"]
    require(report["lockfile"]["digest"] == first["digest"], "verify altered the lockfile")
    require(drift["digest_valid"] and drift["environment_matched"] and drift["catalog_matched"], "fresh lockfile should verify clean")
    require(drift["environment_matched_revision"] and drift["catalog_revision_matched"], "revision flags wrong on fresh lock")
    exported = api.request("GET", f"/api/v1/lockfiles/{lock_id}/export")
    require(exported["digest"] == first["digest"] and exported["releases"] == first["releases"], "export changed the artifact")

    # Upgrade the environment, then lock the new revision: distinct, traceable.
    body = {"environment_id": "integration", "base_revision": 1, "roots": {"render-engine": "2.0.0"}, "reason": "锁定第二个修订"}
    plan = api.request("POST", "/api/v1/plans", body, 201)
    ready = api.request("POST", f"/api/v1/plans/{plan['id']}/validate", {"revision": 1})
    applied = api.request("POST", f"/api/v1/plans/{plan['id']}/apply", {"revision": ready["revision"]})
    require(applied["environment"]["revision"] == 2, "environment did not advance")
    second = api.request("POST", "/api/v1/environments/integration/lockfiles", {}, 201)
    require(second["id"] != first["id"] and second["digest"] != first["digest"], "revisions must yield distinct lockfiles")
    require(second["environment_revision"] == 2 and second["resolved"]["render-engine"] == "2.0.0", "second lock captures the wrong revision")
    listed = api.request("GET", "/api/v1/lockfiles?environment_id=integration")
    require([item["environment_revision"] for item in listed["items"]] == [1, 2], "lockfile listing order is not revision sorted")

    # Catalog moves on: drift becomes visible but nothing is mutated.
    release(api, "atlas-core", "3.0.0")
    drifted = api.request("GET", f"/api/v1/lockfiles/{lock_id}/verify")["drift"]
    require(not drifted["environment_matched"] and not drifted["environment_matched_revision"], "environment drift not detected")
    require(not drifted["catalog_revision_matched"], "catalog revision drift not detected")
    require(drifted["catalog_matched"], "locked releases still present, catalog content must match")
    kinds = {change["component_id"]: change["kind"] for change in drifted["environment_changes"]}
    require(kinds == {"render-engine": "upgrade", "atlas-core": "upgrade"}, "drift changes missing: %s" % kinds)
    require(len(drifted["catalog_changes"]) == 0, "identical releases must not be reported as catalog drift")
    require(api.request("GET", "/api/v1/environments/integration")["revision"] == 2, "verification mutated the environment")
    require(api.request("GET", f"/api/v1/lockfiles/{lock_id}")["digest"] == first["digest"], "verification rewrote the lockfile")

    # A withdrawn locked release is reported as catalog drift, not corruption.
    withdrawn = api.request("POST", "/api/v1/components/render-engine/releases/1.0.0/withdraw", {})
    require(withdrawn["state"] == "withdrawn", "unused release could not be withdrawn")
    drifted = api.request("GET", f"/api/v1/lockfiles/{lock_id}/verify")["drift"]
    drift_kinds = {change["component_id"]: change["kind"] for change in drifted["catalog_changes"]}
    require(drift_kinds == {"render-engine": "withdrawn"}, "withdrawal drift not reported: %s" % drift_kinds)
    require(not drifted["catalog_matched"], "withdrawn release must break catalog match")
    require(drifted["digest_valid"], "withdrawal must not corrupt the lockfile digest")

    # An exported artifact can be verified out of band; tampering breaks the digest.
    external = api.request("POST", "/api/v1/lockfiles", first)
    require(external["lockfile"]["id"] == lock_id and external["drift"]["digest_valid"], "exported lockfile could not be verified")
    tampered = dict(first)
    tampered["resolved"] = dict(first["resolved"])
    tampered["resolved"]["atlas-core"] = "9.9.9"
    bad = api.request("POST", "/api/v1/lockfiles", tampered, 400)
    require(bad["error"]["code"] == "invalid_input", "tampered lockfile must fail its digest")
    api.request("GET", "/api/v1/lockfiles/lock-missing", expected=404)

    # Immutability and integrity survive a durable restart.
    api.stop()
    api.start()
    require(api.request("GET", f"/api/v1/lockfiles/{lock_id}")["digest"] == first["digest"], "lockfile changed after restart")
    restarted = api.request("GET", f"/api/v1/lockfiles/{lock_id}/verify")
    require(restarted["drift"]["digest_valid"], "persisted lockfile lost digest validity")
    require(len(api.request("GET", "/api/v1/lockfiles")["items"]) == 2, "lockfile count changed after restart")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "lockfile"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "lockfile": lockfile}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
