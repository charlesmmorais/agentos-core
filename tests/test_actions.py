"""Crash after durable external commit, before AgentOS receives the receipt."""
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import threading
import unittest
from http.server import ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('write_service', ROOT / 'examples/write_service.py')
service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service)
TOKEN = 'write-test-token-0123456789abcdef'


class ActionRecovery(unittest.TestCase):
    def test_kill_after_commit_recovers_without_duplicate(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            committed, release = threading.Event(), threading.Event()
            puts = []

            def after_commit(_):
                committed.set()
                release.wait(20)

            handler = service.handler_for(str(base / 'remote.sqlite3'), TOKEN, after_commit)

            class Observed(handler):
                def do_PUT(self):
                    puts.append(self.path)
                    super().do_PUT()

            server = ThreadingHTTPServer(('127.0.0.1', 0), Observed)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            process = None
            try:
                state = base / 'state'
                workspace = base / 'input'
                workspace.mkdir()
                script = base / 'observe.py'
                script.write_text('import json,os\nassert "AGENTOS_WRITE_TOKEN" not in os.environ\nprint(json.dumps({"ok":True}))\n')
                env = dict(os.environ, AGENTOS_WRITE_TOKEN=TOKEN)

                def run(action, *args):
                    return subprocess.run([str(ROOT / 'agentos'), action, '--state', str(state), *args],
                        capture_output=True, text=True, env=env, timeout=45)

                image = os.environ.get('AGENTOS_SANDBOX_IMAGE')
                runtime = ['--image', image] if image else ['--executor', 'trusted-host']
                initialized = run('init', *runtime, '--workspace', str(workspace), '--script', str(script),
                    '--cycles', '1', '--write-endpoint', f'http://127.0.0.1:{server.server_port}')
                self.assertEqual(initialized.returncode, 0, initialized.stderr)
                payload = base / 'payload.json'
                payload.write_text(json.dumps({'name': 'recovery test', 'content': 'one immutable record'}))
                proposed = run('action-propose', '--payload', str(payload))
                self.assertEqual(proposed.returncode, 0, proposed.stderr)
                intent = json.loads(proposed.stdout)
                args = ['--intent', intent['id'], '--digest', intent['digest']]
                self.assertNotEqual(run('action-run', '--intent', intent['id']).returncode, 0)
                self.assertNotEqual(run('action-approve', '--intent', intent['id'], '--digest', 'wrong').returncode, 0)
                self.assertEqual(puts, [])
                self.assertEqual(run('action-approve', *args).returncode, 0)
                process = subprocess.Popen([str(ROOT / 'agentos'), 'run', '--state', str(state)],
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
                self.assertTrue(committed.wait(15), 'external write did not commit')
                # Destination has committed, while AgentOS still awaits its response.
                checkpoint = json.loads((state / 'state.json').read_text())
                self.assertEqual(checkpoint['actions'][0]['status'], 'in_flight')
                process.kill()
                process.wait(timeout=5)
                release.set()
                restarted = run('run')
                self.assertEqual(restarted.returncode, 0, restarted.stderr)
                restored = json.loads(run('status').stdout)
                self.assertEqual(restored['status'], 'completed')
                action = restored['actions'][0]
                self.assertEqual(action['status'], 'succeeded')
                self.assertEqual(action['attempts'], 1)
                self.assertEqual(action['checks'], 2)
                self.assertEqual(action['receipt']['digest'], intent['digest'])
                self.assertEqual(len(puts), 1, 'recovery must query rather than resend')
                with sqlite3.connect(base / 'remote.sqlite3') as db:
                    self.assertEqual(db.execute('SELECT COUNT(*) FROM records').fetchone()[0], 1)
                self.assertNotIn(TOKEN, json.dumps(restored))
            finally:
                release.set()
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait()
                server.shutdown()
                server.server_close()
                thread.join()

    def test_reference_service_replay_and_conflict(self):
        with tempfile.TemporaryDirectory() as tmp:
            database = str(Path(tmp) / 'records.sqlite3')
            action_id, digest = 'a' * 32, 'b' * 64
            payload = {'action_id': action_id, 'digest': digest,
                       'payload': {'name': 'record', 'content': 'immutable'}}
            # Restart the destination between identical PUTs; ledger must survive.
            for iteration in range(2):
                server = ThreadingHTTPServer(('127.0.0.1', 0), service.handler_for(database, TOKEN))
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                try:
                    url = f'http://127.0.0.1:{server.server_port}/v1/records/{action_id}'

                    def put(value):
                        request = Request(url, data=json.dumps(value).encode(), method='PUT',
                            headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
                        return urlopen(request, timeout=5)

                    with put(payload) as response:
                        self.assertEqual(response.status, 201 if iteration == 0 else 200)
                    conflict = dict(payload, payload={'name': 'record', 'content': 'modified'})
                    with self.assertRaises(HTTPError) as raised:
                        put(conflict)
                    self.assertEqual(raised.exception.code, 409)
                finally:
                    server.shutdown()
                    server.server_close()
                    thread.join()
            with sqlite3.connect(database) as db:
                self.assertEqual(db.execute('SELECT COUNT(*) FROM records').fetchone()[0], 1)
