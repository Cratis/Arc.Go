#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.

"""Enforce Arc's zero-container runtime path using Go's resolved package graph."""

import json
import os
from pathlib import Path
import subprocess

MODULE = "github.com/cratis/arc.go"
DI = "github.com/cratis/fundamentals.go/dependencyinjection"
CONTAINER = DI + "/container"
EXAMPLE = MODULE + "/examples/nocontainer"
# Only explicitly reviewed DI-demonstrating examples may be excluded. None exist.
DI_EXAMPLES = frozenset()


def packages():
    env = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local")
    result = subprocess.run(
        ["go", "list", "-deps", "-json", "./..."],
        cwd=Path(__file__).resolve().parent.parent,
        env=env,
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    )
    decoder = json.JSONDecoder()
    data = result.stdout
    while data.strip():
        package, end = decoder.raw_decode(data.lstrip())
        yield package
        data = data.lstrip()[end:]


def main():
    # No -test: external/internal _test imports never enter the runtime graph.
    runtime = {
        p["ImportPath"]: p
        for p in packages()
        if p["ImportPath"] == MODULE or p["ImportPath"].startswith(MODULE + "/")
    }
    if MODULE not in runtime or EXAMPLE not in runtime:
        raise SystemExit("Missing root package or compiled no-container example")
    if not DI_EXAMPLES.issubset(runtime):
        raise SystemExit("Stale DI-example exclusion")
    failures = []
    for path, package in sorted(runtime.items()):
        if path in DI_EXAMPLES:
            continue
        if any(d == CONTAINER or d.startswith(CONTAINER + "/") for d in package.get("Deps", [])):
            failures.append(f"{path} depends on the optional Fundamentals container")
    example = runtime[EXAMPLE]
    for field in ("Imports", "TestImports", "XTestImports"):
        for dependency in example.get(field, []):
            if dependency == DI or dependency.startswith(DI + "/"):
                failures.append(f"{EXAMPLE} directly imports {dependency} ({field})")
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"Zero-container boundary verified for {len(runtime) - len(DI_EXAMPLES)} runtime packages; example imports no DI package")


if __name__ == "__main__":
    main()
