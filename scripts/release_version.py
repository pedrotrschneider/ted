#!/usr/bin/env python3
"""Calculate versions without publishing tags; finalize only after successful builds.

The queued workflow serializes main/develop. Successful publication owns a tag;
failed builds consume no version. PRs never mutate release state.
"""
import argparse
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


def validate_publication(version, sha, branch, base, tags):
    tag = 'v' + version
    if tag in tags:
        if git('rev-parse', tag + '^{commit}') != sha:
            raise RuntimeError('Version already belongs to another commit; rerun all jobs to recalculate')
    elif version != next_version(base, branch, tags):
        raise RuntimeError('Version is stale; rerun all jobs to recalculate')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--publish-tag', action='store_true')
    args = parser.parse_args()
    base = Path('version.txt').read_text().strip()
    event = os.environ['GITHUB_EVENT_NAME']
    branch = os.environ['GITHUB_REF_NAME']
    run_id = os.environ['GITHUB_RUN_ID']
    sha = os.environ['GITHUB_SHA']
    publish = event == 'push' and branch in ('main','develop')
    tags = git('tag', '--list', 'v*').splitlines()
    if args.check or args.publish_tag:
        if not publish:
            raise RuntimeError('Only main/develop pushes can publish')
        version = os.environ['RELEASE_VERSION']
        validate_publication(version, sha, branch, base, tags)
        if args.publish_tag and 'v' + version not in tags:
            git('config', 'user.name', 'github-actions[bot]')
            git('config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com')
            message = json.dumps(dict(run_id=run_id, sha=sha, branch=branch))
            git('tag', '-a', f'v{version}', sha, '-m', message)
            git('push', 'origin', f'refs/tags/v{version}')
        return
    version = None
    # Reuse completed tags on rerun, including legacy annotated tags.
    if publish:
        for tag in tags:
            if not TAG.fullmatch(tag):
                continue
            message = git('for-each-ref', '--format=%(contents)', f'refs/tags/{tag}')
            try:
                reservation = json.loads(message)
            except (ValueError, TypeError):
                # GitHub creates a lightweight tag when publishing a draft.
                match = TAG.fullmatch(tag)
                if (git('cat-file', '-t', f'refs/tags/{tag}') == 'commit'
                        and git('rev-parse', tag + '^{commit}') == sha
                        and (match.group(4) is None) == (branch == 'main')):
                    version = tag[1:]
                    break
                continue
            if reservation.get('run_id') == run_id:
                if reservation.get('sha') != sha or reservation.get('branch') != branch:
                    raise RuntimeError('Run reservation does not match this commit/branch')
                version = tag[1:]
                break
    if version is None:
        version = next_version(base, branch if publish else 'main', tags)
        if not publish:
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
