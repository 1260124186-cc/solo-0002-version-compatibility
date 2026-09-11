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
    proof = result.get("proof")
    require(proof and proof["catalog_revision"] == result["catalog_revision"], "proof is missing or not bound to catalog revision")
    require([step["component"] for step in proof["selection"]] == ["panel-shell", "render-engine", "atlas-core"], "proof chain order is not the decision order")
    chain = {step["component"]: step for step in proof["selection"]}
    require(chain["panel-shell"]["introduced_by"] == [{"source": "root", "constraint": "*"}], "root constraint origin is wrong")
    require(chain["render-engine"]["introduced_by"] == [{"source": "panel-shell@1.0.0", "constraint": "*"}], "transitive constraint origin is wrong")
    require(chain["atlas-core"]["introduced_by"] == [{"source": "render-engine@2.0.0", "constraint": "^2.0.0"}], "chosen version is not attributed to the introducing release")
    require(chain["atlas-core"]["version"] == "2.0.0" and chain["atlas-core"]["order"] == 2, "proof step content is wrong")
    require(proof["cycles"] == [], "acyclic resolution reports cycles")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"render-engine": "*", "atlas-core": "1.0.0"}})
    require(result["resolved"]["render-engine"] == "1.0.0", "search did not backtrack")
    chain = {step["component"]: step for step in result["proof"]["selection"]}
    require(any(item["source"] == "root" and item["constraint"] == "1.0.0" for item in chain["atlas-core"]["introduced_by"]), "backtracked proof lost the root constraint")
    # atlas-core is decided first (lexicographic order), so render-engine's
    # constraint can only be verified against the choice already in place.
    require(any(item["source"] == "render-engine@1.0.0" and item["constraint"] == "^1.0.0" for item in chain["atlas-core"]["verified_against"]), "backtracked proof lost the later transitive verification")
    require(chain["render-engine"]["introduced_by"] == [{"source": "root", "constraint": "*"}], "backtracked root origin is wrong")
    result = api.request("POST", "/api/v1/resolve", {"roots": {"render-engine": "2.0.0", "atlas-core": "^1.0.0"}}, 422)
    require(result["error"]["code"] == "no_solution" and result["error"]["conflicts"], "missing conflict evidence")
    for name in ("cycle-alpha", "cycle-beta"):
        component(api, name)
    release(api, "cycle-alpha", "1.0.0", {"cycle-beta": "~1.0.0"})
    release(api, "cycle-beta", "1.0.0", {"cycle-alpha": ">=1.0.0 <2.0.0"})
    result = api.request("POST", "/api/v1/resolve", {"roots": {"cycle-alpha": "*"}})
    require(len(result["resolved"]) == 2 and len(result["edges"]) == 2, "compatible dependency cycle failed")
    proof = result["proof"]
    require(len(proof["selection"]) == 2, "proof expanded the cycle instead of closing it")
    chain = {step["component"]: step for step in proof["selection"]}
    require(chain["cycle-beta"]["introduced_by"] == [{"source": "cycle-alpha@1.0.0", "constraint": "~1.0.0"}], "cycle entry edge is missing from the chain")
    require(chain["cycle-alpha"]["verified_against"] == [{"source": "cycle-beta@1.0.0", "constraint": ">=1.0.0 <2.0.0"}], "closing cycle edge was not recorded as a later verification")
    require(len(proof["cycles"]) == 1, "cycle closure missing or duplicated")
    cycle = proof["cycles"][0]
    require(cycle["nodes"] == ["cycle-alpha", "cycle-beta"], "cycle nodes are not canonical")
    require([(edge["from"], edge["to"]) for edge in cycle["edges"]] == [("cycle-alpha", "cycle-beta"), ("cycle-beta", "cycle-alpha")], "cycle closure edge is missing")



