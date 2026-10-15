import unittest
import json
import os
import subprocess
import tempfile
from pathlib import Path
from release_version import next_version, can_promote

class VersionTests(unittest.TestCase):
    def test_old_rerun_cannot_move_channel_backwards(self):
        self.assertFalse(can_promote('0.1.0','main',['v0.1.1']))
        self.assertFalse(can_promote('0.1.2-rc2','develop',['v0.1.2-rc3']))
        self.assertTrue(can_promote('0.2.0','main',['v0.1.9','v0.2.0-rc9']))
    def test_first_release(self):
        self.assertEqual(next_version('0.1','main',[]),'0.1.0')
        self.assertEqual(next_version('0.1','develop',[]),'0.1.0-rc1')
    def test_candidates_do_not_consume_patch(self):
        tags=['v0.1.0','v0.1.1-rc1','v0.1.1-rc9']
        self.assertEqual(next_version('0.1','main',tags),'0.1.1')
        self.assertEqual(next_version('0.1','develop',tags),'0.1.1-rc10')
    def test_after_main_candidates_reset(self):
        self.assertEqual(next_version('0.1','develop',['v0.1.0','v0.1.0-rc7']),'0.1.1-rc1')
    def test_major_minor_reset_and_unrelated_tags(self):
        tags=['v0.1.9','v0.1.10-rc2','v9.1.7','upstream-3.0','v0.2.0-bad']
        self.assertEqual(next_version('0.2','main',tags),'0.2.0')
        self.assertEqual(next_version('1.0','develop',tags),'1.0.0-rc1')
    def test_numeric_sort_and_gaps(self):
        self.assertEqual(next_version('1.2','main',['v1.2.9','v1.2.11','v1.2.3']),'1.2.12')
    def test_invalid_base(self):
        for value in ['1','v1.2','1.2.3','01.2','1.02','1.2\n3.4','1.2-rc1']:
            with self.assertRaises(ValueError): next_version(value,'main',[])


class ReservationTests(unittest.TestCase):
    def test_push_rerun_develop_and_pr_use_real_tag_ledger(self):
        script = Path(__file__).with_name('release_version.py').resolve()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            remote, repo = root/'remote.git', root/'repo'
            def git(*args):
                return subprocess.check_output(['git',*args], cwd=repo if repo.exists() else root, text=True, stderr=subprocess.DEVNULL).strip()
            git('init','--bare',str(remote))
            git('clone',str(remote),str(repo))
            git('config','user.name','Test')
            git('config','user.email','test@example.invalid')
            (repo/'version.txt').write_text('0.1\n')
            git('add','version.txt')
            git('commit','-m','fixture')
            sha=git('rev-parse','HEAD')
            def run(branch, run_id, event='push'):
                output=root/'output'
                output.write_text('')
                env=dict(os.environ,GITHUB_EVENT_NAME=event,GITHUB_REF_NAME=branch,
                         GITHUB_RUN_ID=str(run_id),GITHUB_RUN_NUMBER=str(run_id),
                         GITHUB_SHA=sha,GITHUB_OUTPUT=str(output),PR_NUMBER='7')
                result=subprocess.check_output(['python3',str(script)],cwd=repo,env=env,text=True,stderr=subprocess.DEVNULL)
                return json.loads(result)
            self.assertEqual(run('develop',1)['version'],'0.1.0-rc1')
            self.assertEqual(run('develop',1)['version'],'0.1.0-rc1')
            self.assertEqual(run('develop',2)['version'],'0.1.0-rc2')
            self.assertEqual(run('main',3)['version'],'0.1.0')
            self.assertEqual(run('develop',4)['version'],'0.1.1-rc1')
            tags=git('ls-remote','--tags','origin')
            preview=run('7/merge',5,'pull_request')
            self.assertEqual(preview['version'],'0.1.1-pr7.5')
            self.assertEqual(preview['publish'],'false')
            self.assertEqual(git('ls-remote','--tags','origin'),tags)
            self.assertEqual(run('main',3)['version'],'0.1.0')
            self.assertEqual(git('rev-parse','HEAD'),sha)

if __name__ == '__main__': unittest.main()

