#!/usr/bin/env python3
"""Check the exact admitted modules and the resolved root dependency boundary."""

import json
import subprocess

files = subprocess.check_output(["git", "ls-files", "-z"]).decode().split("\0")
files = [path for path in files if path]
modules = [path for path in files if path == "go.mod" or path.endswith("/go.mod")]
expected = ["go.mod", "integrations/chronicle/go.mod", "integrations/mongodb/go.mod", "recipes/go.mod", "tools/go.mod"]
if modules != expected:
    raise SystemExit("Only root, tools, integrations/chronicle, integrations/mongodb, and the unpublished recipes modules are supported.")
# recipes/ is compiled documentation evidence, never a release unit.
if subprocess.check_output(["git", "tag", "--list", "recipes/*"]).strip():
    raise SystemExit("The recipes module is unpublished and must never be tagged.")
if any(path == "go.work" or path.endswith("/go.work") for path in files):
    raise SystemExit("Committed workspaces are not supported.")
folded = [path.casefold() for path in files]
if len(set(folded)) != len(folded):
    raise SystemExit("Paths differing only by case are not portable.")
for directory in [".", "tools", "integrations/chronicle", "integrations/mongodb", "recipes"]:
    module = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"], cwd=directory))
    if module.get("Replace"):
        raise SystemExit("Modules must not rely on replace directives; they pin fetchable revisions instead.")
    if directory == "recipes":
        if module["Module"]["Path"] != "github.com/cratis/arc.go/recipes":
            raise SystemExit("The unpublished recipes module must use github.com/cratis/arc.go/recipes.")
    elif any(requirement["Path"] == "github.com/cratis/arc.go/recipes"
             for requirement in module.get("Require", [])):
        raise SystemExit("No consumable module may depend on the unpublished recipes module.")


def forbidden(path):
    return (path == "golang.org/x/tools" or path.startswith("golang.org/x/tools/")
            or path == "github.com/cratis/chronicle.go" or path.startswith("github.com/cratis/chronicle.go/")
            or path == "go.mongodb.org/mongo-driver" or path.startswith("go.mongodb.org/mongo-driver/")
            or path.startswith("github.com/cratis/arc.go/integrations/")
            or path == "github.com/cratis/arc.go/recipes" or path.startswith("github.com/cratis/arc.go/recipes/"))


runtime = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"]))
if any(forbidden(requirement["Path"]) for requirement in runtime.get("Require", [])):
    raise SystemExit("Optional dependencies must stay in their nested modules.")
for command in [["go", "list", "-m", "-f", "{{.Path}}", "all"],
                ["go", "list", "-deps", "-test", "-f", "{{.ImportPath}}", "./..."]]:
    paths = subprocess.check_output(command).decode().splitlines()
    if any(forbidden(path.split(" [", 1)[0]) for path in paths):
        raise SystemExit("The resolved runtime graph must not import optional integrations or their dependencies.")
print("Exact module allowlist and resolved root dependency boundary passed.")
