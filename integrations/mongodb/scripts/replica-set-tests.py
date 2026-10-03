#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root.
"""Required MongoDB 8.0.15 snapshot/observation lane; never uses a shared server."""

import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import time

IMAGE = "mongo:8.0.15@sha256:f4d54619262ae3bc6a0a8efbebcef970b87b8ad70697479a75ce308a6f400158"
LABEL = "io.cratis.arc.mongodb-test-owner"


def provider_test_command(selection=None):
    # One full required tagged run, including observations. Verbose progress
    # exposes an overdue test; missing prerequisites fail inside liveProvider.
    args = ["go", "test", "-v", "-tags=integration", "-count=1", "-timeout=165s"]
    if selection is not None:
        args.extend(["-run", selection])
    return args + ["./..."]


def command(args, deadline, check=True):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("provider phase deadline exceeded")
    result = subprocess.run(args, text=True, capture_output=True, timeout=remaining, check=False)
    if check and result.returncode:
        raise RuntimeError(f"{args[0]} failed ({result.returncode}): {result.stderr}")
    return result


def inspect(reference, deadline):
    return json.loads(command(["docker", "inspect", reference], deadline).stdout)[0]


def save(path, state):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def cleanup(path, state, failed):
    # Logs precede removal, including signal/startup/test failure paths. Only the
    # exact ID (or collision-resistant name during create) with our label is owned.
    deadline = time.monotonic() + 15
    reference = state.get("container_id") or state["name"]
    lookup = command(["docker", "inspect", reference], deadline, check=False)
    if lookup.returncode:
        # Docker may be unavailable, so a failed lookup is NOT proof of removal.
        state["cleanup"] = "container lookup failed; inspect manually"
        save(path, state)
        raise RuntimeError(state["cleanup"])
    container = json.loads(lookup.stdout)[0]
    if container["Config"].get("Labels", {}).get(LABEL) != state["token"]:
        raise RuntimeError("refusing cleanup: ownership token mismatch")
    identifier = container["Id"]
    if state.get("container_id") and identifier != state["container_id"]:
        raise RuntimeError("refusing cleanup: container ID mismatch")
    log_failure = None
    if failed:
        log_path = path.with_suffix(".log")
        try:
            logs = command(["docker", "logs", "--tail", "200", identifier], min(deadline, time.monotonic() + 4), check=False)
            log_path.write_text(logs.stdout + logs.stderr, encoding="utf-8")
            if logs.returncode:
                log_failure = RuntimeError("Docker log collection failed")
        except Exception as error:
            log_failure = error
            log_path.write_text(f"Failure log collection failed: {error}\n", encoding="utf-8")
        print(f"Provider failure logs: {log_path}", flush=True)
    command(["docker", "rm", "--force", "--volumes", identifier], deadline)
    state["container_id"] = identifier
    state["cleanup"] = "exact owned container removed (including anonymous volumes)"
    save(path, state)
    if log_failure is not None:
        raise log_failure


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-file", type=Path, required=True)
    parser.add_argument("--cleanup-only", action="store_true")
    parser.add_argument("--run", help="explicit diagnostic Go test regexp; omit for the required full lane")
    args = parser.parse_args()
    path = args.state_file.resolve()
    if args.cleanup_only:
        state = json.loads(path.read_text(encoding="utf-8"))
        if state.get("cleanup", "").startswith("exact owned container removed"):
            return 0
        cleanup(path, state, True)
        return 0
    if path.exists():
        raise RuntimeError("state file already exists; use a fresh task path")
    if not shutil.which("docker") or not shutil.which("go"):
        raise RuntimeError("required Docker and local Go executables are missing")
    token = secrets.token_hex(16)
    state = {"token": token, "name": "arc-mongodb-" + token, "image": IMAGE}
    created = False
    test = None
    failed = True
    exit_code = 1

    def interrupted(signum, _frame):
        raise InterruptedError(f"provider harness interrupted by signal {signum}")

    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupted)
    try:
        startup = time.monotonic() + 90
        command(["docker", "version"], startup)
        command(["docker", "pull", IMAGE], startup)
        image = inspect(IMAGE, startup)
        if IMAGE.split("@", 1)[1] not in [item.split("@", 1)[1] for item in image["RepoDigests"]]:
            raise RuntimeError("image digest verification failed")
        state["image_id"] = image["Id"]
        state["repo_digests"] = image["RepoDigests"]
        save(path, state)
        # Save ownership before create. If Docker creates but its response is lost,
        # finally locates only this random name and verifies the token before rm.
        created = True
        identifier = command([
            "docker", "create", "--name", state["name"], "--label", f"{LABEL}={token}",
            "--publish", "127.0.0.1::27017", image["Id"], "--replSet", "arc_provider",
            "--bind_ip_all", "--setParameter", "enableTestCommands=1",
        ], startup).stdout.strip()
        state["container_id"] = identifier
        save(path, state)
        container = inspect(identifier, startup)
        if container["Image"] != image["Id"] or container["Config"]["Labels"][LABEL] != token:
            raise RuntimeError("created container identity mismatch")
        command(["docker", "start", identifier], startup)
        initiation = 'if (!db.adminCommand({hello:1}).setName) { rs.initiate({_id:"arc_provider",members:[{_id:0,host:"localhost:27017"}]}); }'
        while True:
            attempt = command(["docker", "exec", identifier, "mongosh", "--quiet", "--eval", initiation], min(startup, time.monotonic() + 5), check=False)
            if attempt.returncode == 0:
                break
            if time.monotonic() >= startup:
                raise TimeoutError("replica-set initiation timed out")
            time.sleep(0.25)
        while True:
            attempt = command(["docker", "exec", identifier, "mongosh", "--quiet", "--eval", 'quit(db.adminCommand({hello:1}).isWritablePrimary ? 0 : 1)'], min(startup, time.monotonic() + 5), check=False)
            if attempt.returncode == 0:
                break
            time.sleep(0.25)
        container = inspect(identifier, startup)
        bindings = container["NetworkSettings"]["Ports"]["27017/tcp"]
        if len(bindings) != 1 or bindings[0]["HostIp"] != "127.0.0.1":
            raise RuntimeError("test server must have exactly one loopback binding")
        state["uri"] = f'mongodb://127.0.0.1:{bindings[0]["HostPort"]}/?directConnection=true&replicaSet=arc_provider'
        save(path, state)
        print(json.dumps(state, indent=2), flush=True)
        environment = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local",
                           ARC_MONGODB_TEST_URI=state["uri"], ARC_MONGODB_TEST_OWNER=token,
                           ARC_MONGODB_TEST_IMAGE_ID=image["Id"], ARC_MONGODB_TEST_IMAGE=IMAGE)
        module = Path(__file__).resolve().parent.parent
        test = subprocess.Popen(provider_test_command(args.run), cwd=module, env=environment)
        exit_code = test.wait(timeout=180)
        failed = exit_code != 0
    except (Exception, KeyboardInterrupt) as error:
        print(f"Required MongoDB provider lane failed: {error}", file=sys.stderr, flush=True)
    finally:
        for signum in (signal.SIGINT, signal.SIGTERM):
            signal.signal(signum, signal.SIG_IGN)
        if test is not None and test.poll() is None:
            test.kill()
            test.wait(timeout=3)
        if created:
            try:
                cleanup(path, state, failed)
            except Exception as error:
                print(f"Owned cleanup failed: {error}; state: {path}", file=sys.stderr)
                exit_code = 1
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
