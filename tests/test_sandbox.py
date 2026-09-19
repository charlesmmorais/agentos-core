"""Real Docker isolation gate. Set AGENTOS_SANDBOX_IMAGE to a local sha256 ID."""
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BIN = ROOT / "agentos"
IMAGE = os.environ.get("AGENTOS_SANDBOX_IMAGE")

@unittest.skipUnless(IMAGE, "Docker integration requires AGENTOS_SANDBOX_IMAGE")
class Sandbox(unittest.TestCase):
    def execute(self, code, success=True):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            script = root / "script.py"
            script.write_text(code)
            workspace = root / "workspace"
            workspace.mkdir()
            (workspace / "safe.txt").write_text("safe")
            secret = root / "host-secret"
            secret.write_text("secret")
            (workspace / "escape").symlink_to(secret)
            state = root / "state"
            subprocess.run([str(BIN), "init", "--state", str(state), "--image", IMAGE,
                            "--script", str(script), "--workspace", str(workspace),
                            "--cycles", "1"], check=True, capture_output=True)
            env = dict(os.environ, AGENTOS_API_TOKEN="must-not-enter-guest")
            result = subprocess.run([str(BIN), "run", "--state", str(state)], env=env,
                                    capture_output=True, timeout=45)
            if success:
                self.assertEqual(result.returncode, 0, result.stderr.decode())
                return json.loads((state / "state.json").read_text())["memory"]
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(json.loads((state / "state.json").read_text())["completed"], 0)

    def test_files_network_broker_and_limits(self):
        result = self.execute('''
import json,os,pathlib,socket,sys,agentos
r=json.load(sys.stdin)
assert os.getuid()==65534
assert "AGENTOS_API_TOKEN" not in os.environ
assert not pathlib.Path("/var/run/docker.sock").exists()
assert not pathlib.Path(r["workspace"],"escape").exists()
assert pathlib.Path(r["workspace"],"safe.txt").read_text()=="safe"
try:
 pathlib.Path(r["workspace"],"write.txt").write_text("bad")
 raise AssertionError("workspace writable")
except OSError: pass
try:
 socket.create_connection(("1.1.1.1",443),timeout=1)
 raise AssertionError("network reachable")
except OSError: pass
try:
 agentos.call("tools.execute")
 raise AssertionError("unauthorized capability")
except PermissionError: pass
assert agentos.context()["agent_id"]==r["agent_id"]
assert agentos.memory() is None
print(json.dumps({"isolated":True,"memory_limit":pathlib.Path("/sys/fs/cgroup/memory.max").read_text().strip()}))
''')
        self.assertTrue(result["isolated"])
        self.assertEqual(result["memory_limit"], "134217728")

    def test_memory_exhaustion(self):
        self.execute("x=bytearray(512*1024*1024)\nprint('{}')", success=False)

    def test_timeout_cleanup(self):
        self.execute("import time\ntime.sleep(90)\nprint('{}')", success=False)

if __name__ == "__main__":
    unittest.main()
