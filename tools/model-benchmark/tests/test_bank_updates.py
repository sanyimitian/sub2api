import base64
import io
import json
import pickle
import shutil
import sqlite3
import threading
from unittest.mock import patch
import zlib

from bank_updates import BankUpdates, changes
from prepare import safe_private_tests
from store import Bank, Problem, Store
from test_worker import Fixture
from update_bank import resolve_datasets


class FakeGrader:
    def check_sandbox(self): pass
    def grade_text(self, question, answer): return {"passed": True}


class BankUpdateTests(Fixture):
    def candidate(self, *, change=False, small=False):
        version = 'public-v2-' + ('a' if change else 'b') * 64
        directory = self.root / 'banks'; directory.mkdir(exist_ok=True)
        path = directory / (version + '.sqlite3')
        shutil.copyfile(self.store.bank.path, path)
        with sqlite3.connect(path) as db:
            manifest = dict(self.store.bank.manifest, version=version)
            db.execute("UPDATE metadata SET value=? WHERE key='manifest'", (json.dumps(manifest),))
            if change:
                row = db.execute("SELECT id,payload FROM questions ORDER BY id LIMIT 1").fetchone()
                q = json.loads(zlib.decompress(row[1])); q['prompt'] = 'Changed question'
                db.execute('UPDATE questions SET payload=? WHERE id=?', (zlib.compress(json.dumps(q).encode()), row[0]))
            if small:
                db.execute("DELETE FROM questions WHERE category='coding'")
        return Bank(path)

    def test_activation_preserves_queued_running_and_historical_versions_after_restart(self):
        original = self.store.state()['suite']
        old_run = self.create()
        running = self.store.claim()
        queued = self.create()
        candidate = self.candidate(change=True)
        new = self.store.activate_bank(candidate.manifest['version'])
        self.assertNotEqual(new['fingerprint'], original['fingerprint'])
        self.assertEqual(new['revision'], original['revision'] + 1)
        old_id = 'coding:0'
        self.assertEqual(self.store.bank_for_version(running['bank_version']).get(old_id)['prompt'], 'Return 42')
        self.assertEqual(self.store.bank.get(old_id)['prompt'], 'Changed question')
        self.assertEqual(self.store.claim()['bank_version'], original['bank_version'])
        self.assertEqual(self.store.detail(queued['id'])['suite'], original)
        self.store.finish(old_run['id'], 'completed')
        latest = self.store.rerun(old_run['id'])
        self.assertEqual(latest['suite']['bank_version'], candidate.manifest['version'])
        reopened = Store(self.store.directory, Bank(self.root / 'bank.sqlite3'), clock=lambda: self.date)
        self.assertEqual(reopened.state()['suite'], new)
        self.assertEqual(reopened.detail(old_run['id'])['suite'], original)

    def test_incompatible_counts_do_not_change_active_bank_or_daily_suite(self):
        before = self.store.state()
        candidate = self.candidate(small=True)
        with self.assertRaises(Problem): self.store.activate_bank(candidate.manifest['version'])
        self.assertEqual(self.store.state()['suite'], before['suite'])
        self.assertEqual(self.store.bank.manifest['version'], before['bank_version'])
        with self.assertRaises(Problem): self.store.bank_for_version('../../credentials.key')

    def test_download_is_single_flight_and_unchanged_data_does_not_refresh_suite(self):
        candidate = self.candidate()
        self.assertEqual(changes(self.store.bank, candidate), {'added': 0, 'changed': 0, 'removed': 0})
        entered, release = threading.Event(), threading.Event()
        def run(*args, **kwargs):
            entered.set(); release.wait(3)
            return json.dumps({'version': candidate.manifest['version']})
        updater = BankUpdates(self.root, self.store, run, FakeGrader())
        before = self.store.state()['suite']
        with patch.object(Bank, 'get', return_value={'ground_truth': '42'}):
            updater.start(); self.assertTrue(entered.wait(2))
            with self.assertRaises(Problem): updater.start()
            release.set(); updater.thread.join(3)
        self.assertEqual(updater.snapshot()['status'], 'current')
        self.assertEqual(self.store.state()['suite'], before)

    def test_candidate_activation_is_explicit_and_failure_is_redacted(self):
        candidate = self.candidate(change=True)
        updater = BankUpdates(self.root, self.store, lambda *a, **kw: json.dumps({'version': candidate.manifest['version']}), FakeGrader())
        original = self.store.state()['suite']
        # Fixtures need a ground truth only for the compatibility check.
        with patch.object(Bank, 'get', return_value={'ground_truth': '42'}): updater.prepare()
        self.assertEqual(updater.snapshot()['status'], 'ready')
        self.assertEqual(self.store.state()['suite'], original)
        with self.assertRaises(Problem): updater.activate('wrong-version')
        updater.activate(candidate.manifest['version'])
        self.assertEqual(updater.snapshot()['status'], 'activated')
        updater.runner = lambda *a, **kw: (_ for _ in ()).throw(ValueError('private-internal-data'))
        updater.prepare()
        self.assertEqual(updater.snapshot()['status'], 'failed')
        self.assertNotIn('private-internal-data', json.dumps(updater.snapshot()))
        self.assertEqual(self.store.bank.manifest['version'], candidate.manifest['version'])
        updater.save(status='checking')
        recovered = BankUpdates(self.root, self.store, updater.runner, FakeGrader())
        self.assertEqual(recovered.snapshot()['status'], 'failed')

    def test_private_tests_are_data_only(self):
        expected = '[{"input":"1","output":"2","testtype":"stdin"}]'
        packed = base64.b64encode(zlib.compress(pickle.dumps(expected))).decode()
        self.assertEqual(json.loads(safe_private_tests(packed)), json.loads(expected))
        self.assertEqual(json.loads(safe_private_tests(expected)), json.loads(expected))
        malicious = base64.b64encode(zlib.compress(pickle.dumps(eval))).decode()
        with self.assertRaises(ValueError): safe_private_tests(malicious)

    def test_discovery_accepts_only_fixed_repository_files_and_commit_ids(self):
        calls = []
        def fetch(url, **kwargs):
            calls.append(url)
            files = ['test6.jsonl', 'test.jsonl', 'test2.jsonl', '../../private.jsonl', 'script.py'] if 'livecodebench' in url else ['data/test-00000-of-00001.parquet']
            return io.BytesIO(json.dumps({'sha': 'a'*40, 'siblings': [{'rfilename': name} for name in files]}).encode())
        with patch('update_bank.urllib.request.urlopen', side_effect=fetch):
            resolved = resolve_datasets()
        self.assertEqual(resolved['coding']['files'], ['test.jsonl', 'test2.jsonl', 'test6.jsonl'])
        self.assertEqual(len(calls), 3)
        with patch('update_bank.urllib.request.urlopen', return_value=io.BytesIO(b'{"sha":"main"}')):
            with self.assertRaises(ValueError): resolve_datasets()
