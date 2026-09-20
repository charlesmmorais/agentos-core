"""Restore lost local inputs and reconcile effects newer than the backup."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import time
import unittest
from http.server import ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]
BIN = ROOT / 'agentos'
spec = importlib.util.spec_from_file_location('recovery_write_service', ROOT / 'examples/write_service.py')
service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service)


class Recovery(unittest.TestCase):
    def cli(self, state, action, *args, env=None):
        return subprocess.run([str(BIN), action, '--state', str(state), *args],
                              env=env, capture_output=True, text=True, timeout=45)

    def initialize(self, base, extra=(), env=None):
        workspace = base / 'input'
        workspace.mkdir()
        (workspace / 'evidence.txt').write_text('persistent evidence')
        script = base / 'observe.py'
        script.write_text('import json,pathlib\nprint(json.dumps({"evidence":pathlib.Path("evidence.txt").read_text()}))\n')
        image = os.environ.get('AGENTOS_SANDBOX_IMAGE')
        runtime = ['--image', image] if image else ['--executor', 'trusted-host']
        state = base / 'original'
        result = self.cli(state, 'init', *runtime, '--workspace', str(workspace), '--script', str(script),
                          '--cycles', '2', '--interval', '1', *extra, env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        return state, workspace, script

    def test_restore_after_losing_original_script_and_inputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            state, workspace, script = self.initialize(base)
            process = subprocess.Popen([str(BIN), 'run', '--state', str(state)], stdout=subprocess.DEVNULL)
            try:
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline:
                    before = json.loads((state / 'state.json').read_text())
                    if before['completed'] == 1:
                        break
                    time.sleep(.01)
                else:
                    self.fail('first cycle did not finish')
                busy = self.cli(state, 'backup', '--output', str(base / 'busy.tar'))
                self.assertNotEqual(busy.returncode, 0, 'backup must respect writer lock')
                self.assertFalse((base / 'busy.tar').exists())
            finally:
                process.terminate()
                process.wait(timeout=10)
            archive = base / 'backup.tar'
            backed = self.cli(state, 'backup', '--output', str(archive))
            self.assertEqual(backed.returncode, 0, backed.stderr)
            report = json.loads(backed.stdout)
            digest = report['sha256']
            self.assertEqual(self.cli(state, 'backup-verify', '--archive', str(archive), '--sha256', digest).returncode, 0)
            shutil.rmtree(workspace)
            script.unlink()
            restored = base / 'restored'
            result = self.cli(restored, 'restore', '--archive', str(archive), '--sha256', digest)
            self.assertEqual(result.returncode, 0, result.stderr)
            snapshot = json.loads(self.cli(restored, 'status').stdout)
            self.assertEqual(snapshot['agent_id'], before['agent_id'])
            self.assertEqual(snapshot['completed'], 1)
            self.assertEqual(snapshot['status'], 'paused')
            self.assertNotEqual(self.cli(restored, 'resume').returncode, 0)
            self.assertNotEqual(self.cli(restored, 'recover').returncode, 0)
            relocated_script = Path(snapshot['protocol']['script'])
            saved_script = relocated_script.read_bytes()
            relocated_script.write_text('print("changed")\n')
            self.assertNotEqual(self.cli(restored, 'recover', '--source-fenced').returncode, 0)
            self.assertFalse(json.loads(self.cli(restored, 'status').stdout)['recovery']['ready'])
            relocated_script.write_bytes(saved_script)
            doctor = self.cli(restored, 'doctor')
            self.assertEqual(doctor.returncode, 0, doctor.stderr)
            activated = self.cli(restored, 'recover', '--source-fenced')
            self.assertEqual(activated.returncode, 0, activated.stderr)
            self.assertEqual(self.cli(restored, 'resume').returncode, 0)
            finished = self.cli(restored, 'run')
            self.assertEqual(finished.returncode, 0, finished.stderr)
            after = json.loads(self.cli(restored, 'status').stdout)
            self.assertEqual(after['completed'], 2)
            self.assertEqual(after['status'], 'completed')
            self.assertEqual(after['memory']['evidence'], 'persistent evidence')
            self.assertEqual([e['cycle'] for e in after['events'] if e['kind'] == 'completed'], [1, 2])
            self.assertEqual(len(after['artifacts']), 2)

    def test_backup_older_than_external_effect_queries_without_put(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            token = 'recovery-write-token-0123456789abcdef'
            env = dict(os.environ, AGENTOS_WRITE_TOKEN=token)
            puts = []
            handler = service.handler_for(str(base / 'external.sqlite3'), token)

            class Observed(handler):
                def do_PUT(self):
                    puts.append(self.path)
                    super().do_PUT()

            server = ThreadingHTTPServer(('127.0.0.1', 0), Observed)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                state, _, _ = self.initialize(base, ['--write-endpoint', f'http://127.0.0.1:{server.server_port}'], env)
                payload = base / 'payload.json'
                payload.write_text(json.dumps({'name': 'record', 'content': 'created after backup'}))
                proposal = self.cli(state, 'action-propose', '--payload', str(payload), env=env)
                self.assertEqual(proposal.returncode, 0, proposal.stderr)
                action = json.loads(proposal.stdout)
                args = ['--intent', action['id'], '--digest', action['digest']]
                self.assertEqual(self.cli(state, 'action-approve', *args, env=env).returncode, 0)
                archive = base / 'before-effect.tar'
                backup = self.cli(state, 'backup', '--output', str(archive), env=env)
                self.assertEqual(backup.returncode, 0, backup.stderr)
                digest = json.loads(backup.stdout)['sha256']
                self.assertEqual(self.cli(state, 'action-run', '--intent', action['id'], env=env).returncode, 0)
                restored = base / 'old-snapshot-restored'
                result = self.cli(restored, 'restore', '--archive', str(archive), '--sha256', digest, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                snapshot = json.loads(self.cli(restored, 'status').stdout)
                self.assertEqual(snapshot['actions'][0]['status'], 'unknown')
                self.assertNotEqual(self.cli(restored, 'action-run', '--intent', action['id'], env=env).returncode, 0)
                reconciled = self.cli(restored, 'action-reconcile', '--intent', action['id'], env=env)
                self.assertEqual(reconciled.returncode, 0, reconciled.stderr)
                snapshot = json.loads(self.cli(restored, 'status').stdout)
                self.assertEqual(snapshot['actions'][0]['status'], 'succeeded')
                self.assertEqual(snapshot['actions'][0]['receipt']['digest'], action['digest'])
                self.assertEqual(len(puts), 1)
                self.assertFalse(snapshot['recovery']['ready'])
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
