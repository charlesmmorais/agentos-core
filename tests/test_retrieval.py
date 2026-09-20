"""MCP -> retrieval -> real Python -> mock LLM -> durable memory.

AGENTOS_SANDBOX_IMAGE switches the same test to the real Docker executor.
"""
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


class RetrievalLifecycle(unittest.TestCase):
    def test_remote_evidence_and_failure_preserve_memory(self):
        methods, model_inputs, failures = [], [], []
        source_text = 'Replication failed at midnight. Recovery requires the verified backup.'

        class Provider(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def respond(self, status, body=None):
                encoded = json.dumps(body).encode() if body is not None else b''
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)

            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if self.path == '/mcp':
                    if self.headers.get('Authorization') != 'Bearer remote-test-secret':
                        failures.append('MCP credentials missing or wrong')
                    method = request['method']
                    methods.append(method)
                    if method == 'initialize':
                        if methods.count('initialize') > 1:
                            return self.respond(503)  # remote outage after first cycle
                        result = {'protocolVersion': '2025-06-18', 'capabilities': {'resources': {}},
                                  'serverInfo': {'name': 'test', 'version': '1'}}
                    elif method == 'notifications/initialized':
                        return self.respond(202)
                    elif method == 'resources/read':
                        if request['params']['uri'] != 'data:replication':
                            failures.append('unauthorized URI requested')
                        result = {'contents': [{'uri': 'data:replication', 'text': source_text}]}
                    else:
                        failures.append('unexpected MCP operation: ' + method)
                        return self.respond(400)
                    return self.respond(200, {'jsonrpc': '2.0', 'id': request['id'], 'result': result})
                if self.path == '/v1/chat/completions':
                    if self.headers.get('Authorization') != 'Bearer model-test-secret':
                        failures.append('LLM credentials missing or wrong')
                    data = json.loads(request['messages'][1]['content'])
                    model_inputs.append(data)
                    source = data['sources'][0]
                    analysis = {'summary': 'Replication failure', 'claims': [{
                        'text': 'Replication failed.', 'source_id': source['id'],
                        'quote': 'Replication failed at midnight.'}]}
                    return self.respond(200, {'choices': [{'finish_reason': 'stop',
                        'message': {'content': json.dumps(analysis)}}]})
                failures.append('unexpected endpoint')
                self.respond(404)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as tmp:
                base = Path(tmp)
                workspace = base / 'workspace'
                workspace.mkdir()  # remote-only corpus must work
                script = base / 'observe.py'
                script.write_text('import json,os\nassert "AGENTOS_MCP_TOKEN" not in os.environ\nassert "AGENTOS_LLM_API_KEY" not in os.environ\nprint(json.dumps({"observed":True}))\n')
                state = base / 'state'
                env = dict(os.environ, AGENTOS_MCP_TOKEN='remote-test-secret', AGENTOS_LLM_API_KEY='model-test-secret')

                def run(action, *args):
                    return subprocess.run([str(ROOT / 'agentos'), action, '--state', str(state), *args],
                                          capture_output=True, text=True, env=env, timeout=60)

                image = os.environ.get('AGENTOS_SANDBOX_IMAGE')
                executor = ['--image', image] if image else ['--executor', 'trusted-host']
                initialized = run('init', *executor, '--workspace', str(workspace), '--script', str(script),
                    '--mission', 'replication recovery', '--interval', '1', '--cycles', '2', '--rag',
                    '--llm-model', 'mock', '--llm-base-url', f'http://127.0.0.1:{server.server_port}/v1',
                    '--llm-max-calls', '2', '--mcp-endpoint', f'http://127.0.0.1:{server.server_port}/mcp',
                    '--mcp-resource', 'data:replication')
                self.assertEqual(initialized.returncode, 0, initialized.stderr)
                result = run('run')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('503', result.stderr)
                checkpoint = json.loads(run('status').stdout)
                self.assertEqual((checkpoint['completed'], checkpoint['model_attempts']), (1, 2))
                memory = checkpoint['memory']
                self.assertEqual(memory['retrieval']['method'], 'bm25-v1')
                self.assertEqual(memory['sources'][0]['origin'], 'mcp')
                self.assertEqual(memory['sources'][0]['uri'], 'data:replication')
                self.assertEqual(memory['sources'][0]['sha256'], hashlib.sha256(source_text.encode()).hexdigest())
                source = memory['sources'][0]
                self.assertEqual(source['text'], source_text[source.get('start_byte', 0):source['end_byte']])
                self.assertEqual(len(model_inputs), 1)
                self.assertEqual(model_inputs[0]['execution'], {'observed': True})
                self.assertEqual(methods, ['initialize', 'notifications/initialized', 'resources/read', 'initialize'])
                self.assertNotEqual(run('run').returncode, 0)  # exhausted before any I/O
                restored = json.loads(run('status').stdout)
                self.assertEqual(restored['status'], 'paused')
                self.assertEqual(restored['memory'], memory)
                self.assertEqual(len(methods), 4)
                self.assertNotIn('remote-test-secret', json.dumps(restored))
                self.assertNotIn('model-test-secret', json.dumps(restored))
                self.assertEqual(failures, [])
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
