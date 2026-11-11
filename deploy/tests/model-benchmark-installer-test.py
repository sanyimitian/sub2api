"""Linux installer preservation checks; Docker operations are stubbed."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


@unittest.skipUnless(hasattr(os, 'geteuid') and os.geteuid() == 0, 'installer requires Linux root')
class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        deploy = Path(__file__).resolve().parent.parent
        (self.root/'deploy').mkdir()
        (self.root/'tools/model-benchmark').mkdir(parents=True)
        (self.root/'bin').mkdir()
        for name in ('install-model-benchmark.sh', 'docker-compose.benchmark.yml'):
            shutil.copy2(deploy/name, self.root/'deploy'/name)
        (self.root/'tools/model-benchmark/Dockerfile').write_text('FROM scratch\n')
        self.compose = self.root/'compose.yml'
        self.compose.write_text('services:\n  sub2api:\n    image: existing-release\n')
        self.envfile = self.root/'.env'
        self.envfile.write_text('DATABASE_PASSWORD=unchanged\nUNRELATED=$(touch '+str(self.root/'should-not-execute')+')\nMODEL_BENCHMARK_HOME='+str(self.root/'home')+'\n')
        docker = self.root/'bin/docker'
        docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$INSTALLER_CALLS"\nexit 0\n')
        docker.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(self.root/'bin')+':'+os.environ['PATH'], 'INSTALLER_CALLS': str(self.root/'calls')}
        self.command = ['bash', str(self.root/'deploy/install-model-benchmark.sh'), '--compose-file', str(self.compose), '--env-file', str(self.envfile), '--project-name', 'existing-project', '--worker-only']

    def test_preserves_settings_credentials_and_original_project_on_repeat(self):
        first = None
        for _ in range(2):
            result = subprocess.run(self.command, env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            content = self.envfile.read_text()
            if first is None: first = content
            self.assertEqual(first, content)
        self.assertIn('DATABASE_PASSWORD=unchanged', first)
        self.assertFalse((self.root/'should-not-execute').exists())
        calls = (self.root/'calls').read_text()
        self.assertIn('--project-name existing-project', calls)
        self.assertNotIn('up -d --no-deps sub2api', calls)
        self.assertEqual(self.envfile.stat().st_mode & 0o777, 0o600)

    def test_rejects_blank_token_without_consuming_next_setting(self):
        self.envfile.write_text('MODEL_BENCHMARK_WORKER_TOKEN=\nMODEL_BENCHMARK_HOME='+str(self.root/'home')+'\n')
        result = subprocess.run(self.command, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('WORKER_TOKEN', result.stderr)

    def test_rejects_normalized_system_directory(self):
        self.envfile.write_text('MODEL_BENCHMARK_WORKER_TOKEN='+('a'*64)+'\nMODEL_BENCHMARK_HOME=/opt/..\n')
        result = subprocess.run(self.command, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('system directory', result.stderr)


if __name__ == '__main__': unittest.main()
