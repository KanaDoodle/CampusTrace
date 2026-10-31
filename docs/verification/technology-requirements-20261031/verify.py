#!/usr/bin/env python3
"""Verify replay counts, raw outputs and source reconstruction independently."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def fingerprint(files):
    return sha(json.dumps(files, sort_keys=True).encode())


def score(report, dataset):
    assert report['Kind'] == dataset['kind']
    rows = {r['id']: r for r in report['Rows']}
    checks = passed = 0
    for case in dataset['cases']:
        row = rows[case['id']]
        for profile, expected in case['expected'].items():
            checks += 1
            passed += row['technology_rule_results'][profile] == expected
        if case.get('expected_go_fit'):
            checks += 1
            passed += row['go_fit'] == case['expected_go_fit']
    assert checks == report['Checks'] and passed == report['Passed']
    assert len(rows) == len(dataset['cases']) == 14 and report['ProviderCalls'] == 0
    return passed, checks


def verify(directory, worktree):
    read = lambda name: json.loads((directory / name).read_text())
    dataset_raw = (directory / 'fixture.json').read_bytes()
    dataset = json.loads(dataset_raw)
    before, after = read('baseline-replay.json'), read('candidate-replay-release.json')
    assert before['DatasetSHA256'] == after['DatasetSHA256'] == sha(dataset_raw)
    assert score(before, dataset) == (21, 85)
    assert score(after, dataset) == (85, 85)
    hashes = read('raw-file-sha256.json')
    with tarfile.open(directory / 'evidence.tar.gz') as archive:
        members = [m for m in archive.getmembers() if m.isfile()]
        assert len(members) == len(hashes) and {m.name for m in members} == set(hashes)
        for m in members:
            assert sha(archive.extractfile(m).read()) == hashes[m.name], m.name
        initial_raw = archive.extractfile('dataset-initial.json').read()
        initial = json.loads(initial_raw)
        initial_report = json.load(archive.extractfile('candidate-replay.json'))
        assert initial_report['DatasetSHA256'] == sha(initial_raw)
        assert score(initial_report, initial) == (79, 85)
        assert [c['text'] for c in initial['cases']] == [c['text'] for c in dataset['cases']]
        assert initial['profiles'] == dataset['profiles']
        full_race = archive.extractfile('full-race.log').read().decode().splitlines()
        integration = archive.extractfile('integration.log').read().decode().splitlines()
        assert not any(line.startswith('FAIL') for line in full_race)
        assert not any(line.startswith(('FAIL', '--- FAIL:')) for line in integration)
        assert any(line.startswith('--- PASS: TestTechnologyAnalysisDoesNotReuseLegacyClaimsOrReactivateOldGeneration') for line in integration)
        assert any(line.startswith('--- PASS: TestMyRPCTwoInstances') for line in integration)
    reference = read('baseline-reference.json')
    base_archive = (directory / reference['archive']).resolve()
    assert sha(base_archive.read_bytes()) == reference['archive_sha256']
    sources = read('source-files.json')
    metadata = read('source-verification.json')
    for role in ('baseline', 'candidate'):
        assert fingerprint(sources[role]) == metadata[role + '_fingerprint']
    with tempfile.TemporaryDirectory(prefix='ct-tech-verify-') as temporary:
        folder = Path(temporary)
        with tarfile.open(base_archive) as archive:
            selected = [m for m in archive.getmembers() if m.name == 'candidate' or m.name.startswith('candidate/')]
            archive.extractall(folder, members=selected, filter='data')
        source = folder / 'candidate'
        current = {p.relative_to(source).as_posix(): sha(p.read_bytes()) for p in source.rglob('*') if p.is_file()}
        assert current == sources['baseline']
        subprocess.run(['git', 'apply', str(directory / 'upgrade.patch')], cwd=source, check=True, capture_output=True)
        patched = {p.relative_to(source).as_posix(): sha(p.read_bytes()) for p in source.rglob('*') if p.is_file()}
        assert patched == sources['candidate'], 'Patch does not reconstruct the tested candidate'
    checked = {'status': 'PASS', 'baseline_checks': before['Passed'], 'candidate_checks': after['Passed'],
               'total_checks': after['Checks'], 'cases': len(dataset['cases']), 'raw_files': len(hashes),
               'source_reconstruction': 'PASS', 'runtime_changed_files': len(metadata['runtime_delta']),
               'race_passed_packages': sum(line.startswith('ok ') for line in full_race),
               'integration_and_related_passed_top_level': sum(line.startswith('--- PASS:') for line in integration),
               'initial_reference_review_preserved': True}
    if worktree:
        names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=worktree).decode().split('\0')
        roots = ('cmd/', 'internal/', 'web/', 'migrations/')
        files = ('go.mod', 'go.sum', 'Dockerfile', 'Dockerfile.search', '.dockerignore', 'docker-compose.yml', 'compose.app.yml', 'compose.search.yml')
        current = {name: sha((worktree / name).read_bytes()) for name in names
                   if name and (name.startswith(roots) or name in files) and (worktree / name).is_file()}
        assert current == sources['candidate'], 'Worktree differs from tested source'
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
