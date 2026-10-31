#!/usr/bin/env python3
"""Freeze the current runtime source, including uncommitted work, for diagnosis."""
import argparse
import json
from pathlib import Path
from lab import snapshot

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    metadata = snapshot(args.output, None)
    (args.output/'source-manifest.json').write_text(json.dumps(metadata, indent=2)+'\n')
    print(json.dumps(metadata, indent=2))
