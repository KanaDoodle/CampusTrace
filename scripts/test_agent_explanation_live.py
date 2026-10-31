"""Guard tests for the opt-in HTTP acceptance entry, using NO MODEL fixtures."""
import http.server
import json
import os
import pathlib
import subprocess
import tempfile
import threading
import unittest


class LiveEntryGuards(unittest.TestCase):
    def setUp(self):
        self.calls = 0
        self.nonces = set()
        self.kind = 'configured-upstream'  # Synthetic label; no inference engine.
        self.redirect = False
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reply(self, value):
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(value).encode())

            def do_GET(self):
                if fixture.redirect:
                    self.send_response(302)
                    self.send_header('Location', fixture.base + '/should-not-follow')
                    self.end_headers()
                    return
                if self.path.endswith('/capabilities'):
                    self.reply({'enabled': True, 'evidence_kind': fixture.kind, 'model': 'NO_MODEL'})
                else:
                    self.reply({'id': 'child', 'state': 'COMPLETED', 'narration': {'draft': 'fixture'}})

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if self.path.endswith('/preview'):
                    self.reply({'review_hash': 'a' * 64, 'messages': [{'role': 'user', 'content': 'NO MODEL'}]})
                else:
                    assert body['confirm'] is True
                    fixture.nonces.add(body['request_key'])
                    fixture.calls += 1
                    self.reply({'id': 'child'})

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.base = 'http://127.0.0.1:' + str(self.server.server_port)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.directory = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.directory.cleanup()

    def invoke(self, *extra):
        script = pathlib.Path(__file__).with_name('verify-agent-explanation-live.py')
        return subprocess.run(['python3', str(script), '--url', self.base, '--parent', 'parent',
                               '--output', self.directory.name, *extra],
                              env=dict(os.environ, CAMPUS_TRACE_TOKEN='synthetic-not-a-secret'),
                              capture_output=True, text=True, timeout=5)

    def test_preview_has_no_generation_and_files_are_private(self):
        self.assertEqual(self.invoke().returncode, 0)
        self.assertEqual(self.calls, 0)
        preview = pathlib.Path(self.directory.name) / 'preview.json'
        self.assertEqual(preview.stat().st_mode & 0o777, 0o600)

    def test_changed_review_cannot_generate(self):
        self.assertNotEqual(self.invoke('--submit-reviewed', 'b' * 64).returncode, 0)
        self.assertEqual(self.calls, 0)

    def test_fixture_is_explicitly_rejected(self):
        self.kind = 'fixture-no-model'
        self.assertNotEqual(self.invoke('--submit-reviewed', 'a' * 64).returncode, 0)
        self.assertEqual(self.calls, 0)

    def test_receipt_reused_without_a_new_logical_nonce(self):
        for _ in range(2):
            self.assertEqual(self.invoke('--submit-reviewed', 'a' * 64).returncode, 0)
        self.assertEqual(self.calls, 2)  # two POSTs; server nonce dedup tested in the real E2E.
        self.assertEqual(len(self.nonces), 1)

    def test_authenticated_redirect_is_not_followed(self):
        self.redirect = True
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('302', result.stderr)
        self.assertEqual(self.calls, 0)


if __name__ == '__main__':
    unittest.main()
