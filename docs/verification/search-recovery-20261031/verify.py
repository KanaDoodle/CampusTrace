#!/usr/bin/env python3
"""Independently verify archived sources, k6 rows and trace relationships."""
import argparse
import csv
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def fingerprint(files):
    return sha(json.dumps(files, sort_keys=True).encode())


def value(summary, name):
    return summary['metrics'].get(name, {}).get('values', {})


def verify(directory, worktree):
    read = lambda name: json.loads((directory / name).read_text())
    result = read('results.json')
    hashes = read('raw-file-sha256.json')
    sources = read('source-file-sha256.json')
    with tarfile.open(directory / 'evidence.tar.gz') as archive:
        members = [m for m in archive.getmembers() if m.isfile()]
        assert len(members) == len(hashes)
        assert {m.name for m in members} == set(hashes)
        for m in members:
            assert sha(archive.extractfile(m).read()) == hashes[m.name], m.name
        reports = {n: json.load(archive.extractfile(f'final-{n}/report.json')) for n in (1000, 10000)}
        reproduction = archive.extractfile('reproduction.log').read().decode()
        assert 'FAIL' in reproduction and 'still waiting for the remote probe' in reproduction
        race = archive.extractfile('full-race.log').read().decode().splitlines()
        assert not any(line.startswith('FAIL') for line in race)
        regression = archive.extractfile('search-regression.log').read().decode().splitlines()
        assert not any(line.startswith('--- FAIL:') for line in regression)
        assert any(line.startswith('--- PASS: TestSearchReadDetachedRecoveryKeepsCurrentACL') for line in regression)
    expected = {f'{role}/{name}': digest for role, files in sources.items() for name, digest in files.items()}
    with tarfile.open(directory / 'source-snapshots.tar.gz') as archive:
        members = [m for m in archive.getmembers() if m.isfile()]
        assert len(members) == len(expected) and {m.name for m in members} == set(expected)
        for m in members:
            assert sha(archive.extractfile(m).read()) == expected[m.name], m.name
    for role, files in sources.items():
        assert fingerprint(files) == result['source'][role + '_fingerprint']
    changes = sorted(p for p in set(sources['baseline']) | set(sources['candidate'])
                     if sources['baseline'].get(p) != sources['candidate'].get(p))
    assert changes == result['source']['runtime_delta']
    assert len(changes) == 7
    for n, report in reports.items():
        assert report['status'] == 'recorded' and report['dataset_and_generation_preserved']
        assert report['seeded_jobs'] == n and len(report['phases']) == 8
        assert report['order'] == ['baseline', 'candidate', 'candidate', 'baseline']
        assert report['source']['runtime_sha256'] == fingerprint(sources['baseline'])
        assert report['candidate_source']['runtime_sha256'] == fingerprint(sources['candidate'])
        for summary in report['phases'].values():
            assert not summary['observed_host_competitors']
        for excluded in report.get('excluded_host_attempts', []):
            assert excluded['summary']['exit_code'] == 0
    assert reports[1000]['environment'] == reports[10000]['environment']
    assert reports[1000]['workload']['load_script_sha256'] == reports[10000]['workload']['load_script_sha256']
    with (directory / 'results.csv').open() as file:
        csv_rows = list(csv.DictReader(file))
    assert len(csv_rows) == len(result['rows']) == 48
    for actual, row in zip(csv_rows, result['rows']):
        for key, expected_value in row.items():
            assert (float(actual[key]) == expected_value if isinstance(expected_value, (int, float))
                    else actual[key] == str(expected_value)), (key, row)
        summary = reports[row['jobs']]['phases'][row['run']]
        kind = row['class']
        assert row['attempts'] == value(summary, kind + '_attempts')['count']
        assert row['failures'] == value(summary, kind + '_failures')['count']
        for key, metric in (('p50_ms', 'med'), ('p95_ms', 'p(95)'), ('p99_ms', 'p(99)')):
            assert row[key] == value(summary, kind + '_ms')[metric]
    totals = {role: {'attempts': 0, 'failures': 0, 'dropped': 0} for role in ('baseline', 'candidate')}
    for row in result['aggregates']:
        summary = reports[row['jobs']]['phases'][row['run']]
        role = 'candidate' if 'candidate' in row['run'] else 'baseline'
        assert row['variant'] == role and row['mode'] == summary['mode']
        for key in ('attempts', 'failures', 'dropped'):
            count = value(summary, 'dropped_iterations' if key == 'dropped' else key).get('count', 0)
            assert count == row[key]
            totals[role][key] += count
        assert row['attempts'] >= 180
        assert row['k6_exit'] == summary['exit_code']
        for key, metric in (('p50_ms', 'med'), ('p95_ms', 'p(95)'), ('p99_ms', 'p(99)')):
            assert row[key] == value(summary, 'latency_ms')[metric]
        if role == 'candidate':
            assert row['failures'] == row['dropped'] == row['k6_exit'] == 0
    assert totals == result['totals']
    for n in (1000, 10000):
        rows = [r for r in result['aggregates'] if r['jobs'] == n and r['mode'] == 'fulltext']
        base = max(r['p95_ms'] for r in rows if r['variant'] == 'baseline')
        candidate = max(r['p95_ms'] for r in rows if r['variant'] == 'candidate')
        assert candidate <= base * 1.5 + 25
    traces = read('recovery-traces.json')
    for sample in traces:
        probe, parent = sample['probe'], sample['foreground_core']
        assert probe['parent_id'] == parent['span_id'] and probe['trace_id'] == parent['trace_id']
        assert probe['name'] == 'search.query.probe' and parent['name'] == 'search.core'
        assert probe['start_ns'] + probe['duration_ms'] * 1e6 > parent['start_ns'] + parent['duration_ms'] * 1e6
    assert len(traces) == result['diagnostic_probe_count'] > 0
    checked = {'status': 'PASS', 'raw_files': len(hashes), 'source_files': len(expected), 'csv_rows': len(csv_rows),
               'race_passed_packages': sum(line.startswith('ok ') for line in race),
               'search_regression_passed_top_level': sum(line.startswith('--- PASS:') for line in regression),
               'totals': totals, 'diagnostic_probe_spans': len(traces)}
    if worktree:
        names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=worktree).decode().split('\0')
        roots = ('cmd/', 'internal/', 'web/', 'migrations/')
        files = ('go.mod', 'go.sum', 'Dockerfile', 'Dockerfile.search', '.dockerignore', 'docker-compose.yml', 'compose.app.yml', 'compose.search.yml')
        current = {name: sha((worktree / name).read_bytes()) for name in names
                   if name and (name.startswith(roots) or name in files) and (worktree / name).is_file()}
        assert current == sources['candidate'], 'Worktree differs from measured candidate'
        checked['worktree_matches_candidate'] = True
    return checked


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check-worktree', type=Path)
    parser.add_argument('--write', action='store_true')
    args = parser.parse_args()
    directory = Path(__file__).resolve().parent
    checked = verify(directory, args.check_worktree)
    if args.write:
        (directory / 'verification.json').write_text(json.dumps(checked, indent=2) + '\n')
    print(json.dumps(checked, indent=2))
