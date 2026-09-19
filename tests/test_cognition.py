"""End-to-end cognitive checkpoint with a deterministic HTTP provider."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]


class CognitionTest(unittest.TestCase):
    def test_cognitive_checkpoint_and_persistent_budget(self):
        requests = []

        class Provider(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                requests.append(payload)
                source = json.loads(payload['messages'][1]['content'])['sources'][0]
                analysis = {'summary': 'Observation', 'claims': [{
                    'text': 'An observation from the file',
                    'source_id': source['id'], 'quote': source['text']}]}
                body = json.dumps({'choices': [{'finish_reason': 'stop',
                    'message': {'content': json.dumps(analysis)}}]}).encode()
                self.send_response(200)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as tmp:
                base = Path(tmp)
                workspace = base / 'input'
                workspace.mkdir()
                source = b'Revenue increased by ten percent.'
                (workspace / 'report.txt').write_bytes(source)
                script = base / 'observe.py'
                script.write_text('import os,json\nassert "AGENTOS_LLM_API_KEY" not in os.environ\nprint(json.dumps({"observed":True}))\n')
                state = base / 'state'
                env = dict(os.environ, AGENTOS_LLM_API_KEY='private-integration-key')

                def run(action, *args):
                    return subprocess.run([str(ROOT / 'agentos'), action, '--state', str(state), *args],
                                          capture_output=True, text=True, env=env, timeout=30)

                initialized = run('init', '--executor', 'trusted-host', '--workspace', str(workspace),
                    '--script', str(script), '--interval', '1', '--cycles', '2',
                    '--llm-model', 'mock', '--llm-base-url', f'http://127.0.0.1:{server.server_port}/v1',
                    '--llm-max-calls', '1')
                self.assertEqual(initialized.returncode, 0, initialized.stderr)
                result = run('run')
                self.assertNotEqual(result.returncode, 0)  # second cycle exhausts budget
                checkpoint = json.loads(run('status').stdout)
                self.assertEqual((checkpoint['completed'], checkpoint['model_attempts'], checkpoint['status']), (1, 1, 'paused'))
                memory = checkpoint['memory']
                self.assertEqual(memory['sources'][0]['sha256'], hashlib.sha256(source).hexdigest())
                self.assertEqual(memory['analysis']['claims'][0]['source_id'], memory['sources'][0]['id'])
                self.assertEqual(memory['evidence_status'], 'quotes_verified_claims_unverified')
                self.assertNotIn('private-integration-key', json.dumps(checkpoint))
                self.assertEqual(run('resume').returncode, 0)
                self.assertNotEqual(run('run').returncode, 0)
                self.assertEqual(len(requests), 1)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
