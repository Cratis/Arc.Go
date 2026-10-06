#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root for full license information.

"""Keep Chronicle command and operation excerpts equal to compiled examples."""

from pathlib import Path
import re
import subprocess


def formatted(body):
    return subprocess.run(["gofmt"], input="package example\n" + body + "\n",
                          text=True, capture_output=True, check=True).stdout


def check_excerpts(source, operations, document):
    blocks = re.findall(r"```go\n(.*?)\n```", document, re.S)
    function = re.search(r"(?ms)^func \(c CreateTask\) Handle.*?^}", source)
    commands = [block for block in blocks if "func (c CreateTask) Handle" in block]
    if function is None or len(commands) != 1:
        raise ValueError("Expected exactly one documented CreateTask.Handle excerpt")
    if formatted(function.group()) != formatted(commands[0]):
        raise ValueError("Chronicle command excerpt differs from its compiled example")

    declarations = re.search(r"(?ms)^type reserveSeat struct.*?^}\n(?=\ntype seatCatalog)", operations)
    registration = re.search(r"(?ms)^\terr := commands.RegisterOperation.*?(?=^\tapp, err := builder.Build)", operations)
    excerpts = [block for block in blocks if "type reserveSeat struct" in block]
    if declarations is None or registration is None or len(excerpts) != 1:
        raise ValueError("Expected exactly one documented reserveSeat/registration excerpt")
    # The guide explicitly asks callers to check each error. Only these panic
    # checks are omitted; every declaration and registration statement is compared.
    registered = registration.group().replace("\tif err != nil {\n\t\tpanic(err)\n\t}\n", "")
    expected = declarations.group() + "\nfunc register() {\n" + registered + "}\n"
    declaration, separator, calls = excerpts[0].partition("err := commands.RegisterOperation")
    if not separator:
        raise ValueError("Expected operation registration in the documented excerpt")
    actual = declaration + "func register() {\n" + separator + calls + "\n}\n"
    if formatted(expected) != formatted(actual):
        raise ValueError("Chronicle operation excerpt differs from its compiled example")


def main():
    root = Path(__file__).resolve().parent.parent
    try:
        check_excerpts(
            (root / "integrations/chronicle/examples/taskboard/main.go").read_text(),
            (root / "integrations/chronicle/operation_example_test.go").read_text(),
            (root / "Documentation/backend/go/chronicle/index.md").read_text())
    except ValueError as error:
        raise SystemExit(str(error)) from error
    print("Chronicle command and operation excerpts match their compiled examples")


if __name__ == "__main__":
    main()
