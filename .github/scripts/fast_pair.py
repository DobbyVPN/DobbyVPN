#!/usr/bin/env python3
"""Resolve immutable Product/Torturer inputs without executing either candidate."""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

SHA = re.compile(r'[0-9a-f]{40}\Z')


def paired_ref(body: str, branch: str, refs: dict[str, str], repository: str, explicit_pr: str = '') -> tuple[str, str]:
    numbers = set(re.findall(r'https://github\.com/' + re.escape(repository) + r'/pull/(\d+)\b', body))
    if explicit_pr:
        if not re.fullmatch(r'[1-9][0-9]*', explicit_pr):
            raise ValueError('paired PR must be a positive number')
        numbers.add(explicit_pr)
    if len(numbers) > 1:
        raise ValueError('ambiguous explicit paired PRs')
    if numbers:
        ref = f'refs/pull/{next(iter(numbers))}/head'
        if ref not in refs:
            raise ValueError('explicit paired PR head is unavailable')
        return refs[ref], ref
    # Only exact canonical branch identities match. Do not guess from a suffix.
    matches = [name for name in refs if name == f'refs/heads/{branch}' and branch != 'main']
    if matches:
        return refs[matches[0]], matches[0]
    if 'refs/heads/main' not in refs:
        raise ValueError('paired main is unavailable')
    return refs['refs/heads/main'], 'refs/heads/main'


def remote_refs(repository: str) -> dict[str, str]:
    output = subprocess.check_output(['git', '-C', repository, 'ls-remote', 'origin', 'refs/heads/*', 'refs/pull/*/head'], text=True)
    refs = {}
    for line in output.splitlines():
        sha, name = line.split('\t')
        if not SHA.fullmatch(sha):
            raise ValueError('remote returned an invalid SHA')
        refs[name] = sha
    return refs


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--event', required=True, type=Path)
    parser.add_argument('--paired-checkout', required=True)
    parser.add_argument('--repository', choices=('DobbyVPN/DobbyVPN', 'DobbyVPN/Torturer'), default='DobbyVPN/DobbyVPN')
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    event = json.loads(args.event.read_text())
    inputs = event.get('inputs') or {}
    pr = event.get('pull_request') or {}
    own_sha = pr.get('head', {}).get('sha') or inputs.get('source_sha')
    branch = pr.get('head', {}).get('ref') or inputs.get('source_branch', '')
    if not isinstance(own_sha, str) or not SHA.fullmatch(own_sha):
        raise ValueError('an exact source SHA is required')
    is_product = args.repository == 'DobbyVPN/DobbyVPN'
    paired_repository = 'DobbyVPN/Torturer' if is_product else 'DobbyVPN/DobbyVPN'
    sha, ref = paired_ref(pr.get('body') or '', branch, remote_refs(args.paired_checkout),
                          paired_repository, inputs.get('torturer_pr' if is_product else 'product_pr', ''))
    record = dict(source_sha=own_sha if is_product else sha, torturer_sha=sha if is_product else own_sha, paired_ref=ref)
    args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(record), flush=True)


if __name__ == '__main__':
    main()
