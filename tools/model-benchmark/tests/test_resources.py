import json
import threading
import unittest
import urllib.error
import urllib.request
from datetime import timedelta
from http.server import ThreadingHTTPServer
from types import SimpleNamespace
from unittest.mock import Mock, patch

from bank_updates import BankUpdates
from engine import Engine
from resources import GIB, ResourceGuard, ResourcePressure
from server import make_handler
from store import Problem
from test_worker import Fixture, FakeGrader


class ThresholdTests(unittest.TestCase):
    def setUp(self):
        self.metrics = {'host_memory_percent': 50, 'worker_memory_percent': 20, 'disk_free_bytes': 20 * GIB}
        self.guard = ResourceGuard('.', sample=lambda: dict(self.metrics))

    def status(self, memory=None, disk=None, worker=None):
        if memory is not None: self.metrics['host_memory_percent'] = memory
        if disk is not None: self.metrics['disk_free_bytes'] = disk * GIB
        if worker is not None: self.metrics['worker_memory_percent'] = worker
        return self.guard.snapshot(force=True)

    def test_memory_boundary_hysteresis_and_worker_limit(self):
        self.assertFalse(self.status(memory=84.99)['blocked'])
        self.assertTrue(self.status(memory=85)['blocked'])
        self.assertTrue(self.status(memory=82)['blocked'])
        self.assertTrue(self.status(memory=80)['blocked'])
        self.assertFalse(self.status(memory=79.99)['blocked'])
        self.assertTrue(self.status(worker=90)['blocked'])
        self.assertFalse(self.status(worker=79)['blocked'])

    def test_disk_warning_stop_and_recovery_are_independent_of_memory(self):
        self.assertEqual(self.status(disk=9)['status'], 'warning')
        self.assertFalse(self.status(disk=5)['blocked'])
        self.assertTrue(self.status(disk=4.99)['blocked'])
        self.assertTrue(self.status(disk=7.99)['blocked'])
        self.assertFalse(self.status(disk=8)['blocked'])
        self.assertTrue(self.status(memory=90, disk=3)['blocked'])
        self.assertTrue(self.status(memory=70, disk=7)['blocked'])
        self.assertFalse(self.status(disk=8)['blocked'])

    def test_monitor_failure_blocks_and_recovers_without_exposing_paths(self):
        self.guard.sample = Mock(side_effect=OSError('/private/path-and-secret'))
        state = self.guard.snapshot(force=True)
        self.assertEqual(state['status'], 'unavailable')
        self.assertTrue(state['blocked'])
        self.assertNotIn('private', json.dumps(state))
        self.guard.sample = lambda: dict(self.metrics)
        self.assertFalse(self.status()['blocked'])

    def test_bank_update_requires_extra_disk_reserve(self):
        self.status(disk=12.9)
        with self.assertRaises(ResourcePressure): self.guard.require_capacity(update=True)
        self.status(disk=13)
        self.guard.require_capacity(update=True)

    def test_memavailable_excludes_reclaimable_cache_and_reads_both_disks(self):
        content = {'/host/meminfo': 'MemTotal: 1000 kB\nMemAvailable: 200 kB\nMemFree: 10 kB\n',
                   '/sys/fs/cgroup/memory.max': '1000', '/sys/fs/cgroup/memory.current': '950',
                   '/sys/fs/cgroup/memory.stat': 'inactive_file 150\n'}
        with (patch.dict('os.environ', {'MODEL_BENCHMARK_MEMINFO': '/host/meminfo', 'MODEL_BENCHMARK_DOCKER_DISK_PATH': '/host/docker'}),
              patch('resources.Path.read_text', lambda p: content[p.as_posix()]),
              patch('resources.Path.is_file', lambda p: p.as_posix() in content),
              patch('resources.shutil.disk_usage', side_effect=[SimpleNamespace(free=20 * GIB), SimpleNamespace(free=4 * GIB)])):
            metrics = self.guard.read_metrics()
        self.assertEqual(metrics['host_memory_percent'], 80)
        self.assertEqual(metrics['worker_memory_percent'], 80)
        self.assertEqual(metrics['disk_free_bytes'], 4 * GIB)


