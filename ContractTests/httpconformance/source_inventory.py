# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.
"""Exact preparation membership, derived from the immutable Git source object."""

import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile

from prepare import REVISION, ROOT_FILES, project_graph, run

FIXTURE_FILES = {
    "Reference.csproj", "Program.cs", "Row.cs", "FixtureReadiness.cs",
    "global.json", "packages.lock.json",
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def relative_path(name):
    path = PurePosixPath(name)
    if not name or path.is_absolute() or ".." in path.parts or str(path) != name or "\\" in name:
        raise ValueError(f"noncanonical inventory path: {name}")
    return path


def snapshot(repository):
    graph = project_graph(repository)
    paths = sorted({str(PurePosixPath(project).parent) for project in graph})
    paths.extend(["Source/DotNET/Shared", "Source/DotNET/AotAnalysis.props", *ROOT_FILES])
    archive = run(["git", "-C", str(repository), "archive", REVISION, "--", *paths])
    files = {}
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        for item in tar:
            relative_path(item.name)
            if item.isdir():
                continue
            if not item.isfile() or item.name in files:
                raise ValueError(f"unsupported archive member: {item.name}")
            files[item.name] = hashlib.sha256(tar.extractfile(item).read()).hexdigest()
    if not files or len(graph) != 6:
        raise ValueError("incomplete pinned source graph")
    return graph, files


def exact_membership(actual, expected, label):
    if set(actual) != set(expected):
        raise ValueError(f"{label} membership: missing {sorted(set(expected) - set(actual))}; "
                         f"added {sorted(set(actual) - set(expected))}")


def file_inventory(root):
    files = set()
    for path in root.rglob("*"):
        if path.is_symlink():
            raise ValueError(f"symlink in prepared inputs: {path}")
        if path.is_file():
            files.add(path.relative_to(root).as_posix())
    return files


def validate_source(repository, source):
    graph, expected = snapshot(repository)
    manifest = source.parent / "source.sha256"
    listed = {}
    for line in manifest.read_text().splitlines():
        hash_value, name = line.split("  ", 1)
        relative_path(name)
        if name in listed:
            raise ValueError(f"duplicate source path: {name}")
        listed[name] = hash_value
    exact_membership(listed, expected, "source manifest")
    if listed != expected:
        raise ValueError("source manifest differs from pinned Git hashes")
    projects_path = source.parent / "projects.txt"
    if projects_path.read_text().splitlines() != graph:
        raise ValueError("source project graph differs from pinned Git graph")
    locks = {str(PurePosixPath(project).parent / "packages.lock.json") for project in graph}
    exact_membership(file_inventory(source), set(expected) | locks, "source tree")
    hashes = {}
    for name, want in expected.items():
        path = source / name
        if digest(path) != want:
            raise ValueError(f"exact source changed: {name}")
        hashes[str(path.resolve())] = want
    for name in locks:
        installed = source / name
        pinned = Path(__file__).parent / "locks" / name
        if installed.read_bytes() != pinned.read_bytes():
            raise ValueError(f"source dependency lock changed: {name}")
        hashes[str(installed.resolve())] = digest(installed)
        hashes[str(pinned.resolve())] = digest(pinned)
    for path in (manifest, projects_path):
        hashes[str(path.resolve())] = digest(path)
    return graph, hashes


def required_hashes(repository, source, artifacts):
    graph, hashes = validate_source(repository, source)
    fixture = Path(__file__).parent / "reference"
    exact_membership(file_inventory(fixture), FIXTURE_FILES, "fixture source")
    for name in FIXTURE_FILES:
        path = fixture / name
        hashes[str(path.resolve())] = digest(path)
    for project in [source / name for name in graph] + [fixture / "Reference.csproj"]:
        path = artifacts / "obj" / project.stem / "project.assets.json"
        assets = json.loads(path.read_bytes(), object_pairs_hook=unique_object)
        if Path(assets["project"]["restore"]["projectPath"]).resolve() != project.resolve():
            raise ValueError(f"restored a different source project: {project.stem}")
        hashes[str(path.resolve())] = digest(path)
    binary_dir = artifacts / "bin/Reference/release"
    files = file_inventory(binary_dir)
    required = {"Arc.Go.HttpConformance.dll", "Arc.Go.HttpConformance.runtimeconfig.json",
                "Arc.Go.HttpConformance.deps.json", "Cratis.Arc.dll", "Cratis.Arc.Core.dll"}
    if not required <= files:
        raise ValueError(f"incomplete built host: {sorted(required - files)}")
    for name in files:
        path = binary_dir / name
        hashes[str(path.resolve())] = digest(path)
    return hashes


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate provenance member: {key}")
        result[key] = value
    return result


def validate_proof(proof, dll):
    if (proof["revision"] != REVISION or proof["sdk"] != "10.0.401" or
            proof["runtime"] != "10.0.12" or proof["dll"] != str(dll)):
        raise ValueError("incomplete or incompatible executable provenance")
    for name in [proof[key] for key in ("repository", "source", "artifacts", "dll")] + list(proof["hashes"]):
        path = Path(name)
        if not path.is_absolute() or str(path.resolve()) != name:
            raise ValueError(f"noncanonical provenance path: {name}")
    source = Path(proof["source"])
    artifacts = Path(proof["artifacts"])
    if dll != artifacts / "bin/Reference/release/Arc.Go.HttpConformance.dll":
        raise ValueError("unlinked executable provenance")
    expected = required_hashes(Path(proof["repository"]), source, artifacts)
    exact_membership(proof["hashes"], expected, "provenance")
    if proof["hashes"] != expected:
        raise ValueError("prepared input changed")
    return len(expected)
