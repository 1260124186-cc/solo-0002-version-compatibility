#!/usr/bin/env python3
"""Bounded operational smoke checks through the running HTTP service."""

import argparse
import hashlib
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


def manifest_digest(m):
    """Recompute the documented canonical digest outside the Go service."""
    content = {
        "format": m["format"],
        "source": m["source"],
        "source_id": m["source_id"],
        "catalog_revision": m["catalog_revision"],
        "roots": {key: m["roots"][key] for key in sorted(m["roots"])},
        "resolved": {key: m["resolved"][key] for key in sorted(m["resolved"])},
        "releases": [
            {"component_id": r["component_id"], "version": r["version"],
             "requires": {key: r["requires"][key] for key in sorted(r["requires"])}}
            for r in sorted(m["releases"], key=lambda r: (r["component_id"], r["version"]))
        ],
    }
    data = json.dumps(content, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    return "sha256:" + hashlib.sha256(data).hexdigest()


def start_peer(directory, name):
    path = Path(directory) / name
    path.mkdir()
    peer = RunningService(path)
    peer.start()
    return peer


def manifest(api):
    for name in ("atlas-core", "render-engine", "panel-shell"):
        component(api, name)
    release(api, "atlas-core", "1.0.0")
    release(api, "atlas-core", "2.0.0")
    release(api, "render-engine", "2.0.0", {"atlas-core": ">=2.0.0 <3.0.0"})
    release(api, "panel-shell", "1.0.0", {"render-engine": "^2.0.0"})
    env = api.request("POST", "/api/v1/environments",
                      {"id": "handoff", "name": "交付环境", "roots": {"panel-shell": "1.0.0"}}, 201)
    require(env["resolved"] == {"panel-shell": "1.0.0", "render-engine": "2.0.0", "atlas-core": "2.0.0"},
            "initial environment resolution failed")
    latest = api.request("GET", "/api/v1/events")["latest"]

    m = api.request("GET", "/api/v1/environments/handoff/manifest")
    require(m["format"] == 1 and m["source"] == "environment" and m["source_id"] == "handoff",
            "manifest identity is wrong")
    require(m["roots"] == {"panel-shell": "1.0.0"}, "manifest roots are wrong")
    require(m["resolved"]["atlas-core"] == "2.0.0", "manifest resolved set is wrong")
    ids = [r["component_id"] for r in m["releases"]]
    require(ids == sorted(ids) and len(ids) == 3, "manifest releases are not canonical")
    requires = {r["component_id"]: r["requires"] for r in m["releases"]}
    require(requires["render-engine"] == {"atlas-core": ">=2.0.0 <3.0.0"}, "manifest lost a dependency definition")
    require(m["digest"] == manifest_digest(m), "digest is not reproducible from the documented canonical form")

    report = api.request("POST", "/api/v1/manifests/verify", m)
    require(report["valid"] and report["digest_match"] and report["releases_checked"] == 3
            and report["issues"] == [], "self verification failed")
    require(api.request("GET", "/api/v1/environments/handoff")["revision"] == 1,
            "manifest flow mutated the environment")
    require(api.request("GET", "/api/v1/events")["latest"] == latest, "manifest flow recorded events")

    plan = api.request("POST", "/api/v1/plans", {"environment_id": "handoff", "base_revision": 1,
                                                 "roots": {"render-engine": "2.0.0"}, "reason": "移除面板"}, 201)
    api.request("GET", f"/api/v1/plans/{plan['id']}/manifest", expected=409)
    api.request("POST", f"/api/v1/plans/{plan['id']}/validate", {"revision": 1})
    pm = api.request("GET", f"/api/v1/plans/{plan['id']}/manifest")
    require(pm["source"] == "plan" and pm["resolved"] == {"render-engine": "2.0.0", "atlas-core": "2.0.0"},
            "plan manifest is wrong")
    require(api.request("POST", "/api/v1/manifests/verify", pm)["valid"], "plan manifest did not verify")
    api.request("GET", "/api/v1/environments/unknown/manifest", expected=404)

    tampered = dict(m, resolved={**m["resolved"], "atlas-core": "1.0.0"})
    report = api.request("POST", "/api/v1/manifests/verify", tampered)
    require(not report["valid"] and not report["digest_match"], "tampering was not detected")
    require([i["kind"] for i in report["issues"]] == ["content_corrupted"], "tampering misreported")

    broken = dict(m, releases=m["releases"][:-1])
    broken["digest"] = manifest_digest(broken)
    report = api.request("POST", "/api/v1/manifests/verify", broken)
    require(report["digest_match"] and [i["kind"] for i in report["issues"]] == ["invalid_manifest"],
            "structural damage misreported")
    require("render-engine" in report["issues"][0]["detail"], "structural issue is not specific")

    report = api.request("POST", "/api/v1/manifests/verify", dict(m, format=2))
    require([i["kind"] for i in report["issues"]] == ["unsupported_format"], "unsupported format misreported")
    api.request("POST", "/api/v1/manifests/verify", dict(m, bogus=1), expected=400)

    peer = start_peer(api.directory, "peer-compatible")
    try:
        for name in ("extra-lib", "atlas-core", "render-engine", "panel-shell"):
            component(peer, name)
        release(peer, "extra-lib", "1.0.0")
        release(peer, "atlas-core", "1.0.0")
        release(peer, "atlas-core", "2.0.0")
        release(peer, "render-engine", "2.0.0", {"atlas-core": ">=2.0.0 <3.0.0"})
        release(peer, "panel-shell", "1.0.0", {"render-engine": "^2.0.0"})
        report = peer.request("POST", "/api/v1/manifests/verify", m)
        require(report["valid"] and report["releases_checked"] == 3,
                "compatible peer rejected the manifest: " + json.dumps(report))
    finally:
        peer.stop()

    peer = start_peer(api.directory, "peer-divergent")
    try:
        component(peer, "atlas-core")
        component(peer, "render-engine")
        release(peer, "atlas-core", "2.0.0")
        release(peer, "render-engine", "2.0.0", {"atlas-core": "~2.0.0"})
        report = peer.request("POST", "/api/v1/manifests/verify", m)
        require(not report["valid"] and report["digest_match"], "divergent peer report is wrong")
        issues = {(i["kind"], i["component_id"]) for i in report["issues"]}
        require(issues == {("missing_release", "panel-shell"), ("requires_mismatch", "render-engine")},
                "issues are not specific: " + json.dumps(report))
    finally:
        peer.stop()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "manifest"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "manifest": manifest}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
