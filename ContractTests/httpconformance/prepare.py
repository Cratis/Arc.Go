#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.
"""Extract the immutable Arc source build graph, never build in its checkout."""

import argparse
import hashlib
import io
from pathlib import Path, PurePosixPath
import subprocess
import shutil
import tarfile
import xml.etree.ElementTree as ET

REVISION = "7c1e78075b737df64f69fddfaae83374f75e3612"
ENTRY = "Source/DotNET/Arc/Arc.csproj"
ROOT_FILES = (
    "Directory.Build.props", "Directory.Build.targets", "Directory.Packages.props",
    "Directory.Packages.NET8-9.props", "Directory.Packages.NET8.props",
    "Directory.Packages.NET9.props", ".editorconfig", ".globalconfig", "stylecop.json",
    "global.json", "LICENSE",
)


def run(command, cwd=None):
    return subprocess.run(command, cwd=cwd, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=20).stdout


def source(repository, name):
    return run(["git", "-C", str(repository), "show", f"{REVISION}:{name}"])


def project_graph(repository):
    pending = [ENTRY]
    graph = set()
    while pending:
        project = pending.pop()
        if project in graph:
            continue
        graph.add(project)
        tree = ET.fromstring(source(repository, project))
        for item in tree.iter("ProjectReference"):
            reference = item.attrib["Include"]
            if "$" in reference:
                raise ValueError(f"unresolved project reference: {project}: {reference}")
            # Resolve lexically, without consulting the sibling working tree.
            parts = []
            for part in (PurePosixPath(project).parent / reference).parts:
                if part == "..":
                    if not parts:
                        raise ValueError("project reference escapes repository")
                    parts.pop()
                elif part != ".":
                    parts.append(part)
            pending.append("/".join(parts))
    return sorted(graph)


def prepare(repository, destination):
    if destination.exists():
        raise ValueError("snapshot destination already exists; no reuse or overwrite")
    graph = project_graph(repository)
    paths = sorted({str(PurePosixPath(project).parent) for project in graph})
    paths.extend(["Source/DotNET/Shared", "Source/DotNET/AotAnalysis.props", *ROOT_FILES])
    archive = run(["git", "-C", str(repository), "archive", REVISION, "--", *paths])
    destination.mkdir(parents=True)
    hashes = []
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        for item in tar:
            name = PurePosixPath(item.name)
            if name.is_absolute() or ".." in name.parts or not (item.isdir() or item.isfile()):
                raise ValueError(f"unsupported archive member: {item.name}")
            target = destination / item.name
            if item.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                data = tar.extractfile(item).read()
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
                hashes.append(f"{hashlib.sha256(data).hexdigest()}  {item.name}")
    # Useful recovery evidence, not a claim of successful restore/build/parity.
    (destination.parent / "source.sha256").write_text("\n".join(sorted(hashes)) + "\n")
    (destination.parent / "projects.txt").write_text("\n".join(graph) + "\n")
    locks = Path(__file__).parent / "locks"
    for project in graph:
        lock = PurePosixPath(project).parent / "packages.lock.json"
        if not (locks / lock).is_file():
            raise ValueError(f"missing pinned restore lock: {lock}")
        shutil.copyfile(locks / lock, destination / lock)
    sdk = run(["dotnet", "--version"], Path(__file__).parent / "reference").decode().strip()
    if sdk != "10.0.401":
        raise ValueError(f"SDK {sdk}; expected 10.0.401 with roll-forward disabled")
    print(f"Arc {REVISION}; SDK {sdk}; {len(graph)} projects; {len(hashes)} exact source files")
    print(f"Task-owned snapshot: {destination}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arc-repository", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    prepare(args.arc_repository.resolve(), args.destination.resolve())
