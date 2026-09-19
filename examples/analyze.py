"""Trusted, read-only demonstration. JSON request on stdin, JSON result on stdout."""
import json
import pathlib
import sys

def analyze(request):
    root = pathlib.Path(request["workspace"])
    # Bounded, nonrecursive scan; do not follow symbolic links.
    files = []
    for index, path in enumerate(root.iterdir()):
        if index >= 1000:
            break
        if not path.is_symlink() and path.is_file():
            files.append({"name": path.name, "bytes": path.stat().st_size})
    return {"cycle_id": request["cycle_id"], "files": sorted(files, key=lambda x: x["name"]),
            "file_count": len(files), "scope": "first 1000 directory entries; nonrecursive"}

if __name__ == "__main__":
    print(json.dumps(analyze(json.load(sys.stdin))))
