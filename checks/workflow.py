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
    def __init__(self, directory, max_steps="50000"):
        self.directory = Path(directory)
        self.process = None
        self.log = None
        self.base = ""
        self.max_steps = max_steps

    def start(self):
        self.log = (self.directory / "server.log").open("w+")
        env = dict(os.environ, COMPAT_ADDRESS="127.0.0.1:0",
                   COMPAT_DATA_DIR=str(self.directory / "state"),
                   COMPAT_MAX_STEPS=self.max_steps, COMPAT_REQUEST_TIMEOUT="10s")
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

    def raw_request(self, method, path, data):
        body = json.dumps(data).encode() if data is not None else None
        request = urllib.request.Request(self.base + path, data=body, method=method,
                                         headers={"Content-Type": "application/json"})
        try:
            response = urllib.request.urlopen(request, timeout=12)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, json.loads(response.read())


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


def build_chain(api, prefix, count):
    for i in range(count):
        component(api, prefix % i)
    for i in range(count):
        requires = {} if i == count - 1 else {prefix % (i + 1): "*"}
        release(api, prefix % i, "1.0.0", requires)


def paged_resolve(api, roots, page_budget, max_pages=1000):
    """Drive a resolution to completion, returning (status, last_payload)."""
    token = None
    for _ in range(max_pages):
        body = {"roots": roots, "step_budget": page_budget} if token is None \
            else {"continuation_token": token, "step_budget": page_budget}
        status, payload = api.raw_request("POST", "/api/v1/resolve", body)
        if status != 200:
            return status, payload
        if payload["complete"]:
            return status, payload
        token = payload["continuation_token"]
    raise RuntimeError("paged resolution did not finish")


