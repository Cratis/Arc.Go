#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.

"""Keep the documented command excerpt equal to its compiled task-board source."""

from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parent.parent
source = (root / "integrations/chronicle/examples/taskboard/main.go").read_text()
document = (root / "Documentation/backend/go/chronicle/index.md").read_text()
function = re.search(r"(?ms)^func \(c CreateTask\) Handle.*?^}", source)
blocks = [block for block in re.findall(r"```go\n(.*?)\n```", document, re.S)
          if "func (c CreateTask) Handle" in block]
if function is None or len(blocks) != 1:
    raise SystemExit("Expected exactly one documented CreateTask.Handle excerpt")


def formatted(body):
    return subprocess.run(["gofmt"], input="package example\n" + body + "\n",
                          text=True, capture_output=True, check=True).stdout


if formatted(function.group()) != formatted(blocks[0]):
    raise SystemExit("Chronicle command excerpt differs from its compiled example")
print("Chronicle command excerpt matches its compiled task-board source")
