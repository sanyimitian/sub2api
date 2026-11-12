import json
import threading
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer
from unittest.mock import Mock

from engine import Engine
from server import make_handler
from store import Problem
from test_worker import Fixture, FakeGrader


class AdminRecordTests(Fixture):
    def setUp(self):
        super().setUp()
        self.store.execution_settings({'user_testing_enabled': True})

    def test_server_filters_all_user_records_before_pagination(self):
        admin = self.create(model='sol')
        ids = []
        for index in range(43):
            run = self.create(model='6.1sol' if index < 41 else '5.6sol')
            with self.store.transaction() as db:
                db.execute('UPDATE runs SET owner_user_id=? WHERE id=?', (7 if index < 21 else 8, run['id']))
            ids.append(run['id'])
        page = self.store.list_runs(1, users_only=True, model='SOL')
        self.assertEqual(page['total'], 43)
        self.assertEqual([r['id'] for r in page['items']], list(reversed(ids))[:20])
        self.assertNotIn(admin['id'], [r['id'] for r in page['items']])
        filtered = self.store.list_runs(2, 7, users_only=True, model='6.1')
        self.assertEqual(filtered['total'], 21)
        self.assertEqual(len(filtered['items']), 1)
        self.assertEqual(filtered['items'][0]['id'], ids[0])
        self.assertEqual(filtered['items'][0]['owner_user_id'], 7)
        self.assertNotIn('credential', json.dumps(filtered))
        self.assertNotIn('owner_user_id', self.store.list_runs(1, 7)['items'][0])
        self.assertEqual(self.store.list_runs(100, 7, users_only=True)['page'], 2)

    def test_model_filters_are_literal_and_invalid_filters_are_rejected(self):
        run = self.create(model='sol_%')
        with self.store.transaction() as db:
            db.execute('UPDATE runs SET owner_user_id=7 WHERE id=?', (run['id'],))
        self.assertEqual(self.store.list_runs(users_only=True, model='_%')['total'], 1)
        self.assertEqual(self.store.list_runs(users_only=True, model="' OR 1=1 --")['total'], 0)
        for params in ({'owner_user_id': 0}, {'owner_user_id': -1}, {'owner_user_id': 2**63}, {'page': 0}, {'model': 'x'*201}, {'model': '\n'}):
            with self.subTest(params=params), self.assertRaises(Problem):
                self.store.list_runs(users_only=True, **params)

    def test_site_filter_combines_with_account_and_model_before_pagination(self):
        site_a, site_b = 'a'*32, 'b'*32
        for index in range(25):
            run = self.create(model='sol' if index < 23 else 'other')
            with self.store.transaction() as db:
                db.execute('UPDATE runs SET owner_user_id=7,owner_site_id=?,owner_site_name=? WHERE id=?',
                           (site_a if index >= 2 else ('', site_b)[index], '甲站' if index >= 2 else '', run['id']))
        self.assertEqual(self.store.list_runs(users_only=True)['total'], 25)
        self.assertEqual(self.store.list_runs(users_only=True, local_only=True)['total'], 1)
        page = self.store.list_runs(2, 7, users_only=True, owner_site_id=site_a, model='sol')
        self.assertEqual(page['total'], 21)
        self.assertEqual(len(page['items']), 1)
        self.assertEqual(page['items'][0]['owner_site_id'], site_a)
        self.assertEqual({site['id'] for site in self.store.user_record_sites()}, {'', site_a, site_b})
        self.assertEqual(self.store.user_record_sites(site_a), [{'id': site_a, 'name': '甲站'}])

    def test_admin_http_route_cannot_be_reached_through_user_namespace(self):
        site_id = 'a'*32
        for site in ('', site_id):
            run = self.create()
            with self.store.transaction() as db:
                db.execute('UPDATE runs SET owner_user_id=7,owner_site_id=?,owner_site_name=?,owner_email=? WHERE id=?',
                           (site, '甲站' if site else '', 'remote@example.com', run['id']))
        server = ThreadingHTTPServer(('127.0.0.1', 0), make_handler(self.store, Engine(self.store, Mock(), FakeGrader()), 'test-token'))
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        def request(path, authorized=True):
            req = urllib.request.Request(f'http://127.0.0.1:{server.server_port}'+path, headers={'Authorization':'Bearer test-token'} if authorized else {})
            try:
                with urllib.request.urlopen(req, timeout=3) as response: return response.status, json.load(response)
            except urllib.error.HTTPError as error: return error.code, json.load(error)
        try:
            self.assertEqual(request('/user-runs?page=1&model=sol')[0], 200)
            self.assertEqual(request('/user-runs', False)[0], 401)
            self.assertEqual(request('/users/7/user-runs')[0], 404)
            local = request('/user-runs?site_id=local')[1]
            remote = request('/user-runs?site_id='+site_id)[1]
            self.assertEqual(local['total'], 1)
            self.assertEqual(local['items'][0]['owner_site_id'], '')
            self.assertEqual(remote['total'], 1)
            self.assertEqual(remote['items'][0]['owner_site_id'], site_id)
            self.assertEqual(local['site_options'], remote['site_options'])
            self.assertEqual(request('/user-runs?site_id='+site_id+'&model=absent')[1]['site_options'], local['site_options'])
            self.assertEqual(request('/user-options?q=remote&site_id='+site_id)[1]['items'][0]['site_id'], site_id)
            self.assertEqual(request('/user-options?q=remote&site_id='+'b'*32)[1]['items'], [])
            self.assertEqual(request('/sites/'+site_id+'/user-runs?site_id=local')[0], 403)
            for path in ('/user-runs?page=0', '/user-runs?user_id=-1', '/user-runs?owner_user_id=7', '/user-runs?model=a&model=b'):
                self.assertEqual(request(path)[0], 400)
            for path in ('/user-runs?site_id=invalid', '/user-runs?site_id=local&site_id='+site_id,
                         '/user-options?q=remote&site_id=invalid', '/user-options?q=remote&site_id='+site_id+'&site_id='+site_id):
                self.assertEqual(request(path)[0], 400)
        finally:
            server.shutdown(); server.server_close(); thread.join(3)