def upgrade(api):
    populate(api)
    env = api.request("POST", "/api/v1/environments", {"id": "integration", "name": "集成环境", "roots": {"render-engine": "1.0.0"}}, 201)
    require(env["resolved"]["atlas-core"] == "1.0.0", "initial environment resolution failed")
    require(env["proof_status"] == "current" and env["proof"]["catalog_revision"] == 8, "environment did not carry a proof bound to the catalog revision in force")
    chain = {step["component"]: step for step in env["proof"]["selection"]}
    require(chain["atlas-core"]["introduced_by"] == [{"source": "render-engine@1.0.0", "constraint": "^1.0.0"}], "environment proof does not explain the transitive choice")
    body = {"environment_id": "integration", "base_revision": 1, "roots": {"render-engine": "2.0.0"}, "reason": "验证新版兼容集合"}
    first = api.request("POST", "/api/v1/plans", body, 201)
    second = api.request("POST", "/api/v1/plans", body, 201)
    require(first["proof_status"] == "absent" and "proof" not in first, "unvalidated plan must not carry a proof")
    path = "/api/v1/plans/" + first["id"]
    ready = api.request("POST", path + "/validate", {"revision": 1})
    require(ready["state"] == "ready" and len(ready["changes"]) == 2, "plan validation did not generate expected changes")
    require(ready["proof_status"] == "current" and ready["proof"]["catalog_revision"] == 8, "validated plan proof is not bound to the catalog revision in force")
    require(ready["proof"]["roots"] == {"render-engine": "2.0.0"}, "plan proof roots mismatch")
    release(api, "atlas-core", "3.0.0")
    stale = api.request("GET", path)
    require(stale["proof_status"] == "stale" and stale["proof"]["catalog_revision"] == 8, "catalog change did not invalidate the old proof explicitly")
    rejected = api.request("POST", path + "/apply", {"revision": ready["revision"]}, 409)
    require(rejected["error"]["code"] == "proof_stale", "stale proof was not rejected with a dedicated code")
    require(api.request("GET", "/api/v1/environments/integration")["revision"] == 1, "stale plan mutated environment")
    ready = api.request("POST", path + "/validate", {"revision": ready["revision"]})
    require(ready["proof_status"] == "current" and ready["proof"]["catalog_revision"] == 9, "revalidation did not rebind the proof to the new catalog revision")
    applied = api.request("POST", path + "/apply", {"revision": ready["revision"]})
    require(applied["environment"]["revision"] == 2 and applied["environment"]["resolved"]["atlas-core"] == "2.0.0", "plan application failed")
    require(applied["environment"]["proof_status"] == "current" and applied["plan"]["proof_status"] == "current", "application did not hand over the proof")
    require(len(applied["environment"]["proof"]["selection"]) == 2, "applied environment proof is incomplete")
    api.request("POST", path + "/apply", {"revision": applied["plan"]["revision"]}, 409)
    other = "/api/v1/plans/" + second["id"]
    api.request("POST", other + "/validate", {"revision": 1}, 409)
    cancelled = api.request("POST", other + "/cancel", {"revision": 1})
    require(cancelled["state"] == "cancelled" and cancelled["proof_status"] == "absent", "plan cancellation failed")
    api.request("POST", "/api/v1/components/atlas-core/releases/2.0.0/withdraw", {}, 409)
    api.stop()
    api.start()
    stored = api.request("GET", path)
    require(stored["state"] == "applied", "applied state lost after restart")
    require(stored["proof_status"] == "current" and stored["proof"]["catalog_revision"] == 9, "proof did not survive restart and revalidate")
    env = api.request("GET", "/api/v1/environments/integration")
    require(env["revision"] == 2, "environment revision lost after restart")
    require(env["proof_status"] == "current" and len(env["proof"]["selection"]) == 2, "environment proof did not survive restart")



def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workflow", choices=("catalog", "resolve", "upgrade"))
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="compat-smoke-") as directory:
        api = RunningService(directory)
        try:
            api.start()
            {"catalog": catalog, "resolve": resolve, "upgrade": upgrade}[args.workflow](api)
            print(args.workflow + ": HTTP workflow passed")
        finally:
            api.stop()


if __name__ == "__main__":
    main()
