# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.

"""Regression coverage for compiled Chronicle excerpt synchronization."""

import importlib.util
from pathlib import Path
import unittest

root = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("checker", root / "scripts/check-chronicle-snippet.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class ChronicleSnippetTests(unittest.TestCase):
    def setUp(self):
        self.source = (root / "integrations/chronicle/examples/taskboard/main.go").read_text()
        self.operations = (root / "integrations/chronicle/operation_example_test.go").read_text()
        self.document = (root / "Documentation/backend/go/chronicle/index.md").read_text()

    def test_current_excerpts_match(self):
        checker.check_excerpts(self.source, self.operations, self.document)

    def test_operation_method_drift_is_rejected(self):
        document = self.document.replace("return seats.Release(ctx, o.BookingID)",
                                         "return seats.Release(ctx, o.SeatID)")
        self.assertNotEqual(document, self.document)
        with self.assertRaisesRegex(ValueError, "operation excerpt differs"):
            checker.check_excerpts(self.source, self.operations, document)

    def test_operation_registration_drift_is_rejected(self):
        operations = self.operations.replace("reserveSeat{c.BookingID, c.SeatID}",
                                             "reserveSeat{c.SeatID, c.BookingID}")
        self.assertNotEqual(operations, self.operations)
        with self.assertRaisesRegex(ValueError, "operation excerpt differs"):
            checker.check_excerpts(self.source, operations, self.document)

    def test_missing_or_duplicate_operation_excerpt_is_rejected(self):
        for document in [self.document.replace("type reserveSeat struct", "type omitted struct"),
                         self.document + "\n```go\ntype reserveSeat struct{}\n```\n"]:
            with self.subTest(document=document[-80:]):
                with self.assertRaisesRegex(ValueError, "exactly one documented reserveSeat"):
                    checker.check_excerpts(self.source, self.operations, document)

    def test_command_drift_is_still_rejected(self):
        document = self.document.replace("TaskCreated(c)", "TaskCreated{}")
        self.assertNotEqual(document, self.document)
        with self.assertRaisesRegex(ValueError, "command excerpt differs"):
            checker.check_excerpts(self.source, self.operations, document)


if __name__ == "__main__":
    unittest.main()
