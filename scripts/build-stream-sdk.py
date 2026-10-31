#!/usr/bin/env python3
"""Build the optional streaming API with an explicit versioned SDK profile.

The regular go.mod/go.sum stay unchanged. Published mode verifies a real public
version; candidate mode remains explicitly unpublished. Both disable workspaces.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import sys

sys.dont_write_bytecode = True
from stream_sdk import prepare, prepare_published


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--candidate', type=pathlib.Path)
    source.add_argument('--version', help='already published v0.2+ version; fetched with public checksum verification')
    parser.add_argument('--output', required=True, type=pathlib.Path)
    args = parser.parse_args()
    repo = pathlib.Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    if output.exists() and any(output.iterdir()):
        raise SystemExit('use a new empty output directory')
    output.mkdir(parents=True, exist_ok=True)
    before = {p: hashlib.sha256((repo / p).read_bytes()).hexdigest() for p in ('go.mod', 'go.sum')}
    if args.candidate:
        env, modfile, _, result = prepare(repo, args.candidate.resolve(), output / 'sdk')
    else:
        env, modfile, _, result = prepare_published(repo, args.version, output / 'sdk')
    with open(output / 'build.log', 'w') as log:
        subprocess.run(['go', 'build', '-modfile=' + str(modfile), '-tags=kanarpc_stream', '-o', str(output / 'api'), './cmd/api'],
                       cwd=repo, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    if any(hashlib.sha256((repo / p).read_bytes()).hexdigest() != digest for p, digest in before.items()):
        raise RuntimeError('regular module files unexpectedly changed')
    result['regular_module_unchanged'] = True
    (output / 'build-result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
