import concurrent.futures
import json
import sqlite3
import threading
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

from engine import Engine
from server import make_handler
from store import Problem, Store
from test_worker import Fixture, FakeGrader


class FederationTests(Fixture):
    def setUp(self):
        super().setUp()
        self.store.execution_settings({'user_testing_enabled': True})
        self.target = {'suite_id': self.store.state()['suite']['id'], 'base_url': 'https://private.example.com/v1',
                       'api_key': 'site-secret', 'model': 'model', 'effort': 'high'}
        self.site_a, self.site_b = 'a' * 32, 'b' * 32

    def test_site_identity_scopes_ownership_quota_and_active_run_persistently(self):
        runs = {site: self.store.create(self.target, 7, site) for site in ('', self.site_a, self.site_b)}
        for site, run in runs.items():
            self.assertEqual(self.store.state(7, site)['active_run']['id'], run['id'])
            self.assertEqual(self.store.list_runs(owner_user_id=7, owner_site_id=site)['total'], 1)
            for other_site in runs:
                if site == other_site:
                    continue
                for action in (self.store.detail, self.store.cancel, self.store.rerun):
                    with self.assertRaises(Problem) as err:
                        action(run['id'], 7, other_site)
                    self.assertEqual(err.exception.status, 404)
        for site, run in runs.items():
            self.store.cancel(run['id'], 7, site)
        for _ in range(2):
            run = self.store.rerun(runs[self.site_a]['id'], 7, self.site_a)
            self.store.cancel(run['id'], 7, self.site_a)
        restarted = Store(self.root / 'data', self.store.bank, clock=lambda: self.date)
        self.assertEqual(restarted.state(7, self.site_a)['quota']['remaining'], 0)
        self.assertEqual(restarted.state(7, self.site_b)['quota']['remaining'], 2)
        self.assertEqual(restarted.state(7)['quota']['remaining'], 2)
        with self.assertRaises(Problem) as err:
            restarted.create(self.target, 7, self.site_a)
        self.assertEqual(err.exception.status, 429)
        self.assertEqual(restarted.list_runs(users_only=True, owner_site_id=self.site_a)['total'], 3)
        self.assertEqual(restarted.list_runs(users_only=True)['total'], 5)
        self.assertEqual(restarted.list_runs(users_only=True, owner_user_id=7)['total'], 1)

    def test_all_sites_share_results_without_cross_site_url_privilege(self):
        runs = {site: self.store.create(self.target, 7, site) for site in ('', self.site_a, self.site_b)}
        for run in runs.values():
            self.store.append(run['id'], {'index': 1, 'category': 'math', 'status': 'passed', 'passed': True, 'answer': 'private-answer'})
            self.store.finish(run['id'], 'failed', 'private-error')
        for viewer_site in runs:
            page = self.store.user_results(7, viewer_site_id=viewer_site)
            results = {item['id']: item['results'][0] for item in page['items']}
            self.assertEqual(set(results), {r['id'] for r in runs.values()})
            for site, run in runs.items():
                result = results[run['id']]
                self.assertEqual(result['url_masked'], site != viewer_site)
                self.assertEqual(result['model'], 'model')
                self.assertEqual(result['effort'], 'high')
                self.assertEqual(result['base_url'] == self.target['base_url'], site == viewer_site)
            for private in ('site-secret', 'private-answer', 'private-error', 'owner_user_id', 'owner_site_id'):
                self.assertNotIn(private, json.dumps(page))
        self.assertTrue(all(not item['results'][0]['url_masked'] for item in self.store.user_results()['items']))

    def test_site_submissions_share_main_queue_and_concurrency_limit(self):
        self.store.execution_settings({'max_concurrency': 1})
        first = self.store.create(self.target, 7, self.site_a)
        second = self.store.create(self.target, 7, self.site_b)
        self.assertEqual(self.store.claim()['id'], first['id'])
        self.assertIsNone(self.store.claim())
        self.store.finish(first['id'], 'completed')
        self.assertEqual(self.store.claim()['id'], second['id'])
        self.store.finish(second['id'], 'completed')
        def submit(_):
            try:
                return self.store.create(self.target, 8, self.site_a)
            except Problem as err:
                self.assertEqual(err.status, 409)
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            accepted = [run for run in pool.map(submit, range(8)) if run]
        self.assertEqual(len(accepted), 1)
        self.assertEqual(self.store.state(8, self.site_a)['quota']['used'], 1)

    def test_upgrade_preserves_local_ownership(self):
        local = self.store.create(self.target, 7)
        with sqlite3.connect(self.store.path) as db:
            for index in ('runs_site_user_quota', 'runs_site_user_active', 'runs_user_quota', 'runs_user_active'):
                db.execute('DROP INDEX IF EXISTS ' + index)
            db.execute('ALTER TABLE runs DROP COLUMN owner_site_id')
        upgraded = Store(self.root / 'data', self.store.bank, clock=lambda: self.date)
        self.assertEqual(upgraded.detail(local['id'], 7)['id'], local['id'])
        self.assertEqual(upgraded.state(7)['quota']['used'], 1)
        self.assertEqual(upgraded.state(7, self.site_a)['quota']['used'], 0)

    def test_account_snapshots_survive_restart_and_backfill_only_the_same_site_user(self):
        legacy = {site: self.store.create(self.target, 7, site) for site in ('', self.site_a, self.site_b)}
        for site, run in legacy.items():
            self.store.cancel(run['id'], 7, site)
        identity = {'email': 'remote@example.com', 'username': '远端账号', 'site_name': '甲站'}
        rerun = self.store.rerun(legacy[self.site_a]['id'], 7, self.site_a, identity=identity)
        restarted = Store(self.root / 'data', self.store.bank, clock=lambda: self.date)
        rows = restarted.list_runs(users_only=True, owner_user_id=7, owner_site_id=self.site_a)['items']
        self.assertEqual(len(rows), 2)
        for row in rows:
            self.assertEqual(row['owner_email'], identity['email'])
            self.assertEqual(row['owner_username'], identity['username'])
            self.assertEqual(row['owner_site_name'], identity['site_name'])
            self.assertEqual(row['base_url'], self.target['base_url'])
        for site in ('', self.site_b):
            row = restarted.list_runs(users_only=True, owner_user_id=7, owner_site_id=site)['items'][0]
            self.assertEqual(row['owner_email'], '')
        options = restarted.remote_user_options('REMOTE')
        self.assertEqual(len(options['items']), 1)
        self.assertEqual(options['items'][0]['site_id'], self.site_a)
        self.assertEqual(restarted.remote_user_options('远端')['items'], options['items'])
        self.assertEqual(restarted.remote_user_options("' OR 1=1 --")['items'], [])
        restarted.cancel(rerun['id'], 7, self.site_a)
        for private in ('remote@example.com', '远端账号', '甲站', 'owner_email', 'owner_site_name'):
            self.assertNotIn(private, json.dumps(restarted.user_results(), ensure_ascii=False))
        restarted.delete_run(rerun['id'])
        with restarted.transaction() as db:
            row = db.execute('SELECT owner_email,owner_username,owner_site_name FROM runs WHERE id=?', (rerun['id'],)).fetchone()
            self.assertEqual(tuple(row), ('', '', ''))

    def test_account_search_keeps_same_ids_on_different_sites_separate(self):
        for site in (self.site_a, self.site_b):
            self.store.create(self.target, 7, site, identity={'email': 'same@example.com', 'site_name': site[:1]})
        options = self.store.remote_user_options('same')['items']
        self.assertEqual({(item['id'], item['site_id']) for item in options}, {(7, self.site_a), (7, self.site_b)})
        filtered = self.store.remote_user_options('same', self.site_b)['items']
        self.assertEqual([(item['id'], item['site_id']) for item in filtered], [(7, self.site_b)])
        # Identity is an explicit trusted argument, never inferred from target parameters.
        self.store.create({**self.target, '_benchmark_identity': {'email': 'forged@example.com'}}, 8, self.site_a)
        self.assertEqual(self.store.remote_user_options('forged')['items'], [])
        for identity in ([], {'email': 'x'*255}, {'username': 'bad\nname'}, {'site_name': 'x'*101}):
            with self.subTest(identity=identity), self.assertRaises(Problem):
                self.store.create(self.target, 9, self.site_a, identity=identity)

    def test_http_namespace_rejects_forged_ownership_and_remote_management(self):
        engine = Engine(self.store, None, FakeGrader())
        server = ThreadingHTTPServer(('127.0.0.1', 0), make_handler(self.store, engine, 'internal-token'))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        def request(site, path, method='GET', data=None, token='internal-token'):
            req = urllib.request.Request(f'http://127.0.0.1:{server.server_port}/sites/{site}{path}', method=method,
                data=json.dumps(data).encode() if data is not None else None,
                headers={'Authorization': 'Bearer ' + token, 'X-Site-ID': self.site_b, 'X-User-ID': '8'})
            try:
                with urllib.request.urlopen(req, timeout=5) as response:
                    return response.status, json.load(response)
            except urllib.error.HTTPError as err:
                return err.code, json.load(err)
        try:
            status, run = request(self.site_a, '/users/7/runs', 'POST', {**self.target, 'owner_site_id': self.site_b, 'owner_user_id': 8,
                '_benchmark_identity': {'email': 'remote@example.com', 'username': 'remote-user', 'site_name': '甲站'}})
            self.assertEqual(status, 202)
            self.assertEqual(request(self.site_a, '/users/7/runs')[1]['total'], 1)
            self.assertEqual(request(self.site_b, '/users/7/runs')[1]['total'], 0)
            self.assertEqual(request(self.site_b, '/runs/' + run['id'])[0], 404)
            self.assertEqual(request(self.site_a, '/runs/' + run['id'])[0], 200)
            self.assertEqual(request(self.site_a, '/user-runs')[1]['items'][0]['owner_site_id'], self.site_a)
            self.assertEqual(request(self.site_a, '/user-runs')[1]['items'][0]['owner_email'], 'remote@example.com')
            self.assertEqual(request(self.site_a, '/user-runs?site_id=' + self.site_b)[0], 403)
            self.assertEqual(request(self.site_a, '/user-options?q=remote')[0], 403)
            self.assertEqual(request(self.site_a, '/users/7/state', token='wrong')[0], 401)
            for path, method in (('/execution', 'PUT'), ('/config', 'PUT'), ('/runs', 'POST'), ('/schedules', 'POST'), ('/bank/update', 'POST'), ('/sites/' + self.site_b + '/state', 'GET')):
                self.assertEqual(request(self.site_a, path, method, {})[0], 403, path)
            self.store.cancel(run['id'], 7, self.site_a)
            self.assertFalse(request(self.site_a, '/users/7/results')[1]['items'][0]['results'][0]['url_masked'])
            self.assertTrue(request(self.site_b, '/users/7/results')[1]['items'][0]['results'][0]['url_masked'])
            self.assertFalse(request(self.site_b, '/results')[1]['items'][0]['results'][0]['url_masked'])
        finally:
            server.shutdown(); server.server_close(); thread.join(5)
