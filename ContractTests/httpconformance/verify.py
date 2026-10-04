#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.
"""Fail closed on changed source, fixture, complete restore graph or runtime pins."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess

REVISION = "7c1e78075b737df64f69fddfaae83374f75e3612"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify(source, artifacts, output):
    fixture = Path(__file__).parent / "reference"
    hashes = {}
    for line in (source.parent / "source.sha256").read_text().splitlines():
        expected, relative = line.split("  ", 1)
        path = source / relative
        if digest(path) != expected:
            raise ValueError(f"exact source changed: {relative}")
        hashes[str(path.resolve())] = expected
    projects = (source.parent / "projects.txt").read_text().splitlines()
    if len(projects) != 6 or len(set(projects)) != 6:
        raise ValueError("incomplete source project graph")
    for relative in projects + [str(fixture / "Reference.csproj")]:
        project = source / relative
        name = project.stem
        lock_path = project.parent / "packages.lock.json"
        lock = json.loads(lock_path.read_bytes())
        assets_path = artifacts / "obj" / name / "project.assets.json"
        assets = json.loads(assets_path.read_bytes())
        if Path(assets["project"]["restore"]["projectPath"]).resolve() != project.resolve():
            raise ValueError(f"restored a different source project: {name}")
        # Every package for every restored TFM is checked, not just Arc/Fundamentals.
        expected_packages = set()
        for dependencies in lock["dependencies"].values():
            for package, entry in dependencies.items():
                if entry["type"].lower() == "project":
                    continue
                key = f"{package}/{entry['resolved']}"
                expected_packages.add(key.lower())
                library = next((v for k, v in assets["libraries"].items() if k.lower() == key.lower()), None)
                if library is None or library.get("sha512") != entry["contentHash"]:
                    raise ValueError(f"unlocked dependency: {name}: {key}")
                if package.lower() in ("cratis.arc", "cratis.arc.core"):
                    raise ValueError("NuGet Arc substitution is forbidden")
        actual_packages = {k.lower() for k, v in assets["libraries"].items() if v["type"] == "package"}
        if actual_packages != expected_packages:
            raise ValueError(f"partial restore lock inventory: {name}")
        if project != fixture / "Reference.csproj":
            pinned = Path(__file__).parent / "locks" / project.relative_to(source).parent / "packages.lock.json"
            if lock_path.read_bytes() != pinned.read_bytes():
                raise ValueError(f"source dependency lock changed: {name}")
            hashes[str(pinned.resolve())] = digest(pinned)
        hashes[str(lock_path.resolve())] = digest(lock_path)
        hashes[str(assets_path.resolve())] = digest(assets_path)
    for path in fixture.iterdir():
        if path.is_file():
            hashes[str(path.resolve())] = digest(path)
    binary = artifacts / "bin/Reference/release/Arc.Go.HttpConformance.dll"
    for path in binary.parent.iterdir():
        if path.is_file():
            hashes[str(path.resolve())] = digest(path)
    config = json.loads(binary.with_suffix(".runtimeconfig.json").read_bytes())["runtimeOptions"]
    if config["rollForward"] != "Disable" or any(f["version"] != "10.0.12" for f in config["frameworks"]):
        raise ValueError("unlocked runtime patch")
    sdk = subprocess.run(["dotnet", "--version"], cwd=fixture, check=True,
                         capture_output=True, text=True, timeout=10).stdout.strip()
    runtime = subprocess.run(["dotnet", str(binary), "--provenance"], cwd=fixture, check=True,
                             capture_output=True, text=True, timeout=10).stdout.strip().splitlines()
    if sdk != "10.0.401" or len(runtime) != 2 or runtime[0] != "Microsoft.NETCore.App 10.0.12" or runtime[1].split("+")[0] != "Microsoft.AspNetCore.App 10.0.12":
        raise ValueError(f"wrong executable SDK/runtime: {sdk}; {runtime}")
    output.write_text(json.dumps({"revision": REVISION, "sdk": sdk, "runtime": "10.0.12",
                                  "dll": str(binary.resolve()), "hashes": hashes}, indent=2) + "\n")
    print(f"Verified {len(hashes)} source/fixture/lock/assets/binary files; all seven project restores; SDK {sdk}; both runtimes 10.0.12")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    verify(args.source.resolve(), args.artifacts.resolve(), args.output.resolve())
