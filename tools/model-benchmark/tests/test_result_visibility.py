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

    def insert_results(self, count, schedule=None, mixed_admin=False):
        ids = []
        with self.store.transaction() as db:
            suite = self.store._suite(db)
            for i in range(count):
                run_id = self.store._insert_run(db, suite["id"], "https://test.one/private/v1", "public-model", "high",
                    self.store.cipher.encrypt(b"secret-key"), schedule_id=schedule, owner_user_id=None if schedule or (mixed_admin and i % 2 == 0) else i + 1)
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
        expected = self.insert_results(53, mixed_admin=True)
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
        sources = [item["results"][0]["test_source"] for page in (first, second, third) for item in page["items"]]
        self.assertEqual(sources.count("admin"), 27)
        self.assertEqual(sources.count("user"), 26)
        for cursor in ("garbage", "x" * 1025, "e30"):
            with self.assertRaises(Problem): self.store.user_results(999, cursor)

    def test_admin_one_off_results_are_shared_over_http_with_server_side_masking(self):
        self.insert_results(1)
        admin = self.create(base_url="https://admin.example:8443/private/v1", test_source="user")
        self.store.append(admin["id"], {"index": 1, "category": "math", "status": "passed", "passed": True, "answer": "private-answer"})
        self.store.finish(admin["id"], "completed", "private-error")
        repeated = self.store.rerun(admin["id"])
        self.store.finish(repeated["id"], "failed")
        deleted = self.create()
        self.store.finish(deleted["id"], "cancelled")
        self.store.delete_run(deleted["id"])
        self.create()  # Unfinished runs must not be published.
        plan = self.store.create_schedule({"name": "Private schedule", "base_url": "https://schedule.example/v1", "api_key": "secret", "model": "model", "cron_expression": "* * * * *"})
        scheduled = self.store.run_schedule(plan["id"])  # The schedule's Run now button is still a schedule execution.
        self.store.finish(scheduled["id"], "completed")
        self.store.execution_settings({"user_testing_enabled": False})
        engine = Engine(self.store, None, FakeGrader())
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, "fixture-token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            for path, masked in (("/results", False), ("/users/2/results?admin=true", True), ("/sites/" + "a" * 32 + "/users/2/results", True)):
                req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", headers={"Authorization": "Bearer fixture-token"})
                with urllib.request.urlopen(req) as response:
                    body = response.read().decode()
                records = {item["id"]: item["results"][0] for item in json.loads(body)["items"]}
                self.assertEqual(len(records), 3)
                self.assertEqual({r["test_source"] for r in records.values()}, {"admin", "user"})
                for run in (admin, repeated):
                    result = records[run["id"]]
                    self.assertEqual(result["test_source"], "admin")
                    self.assertEqual(result["url_masked"], masked)
                    self.assertEqual(result["base_url"], "https://****n.example" if masked else admin["base_url"])
                if masked:
                    for hidden in ("admin.example", "private/v1", "8443"):
                        self.assertNotIn(hidden, body)
                for secret in ("test-secret-DO-NOT-EXPOSE", "private-answer", "private-error", "credential", "owner_user_id"):
                    self.assertNotIn(secret, body)
            self.assertEqual(self.store.public_results()["items"], [])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(5)

    def test_manual_retest_of_a_group_schedule_does_not_expose_group_url(self):
        plan = self.store.create_schedule({"name": "Group schedule", "base_url": "https://local.example/v1", "api_key": "secret", "model": "model", "group_id": 7, "group_name": "Premium", "cron_expression": "* * * * *"})
        scheduled = self.store.run_schedule(plan["id"])
        self.store.finish(scheduled["id"], "failed")
        manual = self.store.rerun(scheduled["id"])
        self.store.finish(manual["id"], "failed")
        for viewer in (None, 2):
            page = self.store.user_results(viewer)
            self.assertEqual([item["id"] for item in page["items"]], [manual["id"]])
            result = page["items"][0]["results"][0]
            self.assertEqual(result["group_name"], "Premium")
            self.assertEqual(result["test_source"], "admin")
            self.assertNotIn("base_url", result)
            self.assertNotIn("local.example", json.dumps(page))

    def test_site_pages_mask_legacy_urls_and_recheck_visibility(self):
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
            self.assertTrue(all(result["base_url"] == "https://***t.one" and result["url_masked"] for result in results))
            self.assertNotIn("test.one", json.dumps(page))
            self.assertNotIn("private/v1", json.dumps(page))
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
                if masked:
                    self.assertNotIn("test.one", json.dumps(page))
                    self.assertNotIn("private/v1", json.dumps(page))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(5)

    def test_shared_http_responses_hide_group_urls_and_mask_old_remote_cache(self):
        legacy = self.store.create_schedule({"name": "Legacy", "base_url": "https://private.example:8443/secret-path", "api_key": "secret",
            "model": "public-model", "cron_expression": "* * * * *", "public_results": True})
        grouped = self.store.create_schedule({"name": "Group test", "base_url": "https://local.example:8443/secret-path", "api_key": "secret",
            "model": "public-model", "group_id": 7, "group_name": "Premium", "cron_expression": "* * * * *", "public_results": True})
        for plan in (legacy, grouped):
            run = self.store.run_schedule(plan["id"])
            self.store.finish(run["id"], "failed")
            self.displays.save({"name": plan["name"], "source_type": "local", "schedule_id": plan["id"]})
        remote = self.displays.save({"name": "Remote", "source_type": "remote", "remote_url": "https://source.example/results"})
        old = next(item for item in self.store.public_results()["items"] if item["schedule_id"] == legacy["id"])
        old.update(base_url="https://hidden.example:8443/secret-path", url_masked=False)
        cached = [{"id": "old-remote", "name": "Remote task", "model": old["model"], "effort": old["effort"], "results": [old]}]
        with self.store.transaction() as db:
            db.execute("UPDATE displays SET cache=? WHERE id=?", (json.dumps(cached), remote["id"]))
        engine = Engine(self.store, None, FakeGrader())
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, "fixture-token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            for path in ("/public-results", "/showcase"):
                req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", headers={"Authorization": "Bearer fixture-token"})
                with urllib.request.urlopen(req) as response:
                    body = response.read().decode()
                for hidden in ("private.example", "local.example", "hidden.example", "secret-path", "8443"):
                    self.assertNotIn(hidden, body)
                page = json.loads(body)
                results = page["items"] if path == "/public-results" else [r for source in page["items"] for group in source["items"] for r in group["results"]]
                group_result = next(r for r in results if r.get("group_id") == 7)
                self.assertNotIn("base_url", group_result)
                self.assertEqual(group_result["group_name"], "Premium")
                self.assertTrue(all(r.get("url_masked") for r in results if not r.get("group_id")))
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
