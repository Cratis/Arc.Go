# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root.
"""No-Docker ownership and failure-cleanup regressions for the provider harness."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("harness", Path(__file__).with_name("replica-set-tests.py"))
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


class HarnessTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "state.json"
        self.state = {"name": "arc-mongodb-unique", "token": "owner", "container_id": "exact-id"}
        self.container = {"Id": "exact-id", "Config": {"Labels": {harness.LABEL: "owner"}}}
        self.calls = []

    def invoke(self, args, deadline, check=True):
        self.assertGreater(deadline, harness.time.monotonic())
        self.calls.append(args)
        output = json.dumps([self.container]) if args[1] == "inspect" else "diagnostics"
        return subprocess.CompletedProcess(args, 0, output, "")

    def test_failure_logs_precede_exact_owned_volume_cleanup(self):
        with mock.patch.object(harness, "command", side_effect=self.invoke):
            harness.cleanup(self.path, self.state, True)
        self.assertEqual([args[1] for args in self.calls], ["inspect", "logs", "rm"])
        self.assertEqual(self.calls[-1], ["docker", "rm", "--force", "--volumes", "exact-id"])
        self.assertEqual(self.path.with_suffix(".log").read_text(), "diagnostics")
        self.assertTrue(json.loads(self.path.read_text())["cleanup"].startswith("exact owned container removed"))

    def test_mismatched_owner_or_id_is_never_removed(self):
        for mismatch in ("token", "id"):
            with self.subTest(mismatch=mismatch):
                self.container["Id"] = "other-id" if mismatch == "id" else "exact-id"
                self.container["Config"]["Labels"][harness.LABEL] = "other-owner" if mismatch == "token" else "owner"
                self.calls = []
                with mock.patch.object(harness, "command", side_effect=self.invoke):
                    with self.assertRaises(RuntimeError):
                        harness.cleanup(self.path, self.state, True)
                self.assertEqual([args[1] for args in self.calls], ["inspect"])

    def test_log_timeout_still_attempts_exact_cleanup_and_fails_lane(self):
        def timeout_logs(args, deadline, check=True):
            if args[1] == "logs":
                self.calls.append(args)
                raise subprocess.TimeoutExpired(args, 4)
            return self.invoke(args, deadline, check)
        with mock.patch.object(harness, "command", side_effect=timeout_logs):
            with self.assertRaises(subprocess.TimeoutExpired):
                harness.cleanup(self.path, self.state, True)
        self.assertEqual([args[1] for args in self.calls], ["inspect", "logs", "rm"])
        self.assertIn("Failure log collection failed", self.path.with_suffix(".log").read_text())

    def test_lost_create_response_uses_only_random_name_with_verified_token(self):
        del self.state["container_id"]
        with mock.patch.object(harness, "command", side_effect=self.invoke):
            harness.cleanup(self.path, self.state, False)
        self.assertEqual(self.calls[0], ["docker", "inspect", "arc-mongodb-unique"])
        self.assertEqual(self.calls[-1][-1], "exact-id")

    def test_unavailable_lookup_is_not_reported_as_successful_cleanup(self):
        failure = subprocess.CompletedProcess([], 1, "", "daemon unavailable")
        with mock.patch.object(harness, "command", return_value=failure):
            with self.assertRaises(RuntimeError):
                harness.cleanup(self.path, self.state, True)
        self.assertIn("lookup failed", json.loads(self.path.read_text())["cleanup"])


if __name__ == "__main__":
    unittest.main()
