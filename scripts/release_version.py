#!/usr/bin/env python3
"""Reserve a release version with annotated Git tags; no branch commits.

Run only in the queued release workflow. Tags are durable reservations, including
failed builds. A workflow rerun finds its run ID in the annotation and reuses it.
PRs calculate a preview version without pushing anything.
"""
import json
import os
import re
import subprocess
from pathlib import Path

TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc([1-9][0-9]*))?")


def next_version(base, channel, tags):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", base):
        raise ValueError("version.txt must contain only MAJOR.MINOR, e.g. 0.1")
    releases = [m.groups() for t in tags if (m := TAG.fullmatch(t))]
    major, minor = base.split('.')
    stable = [int(p) for a, b, p, rc in releases if (a,b)==(major,minor) and rc is None]
    patch = max(stable, default=-1) + 1
    core = f"{base}.{patch}"
    if channel == 'main':
        return core
    candidates = [int(rc) for a,b,p,rc in releases if (a,b,int(p))==(major,minor,patch) and rc]
    return f"{core}-rc{max(candidates, default=0)+1}"


def can_promote(version, channel, tags):
    def key(match):
        a,b,p,rc = match.groups()
        return (int(a),int(b),int(p),int(rc or 0))
    current = TAG.fullmatch('v'+version)
    if current is None: return False
    peers = [m for t in tags if (m := TAG.fullmatch(t))
             and (m.group(4) is None) == (channel == 'main')]
    return all(key(current) >= key(peer) for peer in peers)


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


def main():
    base = Path('version.txt').read_text().strip()
    event = os.environ['GITHUB_EVENT_NAME']
    branch = os.environ['GITHUB_REF_NAME']
    run_id = os.environ['GITHUB_RUN_ID']
    sha = os.environ['GITHUB_SHA']
    publish = event == 'push' and branch in ('main','develop')
    tags = git('tag', '--list', 'v*').splitlines()
    version = None
    # An annotated reservation survives both failed jobs and rerun-all-jobs.
    if publish:
        for tag in tags:
            if not TAG.fullmatch(tag):
                continue
            message = git('for-each-ref', '--format=%(contents)', f'refs/tags/{tag}')
            try:
                reservation = json.loads(message)
            except (ValueError, TypeError):
                continue
            if reservation.get('run_id') == run_id:
                if reservation.get('sha') != sha or reservation.get('branch') != branch:
                    raise RuntimeError('Run reservation does not match this commit/branch')
                version = tag[1:]
                break
    if version is None:
        version = next_version(base, branch if publish else 'main', tags)
        if publish:
            git('config', 'user.name', 'github-actions[bot]')
            git('config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com')
            message = json.dumps(dict(run_id=run_id, sha=sha, branch=branch))
            git('tag', '-a', f'v{version}', sha, '-m', message)
            # No force: a conflicting reservation fails closed, never overwrites.
            git('push', 'origin', f'refs/tags/v{version}')
        else:
            version += f"-pr{os.environ['PR_NUMBER']}.{run_id}"
    core = version.split('-')[0]
    # Monotonic across major/minor changes and identical on workflow reruns.
    build_number = 1000 + int(os.environ['GITHUB_RUN_NUMBER'])
    outputs = dict(version=version, core=core, major=core.split('.')[0],
                   tag=f'v{version}', build_number=str(build_number),
                   publish=str(publish).lower(), promote=str(publish and can_promote(version, branch, tags)).lower(), prerelease=str(branch=='develop').lower())
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        for key, value in outputs.items():
            output.write(f'{key}={value}\n')
    print(json.dumps(outputs))

if __name__ == '__main__':
    main()