def resume(api):
    # Boundary alignment. A chain of N components needs N expansion steps plus
    # one terminal call that observes the finished solution. With
    # COMPAT_MAX_STEPS=100, a chain of 100 must complete (reporting 101 steps)
    # identically whether resolved once or continued across requests.
    build_chain(api, "chain-%03d", 100)
    roots100 = {"chain-000": "*"}
    one_shot = api.request("POST", "/api/v1/resolve", {"roots": roots100})
    require(one_shot["complete"] and one_shot["steps"] == 101
            and len(one_shot["resolved"]) == 100,
            "chain of 100 must complete in 101 steps in one shot")

    # Step-budget contract. A page given budget b performs b expansions and
    # pauses reporting b+1 steps: the next child call's entry step is already
    # charged (precharged), but its expansion belongs to the following page.
    # The very last observation is terminal: it is admitted even when the
    # expansion budget is spent, so finishing across a boundary reports the
    # same N+1 steps as a one-shot run.
    first = api.request("POST", "/api/v1/resolve", {"roots": roots100, "step_budget": 50})
    require((not first["complete"]) and first["status"] == "budget_exhausted"
            and first["steps"] == 51 and len(first["provisional"]["selected"]) == 50,
            "a 50-expansion page must pause reporting 51 steps with 50 selections")
    require(first["resolved"] == {} and first["edges"] == [],
            "paused response must not expose a final answer")

    # The continuation has 50 expansions left; spending all 100 expansions on
    # a chain of 100 leaves only the terminal observation, which completes.
    boundary = api.request("POST", "/api/v1/resolve",
                           {"continuation_token": first["continuation_token"], "step_budget": 50})
    require(boundary["complete"] and boundary["steps"] == 101,
            "continuation across the cap boundary must complete with the one-shot step count")
    require(boundary["resolved"] == one_shot["resolved"]
            and boundary["edges"] == one_shot["edges"],
            "continued result must equal the one-shot result")

    # The same holds when the final continuation still declares a budget of 1:
    # only the terminal observation remains, which a budget never gates.
    first = api.request("POST", "/api/v1/resolve", {"roots": roots100, "step_budget": 99})
    require((not first["complete"]) and first["steps"] == 100,
            "99-expansion page must pause reporting 100 steps")
    tiny = api.request("POST", "/api/v1/resolve",
                       {"continuation_token": first["continuation_token"], "step_budget": 1})
    require(tiny["complete"] and tiny["steps"] == 101
            and tiny["resolved"] == one_shot["resolved"],
            "terminal observation must be admitted even with a one-step page")

    # A page budget already covering every expansion simply completes, proving
    # paging never changes the outcome when no boundary is crossed.
    direct = api.request("POST", "/api/v1/resolve", {"roots": roots100, "step_budget": 100})
    require(direct["complete"] and direct["steps"] == 101
            and direct["resolved"] == one_shot["resolved"],
            "an in-budget paged request must equal one-shot")

    # A chain of 101 needs 101 expansions: the cap truly rejects non-terminal
    # work at step 101, both one-shot and paged.
    build_chain(api, "longer-%03d", 101)
    roots101 = {"longer-000": "*"}
    status, direct = api.raw_request("POST", "/api/v1/resolve", {"roots": roots101})
    require(status == 422 and direct["error"]["code"] == "limit_exceeded",
            "chain of 101 must hit the global step cap in one shot")
    status, paged = paged_resolve(api, roots101, 100)
    require(status == 422 and paged["error"]["code"] == "limit_exceeded",
            "chain of 101 must hit the global step cap while paging")

    # Tokens hold no process-local state: a pause near the boundary survives
    # restart and still finishes at 101 steps.
    paused = api.request("POST", "/api/v1/resolve", {"roots": roots100, "step_budget": 99})
    token = paused["continuation_token"]
    api.stop()
    api.start()
    continued = api.request("POST", "/api/v1/resolve",
                            {"continuation_token": token, "step_budget": 1})
    require(continued["complete"] and continued["steps"] == 101
            and continued["resolved"] == one_shot["resolved"],
            "boundary resume after restart failed")

    # Any catalog change invalidates an outstanding boundary token with 409.
    paused = api.request("POST", "/api/v1/resolve", {"roots": roots100, "step_budget": 99})
    token = paused["continuation_token"]
    release(api, "chain-099", "1.1.0")
    stale = api.request("POST", "/api/v1/resolve", {"continuation_token": token}, 409)
    require(stale["error"]["code"] == "conflict", "stale token not rejected")

    # No-solution evidence is produced only at the end and is identical for a
    # one-shot and a multi-page search.
    build_chain(api, "dead-%02d", 40)
    dead_roots = {"dead-00": "*", "dead-39": "^2.0.0"}
    status, direct = api.raw_request("POST", "/api/v1/resolve", {"roots": dead_roots})
    require(status == 422 and direct["error"]["code"] == "no_solution"
            and direct["error"]["conflicts"], "missing no-solution evidence")
    status, paged = paged_resolve(api, dead_roots, 10)
    require(status == 422 and paged["error"]["code"] == "no_solution",
            "paged search must still prove infeasibility")
    require(paged["error"]["conflicts"] == direct["error"]["conflicts"],
            "paged and one-shot conflict evidence must match")

    # The 128-component node cap is enforced identically while paging. Run a
    # 129-long chain under a larger step cap via a fresh data directory.
    api.stop()
    api.max_steps = "1000"
    api.start()
    build_chain(api, "wide-%03d", 129)
    status, direct = api.raw_request("POST", "/api/v1/resolve", {"roots": {"wide-000": "*"}})
    require(status == 422 and direct["error"]["code"] == "limit_exceeded",
            "129-node chain must exceed the node cap in one shot")
    status, paged = paged_resolve(api, {"wide-000": "*"}, 100)
    require(status == 422 and paged["error"]["code"] == "limit_exceeded",
            "129-node chain must exceed the node cap while paging")


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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "resume", "upgrade"))
    args = parser.parse_args()
    max_steps = "100" if args.workflow == "resume" else "50000"
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory, max_steps=max_steps)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "resume": resume,
             "upgrade": upgrade}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
