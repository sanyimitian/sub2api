import json
import threading
import urllib.error
import urllib.request
from datetime import timedelta
from http.server import ThreadingHTTPServer
from unittest.mock import Mock, patch

from engine import Engine
from server import make_handler
from store import Problem, Store
from test_worker import Fixture, FakeGrader


class DeleteResultTests(Fixture):
    def setUp(self):
        super().setUp()
        self.engine = Engine(self.store, Mock(), FakeGrader())
        self.store.execution_settings({'user_testing_enabled': True})

    def finish(self, run):
        self.store.append(run['id'], {'index': 1, 'category': 'math', 'status': 'passed',
                                     'passed': True, 'answer': 'private-answer'})
        self.store.finish(run['id'], 'completed')
        artifact = self.store.directory / 'artifacts' / run['id']
        artifact.mkdir(parents=True)
        (artifact / '001.json').write_text('private-answer')
        return artifact

    def test_delete_erases_result_and_artifacts_and_preserves_user_quota(self):
        target = {'suite_id': self.store.state()['suite']['id'], 'base_url': 'https://example.com',
                  'model': 'model', 'api_key': 'private-key'}
        run = self.store.create(target, 7)
        artifact = self.finish(run)
        before = self.store.state(7)['quota']
        self.assertEqual(self.store.delete_run(run['id']), {'deleted': True})
        self.assertFalse(artifact.exists())
        self.assertEqual(self.store.state(7)['quota'], before)
        self.assertEqual(self.store.list_runs()['total'], 0)
        self.assertEqual(self.store.list_runs(1, 7)['total'], 0)
        self.assertEqual(self.store.list_runs(users_only=True)['total'], 0)
        self.assertEqual(self.store.user_results()['items'], [])
        for action in (self.store.detail, self.store.rerun, self.store.cancel):
            with self.assertRaises(Problem) as caught:
                action(run['id'])
            self.assertEqual(caught.exception.status, 404)
        with self.store.transaction() as db:
            row = db.execute('SELECT * FROM runs WHERE id=?', (run['id'],)).fetchone()
            for field in ('model', 'base_url', 'effort', 'error'):
                self.assertEqual(row[field], '')
            self.assertEqual(row['credential'], b'')
            self.assertEqual(row['results'], '[]')
        self.assertEqual(self.store.delete_run(run['id']), {'deleted': True})
        reopened = Store(self.store.directory, self.store.bank, clock=lambda: self.date)
        self.assertEqual(reopened.list_runs()['total'], 0)
        self.assertEqual(reopened.state(7)['quota'], before)
        for _ in range(2):
            other = self.store.create(target, 7)
            self.store.finish(other['id'], 'completed')
        with self.assertRaises(Problem) as caught:
            self.store.create(target, 7)
        self.assertEqual(caught.exception.status, 429)

    def test_delete_removes_shared_schedule_result_without_changing_next_run(self):
        plan = self.store.create_schedule({'name': 'Test', 'base_url': 'https://example.com',
            'model': 'model', 'api_key': 'key', 'effort': 'medium', 'cron_expression': '@every 30m', 'public_results': True})
        self.engine.displays.save({'name': 'Local', 'source_type': 'local', 'schedule_id': plan['id']})
        self.date += timedelta(minutes=30)
        self.store.enqueue_due_schedules()
        run = self.store.claim()
        self.finish(run)
        before = self.store.list_schedules()['items'][0]
        self.assertEqual(len(self.store.public_results()['items']), 1)
        self.assertTrue(self.engine.displays.showcase()['items'][0]['items'])
        self.store.delete_run(run['id'])
        self.assertEqual(self.store.public_results()['items'], [])
        self.assertEqual(self.engine.displays.showcase()['items'][0]['items'], [])
        after = self.store.list_schedules()['items'][0]
        self.assertIsNone(after['last_run_id'])
        self.assertEqual(after['next_run_at'], before['next_run_at'])
        self.assertTrue(after['enabled'])
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.date += timedelta(minutes=30)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)

    def test_active_results_are_rejected_and_finished_states_are_deletable(self):
        for status in ('queued', 'running', 'cancelling', 'completed', 'failed', 'cancelled', 'interrupted'):
            with self.subTest(status=status):
                run = self.create()
                with self.store.transaction() as db:
                    db.execute('UPDATE runs SET status=? WHERE id=?', (status, run['id']))
                if status in ('queued', 'running', 'cancelling'):
                    with self.assertRaises(Problem) as caught:
                        self.store.delete_run(run['id'])
                    self.assertEqual(caught.exception.status, 409)
                    self.assertEqual(self.store.detail(run['id'])['status'], status)
                else:
                    self.assertTrue(self.store.delete_run(run['id'])['deleted'])

    def test_existing_retest_is_preserved_when_parent_is_deleted(self):
        parent = self.create()
        self.finish(parent)
        child = self.store.rerun(parent['id'])
        self.store.delete_run(parent['id'])
        self.assertEqual(self.store.detail(child['id'])['rerun_of'], parent['id'])
        self.assertEqual(self.store.claim()['id'], child['id'])

    def test_cleanup_failure_is_retryable_and_periodic_cleanup_reclaims_files(self):
        run = self.create()
        artifact = self.finish(run)
        with patch('store.shutil.rmtree', side_effect=OSError('unavailable')):
            with self.assertRaises(Problem) as caught:
                self.store.delete_run(run['id'])
        self.assertEqual(caught.exception.status, 500)
        self.assertIn('测试结果已移除，但答案文件清理失败', str(caught.exception))
        self.assertEqual(self.store.list_runs()['total'], 0)
        self.assertTrue(artifact.exists())
        self.store.prune_history()
        self.assertFalse(artifact.exists())

    def test_delete_is_only_available_to_local_admin_namespace(self):
        run = self.create()
        self.finish(run)
        server = ThreadingHTTPServer(('127.0.0.1', 0), make_handler(self.store, self.engine, 'token'))
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        def request(path, token='token'):
            req = urllib.request.Request(f'http://127.0.0.1:{server.server_port}' + path,
                                         headers={'Authorization': 'Bearer ' + token}, method='DELETE')
            try:
                with urllib.request.urlopen(req, timeout=3) as response:
                    return response.status, json.load(response)
            except urllib.error.HTTPError as error:
                return error.code, json.load(error)
        try:
            path = '/runs/' + run['id']
            self.assertEqual(request(path, 'invalid')[0], 401)
            self.assertEqual(request('/users/7' + path)[0], 403)
            self.assertEqual(request('/sites/' + 'a'*32 + path)[0], 403)
            self.assertEqual(request('/sites/' + 'a'*32 + '/users/7' + path)[0], 403)
            self.assertEqual(request('/runs/' + '0'*32)[0], 404)
            with patch('store.shutil.rmtree', side_effect=OSError('/private/path')):
                status, data = request(path)
            self.assertEqual(status, 500)
            self.assertIn('测试结果已移除，但答案文件清理失败', data['message'])
            self.assertNotIn('/private/path', data['message'])
            self.assertEqual(request(path), (200, {'deleted': True}))
        finally:
            server.shutdown(); server.server_close(); thread.join(3)
