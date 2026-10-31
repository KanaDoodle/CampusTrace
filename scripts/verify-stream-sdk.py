#!/usr/bin/env python3
"""Accept a packaged SDK from an independent CampusTrace source snapshot.

No sibling checkout, go.work, replace directive or public release is assumed.
"""
import argparse
import json
import pathlib
import subprocess
import sys

sys.dont_write_bytecode = True
from stream_sdk import prepare, snapshot


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', required=True, type=pathlib.Path)
    parser.add_argument('--output', required=True, type=pathlib.Path)
    parser.add_argument('--browser', help='optional installed Playwright module')
    args = parser.parse_args()
    work = args.output.resolve()
    if work.exists() and any(work.iterdir()):
        raise SystemExit('use a new empty output directory')
    work.mkdir(parents=True, exist_ok=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    consumer = work / 'independent-consumer'
    snapshot(repo, consumer)
    env, modfile, sdk, resolution = prepare(consumer, args.candidate.resolve(), work / 'sdk')
    for name, command in [
        ('consumer-build', ['go', 'build', '-modfile=' + str(modfile), '-tags=kanarpc_stream', '-o', str(work / 'api'), './cmd/api']),
        ('consumer-race', ['go', 'test', '-modfile=' + str(modfile), '-tags=kanarpc_stream', '-race', '-count=1', '-json', './internal/inferencebridge', './internal/agent', './internal/transport']),
    ]:
        with open(work / (name + '.log'), 'w') as out:
            subprocess.run(command, cwd=consumer, env=env, stdout=out, stderr=subprocess.STDOUT, check=True)
    command = ['python3', 'scripts/verify-rpc-agent.py', '--sdk-candidate', str(args.candidate.resolve()), '--output', str(work / 'process')]
    if args.browser:
        command += ['--playwright-module', args.browser]
    with open(work / 'process.log', 'w') as out:
        subprocess.run(command, cwd=consumer, env=env, stdout=out, stderr=subprocess.STDOUT, check=True)
    result = {'status': 'PASS', 'sdk': resolution, 'consumer': 'independent-source-snapshot',
              'checks': ['versioned-module-download', 'no-workspace-or-replace', 'consumer-build', 'consumer-race', 'API-RPC-HTTP-process-fixture'],
              'browser': 'PASS' if args.browser else 'NOT_RUN', 'backend': 'fixture-no-model', 'real_vllm': 'NOT_VALIDATED'}
    (work / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
