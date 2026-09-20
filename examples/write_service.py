"""Local reference record service. SQLite is the effect and idempotency ledger.

Not a general-purpose gateway: immutable records only, no external side effects.
"""
import argparse
from contextlib import closing
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import re
import sqlite3


def handler_for(database, token, after_commit=None):
    if len(token) < 32:
        raise ValueError('AGENTOS_WRITE_TOKEN requires at least 32 characters')
    with closing(sqlite3.connect(database)) as db:
        db.execute('PRAGMA journal_mode=WAL')
        db.execute('PRAGMA synchronous=FULL')
        db.execute('CREATE TABLE IF NOT EXISTS records (id TEXT PRIMARY KEY, digest TEXT NOT NULL, payload TEXT NOT NULL)')
        db.commit()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def reply(self, code, value=None):
            data = json.dumps(value).encode() if value is not None else b''
            self.send_response(code)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            try:
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError):
                pass

        def authorized_id(self):
            if not hmac.compare_digest(self.headers.get('Authorization', ''), 'Bearer ' + token):
                self.reply(401)
                return None
            match = re.fullmatch(r'/v1/records/([0-9a-f]{32})', self.path)
            if not match:
                self.reply(404)
                return None
            return match.group(1)

        @staticmethod
        def receipt(action_id, digest):
            return {'action_id': action_id, 'digest': digest, 'record_id': action_id, 'status': 'committed'}

        def do_GET(self):
            action_id = self.authorized_id()
            if action_id is None:
                return
            with closing(sqlite3.connect(database)) as db:
                row = db.execute('SELECT digest FROM records WHERE id=?', (action_id,)).fetchone()
            self.reply(200, self.receipt(action_id, row[0])) if row else self.reply(404)

        def do_PUT(self):
            action_id = self.authorized_id()
            if action_id is None:
                return
            self.connection.settimeout(10)
            try:
                length = int(self.headers.get('Content-Length', '0'))
                if not 0 < length <= 65536:
                    raise ValueError('length')
                request = json.loads(self.rfile.read(length))
                if set(request) != {'action_id', 'digest', 'payload'} or request['action_id'] != action_id:
                    raise ValueError('envelope')
                digest = request['digest']
                if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
                    raise ValueError('digest')
                payload = request['payload']
                if set(payload) != {'name', 'content'}:
                    raise ValueError('payload')
                for key, limit in [('name', 120), ('content', 8192)]:
                    if not isinstance(payload[key], str) or not payload[key].strip() or len(payload[key].encode()) > limit:
                        raise ValueError(key)
                canonical = json.dumps(payload, sort_keys=True, separators=(',', ':'), ensure_ascii=False)
            except (ValueError, TypeError, KeyError, OSError):
                self.reply(400)
                return
            try:
                with closing(sqlite3.connect(database, timeout=5)) as db:
                    db.execute('PRAGMA synchronous=FULL')
                    db.execute('BEGIN IMMEDIATE')
                    row = db.execute('SELECT digest,payload FROM records WHERE id=?', (action_id,)).fetchone()
                    if row and row != (digest, canonical):
                        db.rollback()
                        self.reply(409)
                        return
                    if not row:
                        db.execute('INSERT INTO records VALUES (?,?,?)', (action_id, digest, canonical))
                    db.commit()  # effect and receipt identity become durable together
            except sqlite3.Error:
                self.reply(503)
                return
            if not row and after_commit is not None:
                after_commit(action_id)  # fault-injection hook used by integration tests
            self.reply(200 if row else 201, self.receipt(action_id, digest))

    return Handler


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='records.sqlite3')
    parser.add_argument('--port', type=int, default=8090)
    args = parser.parse_args()
    os.umask(0o077)
    server = ThreadingHTTPServer(('127.0.0.1', args.port), handler_for(args.db, os.environ.get('AGENTOS_WRITE_TOKEN', '')))
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
