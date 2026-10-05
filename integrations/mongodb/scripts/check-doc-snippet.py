#!/usr/bin/env python3
# Copyright (c) Cratis. All rights reserved.
# Licensed under the MIT license. See LICENSE file in the project root.
"""Check the MongoDB guide's declaration excerpt against compiled sample source."""

from pathlib import Path
import re
import sys

root = Path(__file__).resolve().parents[3]
source = (root / "integrations/mongodb/examples/snapshot/snapshot.go").read_text(encoding="utf-8")
guide = (root / "Documentation/backend/go/mongodb/index.md").read_text(encoding="utf-8")
pattern = r"type Author struct \{.*?\n\}\n\n// AllActive.*?\nfunc \(Author\) AllActive.*?\n\}"
compiled = re.search(pattern, source, re.DOTALL)
documented = re.findall(r"<!-- mongodb-snippet: Author -->\s*```go\n(.*?)\n```", guide, re.DOTALL)
if compiled is None or len(documented) != 1 or compiled.group(0).expandtabs(4) != documented[0]:
    sys.exit("MongoDB Author declaration excerpt drifted from compiled example")
observation = (root / "integrations/mongodb/examples/observation/observation.go").read_text(encoding="utf-8")
observed = re.search(r"// ActiveAuthors.*?\nfunc ActiveAuthors.*?\n\}", observation, re.DOTALL)
excerpt = re.findall(r"<!-- mongodb-observation-snippet: ActiveAuthors -->\s*```go\n(.*?)\n```", guide, re.DOTALL)
if observed is None or len(excerpt) != 1 or observed.group(0).expandtabs(4) != excerpt[0]:
    sys.exit("MongoDB observation excerpt drifted from compiled example")
print("MongoDB snapshot and observation declarations match compiled manual examples")
