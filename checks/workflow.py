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


def component(api, name, body=None):
    payload = body or {"id": name, "name": name, "description": ""}
    return api.request("POST", "/api/v1/components", payload, 201)


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


def tighten_policy(directory, component_id, allowed_consumers):
    """Edit the persisted component policy while the server is stopped."""
    path = Path(directory) / "state" / "state.json"
    data = json.loads(path.read_text())
    data["catalog"]["components"][component_id]["allowed_consumers"] = allowed_consumers
    data["catalog"]["visibility_revision"] = data["catalog"].get("visibility_revision", 0) + 1
    path.write_text(json.dumps(data))


def visibility(api):
    # Same family: an internal component stays reachable through a public
    # family entry point and cannot be named directly from a root request.
    component(api, "core-engine", {"id": "core-engine", "name": "core-engine",
                                   "family": "core", "visibility": "internal"})
    component(api, "core-facade", {"id": "core-facade", "name": "core-facade",
                                   "family": "core"})
    component(api, "stranger-app")
    release(api, "core-engine", "1.0.0")
    release(api, "core-facade", "1.0.0", {"core-engine": "^1.0.0"})
    # An edge-level internal declaration on a same-family edge is accepted.
    release(api, "core-facade", "1.1.0",
            {"core-engine": {"constraint": "^1.0.0", "visibility": "internal"}})
    facade = api.request("GET", "/api/v1/components/core-facade/releases")
    require(any(item.get("internal_dependencies") == ["core-engine"] for item in facade["items"]),
            "internal edge declaration was not persisted")
    # An internal edge assertion against a public target is invalid input.
    api.request("POST", "/api/v1/components/stranger-app/releases",
                {"version": "1.0.0",
                 "requires": {"core-facade": {"constraint": "*", "visibility": "internal"}}}, 400)
    result = api.request("POST", "/api/v1/resolve", {"roots": {"core-facade": "*"}})
    require(result["resolved"]["core-engine"] == "1.0.0",
            "internal component must resolve through a same-family entry point")
    api.request("POST", "/api/v1/resolve", {"roots": {"core-engine": "*"}}, 422)
    api.request("POST", "/api/v1/components/stranger-app/releases",
                {"version": "1.0.0", "requires": {"core-engine": "*"}}, 422)

    # An internal component without a family is rejected at registration.
    api.request("POST", "/api/v1/components",
                {"id": "bad-internal", "name": "bad", "visibility": "internal"}, 400)

    # Allowed consumer grant via a separately created internal secret. The
    # granted component must exist before it can appear on the allow list.
    component(api, "trusted-app")
    component(api, "secret-store", {"id": "secret-store", "name": "secret-store",
                                    "family": "secret-team", "visibility": "internal",
                                    "allowed_consumers": ["trusted-app"]})
    release(api, "secret-store", "1.0.0")
    release(api, "trusted-app", "1.0.0", {"secret-store": "*"})
    result = api.request("POST", "/api/v1/resolve", {"roots": {"trusted-app": "*"}})
    require(result["resolved"]["secret-store"] == "1.0.0",
            "component on the allowed consumer list was rejected")

    # Cross-family cycle back edge: registration refuses the illegal edge even
    # though the internal engine is reachable through the facade.
    component(api, "partner-bridge", {"id": "partner-bridge", "name": "partner-bridge",
                                      "family": "partner"})
    release(api, "core-engine", "2.0.0", {"partner-bridge": "^1.0.0"})
    release(api, "partner-bridge", "1.0.0", {"core-engine": "^1.0.0"}, expected=422)

    # facade 2.x pulls engine 2.x, which needs partner-bridge; that bridge has
    # no legal release (its back edge was refused), so resolution must backtrack
    # to engine 1.x while keeping the newer, otherwise compatible facade.
    release(api, "core-facade", "2.0.0", {"core-engine": "*"})
    result = api.request("POST", "/api/v1/resolve", {"roots": {"core-facade": "*"}})
    require(result["resolved"]["core-facade"] == "2.0.0"
            and result["resolved"]["core-engine"] == "1.0.0",
            "solver did not backtrack to a visibility-legal set")

    # Plan validation and application enforce the same boundary.
    env = api.request("POST", "/api/v1/environments",
                      {"id": "stage", "name": "stage", "roots": {"core-facade": "1.0.0"}}, 201)
    require(env["resolved"]["core-engine"] == "1.0.0", "environment lost internal selection")
    illegal = api.request("POST", "/api/v1/plans",
                          {"environment_id": "stage", "base_revision": 1,
                           "roots": {"core-engine": "*"}, "reason": "bypass"}, 201)
    api.request("POST", f"/api/v1/plans/{illegal['id']}/validate", {"revision": 1}, 422)
    require(api.request("GET", f"/api/v1/plans/{illegal['id']}")["state"] == "draft",
            "failed validation must keep the plan draft")
    legal = api.request("POST", "/api/v1/plans",
                        {"environment_id": "stage", "base_revision": 1,
                         "roots": {"core-facade": "*"}, "reason": "via facade"}, 201)
    ready = api.request("POST", f"/api/v1/plans/{legal['id']}/validate", {"revision": 1})
    require(ready["visibility_revision"] >= 1, "validated plan must record the visibility revision")
    applied = api.request("POST", f"/api/v1/plans/{legal['id']}/apply",
                          {"revision": ready["revision"]})
    require(applied["environment"]["resolved"]["core-engine"] == "1.0.0",
            "application of an internal selection failed")
    api.request("POST", "/api/v1/components/core-engine/releases/1.0.0/withdraw", {}, 409)

    # Tighten the secret-store policy while stopped. The historical environment
    # stays readable and bootable; a new resolution that reaches the now-revoked
    # consumer grant is rejected at decision time.
    secret_env = api.request("POST", "/api/v1/environments",
                             {"id": "secret-stage", "name": "secret-stage",
                              "roots": {"trusted-app": "1.0.0"}}, 201)
    require(secret_env["visibility_revision"] >= 1,
            "environment must record the visibility revision")
    api.stop()
    tighten_policy(api.directory, "secret-store", [])
    api.start()
    stage = api.request("GET", "/api/v1/environments/secret-stage")
    require(stage["resolved"]["secret-store"] == "1.0.0",
            "environment using an internal implementation became unreadable after tightening")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"trusted-app": "*"}}, 422)
    require(result["error"]["code"] == "visibility_denied",
            "new resolution must be rejected after the grant is revoked")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "visibility"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "visibility": visibility}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
