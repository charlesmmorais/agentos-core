"""Example that uses only the scoped read-only broker and exported workspace."""
import json
import pathlib
import sys
import agentos

request = json.load(sys.stdin)
print(json.dumps({"identity": agentos.context(), "previous": agentos.memory(),
                  "files": sorted(p.name for p in pathlib.Path(request["workspace"]).iterdir())}))
