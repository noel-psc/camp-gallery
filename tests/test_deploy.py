"""Exercise release/update commands with fake Docker and SSH; no live deployment."""
import fcntl
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'crpi-krl99lxt28fuqfvk.cn-shanghai.personal.cr.aliyuncs.com/noel-dev/camp-gallery'

FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv[1:]
if args[:2] == ['cp', '-a']: args = ['cp', *args[2:]]
root = Path(os.environ['FAKE_ROOT'])
state_file = root / 'state.json'
state = json.loads(state_file.read_text()) if state_file.exists() else {}
name = Path(sys.argv[0]).name
with (root / 'calls.jsonl').open('a') as out:
    out.write(json.dumps([name, *args]) + '\n')
def save(): state_file.write_text(json.dumps(state))
def fail(): sys.exit(1)
if name in ('ssh', 'scp'):
    sys.exit(0)
if args[0] == 'info': sys.exit(0)
if args[:2] == ['compose', 'version']: sys.exit(0)
if args[0] == 'compose':
    if args[1:3] != ['--env-file', '.env']: raise RuntimeError(args)
    op = args[3:]
    if op[:1] == ['ps']:
        print(state.get('container', 'old-container'))
    elif op[:1] == ['stop']:
        state['stopped'] = True
    elif op[:1] == ['start']:
        state['stopped'] = False
        state['container'] = 'old-container'
    elif op[:1] == ['up']:
        env = Path('.env').read_text()
        state['container'] = 'restored-container' if 'camp-gallery-rollback:' in env else 'new-container'
        state['stopped'] = False
    else: raise RuntimeError(args)
    save()
elif args[0] == 'inspect':
    if '--format' in args:
        fmt = args[args.index('--format') + 1]
        failed = os.environ.get('FAIL_HEALTH') and args[1] == 'new-container'
        if fmt == '{{.State.Status}}': print('exited' if failed else 'running')
        elif '.State.Health' in fmt: print('unhealthy' if failed else 'healthy')
        else: raise RuntimeError(args)
    else:
        print(json.dumps([{
            'Image': 'sha256:old-image-id',
            'Mounts': [{'Destination': '/data', 'Type': 'volume', 'Name': 'original-data'}],
            'Config': {'Labels': {'com.docker.compose.project.config_files': str(Path.cwd() / 'docker-compose.yml')}}
        }]))
elif args[:2] == ['image', 'inspect']:
    print(json.dumps([{'Config': {'Healthcheck': {'Test': ['CMD', '/camp', 'healthcheck']}}}]))
elif args[0] == 'pull':
    if os.environ.get('FAIL_PULL'): fail()
elif args[0] == 'cp':
    if args[1].startswith('old-container:'):
        if os.environ.get('FAIL_BACKUP'): fail()
        folder = Path(args[2])
        folder.mkdir(parents=True)
        (folder / 'camp.sqlite3').write_bytes(b'original-database')
        (folder / 'uploads').mkdir()
        (folder / 'uploads' / 'photo.png').write_bytes(b'original-photo')
    elif args[2].startswith('restore-container:'):
        folder = Path(args[1])
        assert (folder / 'camp.sqlite3').read_bytes() == b'original-database'
        assert (folder / 'uploads' / 'photo.png').read_bytes() == b'original-photo'
        state['restored_original_data'] = True
        save()
    else: raise RuntimeError(args)
elif args[:2] == ['volume', 'create']:
    state['recovery_volume'] = args[2]
    save()
    print(args[2])
