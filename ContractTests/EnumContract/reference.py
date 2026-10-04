#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.
"""Run the actual pinned Arc options; never substitute a copied converter."""

import argparse
import hashlib
import json
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parent


def run(command):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=60).stdout


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifacts", type=pathlib.Path, required=True)
    parser.add_argument("--capture", action="store_true")
    args = parser.parse_args()
    artifacts = args.artifacts.resolve()
    if run(["dotnet", "--version"]).strip() != "10.0.401":
        raise RuntimeError("Requires installed SDK 10.0.401; no roll-forward qualification")
    lock = json.loads((ROOT / "reference/packages.lock.json").read_text())["dependencies"]["net10.0"]
    assets = json.loads((artifacts / "obj/Reference/project.assets.json").read_text())
    for name, version in {"Cratis.Arc.Core": "22.48.2", "Cratis.Fundamentals": "7.19.6"}.items():
        entry = lock[name]
        if entry["resolved"] != version or entry["requested"] != f"[{version}, {version}]":
            raise RuntimeError(f"Unexpected package {name}")
    for name, entry in lock.items():
        if assets["libraries"][f'{name}/{entry["resolved"]}']["sha512"] != entry["contentHash"]:
            raise RuntimeError(f"Unlocked restore for {name}")
    profile = json.loads(run(["dotnet", str(artifacts / "bin/Reference/release/Reference.dll"), str(ROOT / "corpus.json")]))
    if profile["runtime"] != "10.0.12" or profile["arc"] != "1.0.0+7c1e78075b737df64f69fddfaae83374f75e3612" or profile["fundamentals"] != "1.0.0+14037b1ff8346b7944ad48065b8f66feca80dab5":
        raise RuntimeError(f'Unexpected executed assembly/runtime: {profile["arc"]}, {profile["fundamentals"]}, {profile["runtime"]}')
    corpus = json.loads((ROOT / "corpus.json").read_text())
    for operation in ("reads", "writes"):
        expected = [(row["type"], row["id"]) for row in corpus[operation]]
        actual = [(row["Type"], row["ID"]) for row in profile[operation]]
        if not expected or len(expected) != len(set(expected)) or actual != expected:
            raise RuntimeError(f"Incomplete or duplicated {operation}")
        for row in profile[operation]:
            if not isinstance(row["accepted"], bool) or (row["error"] is None) != row["accepted"]:
                raise RuntimeError("Invalid outcome")
            if operation == "reads" and row["accepted"]:
                if row["write"] is None or not isinstance(row["write"]["Accepted"], bool):
                    raise RuntimeError("Missing independent reserialization outcome")
    profile["inputs"] = {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest() for name in (
        "corpus.json", "reference/Reference.csproj", "reference/Program.cs", "reference/packages.lock.json"
    )}
    output = json.dumps(profile, indent=2, ensure_ascii=True) + "\n"
    if args.capture:
        (ROOT / "profile.json").write_text(output)
    elif json.loads((ROOT / "profile.json").read_text()) != profile:
        raise RuntimeError("Pinned enum reference drift")
    print(f'Arc enum profile: {len(profile["reads"])} reads, {len(profile["writes"])} independent writes; verified')


if __name__ == "__main__":
    main()
