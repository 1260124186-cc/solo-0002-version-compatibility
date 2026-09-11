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


def profiles(api):
    populate(api)
    body = {
        "id": "edge-stack",
        "name": "边缘标准栈",
        "description": "现场环境常用组件约束",
        "roots": {"render-engine": "1.0.0", "panel-shell": "*"},
    }
    created = api.request("POST", "/api/v1/profiles", body, 201)
    require(created["revision"] == 1 and created["state"] == "active"
            and len(created["revisions"]) == 1, "profile was not created at revision 1")
    # Saving does not require a feasible solution or existing components.
    api.request("POST", "/api/v1/profiles", {
        "id": "future-stack", "name": "future", "description": "",
        "roots": {"panel-shell": "^9.9.9", "missing-stack": "*"},
    }, 201)
    api.request("POST", "/api/v1/profiles",
                {"id": "edge-stack", "name": "dup", "description": "", "roots": {"panel-shell": "*"}}, 409)
    env = api.request("POST", "/api/v1/profiles/edge-stack/environments",
                      {"id": "edge-a", "name": "边缘环境 A"}, 201)
    require(env["resolved"] == {"panel-shell": "1.0.0", "render-engine": "1.0.0", "atlas-core": "1.0.0"},
            "environment was not solved from the profile revision")
    require(env["profile_id"] == "edge-stack" and env["profile_revision"] == 1, "environment lost profile provenance")
    # Generating from a saved but unsatisfiable profile revision fails at solve time.
    api.request("POST", "/api/v1/profiles/future-stack/environments",
                {"id": "edge-x", "name": "无解环境"}, 404)
    release(api, "panel-shell", "2.0.0", {"render-engine": "2.0.0"})
    component(api, "missing-stack")
    api.request("POST", "/api/v1/profiles/future-stack/environments",
                {"id": "edge-x", "name": "无解环境"}, 422)
    # Edit the profile to revision 2; environment generated from revision 1 must be unchanged.
    updated = api.request("PATCH", "/api/v1/profiles/edge-stack", {
        "revision": 1, "name": "边缘标准栈", "description": "放宽 panel 约束",
        "roots": {"render-engine": "*", "panel-shell": "*"},
    })
    require(updated["revision"] == 2 and len(updated["revisions"]) == 2, "profile edit did not add a revision")
    require(api.request("GET", "/api/v1/environments/edge-a")["resolved"]["panel-shell"] == "1.0.0",
            "editing a profile mutated an existing environment")
    api.request("PATCH", "/api/v1/profiles/edge-stack", {
        "revision": 1, "name": "stale", "description": "", "roots": {"panel-shell": "*"},
    }, 409)
    # Generate explicitly from the old revision and from the current one.
    old = api.request("POST", "/api/v1/profiles/edge-stack/environments",
                      {"id": "edge-old", "name": "旧修订", "profile_revision": 1}, 201)
    require(old["profile_revision"] == 1 and old["resolved"]["render-engine"] == "1.0.0",
            "explicit old revision generation failed")
    current = api.request("POST", "/api/v1/profiles/edge-stack/environments",
                          {"id": "edge-new", "name": "新修订"}, 201)
    require(current["profile_revision"] == 2 and current["resolved"]["panel-shell"] == "2.0.0",
            "default revision generation did not use the current snapshot")
    api.request("POST", "/api/v1/profiles/edge-stack/environments",
                {"id": "edge-gone", "name": "缺修订", "profile_revision": 99}, 404)
    # Deactivate: no new environments, existing ones stay available.
    deactivated = api.request("POST", "/api/v1/profiles/edge-stack/deactivate", {"revision": 2})
    require(deactivated["state"] == "inactive" and deactivated["revision"] == 3
            and deactivated["inactive_at"], "profile deactivation failed")
    api.request("POST", "/api/v1/profiles/edge-stack/environments",
                {"id": "edge-blocked", "name": "停用后禁止"}, 409)
    api.request("POST", "/api/v1/profiles/edge-stack/deactivate", {"revision": 3}, 409)
    api.request("PATCH", "/api/v1/profiles/edge-stack", {
        "revision": 3, "name": "x", "description": "", "roots": {"panel-shell": "*"},
    }, 409)
    require(api.request("GET", "/api/v1/environments/edge-a")["resolved"]["panel-shell"] == "1.0.0",
            "deactivating a profile broke an existing environment")
    listed = api.request("GET", "/api/v1/profiles")
    require(listed["total"] == 2, "profile listing count is wrong")
    api.stop()
    api.start()
    restarted = api.request("GET", "/api/v1/profiles/edge-stack")
    require(restarted["state"] == "inactive" and len(restarted["revisions"]) == 3,
            "profile revision history did not survive restart")
    require(api.request("GET", "/api/v1/environments/edge-a")["profile_revision"] == 1,
            "environment provenance did not survive restart")
    actions = [event["action"] for event in api.request("GET", "/api/v1/events")["items"]]
    for expected in ("created", "updated", "deactivated", "created_from_profile"):
        require(expected in actions, f"event stream is missing {expected}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "profiles"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "profiles": profiles}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
