#!/usr/bin/env python3
"""Unified verification entry: build, vet, and HTTP smoke checks.

Stages run in a fixed order and the first failure stops the run: the
failing stage is reported and the exit status is non-zero. Smoke stages
start a temporary service and remove their temporary data themselves;
when this script is interrupted it forwards SIGINT to the active stage's
process group so that cleanup still runs before the script exits.
"""

import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent

STAGES = [
    ("build", ["go", "build", "-o", "build/compat-server", "./cmd/server"]),
    ("vet", ["go", "vet", "./..."]),
    ("smoke-catalog", [sys.executable, "checks/workflow.py", "catalog"]),
    ("smoke-resolve", [sys.executable, "checks/workflow.py", "resolve"]),
    ("smoke-upgrade", [sys.executable, "checks/workflow.py", "upgrade"]),
]


def exit_on_signal(signum, frame):
    sys.exit(128 + signum)


def stop_child(child):
    """Interrupt the stage's process group, then force-kill as a fallback."""
    try:
        os.killpg(child.pid, signal.SIGINT)
    except (ProcessLookupError, PermissionError):
        return
    try:
        child.wait(timeout=15)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            pass


def run_stage(name, command):
    print(f"    {shlex.join(command)}", flush=True)
    try:
        child = subprocess.Popen(command, cwd=ROOT, start_new_session=True)
    except FileNotFoundError as error:
        print(f"verify: cannot start stage '{name}': {error}", file=sys.stderr)
        return 127
    try:
        return child.wait()
    except BaseException:
        stop_child(child)
        raise


def main():
    signal.signal(signal.SIGTERM, exit_on_signal)
    total = len(STAGES)
    for index, (name, command) in enumerate(STAGES, 1):
        print(f"==> [{index}/{total}] {name}", flush=True)
        code = run_stage(name, command)
        if code != 0:
            print(f"verify: FAILED at stage [{index}/{total}] '{name}' "
                  f"(exit code {code}); remaining stages skipped", file=sys.stderr)
            return code if code > 0 else 1
    print(f"verify: all {total} stages passed", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("verify: interrupted", file=sys.stderr)
        sys.exit(130)