elif args[0] == 'create': print('restore-container')
elif args[0] in ('tag', 'push', 'rm'): pass
elif args[:2] == ['buildx', 'build']: pass
else: raise RuntimeError(args)
'''


class DeployTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        for name in ('docker', 'ssh', 'scp'):
            tool = self.bin / name
            tool.write_text(FAKE)
            tool.chmod(0o755)
        self.site = self.root / 'site'
        self.site.mkdir()
        (self.site / '.env').write_text('IMAGE=old-version\nADMIN_PASSWORD=keep-secret\nSECRET_KEY=keep-key\nPUBLIC_BASE_URL=http://noel-psc.cn:8000\n')
        (self.site / 'docker-compose.yml').write_text('services:\n  camp:\n    image: ${IMAGE}\n')
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'], FAKE_ROOT=str(self.root), DEPLOY_DIR=str(self.site), UPDATE_TIMEOUT_SECONDS='2')
        for key in ('ACR_REGISTRY', 'ACR_NAMESPACE', 'IMAGE_NAME', 'PLATFORM', 'FAIL_PULL', 'FAIL_HEALTH', 'FAIL_BACKUP'):
            self.env.pop(key, None)

    def run_script(self, script, *args, **env):
        return subprocess.run(['bash', str(ROOT / 'deploy' / script), *args], cwd=self.site, env={**self.env, **env}, text=True, capture_output=True, timeout=20)

    def calls(self):
        file = self.root / 'calls.jsonl'
        return [json.loads(line) for line in file.read_text().splitlines()] if file.exists() else []

    def state(self):
        return json.loads((self.root / 'state.json').read_text())

    def test_update_backs_up_and_preserves_settings(self):
        result = self.run_script('update.sh', 'test-version')
        self.assertEqual(result.returncode, 0, result.stderr)
        text = (self.site / '.env').read_text()
        self.assertIn(REPOSITORY + ':test-version', text)
        for setting in ('ADMIN_PASSWORD=keep-secret', 'SECRET_KEY=keep-key', 'PUBLIC_BASE_URL=http://noel-psc.cn:8000'):
            self.assertIn(setting, text)
        backups = list((self.site / 'backups').glob('update-*'))
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0] / 'data/uploads/photo.png').read_bytes(), b'original-photo')
        self.assertEqual(self.state()['container'], 'new-container')
        self.assertNotIn('keep-secret', result.stdout + result.stderr)
        ops = self.calls()
        pull = next(i for i, c in enumerate(ops) if c[1] == 'pull')
        stop = next(i for i, c in enumerate(ops) if c[-2:] == ['stop', 'camp'])
        self.assertLess(pull, stop)

    def test_pull_failure_leaves_running_service_and_configuration(self):
        before = (self.site / '.env').read_bytes()
        result = self.run_script('update.sh', FAIL_PULL='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.site / '.env').read_bytes(), before)
        self.assertFalse(any(c[-2:] == ['stop', 'camp'] for c in self.calls()))

    def test_backup_failure_restarts_original_without_recreate(self):
        result = self.run_script('update.sh', FAIL_BACKUP='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.state()['container'], 'old-container')
        self.assertFalse(self.state()['stopped'])
        self.assertFalse(any('up' in c for c in self.calls()))
        self.assertIn('IMAGE=old-version', (self.site / '.env').read_text())

    def test_health_failure_restores_snapshot_to_new_volume(self):
        result = self.run_script('update.sh', FAIL_HEALTH='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.state()['restored_original_data'])
        self.assertEqual(self.state()['container'], 'restored-container')
        self.assertNotEqual(self.state()['recovery_volume'], 'original-data')
        text = (self.site / '.env').read_text()
        self.assertIn('COMPOSE_FILE=', text)
        self.assertIn('camp-gallery-rollback:', text)
        recovery = json.loads((self.site / 'compose.camp-recovery.json').read_text())
        self.assertEqual(recovery['services']['camp']['image'], '${IMAGE}')
        self.assertEqual(recovery['volumes']['camp-recovery-data']['name'], self.state()['recovery_volume'])
        self.assertFalse(any('down' in c or 'prune' in c for c in self.calls()))

    def test_update_lock_prevents_parallel_deployment(self):
        with (self.site / '.camp-update.lock').open('w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.run_script('update.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(c[1] == 'pull' for c in self.calls()))

    def test_release_rebuilds_and_publishes_unique_tag_and_latest(self):
        result = self.run_script('release.sh', 'test-version', SOURCE_IMAGE='old-image')
        self.assertEqual(result.returncode, 0, result.stderr)
        ops = self.calls()
        self.assertTrue(any(c[1:3] == ['buildx', 'build'] for c in ops))
        pushes = [c[2] for c in ops if c[1] == 'push']
        self.assertEqual(pushes, [REPOSITORY + ':test-version', REPOSITORY + ':latest'])
        self.assertFalse(any('old-image' in c for c in ops))

    def test_release_can_deploy_over_ssh_after_push(self):
        result = self.run_script('release.sh', '--deploy', 'ecs-user@139.196.79.195', 'test-version')
        self.assertEqual(result.returncode, 0, result.stderr)
        ops = self.calls()
        self.assertEqual(ops[0][0], 'ssh')
        self.assertEqual(ops[-2][0], 'ssh')
        self.assertIn('sudo -n tee /opt/camp-gallery/update.sh', ops[-2][-1])
        self.assertIn('cd /opt/camp-gallery', ops[0][-1])
        self.assertIn('cd /opt/camp-gallery', ops[-1][-1])
        self.assertFalse(any('~/camp-gallery' in part for call in ops for part in call))
        self.assertEqual(ops[-1][0], 'ssh')
        self.assertIn(REPOSITORY + ':test-version', ops[-1][-1])

    def test_rejects_invalid_version_and_ssh_target(self):
        self.assertNotEqual(self.run_script('release.sh', 'latest').returncode, 0)
        self.assertNotEqual(self.run_script('release.sh', '--deploy', 'host;touch bad').returncode, 0)
        self.assertNotEqual(self.run_script('update.sh', 'bad tag').returncode, 0)
        self.assertEqual(self.calls(), [])


if __name__ == '__main__':
    unittest.main()
