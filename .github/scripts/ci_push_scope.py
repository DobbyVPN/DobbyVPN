"""Classify a main push from its exact Git trees; uncertainty selects full CI."""
from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess

from ci_scope import classify_paths


def select_push_scope(before: str, after: str, *, root: Path = Path('.')) -> tuple[str, str]:
    if any(not re.fullmatch(r'[0-9a-f]{40}', sha) or sha == '0' * 40 for sha in (before, after)):
        return 'full', 'push-revisions-unavailable'
    try:
        current = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=root, capture_output=True, check=True)
        if current.stdout.decode('ascii').strip() != after:
            return 'full', 'push-checkout-mismatch'
        result = subprocess.run(
            ['git', 'diff', '--name-status', '-z', '--find-renames', before, after, '--'],
            cwd=root, capture_output=True, check=True,
        )
        fields = result.stdout.decode('utf-8').split('\0')
        if not fields or fields.pop() != '':
            return 'full', 'push-diff-invalid'
        paths = []
        index = 0
        while index < len(fields):
            status = fields[index]
            index += 1
            if not re.fullmatch(r'[ACDMRTUXB][0-9]*', status):
                return 'full', 'push-diff-invalid'
            count = 2 if status[0] in 'RC' else 1
            for _ in range(count):
                paths.append(fields[index])
                index += 1
    except (OSError, subprocess.SubprocessError, UnicodeError, IndexError):
        return 'full', 'push-diff-unavailable'
    return classify_paths(paths)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', required=True)
    parser.add_argument('--after', required=True)
    args = parser.parse_args()
    scope, reason = select_push_scope(args.before, args.after)
    print(f'scope={scope}')
    print(f'reason={reason}')
    print('reused_run_id=')


if __name__ == '__main__':
    main()