class AdmissionTests(Fixture):
    def setUp(self):
        super().setUp()
        self.metrics = {'host_memory_percent': 50, 'worker_memory_percent': 10, 'disk_free_bytes': 20 * GIB}
        self.guard = ResourceGuard(self.root, sample=lambda: dict(self.metrics))
        self.store.resources = self.guard
        self.store.execution_settings({'user_testing_enabled': True})

    def target(self):
        return {'suite_id': self.store.state()['suite']['id'], 'base_url': 'https://example.com/v1',
                'api_key': 'test-secret', 'model': 'fixture'}

    def plan(self):
        return self.store.create_schedule({**self.target(), 'name': 'Every minute', 'cron_expression': '@every 1m'})

    def test_blocked_user_admin_retest_and_manual_schedule_do_not_consume_quota(self):
        old = self.store.create(self.target(), 7)
        self.store.finish(old['id'], 'completed')
        plan = self.plan()
        before = self.store.state(7)['quota']
        self.metrics['host_memory_percent'] = 90
        for action in (lambda: self.store.create(self.target(), 7), lambda: self.create(),
                       lambda: self.store.rerun(old['id'], 7), lambda: self.store.run_schedule(plan['id'])):
            with self.assertRaises(Problem) as error: action()
            self.assertEqual(error.exception.status, 503)
        self.assertEqual(self.store.state(7)['quota'], before)
        self.assertEqual(self.store.list_runs()['total'], 1)

    def test_existing_queue_retained_cancel_available_and_resumes_in_order(self):
        first, second = self.create(), self.create()
        self.metrics['host_memory_percent'] = 85
        self.assertIsNone(self.store.claim())
        self.assertEqual(self.store.detail(first['id'])['status'], 'queued')
        self.store.cancel(first['id'])
        self.metrics['host_memory_percent'] = 79
        self.assertEqual(self.store.claim()['id'], second['id'])

    def test_schedules_pause_without_duplicate_catchup(self):
        plan = self.plan()
        self.date += timedelta(days=1)
        self.metrics['host_memory_percent'] = 90
        for _ in range(3): self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.assertEqual(self.store.list_schedules()['items'][0]['next_run_at'], plan['next_run_at'])
        self.metrics['host_memory_percent'] = 70
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.assertEqual(self.store.list_runs()['total'], 1)

    def test_bank_update_blocked_before_background_work(self):
        runner = Mock()
        updater = BankUpdates(self.root, self.store, runner, FakeGrader())
        self.metrics['disk_free_bytes'] = 12 * GIB
        with self.assertRaises(Problem) as error: updater.start()
        self.assertEqual(error.exception.status, 503)
        self.assertIsNone(updater.thread)
        runner.assert_not_called()

    def test_pressure_after_response_pauses_grading_then_resumes_without_repeating_request(self):
        self.store.refresh({'coding': 1, 'math': 0, 'reasoning': 0})
        run = self.create()
        claimed = self.store.claim()
        waiting, resume = threading.Event(), threading.Event()
        client = Mock()
        def generate(*args):
            self.metrics['host_memory_percent'] = 90
            return {'status': 'completed', 'output': [{'type': 'message', 'content': [{'type': 'output_text', 'text': '42'}]}]}
        client.generate.side_effect = generate
        grader = Mock(wraps=FakeGrader())
        engine = Engine(self.store, client, grader)
        def wait(_):
            waiting.set()
            resume.wait(3)
            return False
        with patch.object(engine.stopping, 'wait', side_effect=wait):
            thread = threading.Thread(target=engine.execute, args=(claimed,))
            thread.start()
            try:
                self.assertTrue(waiting.wait(2))
                grader.grade.assert_not_called()
                self.assertEqual(self.store.detail(run['id'])['status'], 'running')
            finally:
                self.metrics['host_memory_percent'] = 70
                resume.set(); thread.join(4)
        self.assertFalse(thread.is_alive())
        self.assertEqual(client.generate.call_count, 1)
        self.assertEqual(grader.grade.call_count, 1)
        self.assertEqual(self.store.detail(run['id'])['status'], 'completed')

    def test_cancel_while_paused_finishes_without_upstream_requests(self):
        run = self.create()
        claimed = self.store.claim()
        self.metrics['host_memory_percent'] = 90
        client = Mock()
        engine = Engine(self.store, client, FakeGrader())
        def wait(_):
            self.store.cancel(run['id'])
            return False
        with patch.object(engine.stopping, 'wait', side_effect=wait):
            engine.execute(claimed)
        self.assertEqual(self.store.detail(run['id'])['status'], 'cancelled')
        client.generate.assert_not_called()

    def test_bank_download_failure_removes_temporary_files_and_keeps_active_bank(self):
        from pathlib import Path
        from update_bank import main
        original = self.store.bank.path.read_bytes()
        resolved = {category: {'repo': 'fixture', 'revision': 'a' * 40, 'files': ['sample']}
                    for category in ('coding', 'math', 'reasoning')}
        def download(url, path, **kwargs):
            Path(path).write_bytes(b'partial')
            raise ResourcePressure('low disk')
        with (patch('sys.argv', ['update_bank.py', '--home', str(self.root)]),
              patch('update_bank.ResourceGuard', return_value=self.guard),
              patch('update_bank.resolve_datasets', return_value=resolved),
              patch('update_bank.download', side_effect=download)):
            with self.assertRaises(ResourcePressure): main()
        self.assertEqual(list(self.root.glob('bank-update-*')), [])
        self.assertEqual(self.store.bank.path.read_bytes(), original)

    def test_state_exposes_metrics_only_to_admin_and_read_cancel_stay_available(self):
        run = self.store.create(self.target(), 7)
        engine = Engine(self.store, Mock(), FakeGrader())
        server = ThreadingHTTPServer(('127.0.0.1', 0), make_handler(self.store, engine, 'test-token'))
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        def request(path, method='GET'):
            req = urllib.request.Request(f'http://127.0.0.1:{server.server_port}'+path, method=method,
                data=b'{}' if method == 'POST' else None, headers={'Authorization': 'Bearer test-token'})
            try:
                with urllib.request.urlopen(req, timeout=3) as response: return response.status, json.load(response)
            except urllib.error.HTTPError as error: return error.code, json.load(error)
        try:
            self.metrics['host_memory_percent'] = 90
            self.guard.snapshot(force=True)
            self.assertTrue(request('/state')[1]['resources']['blocked'])
            user = request('/users/7/state')[1]
            self.assertTrue(user['ready'])
            self.assertNotIn('host_memory_percent', user['resources'])
            self.assertEqual(request('/users/7/runs/'+run['id']+'/cancel', 'POST')[0], 200)
            self.assertEqual(request('/users/7/runs/'+run['id']+'/rerun', 'POST')[0], 503)
            self.assertEqual(request('/public-results')[0], 200)
        finally:
            server.shutdown(); server.server_close(); thread.join(3)
