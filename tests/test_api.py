"""Real loopback API, concurrent cancellation and durable artifacts."""
import json
import os
import pathlib
import secrets
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
BIN = ROOT / "agentos"

class APILifecycle(unittest.TestCase):
    def test_pause_resume_cancel_live(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = pathlib.Path(tmp) / "state"
            subprocess.run([str(BIN), "init", "--executor", "trusted-host", "--state", str(state),
                            "--workspace", str(ROOT / "examples/data"),
                            "--script", str(ROOT / "examples/analyze.py"),
                            "--interval", "30", "--cycles", "3"], check=True)
            subprocess.run([str(BIN), "pause", "--state", str(state)], check=True)
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0))
                port = sock.getsockname()[1]
            token = secrets.token_hex(32)
            env = dict(os.environ, AGENTOS_API_TOKEN=token)
            process = subprocess.Popen([str(BIN), "serve", "--state", str(state),
                                        "--listen", f"127.0.0.1:{port}"], env=env,
                                       stdout=subprocess.DEVNULL)
            def call(path, method="GET", authorized=True):
                request = urllib.request.Request(f"http://127.0.0.1:{port}/v1/{path}", method=method)
                if authorized:
                    request.add_header("Authorization", "Bearer " + token)
                with urllib.request.urlopen(request, timeout=2) as response:
                    body = response.read()
                    return json.loads(body) if body else None
            try:
                deadline = time.monotonic() + 10
                while True:
                    try:
                        current = call("state")
                        break
                    except urllib.error.URLError:
                        if time.monotonic() >= deadline:
                            raise
                        time.sleep(0.05)
                self.assertEqual(current["status"], "paused")
                with self.assertRaises(urllib.error.HTTPError) as rejected:
                    call("control/resume", "POST", authorized=False)
                self.assertEqual(rejected.exception.code, 401)
                call("control/resume", "POST")
                deadline = time.monotonic() + 10
                while call("state")["completed"] < 1:
                    if time.monotonic() >= deadline:
                        self.fail("no cycle completed")
                    time.sleep(0.05)
                self.assertEqual(call("artifacts/1")["file_count"], 1)
                call("control/pause", "POST")
                self.assertEqual(call("state")["status"], "paused")
                call("control/cancel", "POST")
                with self.assertRaises(urllib.error.HTTPError):
                    call("control/resume", "POST")
            finally:
                process.terminate()
                process.wait(timeout=10)
            persisted = json.loads((state / "state.json").read_text())
            self.assertEqual(persisted["status"], "cancelled")
            self.assertEqual(len(persisted["artifacts"]), 1)

if __name__ == "__main__":
    unittest.main()
