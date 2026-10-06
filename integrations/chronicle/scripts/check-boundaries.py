#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.

"""Check optional SDK and container boundaries using the resolved runtime graph."""

import json
import os
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parent.parent
module = "github.com/cratis/arc.go/integrations/chronicle"
sdk = "github.com/cratis/chronicle.go"
container = "github.com/cratis/fundamentals.go/dependencyinjection/container"
result = subprocess.run(
    ["go", "list", "-deps", "-json", "./..."],
    cwd=root,
    env=dict(os.environ, GOWORK="off", GOTOOLCHAIN="local"),
    check=True,
    stdout=subprocess.PIPE,
    text=True,
)
data = result.stdout
decoder = json.JSONDecoder()
packages = {}
while data.strip():
    package, end = decoder.raw_decode(data.lstrip())
    packages[package["ImportPath"]] = package
    data = data.lstrip()[end:]
if module not in packages or module + "/sdk" not in packages:
    raise SystemExit("Missing integration behavior or SDK package")
for name, package in packages.items():
    if not (name == module or name.startswith(module + "/")):
        continue
    dependencies = package.get("Deps", [])
    if any(d == container or d.startswith(container + "/") for d in dependencies):
        raise SystemExit(f"{name} requires a container")
    if name == module and any(d == sdk or d.startswith(sdk + "/") for d in dependencies):
        raise SystemExit("SDK dependency leaked into SDK-independent behavior")
print("Chronicle behavior is SDK-independent; integration runtime requires no container")
