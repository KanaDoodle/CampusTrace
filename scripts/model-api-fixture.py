#!/usr/bin/env python3
"""Loopback Chat Completions protocol fixture. NO MODEL, GPU or cloud API.

Used only by verify-agent-explanation.py. Request contract failures and
HTTP disconnects are counted; input, credentials and generated text are not logged.
"""
import http.server
import json
import threading
import time


def main():
    lock = threading.Lock()
    counters = dict(calls=0, completed=0, canceled=0, active=0, invalid_requests=0)

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, status, value):
            raw = json.dumps(value).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            if self.path != '/fixture/status':
                self.reply(404, {})
                return
            with lock:
                status = dict(counters, backend='fixture-no-model', model_runtime='none')
            self.reply(200, status)

        def do_POST(self):
            valid = False
            try:
                length = int(self.headers.get('Content-Length', '0'))
                if length <= 0 or length > 256 * 1024:
                    raise ValueError('invalid length')
                body = json.loads(self.rfile.read(length))
                valid = (self.path == '/v1/chat/completions'
                         and self.headers.get('Authorization') == 'Bearer synthetic-api-fixture-key'
                         and body.get('model') == 'fixture-model' and body.get('stream') is True
                         and body.get('max_tokens') == 512
                         and body.get('stream_options') == {'include_usage': True}
                         and set(body) <= {'model', 'messages', 'stream', 'stream_options', 'max_tokens', 'temperature'}
                         and bool(body.get('messages'))
                         and all(set(m) == {'role', 'content'} and m['role'] in ('system', 'user') for m in body['messages']))
            except (ValueError, KeyError, TypeError):
                pass
            if not valid:
                with lock:
                    counters['invalid_requests'] += 1
                self.reply(400, {'error': 'invalid synthetic fixture request'})
                return
            with lock:
                counters['calls'] += 1
                counters['active'] += 1
            complete = False
            try:
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Cache-Control', 'no-store')
                self.end_headers()

                def send(value):
                    self.wfile.write(('data: ' + json.dumps(value, ensure_ascii=False) + '\n\n').encode())
                    self.wfile.flush()

                for i in range(40):
                    text = '无模型 API 测试说明，仅用于流式、取消和恢复验证。' * 3
                    if i == 10:
                        text += '<img src=x onerror="window.__narrationExecuted=true">'
                    send({'model': 'fixture-canonical-revision', 'choices': [{'index': 0, 'delta': {'content': text}, 'finish_reason': None}]})
                    time.sleep(.04)
                send({'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}]})
                send({'choices': [], 'usage': {'prompt_tokens': 100, 'completion_tokens': 20}})
                self.wfile.write(b'data: [DONE]\n\n')
                self.wfile.flush()
                complete = True
            except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                pass
            finally:
                with lock:
                    counters['active'] -= 1
                    counters['completed' if complete else 'canceled'] += 1

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    print(json.dumps({'event': 'ready', 'address': '127.0.0.1:' + str(server.server_port), 'backend': 'fixture-no-model'}), flush=True)
    server.serve_forever()


if __name__ == '__main__':
    main()
