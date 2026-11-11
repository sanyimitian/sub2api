import json
import os
import stat
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from environment import Environment
from prepare import main as prepare_main, make_grader_sources_readable
from server import make_handler


class EnvironmentTests(unittest.TestCase):
    def test_sandbox_permissions_repair_only_public_sources_without_following_links(self):
        source = self.home / 'sources/LiveCodeBench'
        module = source / 'lcb_runner/evaluation/testing_util.py'
        module.parent.mkdir(parents=True)
        module.write_text('public scorer')
        source.chmod(0o700); module.parent.chmod(0o700); module.chmod(0o600)
        secret = self.home / 'credentials.key'
        secret.write_text('secret'); secret.chmod(0o600)
        (source / 'link').symlink_to(secret)
        git = source / '.git'; git.mkdir(); git.chmod(0o700)
        make_grader_sources_readable(source)
        self.assertEqual(stat.S_IMODE(source.stat().st_mode), 0o755)
        self.assertEqual(stat.S_IMODE(module.stat().st_mode), 0o644)
        self.assertEqual(stat.S_IMODE(secret.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(git.stat().st_mode), 0o700)
        self.assertTrue((source / 'link').is_symlink())

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)

    def wait_for(self, condition):
        deadline = time.monotonic() + 3
        while not condition() and time.monotonic() < deadline:
            time.sleep(.01)
        self.assertTrue(condition())

    def test_missing_image_builds_once_and_uses_fixed_local_arguments(self):
        commands = []
        def run(args, **kwargs):
            commands.append(args)
            if args[:3] == ['docker', 'image', 'inspect'] and not any(c[:2] == ['docker', 'build'] for c in commands):
                raise RuntimeError('missing')
            return 'sha256:fixture'
        activated = []
        env = Environment(self.home, 'fixture-image:v1', 'UTC', runner=run, activate=activated.append)
        env.prepare()
        self.assertEqual(activated, ['sha256:fixture'])
        self.assertEqual(env.snapshot()['status'], 'ready')
        self.assertEqual(env.snapshot()['progress'], 100)
        self.assertEqual(sum(c[:2] == ['docker', 'build'] for c in commands), 1)
        self.assertTrue(any(c[1].endswith('prepare.py') and c[-1] == str(self.home) for c in commands if len(c) > 1))
        env.prepare()
        self.assertEqual(sum(c[:2] == ['docker', 'build'] for c in commands), 1)

    def test_single_background_attempt_recovers_without_leaking_raw_errors(self):
        entered, release = threading.Event(), threading.Event()
        attempts = []
        env = Environment(self.home, 'fixture', 'UTC', retry_seconds=.02)
        def prepare():
            attempts.append(1)
            if len(attempts) == 1:
                env.update('docker', 10, 'Checking Docker')
                entered.set(); release.wait(3)
                raise RuntimeError('private-token-and-internal-path')
            env.engine = SimpleNamespace(grader=SimpleNamespace(check_ready=lambda: None), available=threading.Event(), stopping=threading.Event(), wake=threading.Event())
            env.update('ready', 100, 'Ready', status='ready')
        env.prepare = prepare
        self.addCleanup(env.close)
        env.start(); self.assertTrue(entered.wait(3))
        for _ in range(20): env.start(); env.retry()
        self.assertEqual(len(attempts), 1)
        release.set()
        self.wait_for(lambda: env.snapshot()['status'] == 'ready')
        self.assertEqual(len(attempts), 2)
        self.assertNotIn('private-token', json.dumps(env.snapshot()))

    def test_failure_waits_then_admin_retry_wakes_single_attempt(self):
        env = Environment(self.home, 'fixture', 'UTC', retry_seconds=300)
        env.prepare = lambda: (_ for _ in ()).throw(RuntimeError('SECRET'))
        self.addCleanup(env.close)
        env.start()
        self.wait_for(lambda: env.snapshot()['status'] == 'failed')
        self.assertEqual(env.snapshot()['attempt'], 1)
        self.assertIsNotNone(env.snapshot()['next_retry_at'])
        self.assertNotIn('SECRET', json.dumps(env.snapshot()))
        env.retry()
        self.wait_for(lambda: env.snapshot()['attempt'] == 2)

    def test_status_is_available_without_bank_and_users_cannot_trigger_installs(self):
        env = Environment(self.home, 'fixture', 'UTC')
        server = ThreadingHTTPServer(('127.0.0.1', 0), make_handler(None, None, 'test-token', env))
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        def request(path, method='GET', body=None, authorized=True):
            req = urllib.request.Request(f'http://127.0.0.1:{server.server_port}'+path, method=method,
                data=json.dumps(body).encode() if body is not None else None,
                headers={'Authorization': 'Bearer test-token'} if authorized else {})
            try:
                with urllib.request.urlopen(req, timeout=3) as response: return response.status, json.load(response)
            except urllib.error.HTTPError as error: return error.code, json.load(error)
        try:
            status, data = request('/state')
            self.assertEqual(status, 200); self.assertFalse(data['ready']); self.assertIsNone(data['suite'])
            self.assertEqual(request('/users/7/state')[0], 200)
            self.assertEqual(request('/runs')[1]['items'], [])
            self.assertEqual(request('/users/7/runs', 'POST', {})[0], 503)
            self.assertEqual(request('/public-results')[0], 503)
            self.assertEqual(request('/environment/retry', 'POST', {}, authorized=False)[0], 401)
            self.assertEqual(request('/users/7/environment/retry', 'POST', {})[0], 404)
            self.assertEqual(request('/environment/retry', 'POST', {'command': 'injected'})[0], 400)
            self.assertEqual(request('/environment/retry', 'POST', {})[0], 202)
            self.assertFalse((self.home/'bank.sqlite3').exists())
        finally:
            server.shutdown(); server.server_close(); thread.join(3)

    def test_existing_bank_is_preserved_while_missing_sources_are_checked(self):
        bank = self.home/'bank.sqlite3'; bank.write_bytes(b'existing-frozen-bank')
        with patch('sys.argv', ['prepare.py', '--home', str(self.home)]), patch('prepare.sources') as sources:
            prepare_main()
        sources.assert_called_once_with(self.home, None, download_datasets=False)
        self.assertEqual(bank.read_bytes(), b'existing-frozen-bank')


if __name__ == '__main__': unittest.main()
