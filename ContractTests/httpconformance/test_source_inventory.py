# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.
"""Plant membership defects only in private temporary source/build directories."""

import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import source_inventory as inventory
from prepare import REVISION


class SourceMembershipTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source = self.root / "source"
        self.source.mkdir()
        locks = Path(inventory.__file__).parent / "locks"
        self.graph = sorted(str(path.relative_to(locks).with_name(path.parent.name + ".csproj"))
                            for path in locks.rglob("packages.lock.json"))
        self.expected = {}
        for project in self.graph:
            folder = self.source / Path(project).parent
            folder.mkdir(parents=True)
            for name, data in ((Path(project).name, b"<Project/>"), ("Original.cs", b"// original")):
                path = folder / name
                path.write_bytes(data)
                self.expected[path.relative_to(self.source).as_posix()] = inventory.digest(path)
            lock = Path(project).parent / "packages.lock.json"
            shutil.copyfile(locks / lock, self.source / lock)
        self.manifest = self.root / "source.sha256"
        self.manifest.write_text("".join(f"{value}  {key}\n" for key, value in sorted(self.expected.items())))
        (self.root / "projects.txt").write_text("\n".join(self.graph) + "\n")
        self.mock = patch.object(inventory, "snapshot", return_value=(self.graph, self.expected))
        self.mock.start()
        self.addCleanup(self.mock.stop)

    def validate(self):
        return inventory.validate_source(self.root, self.source)

    def test_complete_source_and_exact_six_installed_locks(self):
        graph, hashes = self.validate()
        self.assertEqual(graph, self.graph)
        self.assertEqual(len(hashes), len(self.expected) + 12 + 2)

    def test_added_compilation_file(self):
        (self.source / Path(self.graph[0]).parent / "Added.cs").write_text("// can compile")
        with self.assertRaisesRegex(ValueError, "source tree membership"):
            self.validate()

    def test_omitted_manifest_entry_even_when_tree_is_complete(self):
        self.manifest.write_text("\n".join(self.manifest.read_text().splitlines()[1:]) + "\n")
        with self.assertRaisesRegex(ValueError, "source manifest membership"):
            self.validate()

    def test_added_file_and_added_manifest_entry(self):
        path = self.source / "Added.cs"
        path.write_bytes(b"// added")
        self.manifest.write_text(self.manifest.read_text() + f"{inventory.digest(path)}  Added.cs\n")
        with self.assertRaisesRegex(ValueError, "source manifest membership"):
            self.validate()

    def test_duplicate_manifest_path(self):
        self.manifest.write_text(self.manifest.read_text() + self.manifest.read_text().splitlines()[0] + "\n")
        with self.assertRaisesRegex(ValueError, "duplicate source path"):
            self.validate()

    def test_traversal_and_noncanonical_paths(self):
        for name in ("../Added.cs", "/Added.cs", "./Added.cs", "a/../Added.cs", "a//Added.cs", "a\\Added.cs"):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "noncanonical"):
                inventory.relative_path(name)

    def test_source_and_manifest_hash_mismatch(self):
        (self.source / next(iter(self.expected))).write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "exact source changed"):
            self.validate()
        self.manifest.write_text(self.manifest.read_text().replace(next(iter(self.expected.values())), "0" * 64))
        with self.assertRaisesRegex(ValueError, "pinned Git hashes"):
            self.validate()

    def test_project_omission(self):
        (self.root / "projects.txt").write_text("\n".join(self.graph[:-1]) + "\n")
        with self.assertRaisesRegex(ValueError, "pinned Git graph"):
            self.validate()

    def test_seventh_fixture_lock_is_not_a_source_exception(self):
        (self.source / "packages.lock.json").write_bytes(b"{}")
        with self.assertRaisesRegex(ValueError, "source tree membership"):
            self.validate()

    def proof(self):
        artifacts = self.root / "artifacts"
        for project in self.graph + ["Reference.csproj"]:
            path = artifacts / "obj" / Path(project).stem / "project.assets.json"
            path.parent.mkdir(parents=True)
            restored_project = self.source / project if project != "Reference.csproj" else Path(inventory.__file__).parent / "reference" / project
            path.write_text(json.dumps({"project": {"restore": {"projectPath": str(restored_project)}}}))
        binaries = artifacts / "bin/Reference/release"
        binaries.mkdir(parents=True)
        for name in ("Arc.Go.HttpConformance.dll", "Arc.Go.HttpConformance.runtimeconfig.json",
                     "Arc.Go.HttpConformance.deps.json", "Cratis.Arc.dll", "Cratis.Arc.Core.dll"):
            (binaries / name).write_bytes(b"built")
        # Generated sources belong to artifacts/obj, not the pinned source inventory.
        (artifacts / "obj/Reference/Generated.cs").write_bytes(b"// generated")
        return {"revision": REVISION, "sdk": "10.0.401", "runtime": "10.0.12",
                "repository": str(self.root), "source": str(self.source), "artifacts": str(artifacts),
                "dll": str(binaries / "Arc.Go.HttpConformance.dll"),
                "hashes": inventory.required_hashes(self.root, self.source, artifacts)}

    def test_complete_proof_and_generated_outputs(self):
        proof = self.proof()
        self.assertEqual(inventory.validate_proof(proof, Path(proof["dll"])), len(proof["hashes"]))

    def test_each_required_provenance_path_cannot_be_omitted(self):
        proof = self.proof()
        hashes = proof["hashes"]
        for omitted in hashes:
            with self.subTest(path=omitted), self.assertRaisesRegex(ValueError, "provenance membership"):
                inventory.validate_proof({**proof, "hashes": {k: v for k, v in hashes.items() if k != omitted}},
                                         Path(proof["dll"]))

    def test_added_compilation_file_after_proof(self):
        proof = self.proof()
        (self.source / Path(self.graph[0]).parent / "Added.cs").write_bytes(b"// added after build")
        with self.assertRaisesRegex(ValueError, "source tree membership"):
            inventory.validate_proof(proof, Path(proof["dll"]))

    def test_provenance_hash_mismatch(self):
        proof = self.proof()
        proof["hashes"][proof["dll"]] = "0" * 64
        with self.assertRaisesRegex(ValueError, "prepared input changed"):
            inventory.validate_proof(proof, Path(proof["dll"]))

    def test_duplicate_provenance_path(self):
        with self.assertRaisesRegex(ValueError, "duplicate provenance member"):
            json.loads('{"hashes":{"/same":"a","/same":"b"}}', object_pairs_hook=inventory.unique_object)

    def test_provenance_traversal(self):
        proof = self.proof()
        proof["hashes"][str(self.source) + "/../source/Added.cs"] = hashlib.sha256(b"").hexdigest()
        with self.assertRaisesRegex(ValueError, "noncanonical provenance"):
            inventory.validate_proof(proof, Path(proof["dll"]))


