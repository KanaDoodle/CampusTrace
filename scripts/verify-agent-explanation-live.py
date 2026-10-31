#!/usr/bin/env python3
"""Opt-in configured-upstream acceptance. First preview, then submit its reviewed hash.

Bearer token comes only from CAMPUS_TRACE_TOKEN. No automatic retries/resumes.
Generated evidence may contain private material: output files have mode 0600.
This measures application behavior, never GPU performance or model quality.
"""
import argparse
import json
import os
import pathlib
import time
import urllib.parse
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True, help='CampusTrace API URL')
    parser.add_argument('--parent', required=True, help='completed read-only skill execution ID')
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--mask-name', default='')
    parser.add_argument('--submit-reviewed', help='exact review_hash after inspecting preview.json; may incur provider cost')
    args = parser.parse_args()
    token = os.environ.get('CAMPUS_TRACE_TOKEN', '')
    if not token:
        raise SystemExit('NOT RUN: set CAMPUS_TRACE_TOKEN locally; never paste it into a report.')
    base = args.url.rstrip('/')
    url = urllib.parse.urlsplit(base)
    if url.username or url.password or url.query or url.fragment:
        raise SystemExit('The API base URL must not contain credentials, query or fragment.')
    if url.scheme != 'https' and not (url.scheme == 'http' and url.hostname in ('127.0.0.1', 'localhost', '::1')):
        raise SystemExit('Use HTTPS or a loopback URL for the authenticated acceptance endpoint.')
    args.output.mkdir(parents=True, exist_ok=True)

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    opener = urllib.request.build_opener(NoRedirect())

    def save(name, value):
        target = args.output / name
        with os.fdopen(os.open(target, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600), 'w') as out:
            os.chmod(target, 0o600)
            json.dump(value, out, indent=2, ensure_ascii=False)

    def request(path, body=None):
        req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(),
                                     headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        with opener.open(req, timeout=15) as response:
            return json.load(response)

    capabilities = request('/api/agent/narration/capabilities')
    if not capabilities.get('enabled') or capabilities.get('evidence_kind') != 'configured-upstream':
        raise SystemExit('NOT RUN: configured upstream required; fixture/disabled service is rejected.')
    path = '/api/agent/executions/' + urllib.parse.quote(args.parent, safe='')
    preview = request(path + '/narration/preview', {'mask_name': args.mask_name})
    save('preview.json', preview)
    if not args.submit_reviewed:
        print('Preview saved. Review every message in preview.json, then rerun with --submit-reviewed ' + preview['review_hash'])
        return
    if args.submit_reviewed != preview['review_hash']:
        raise SystemExit('Reviewed input has changed. Inspect the new preview before authorizing a new call.')
    receipt_path = args.output / 'receipt.json'
    receipt = {'url': base, 'parent': args.parent, 'review_hash': preview['review_hash'], 'request_key': uuid.uuid4().hex}
    if receipt_path.exists():
        old = json.loads(receipt_path.read_text())
        if any(old.get(key) != receipt[key] for key in ('url', 'parent', 'review_hash')):
            raise SystemExit('Existing receipt belongs to a different review. Use a new output directory.')
        receipt = old
    # Persist nonce before sending. Rerunning after a lost response reads the same child.
    save('receipt.json', receipt)
    value = request(path + '/narration', {'request_key': receipt['request_key'], 'review_hash': preview['review_hash'],
                                         'confirm': True, 'mask_name': args.mask_name})
    child_path = '/api/agent/executions/' + urllib.parse.quote(value['id'], safe='')
    start = time.monotonic()
    first_snapshot = None
    while time.monotonic() - start < 45:
        value = request(child_path)
        if value.get('narration', {}).get('draft') and first_snapshot is None:
            first_snapshot = round((time.monotonic() - start) * 1000, 2)
        if value['state'] != 'RUNNING' or value.get('can_resume'):
            break
        time.sleep(.15)
    save('execution.json', value)
    result = {'status': 'PASS' if value['state'] == 'COMPLETED' else 'INCOMPLETE', 'task_id': value['id'],
              'backend': capabilities['evidence_kind'], 'model': capabilities['model'], 'state': value['state'],
              'first_observed_snapshot_ms': first_snapshot,
              'limits': ['Operator-configured backend label is not independent engine attestation.',
                         'Snapshot latency includes API/SQL/polling/holdback; not engine TTFT or performance gains.',
                         'Interrupted requests are never automatically retried. Use the UI to review an explicit retry.']}
    save('result.json', result)
    print(json.dumps(result, ensure_ascii=False))
    if result['status'] != 'PASS':
        raise SystemExit(3)


if __name__ == '__main__':
    main()
