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


def change_sets(api):
    populate(api)
    first_env = api.request("POST", "/api/v1/environments", {"id": "canary", "name": "金丝雀", "roots": {"render-engine": "1.0.0"}}, 201)
    second_env = api.request("POST", "/api/v1/environments", {"id": "stable", "name": "稳定", "roots": {"render-engine": "1.0.0"}}, 201)
    require(first_env["revision"] == 1 and second_env["revision"] == 1, "initial environment revisions are wrong")
    body = {"base_revision": 1, "roots": {"render-engine": "2.0.0"}, "reason": "批量升级"}
    first_plan = api.request("POST", "/api/v1/plans", {**body, "environment_id": "canary"}, 201)
    second_plan = api.request("POST", "/api/v1/plans", {**body, "environment_id": "stable"}, 201)
    draft_plan = api.request("POST", "/api/v1/plans", {**body, "environment_id": "canary"}, 201)
    first, second, draft = first_plan["id"], second_plan["id"], draft_plan["id"]

    # Input boundaries: at least two plans, no duplicate entries, no unknown plans.
    api.request("POST", "/api/v1/change-sets", {"plan_ids": [first], "reason": "批量升级"}, 400)
    api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, first], "reason": "批量升级"}, 400)
    api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, "plan-missing"], "reason": "批量升级"}, 404)
    # The same environment must not appear twice.
    api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, draft], "reason": "批量升级"}, 400)
    # Every referenced plan must still be usable.
    api.request("POST", "/api/v1/plans/" + draft + "/cancel", {"revision": 1})
    api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, draft], "reason": "批量升级"}, 409)    # Draft sets cannot be validated while a member plan is still draft.
    created = api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, second], "reason": "批量升级"}, 201)
    set_path = "/api/v1/change-sets/" + created["id"]
    require(created["state"] == "draft" and len(created["entries"]) == 2, "change set was not created as draft")
    api.request("POST", set_path + "/validate", {"revision": 1}, 409)

    # Each member plan is validated independently first, then the set records
    # the plan/environment/catalog revision basis.
    first_ready = api.request("POST", "/api/v1/plans/" + first + "/validate", {"revision": 1})
    second_ready = api.request("POST", "/api/v1/plans/" + second + "/validate", {"revision": 1})
    # Validating a member plan invalidates a draft set that already bundled it.
    require(api.request("GET", set_path)["state"] == "invalidated", "set did not invalidate when a member plan changed")
    recreated = api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, second], "reason": "批量升级"}, 201)
    set_path = "/api/v1/change-sets/" + recreated["id"]
    ready_set = api.request("POST", set_path + "/validate", {"revision": 1})
    require(ready_set["state"] == "ready", "change set did not become ready")
    entries = {entry["environment_id"]: entry for entry in ready_set["entries"]}
    require(entries["canary"]["plan_revision"] == first_ready["revision"] == 2, "plan revision basis missing")
    require(entries["canary"]["environment_revision"] == 1 and entries["canary"]["catalog_revision"] >= 1, "environment or catalog basis missing")

    # A stale catalog basis rejects the whole group without touching anything.
    release(api, "atlas-core", "3.0.0")
    api.request("POST", set_path + "/apply", {"revision": ready_set["revision"]}, 409)
    require(api.request("GET", "/api/v1/environments/canary")["revision"] == 1, "stale set partially applied")
    require(api.request("GET", set_path)["state"] == "ready", "rejected apply changed the set")

    # Re-validate plans and set; an invalidated set is terminal, so a new set
    # is created once every member basis is fresh again.
    first_ready = api.request("POST", "/api/v1/plans/" + first + "/validate", {"revision": first_ready["revision"]})
    second_ready = api.request("POST", "/api/v1/plans/" + second + "/validate", {"revision": second_ready["revision"]})
    require(api.request("GET", set_path)["state"] == "invalidated", "set did not invalidate on member re-validation")
    recreated = api.request("POST", "/api/v1/change-sets", {"plan_ids": [first, second], "reason": "批量升级"}, 201)
    set_path = "/api/v1/change-sets/" + recreated["id"]
    ready_set = api.request("POST", set_path + "/validate", {"revision": 1})
    applied = api.request("POST", set_path + "/apply", {"revision": ready_set["revision"]})
    require(applied["change_set"]["state"] == "applied", "change set was not applied")
    require(len(applied["applications"]) == 2, "group application did not cover both plans")
    for env_id in ("canary", "stable"):
        env = api.request("GET", "/api/v1/environments/" + env_id)
        require(env["revision"] == 2 and env["resolved"]["atlas-core"] == "2.0.0", f"environment {env_id} was not upgraded")
    require(api.request("GET", "/api/v1/plans/" + first)["state"] == "applied", "member plan state did not update")
    api.request("POST", set_path + "/apply", {"revision": applied["change_set"]["revision"]}, 409)

    # Cancelling a set leaves its independent plans untouched.
    cancel_body = {"base_revision": 2, "roots": {"render-engine": "1.0.0"}, "reason": "回滚预演"}
    rollback_first = api.request("POST", "/api/v1/plans", {**cancel_body, "environment_id": "canary"}, 201)
    rollback_second = api.request("POST", "/api/v1/plans", {**cancel_body, "environment_id": "stable"}, 201)
    api.request("POST", "/api/v1/plans/" + rollback_first["id"] + "/validate", {"revision": 1})
    api.request("POST", "/api/v1/plans/" + rollback_second["id"] + "/validate", {"revision": 1})
    cancellable = api.request("POST", "/api/v1/change-sets", {"plan_ids": [rollback_first["id"], rollback_second["id"]], "reason": "回滚预演"}, 201)
    cancel_path = "/api/v1/change-sets/" + cancellable["id"]
    ready_cancel = api.request("POST", cancel_path + "/validate", {"revision": 1})
    cancelled = api.request("POST", cancel_path + "/cancel", {"revision": ready_cancel["revision"]})
    require(cancelled["state"] == "cancelled", "set cancellation failed")
    require(api.request("GET", "/api/v1/plans/" + rollback_first["id"])["state"] == "ready", "cancelling the set cancelled a standalone plan")

    # A single-plan application invalidates any other live set referencing it.
    solo = api.request("POST", "/api/v1/plans", {**cancel_body, "environment_id": "canary"}, 201)
    solo_ready = api.request("POST", "/api/v1/plans/" + solo["id"] + "/validate", {"revision": 1})
    grouped = api.request("POST", "/api/v1/change-sets", {"plan_ids": [solo["id"], rollback_second["id"]], "reason": "共享方案"}, 201)
    grouped_path = "/api/v1/change-sets/" + grouped["id"]
    api.request("POST", grouped_path + "/validate", {"revision": 1})
    api.request("POST", "/api/v1/plans/" + solo["id"] + "/apply", {"revision": solo_ready["revision"]})
    require(api.request("GET", grouped_path)["state"] == "invalidated", "individual apply did not invalidate the set")
    require(api.request("GET", grouped_path)["invalidated_reason"], "invalidation reason missing")

    api.stop()
    api.start()
    require(api.request("GET", set_path)["state"] == "applied", "applied set lost after restart")
    require(api.request("GET", "/api/v1/environments/stable")["revision"] == 2, "environment revision lost after restart")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade", "change-sets"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade, "change-sets": change_sets}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
