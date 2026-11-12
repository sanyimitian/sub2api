import copy
import json
from unittest.mock import Mock, patch

from api_errors import summarize_api_errors
from displays import sanitize_feed
from engine import Engine
from store import Store
from test_worker import Fixture, FakeGrader
from transport import RequestFailure


class APIErrorTests(Fixture):
    def test_http_timeout_is_persisted_and_grouped_after_restart(self):
        self.store.refresh({'coding': 0, 'math': 2, 'reasoning': 0})
        client = Mock(generate=Mock(side_effect=RequestFailure('API 返回 HTTP 524', 524)))
        run = self.create()
        Engine(self.store, client, FakeGrader()).execute(self.store.claim())
        reopened = Store(self.store.directory, self.store.bank, clock=lambda: self.date)
        detail = reopened.detail(run['id'])
        self.assertEqual(detail['status'], 'failed')
        self.assertIsNone(detail['pass_rate'])
        self.assertEqual(detail['api_error_details'], [{'code': 'http_error', 'http_status': 524, 'count': 2}])
        self.assertEqual(reopened.list_runs()['items'][0]['api_error_details'], detail['api_error_details'])
        for result in detail['results']:
            self.assertEqual(result['http_status'], 524)
            self.assertEqual(result['api_error_code'], 'http_error')
        self.assertEqual(client.generate.call_count, 2)

    def test_local_request_rejection_is_not_reported_as_an_upstream_http_response(self):
        client = Mock(generate=Mock(side_effect=RequestFailure('API 地址不能指向本机或内网', 400, code='request_error')))
        run = self.create()
        Engine(self.store, client, FakeGrader()).execute(self.store.claim())
        detail = self.store.detail(run['id'])
        self.assertEqual(detail['api_error_details'], [{'code': 'request_error', 'count': 1}])
        self.assertNotIn('http_status', detail['results'][0])
        client.generate.assert_called_once()

    def test_connection_and_response_errors_remain_distinct(self):
        self.store.refresh({'coding': 0, 'math': 1, 'reasoning': 0})
        for error in (RequestFailure('连接异常'), RequestFailure('格式异常', code='response_error')):
            with self.subTest(code=error.code), patch('engine.time.sleep'):
                run = self.create()
                client = Mock(generate=Mock(side_effect=error))
                Engine(self.store, client, FakeGrader()).execute(self.store.claim())
                self.assertEqual(self.store.detail(run['id'])['api_error_details'], [{'code': error.code, 'count': 1}])
                # Existing retry behavior is retained; counts measure questions, not attempts.
                self.assertEqual(client.generate.call_count, 3)

    def historical_feed(self):
        self.store.refresh({'coding': 0, 'math': 4, 'reasoning': 0})
        plan = self.store.create_schedule({'name': 'Public plan', 'base_url': 'https://example.com/v1',
            'model': 'test-model', 'api_key': 'private-key', 'effort': 'medium',
            'cron_expression': '* * * * *', 'public_results': True})
        run = self.store.run_schedule(plan['id'])
        for index, status, message in ((1, 'api_error', 'API 返回 HTTP 524'),
                (2, 'api_error', 'API 返回 HTTP 524'), (3, 'grading_error', '评分器异常'),
                (4, 'response_incomplete', '上游响应未完成')):
            self.store.append(run['id'], {'index': index, 'category': 'math', 'passed': False,
                'status': status, 'message': message, 'answer': 'private-answer'})
        self.store.finish(run['id'], 'failed')
        return run['id'], self.store.public_results()

    def test_historical_errors_flow_to_public_cards_without_raw_messages(self):
        run_id, feed = self.historical_feed()
        summary = [{'code': 'http_error', 'http_status': 524, 'count': 2}]
        self.assertEqual(self.store.detail(run_id)['api_error_details'], summary)
        item = feed['items'][0]
        self.assertEqual(item['api_error_details'], summary)
        self.assertEqual(item['grading_errors'], 1)
        self.assertEqual(item['incomplete_responses'], 1)
        self.assertIsNone(item['pass_rate'])
        for hidden in ('private-key', 'private-answer', 'message', 'results'):
            self.assertNotIn(hidden, json.dumps(item))
        item['api_error_details'][0]['message'] = 'private-key <script>alert(1)</script>'
        imported = sanitize_feed(feed)[0]['results'][0]
        self.assertEqual(imported['api_error_details'], summary)
        self.assertNotIn('private-key', json.dumps(imported))
        del item['api_error_details']
        self.assertNotIn('api_error_details', sanitize_feed(feed)[0]['results'][0])

    def test_external_error_details_are_strictly_validated(self):
        _, feed = self.historical_feed()
        valid = {'code': 'http_error', 'http_status': 524, 'count': 2}
        for details in (None, {}, [None], [dict(valid, code='<script>')], [dict(valid, code=[])],
                [dict(valid, http_status='524')], [dict(valid, http_status=200)],
                [dict(valid, http_status=600)], [dict(valid, count=True)], [dict(valid, count=0)],
                [dict(valid, count=3)], [valid, valid], [dict(valid, code='request_error')]):
            with self.subTest(details=details):
                bad = copy.deepcopy(feed)
                bad['items'][0]['api_error_details'] = details
                with self.assertRaises(ValueError):
                    sanitize_feed(bad)

    def test_unknown_legacy_messages_are_not_exported_or_guessed(self):
        results = [
            {'status': 'api_error', 'message': 'API 返回 HTTP 404：模型未找到'},
            {'status': 'api_error', 'message': 'API 请求超时、连接中断或响应格式不正确'},
            {'status': 'api_error', 'message': 'API 返回格式不正确'},
            {'status': 'api_error', 'message': '测试已停止'},
            {'status': 'api_error', 'message': 'private-key <script>HTTP 524</script>'},
            {'status': 'api_error', 'message': {}, 'api_error_code': []},
            {'status': 'failed', 'message': 'HTTP 524'},
        ]
        summary = summarize_api_errors(results)
        self.assertEqual(sum(item['count'] for item in summary), 6)
        self.assertIn({'code': 'unknown', 'count': 2}, summary)
        self.assertIn({'code': 'http_error', 'http_status': 404, 'count': 1}, summary)
        self.assertNotIn('private-key', json.dumps(summary))
        self.assertNotIn('524', json.dumps(summary))
