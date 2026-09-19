"""Black-box tests of the compiled Go executable; standard library only."""
import json
import pathlib
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BIN = ROOT / "agentos"

class Lifecycle(unittest.TestCase):
    def test_kill_restore_and_continue(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = pathlib.Path(tmp) / "original"
            subprocess.run([str(BIN), "init", "--state", str(state),
                            "--workspace", str(ROOT / "examples/data"),
                            "--script", str(ROOT / "examples/analyze.py"),
                            "--interval", "2", "--cycles", "3"], check=True)
            process = subprocess.Popen([str(BIN), "run", "--state", str(state)],
                                       stdout=subprocess.DEVNULL)
            try:
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    before = json.loads((state / "state.json").read_text())
                    if before["completed"] >= 1:
                        break
                    time.sleep(0.02)
                else:
                    self.fail("first cycle did not finish")
            finally:
                process.kill()
                process.wait(timeout=5)
            # Restore into a new state directory after killing the original owner.
            restored = pathlib.Path(tmp) / "restored"
            shutil.copytree(state, restored)
            subprocess.run([str(BIN), "run", "--state", str(restored)],
                           check=True, timeout=15, stdout=subprocess.DEVNULL)
            after = json.loads((restored / "state.json").read_text())
            self.assertEqual(before["agent_id"], after["agent_id"])
            self.assertEqual(after["completed"], 3)
            self.assertEqual(after["status"], "completed")
            completions = [e["cycle"] for e in after["events"] if e["kind"] == "completed"]
            self.assertEqual(completions, [1, 2, 3])
            self.assertEqual(after["memory"]["file_count"], 1)

if __name__ == "__main__":
    unittest.main()
