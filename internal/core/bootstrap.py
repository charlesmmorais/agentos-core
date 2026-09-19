"""Trusted preflight: require effective cgroup v2 resource limits before guest code."""
import pathlib
import runpy
import sys

root = pathlib.Path("/sys/fs/cgroup")
memory = int((root / "memory.max").read_text())
pids = int((root / "pids.max").read_text())
quota, period = map(int, (root / "cpu.max").read_text().split())
swap = int((root / "memory.swap.max").read_text())
if not (0 < memory <= 134217728 and 0 < pids <= 32 and 0 < quota / period <= 0.5 and swap == 0):
    raise RuntimeError("sandbox resource limits not enforced")
status = dict(line.split(":", 1) for line in pathlib.Path("/proc/self/status").read_text().splitlines() if ":" in line)
if status["Seccomp"].strip() != "2" or status["NoNewPrivs"].strip() != "1" or int(status["CapEff"].strip(), 16) != 0:
    raise RuntimeError("sandbox privilege restrictions not enforced")
sys.path.insert(0, "/input/sdk")
runpy.run_path("/input/script.py", run_name="__main__")
