import json
from datetime import timedelta
from http.server import ThreadingHTTPServer
import threading
import urllib.request

from displays import Displays
from engine import Engine
from server import make_handler
from store import Problem, mask_result_url
from test_worker import Fixture, FakeGrader


class ResultVisibilityTests(Fixture):
    def setUp(self):
        super().setUp()
        self.store.execution_settings({"user_testing_enabled": True})
        self.displays = Displays(self.store)
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})

    def insert_results(self, count, schedule=None):
        ids = []
        with self.store.transaction() as db:
            suite = self.store._suite(db)
            for i in range(count):
                run_id = self.store._insert_run(db, suite["id"], "https://test.one/private/v1", "public-model", "high",
                    self.store.cipher.encrypt(b"secret-key"), schedule_id=schedule, owner_user_id=None if schedule else i + 1)
                results = json.dumps([{"index": 1, "category": "math", "passed": True, "status": "passed", "answer": "secret-answer"}])
                # 相同结束时间也必须稳定分页。
                db.execute("UPDATE runs SET finished_at=?,status='completed',results=?,error='secret-error' WHERE id=?",
                    (self.date.isoformat(), results, run_id))
                ids.append(run_id)
        return sorted(ids, reverse=True)

    def test_url_visibility_is_applied_before_serialization(self):
        self.insert_results(1)
        for viewer, expected in ((None, "https://test.one/private/v1"), (1, "https://test.one/private/v1"), (2, "https://***t.one")):
            page = self.store.user_results(viewer)
            result = page["items"][0]["results"][0]
            self.assertEqual(result["base_url"], expected)
            self.assertEqual(result["url_masked"], viewer == 2)
            self.assertEqual(result["model"], "public-model")
            self.assertEqual(result["effort"], "high")
            for secret in ("secret-key", "secret-answer", "secret-error", "credential", "owner_user_id"):
                self.assertNotIn(secret, json.dumps(page))
            if viewer == 2:
                self.assertNotIn("test.one", json.dumps(page))
                self.assertNotIn("private/v1", json.dumps(page))
        self.store.execution_settings({"user_testing_enabled": False})
        self.assertEqual(len(self.store.user_results(2)["items"]), 1)

    def test_mask_handles_protocols_subdomains_ips_and_paths(self):
        for value, expected in (
            ("http://test.one/v1", "http://***t.one"),
            ("https://atest.cn:8443/private", "https://****t.cn"),
            ("https://api.test.one/v1", "https://*******t.one"),
            ("https://t.cn/private", "https://***t.cn"),
            ("https://192.0.2.1/v1", "https://****"),
            ("https://[2001:db8::1]/v1", "https://****"),
            ("http://localhost/private", "http://****"),
        ):
            self.assertEqual(mask_result_url(value), expected)

    def test_user_pages_remain_stable_when_new_results_arrive(self):
        expected = self.insert_results(53)
        first = self.store.user_results(999)
        self.assertEqual(len(first["items"]), 24)
        # 插入更晚结束的结果，不能把下一页挤出重复记录。
        self.date += timedelta(minutes=1)
        with self.store.transaction() as db:
            run = self.store._insert_run(db, self.store._suite(db)["id"], "https://new.one/v1", "new", "low", b"", owner_user_id=999)
            db.execute("UPDATE runs SET status='failed',finished_at=? WHERE id=?", (self.date.isoformat(), run))
        second = self.store.user_results(999, first["next_cursor"])
        third = self.store.user_results(999, second["next_cursor"])
        self.assertEqual([len(p["items"]) for p in (first, second, third)], [24, 24, 5])
        self.assertEqual([item["id"] for p in (first, second, third) for item in p["items"]], expected)
        self.assertIsNone(third["next_cursor"])
        for cursor in ("garbage", "x" * 1025, "e30"):
            with self.assertRaises(Problem): self.store.user_results(999, cursor)

    def test_site_pages_include_full_urls_and_recheck_visibility(self):
        plan = self.store.create_schedule({"name": "Featured", "base_url": "https://test.one/v1", "api_key": "secret",
            "model": "public-model", "effort": "high", "cron_expression": "* * * * *", "public_results": True})
        self.displays.save({"name": "Site", "source_type": "local", "schedule_id": plan["id"], "max_results": 1})
        expected = self.insert_results(53, plan["id"])
        found, sizes, cursor = [], [], ""
        while True:
            page = self.displays.showcase(cursor)
            results = [result for display in page["items"] for group in display["items"] for result in group["results"]]
            sizes.append(len(results))
            found.extend(result["id"] for result in results)
            self.assertTrue(all(result["base_url"] == "https://test.one/private/v1" for result in results))
            cursor = page["next_cursor"]
            if not cursor: break
        self.assertEqual(sizes, [24, 24, 5])
        self.assertEqual(found, expected)
        self.assertEqual(self.store.user_results()["items"], [])
        first = self.displays.showcase()
        self.store.update_schedule(plan["id"], {"public_results": False})
        self.assertEqual(self.displays.showcase(first["next_cursor"])["items"], [])
        with self.assertRaises(Problem): self.store.user_results(1, first["next_cursor"])

    def test_worker_ignores_role_and_owner_query_spoofing(self):
        self.insert_results(1)
        engine = Engine(self.store, None, FakeGrader())
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, "fixture-token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            for path, masked in (("/results", False), ("/users/1/results", False), ("/users/2/results?owner_user_id=1&admin=true", True)):
                req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", headers={"Authorization": "Bearer fixture-token", "X-Admin": "true"})
                with urllib.request.urlopen(req) as response: page = json.load(response)
                self.assertEqual(page["items"][0]["results"][0]["url_masked"], masked)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(5)

    def test_site_page_limit_is_global_across_sources(self):
        for index in range(2):
            plan = self.store.create_schedule({"name": f"Plan {index}", "base_url": "https://test.one/v1", "api_key": "secret",
                "model": "public-model", "effort": "high", "cron_expression": "* * * * *", "public_results": True})
            self.displays.save({"name": f"Source {index}", "source_type": "local", "schedule_id": plan["id"]})
            self.insert_results(20, plan["id"])
        first = self.displays.showcase()
        second = self.displays.showcase(first["next_cursor"])
        ids = lambda page: [result["id"] for source in page["items"] for group in source["items"] for result in group["results"]]
        self.assertEqual(len(ids(first)), 24)
        self.assertEqual(len(ids(second)), 16)
        self.assertEqual(len(set(ids(first) + ids(second))), 40)
        self.assertIsNone(second["next_cursor"])

    def test_legacy_remote_ids_shared_between_schedules_do_not_skip_a_page(self):
        plan = self.store.create_schedule({"name": "Local", "base_url": "https://test.one/v1", "api_key": "secret",
            "model": "public-model", "effort": "high", "cron_expression": "* * * * *", "public_results": True})
        self.displays.save({"name": "Local", "source_type": "local", "schedule_id": plan["id"]})
        self.insert_results(23, plan["id"])
        result = {**self.store.public_results()["items"][0], "id": "legacy-run", "finished_at": (self.date - timedelta(minutes=1)).isoformat()}
        groups = [{"id": f"task-{index}", "name": f"Task {index}", "model": result["model"], "effort": result["effort"], "results": [result]} for index in range(2)]
        source = self.displays.save({"name": "Legacy", "source_type": "remote", "remote_url": "https://remote.one/results"})
        with self.store.transaction() as db:
            db.execute("UPDATE displays SET cache=? WHERE id=?", (json.dumps(groups), source["id"]))
        first = self.displays.showcase()
        second = self.displays.showcase(first["next_cursor"])
        identities = lambda page: [(source["id"], group["id"], result["id"]) for source in page["items"] for group in source["items"] for result in group["results"]]
        self.assertEqual(len(identities(first)), 24)
        self.assertEqual(len(identities(second)), 1)
        self.assertEqual(len(set(identities(first) + identities(second))), 25)
        self.assertIsNone(second["next_cursor"])
