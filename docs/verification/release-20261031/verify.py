#!/usr/bin/env python3
"""Check frozen release evidence without running services or model calls."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def verify(directory, worktree=None):
    read = lambda name: json.loads((directory / name).read_text())
    summary = read('summary.json')
    sources = read('source-files.json')
    assert sha(json.dumps(sources, sort_keys=True).encode()) == summary['runtime_fingerprint']
    hashes = read('raw-file-sha256.json')
    with tarfile.open(directory / 'evidence.tar.gz') as archive:
        members = [m for m in archive.getmembers() if m.isfile()]
        assert len(members) == len(hashes)
        assert {m.name for m in members} == set(hashes)
        for member in members:
            assert sha(archive.extractfile(member).read()) == hashes[member.name], member.name
        load = lambda name: json.load(archive.extractfile(name))
        journey = load('journey.json')
        assert journey['status'] == 'PASS' and len(journey['checks']) == 7
        assert journey['external_model_calls'] == 0
        results = Counter()
        cas_checks = 0
        for line in archive.extractfile('integration/go-test.jsonl'):
            event = json.loads(line)
            if event.get('Test') and '/' not in event['Test'] and event['Action'] in ('pass', 'skip', 'fail'):
                results[event['Action']] += 1
            if event.get('Test', '').startswith('TestRebuildPreparationReconcilesOnlyMatchingPublishedCandidate/') and event['Action'] == 'pass':
                cas_checks += 1
        assert dict(results) == {'pass': 617, 'skip': 24}
        assert cas_checks == 9
        assert summary['integration_top_level'] == dict(results)
        integration = load('integration/result.json')
        assert integration['status'] == 'PASS' and integration['race']
        assert integration['remaining_fixture_schemas'] == integration['remaining_fixture_accounts'] == 0
        assert integration['template_guard_preserved']
        browser = load('browser/browser-result.json')
        assert browser['status'] == 'PASS' and len(browser['checks']) == 17 and browser['page_errors'] == []
        frontend = archive.extractfile('frontend.log').read().decode()
        assert 'tests 453' in frontend and 'pass 453' in frontend and 'fail 0' in frontend
        contracts = archive.extractfile('performance-contracts.log').read().decode()
        assert 'Ran 16 tests' in contracts and '\nOK\n' in contracts and 'pass 6' in contracts and 'fail 0' in contracts
        boundary = archive.extractfile('dependency-boundary.log').read().decode()
        assert 'PASS: independent Search dependency boundary.' in boundary
        assert 'PASS: workspace disabled; published KanaRPC-Go v0.1.0; public facade boundary.' in boundary
        before = archive.extractfile('rebuild-cas-before.txt').read().decode()
        assert before.count('matching publication reported as failure') == 4
        native = archive.extractfile('final-race.txt').read().decode().splitlines()
        assert not any(line.startswith('FAIL') for line in native)
        assert sum(line.startswith('ok ') for line in native) == 25
    if worktree:
        names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=worktree).decode().split('\0')
        roots = ('cmd/', 'internal/', 'web/', 'migrations/')
        files = ('go.mod', 'go.sum', 'Dockerfile', 'Dockerfile.search', '.dockerignore', 'docker-compose.yml', 'compose.app.yml', 'compose.search.yml')
        current = {name: sha((worktree / name).read_bytes()) for name in names
                   if name and (name.startswith(roots) or name in files) and (worktree / name).is_file()}
        assert current == sources, 'Worktree runtime differs from tested release'
        for name, expected in read('harness-files.json').items():
            assert sha((worktree / name).read_bytes()) == expected, name
    return {'status': 'PASS', 'runtime_fingerprint': summary['runtime_fingerprint'],
            'raw_files': len(hashes), 'journey_checks': 7, 'browser_checks': 17,
            'integration_top_level': dict(results), 'frontend_pass': 453,
            'worktree_checked': worktree is not None}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check-worktree', type=Path)
    args = parser.parse_args()
    print(json.dumps(verify(Path(__file__).resolve().parent, args.check_worktree), indent=2))