def actual_snapshot_regressions(proof_path):
    proof = json.loads(proof_path.read_bytes(), object_pairs_hook=inventory.unique_object)
    dll = Path(proof["dll"])
    inventory.validate_proof(proof, dll)
    source = Path(proof["source"])
    added = source / "Source/DotNET/Arc.Core/Added.cs"
    manifest = source.parent / "source.sha256"
    original = manifest.read_bytes()

    def rejected(label, candidate=proof):
        try:
            inventory.validate_proof(candidate, dll)
        except ValueError as error:
            print(f"Rejected actual snapshot {label}: {error}")
        else:
            raise AssertionError(f"admitted actual snapshot {label}")

    with added.open("xb") as stream:
        stream.write(b"namespace ProvenanceRegression; public class Added {}\n")
    try:
        rejected("added compilation file")
    finally:
        added.unlink()
    try:
        manifest.write_bytes(b"\n".join(original.splitlines()[1:]) + b"\n")
        rejected("omitted source inventory entry")
    finally:
        manifest.write_bytes(original)
    omitted = str(source / "Source/DotNET/Arc.Core/Queries/QueryPipeline.cs")
    if omitted not in proof["hashes"]:
        raise AssertionError("required actual source witness missing")
    rejected("omitted required provenance entry", {**proof, "hashes": {
        name: value for name, value in proof["hashes"].items() if name != omitted}})
    count = inventory.validate_proof(proof, dll)
    print(f"Actual pinned snapshot restored unchanged: {count} required files; 3 planted defects rejected")


if __name__ == "__main__":
    import sys
    if len(sys.argv) == 3 and sys.argv[1] == "--actual-proof":
        actual_snapshot_regressions(Path(sys.argv[2]))
    else:
        unittest.main()
