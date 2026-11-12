import concurrent.futures
import copy
from datetime import timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import sqlite3
import threading
import time
import urllib.request
import urllib.error
from unittest.mock import patch

from displays import Displays, feed_url, fetch_feed, sanitize_feed
from engine import Engine
from server import make_handler
from store import Problem, Store
from test_worker import Fixture, FakeGrader


class DisplayTests(Fixture):
    def setUp(self):
        super().setUp()
        self.displays = Displays(self.store)

    def plan(self, **overrides):
        return self.store.create_schedule({"name": "Local plan", "base_url": "https://example.com/v1", "model": "test-model",
            "api_key": "private-secret", "effort": "medium", "cron_expression": "* * * * *", "public_results": True, **overrides})

    def feed(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        plan = self.plan()
        self.date += timedelta(minutes=1)
        self.store.enqueue_due_schedules()
        run = self.store.claim()
        self.store.append(run["id"], {"index": 1, "category": "math", "passed": True, "status": "passed", "answer": "private-answer"})
        self.store.finish(run["id"], "completed")
        return plan, self.store.public_results()

    def remote(self, **extra):
        return self.displays.save({"name": "Remote", "source_type": "remote", "remote_url": "https://external.example/api/v1/model-benchmarks/public-results", **extra})

    def test_incomplete_response_counts_survive_public_export_and_import(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        self.plan()
        self.date += timedelta(minutes=1)
        self.store.enqueue_due_schedules()
        run = self.store.claim()
        self.store.append(run["id"], {"index": 1, "category": "math", "passed": False,
                                      "status": "response_incomplete", "incomplete_reason": "max_output_tokens"})
        self.store.finish(run["id"], "failed")
        feed = self.store.public_results()
        item = feed["items"][0]
        self.assertEqual(item["incomplete_responses"], 1)
        self.assertEqual(item["api_errors"], 0)
        imported = sanitize_feed(feed)[0]["results"][0]
        self.assertEqual(imported["incomplete_responses"], 1)
        self.assertIsNone(imported["pass_rate"])
        del item["incomplete_responses"]
        self.assertNotIn("incomplete_responses", sanitize_feed(feed)[0]["results"][0])

    def test_configurable_concurrency_shared_across_all_tasks_and_restart(self):
        self.store.execution_settings({"user_testing_enabled": True})
        self.assertEqual(self.store.execution_settings()["max_concurrency"], 5)
        admin = self.create()
        target = {"suite_id": admin["suite_id"], "base_url": "https://example.com", "api_key": "fixture", "model": "test"}
        user = self.store.create(target, 7)
        self.plan()
        self.date += timedelta(minutes=1)
        self.store.enqueue_due_schedules()
        self.store.execution_settings({"max_concurrency": 2})
        self.assertEqual(self.store.claim()["id"], admin["id"])
        self.assertEqual(self.store.claim()["id"], user["id"])
        self.assertIsNone(self.store.claim())
        self.store.execution_settings({"max_concurrency": 1})
        self.store.finish(admin["id"], "completed")
        self.assertIsNone(self.store.claim())
        self.store.finish(user["id"], "completed")
        self.assertIsNotNone(self.store.claim())
        for _ in range(10): self.create()
        self.store.execution_settings({"max_concurrency": 8})
        restarted = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            accepted = [r for r in pool.map(lambda _: restarted.claim(), range(12)) if r]
        self.assertEqual(len(accepted), 7)
        self.assertEqual(restarted.state()["execution"], {"max_concurrency": 8, "running": 8, "queued": 3, "user_testing_enabled": True})
        for value in (0, 101, True, 1.5, "5"):
            with self.assertRaises(Problem): self.store.execution_settings({"max_concurrency": value})

    def test_engine_increases_limit_live_above_default(self):
        for _ in range(8): self.create()
        self.store.execution_settings({"max_concurrency": 1})
        engine = Engine(self.store, None, FakeGrader())
        gate = threading.Event()
        entered = []
        def execute(run):
            entered.append(run["id"])
            gate.wait(10)
            self.store.finish(run["id"], "completed")
        engine.execute = execute
        engine.start()
        try:
            deadline = time.monotonic() + 3
            while not entered and time.monotonic() < deadline: time.sleep(.02)
            self.assertEqual(len(entered), 1)
            self.store.execution_settings({"max_concurrency": 7})
            engine.wake.set()
            deadline = time.monotonic() + 3
            while len(entered) < 7 and time.monotonic() < deadline: time.sleep(.02)
            self.assertEqual(len(entered), 7)
            self.assertEqual(self.store.execution_settings()["queued"], 1)
        finally:
            engine.stopping.set()
            gate.set()
            engine.wake.set()
            for thread in engine.threads: thread.join(10)
            engine.scheduler.join(10)
            engine.display_thread.join(10)

    def test_local_display_is_explicit_and_revoked_by_private_or_deleted_plan(self):
        plan, feed = self.feed()
        self.assertEqual(len(feed["items"]), 1)
        self.assertEqual(self.displays.showcase()["items"], [])
        display = self.displays.save({"name": "Featured", "source_type": "local", "schedule_id": plan["id"]})
        self.assertEqual(self.displays.showcase()["items"][0]["items"], sanitize_feed(feed))
        self.store.update_schedule(plan["id"], {"public_results": False})
        self.assertEqual(self.store.public_results()["items"], [])
        self.assertEqual(self.displays.showcase()["items"], [])
        with self.assertRaises(Problem): self.displays.save({"name": "Forbidden", "source_type": "local", "schedule_id": plan["id"]})
        self.displays.save({"enabled": False}, display["id"])
        self.store.update_schedule(plan["id"], {"public_results": True})
        self.assertEqual(self.displays.showcase()["items"], [])
        self.displays.save({"enabled": True}, display["id"])
        self.store.delete_schedule(plan["id"])
        self.assertEqual(self.displays.showcase()["items"], [])

    def test_remote_refresh_interval_persistent_cache_failure_and_revocation(self):
        _, data = self.feed()
        self.displays.fetcher = lambda _: sanitize_feed({"code": 0, "data": data})
        display = self.remote()
        self.assertEqual(display["refresh_minutes"], 60)
        self.assertTrue(self.displays.showcase()["items"][0]["pending"])
        claimed = self.displays.claim_refresh()
        self.assertIsNone(self.displays.claim_refresh())
        self.displays.fetch(claimed)
        self.assertEqual(self.displays.showcase()["items"][0]["items"], sanitize_feed(data))
        self.assertIsNone(self.displays.claim_refresh())
        restarted = Displays(Store(self.root / "data", self.store.bank, clock=lambda: self.date))
        self.assertEqual(restarted.showcase(), self.displays.showcase())
        self.date += timedelta(minutes=60)
        self.displays.fetcher = lambda _: (_ for _ in ()).throw(ValueError("SECRET upstream body"))
        self.displays.fetch(self.displays.claim_refresh())
        group = self.displays.showcase()["items"][0]
        self.assertTrue(group["stale"])
        self.assertEqual(group["items"], sanitize_feed(data))
        self.assertNotIn("SECRET", json.dumps(self.displays.list()))
        self.displays.fetcher = lambda _: []
        self.displays.request_refresh(display["id"])
        self.displays.fetch(self.displays.claim_refresh())
        self.assertEqual(self.displays.showcase()["items"][0]["items"], [])
        self.assertFalse(self.displays.showcase()["items"][0]["stale"])
        # Imported results never appear in the public export.
        self.assertEqual(self.store.public_results()["items"], data["items"])

    def test_fetch_races_edits_delete_pause_and_lease_expiry(self):
        _, data = self.feed()
        self.displays.fetcher = lambda _: sanitize_feed(data)
        display = self.remote()
        old = self.displays.claim_refresh()
        self.displays.save({"remote_url": "https://different.example/feed"}, display["id"])
        self.displays.fetch(old)
        self.assertEqual(self.displays.showcase()["items"][0]["items"], [])
        first = self.displays.claim_refresh()
        self.date += timedelta(seconds=121)
        second = self.displays.claim_refresh()
        self.assertNotEqual(first["lease_token"], second["lease_token"])
        self.displays.fetch(first)
        self.assertIsNone(self.displays.list()["items"][0]["last_success_at"])
        self.displays.save({"enabled": False}, display["id"])
        self.displays.fetch(second)
        self.assertEqual(self.displays.showcase()["items"], [])
        self.displays.save({"enabled": True}, display["id"])
        pending = self.displays.claim_refresh()
        self.displays.delete(display["id"])
        self.displays.fetch(pending)
        self.assertEqual(self.displays.list()["items"], [])

    def test_feed_whitelist_validates_schema_recomputes_percentages(self):
        _, data = self.feed()
        data["api_key"] = "top-secret"
        data["items"][0]["credential"] = "secret"
        result = data["items"][0]
        result.update(api_key="result-secret", answer="hidden answer", base_url="https://private.example", pass_rate=12)
        cleaned = sanitize_feed({"code": 0, "message": "success", "data": data})
        self.assertEqual(cleaned[0]["results"][0]["pass_rate"], 100)
        self.assertEqual(cleaned[0]["results"][0]["base_url"], "https://private.example/")
        for field in ("api_key", "credential", "answer"): self.assertNotIn(field, json.dumps(cleaned))
        for key, value in (("total", -1), ("status", "running"), ("completed", 0), ("elapsed_seconds", float("nan"))):
            bad = copy.deepcopy(data)
            bad["items"][0][key] = value
            with self.assertRaises(ValueError): sanitize_feed(bad)
        with self.assertRaises(ValueError): sanitize_feed({"version": 99, "items": []})
        with self.assertRaises(ValueError): sanitize_feed({"code": 1, "data": data})

    def test_fetch_transport_no_auth_redirects_or_private_targets(self):
        _, data = self.feed()
        captured = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_): pass
            def do_GET(self):
                captured.append(dict(self.headers))
                self.send_response(302 if self.path == "/redirect" else 200)
                self.send_header("Location", "/feed")
                self.end_headers()
                self.wfile.write(json.dumps({"code": 0, "data": data}).encode())
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        url = f"http://127.0.0.1:{server.server_port}"
        try:
            with self.assertRaises(Exception): fetch_feed(url + "/feed")
            with patch("displays.public_addresses", return_value=["127.0.0.1"]):
                self.assertEqual(fetch_feed(url + "/feed"), sanitize_feed(data))
                with self.assertRaises(Exception): fetch_feed(url + "/redirect")
            self.assertEqual(len(captured), 2)
            self.assertTrue(all("Authorization" not in h and "Cookie" not in h for h in captured))
            for invalid in ("file:///tmp/feed", "https://user:password@example.com/feed", "https://@example.com/feed", "https://example.com/#fragment", "https://example.com/a\r\nb", "https://example.com/a\x7fb", "http://example.com:0/feed"):
                with self.assertRaises(Problem): feed_url(invalid)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(5)

    def test_upgrade_retains_counts_and_defaults_site_limit(self):
        suite = self.store.state()["suite"]
        with sqlite3.connect(self.store.path) as db: db.execute("ALTER TABLE config DROP COLUMN max_concurrency")
        upgraded = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(upgraded.state()["suite"], suite)
        self.assertEqual(upgraded.execution_settings()["max_concurrency"], 5)

    def test_export_is_latest_twenty_and_showcase_ignores_legacy_limit(self):
        first, _ = self.feed()
        second = self.plan(name="Second")
        private = self.plan(name="Private", public_results=False)
        ids = []
        # Insert timestamps out of sequence; row creation order cannot decide results.
        with self.store.transaction() as db:
            suite_id = self.store._suite(db)["id"]
            for i in list(range(32))[::-1]:
                plan_id = first["id"] if i % 2 else second["id"]
                row = db.execute("SELECT * FROM schedules WHERE id=?", (plan_id,)).fetchone()
                run_id = self.store._insert_run(db, suite_id, row["base_url"], row["model"], row["effort"], row["credential"], schedule_id=plan_id)
                finished = (self.date + timedelta(days=20, minutes=i)).isoformat()
                results = json.dumps([{"index": 1, "category": "math", "status": "passed", "passed": True}])
                db.execute("UPDATE runs SET finished_at=?,status='completed',results=? WHERE id=?", (finished, results, run_id))
                ids.append((i, run_id, plan_id))
            row = db.execute("SELECT * FROM schedules WHERE id=?", (private["id"],)).fetchone()
            hidden = self.store._insert_run(db, suite_id, row["base_url"], row["model"], row["effort"], row["credential"], schedule_id=private["id"])
            db.execute("UPDATE runs SET status='failed',finished_at=? WHERE id=?", ((self.date + timedelta(days=30)).isoformat(), hidden))
        feed = self.store.public_results()
        self.assertEqual(feed["version"], 2)
        self.assertEqual(len(feed["items"]), 20)
        self.assertEqual([r["id"] for r in feed["items"]], [r[1] for r in ids[:20]])
        self.assertTrue(all(r["schedule_name"] in ("Local plan", "Second") for r in feed["items"]))
        self.assertEqual(sum(len(group["results"]) for group in sanitize_feed(feed)), 20)
        local = self.displays.save({"name": "History", "source_type": "local", "schedule_id": first["id"], "max_results": 3})
        local_results = self.displays.showcase()["items"][0]["items"][0]["results"]
        self.assertEqual([r["id"] for r in local_results[:16]], [r[1] for r in ids if r[2] == first["id"]])
        self.assertEqual(len(local_results), 17)
        self.displays.save({"max_results": 100}, local["id"])
        self.assertEqual(len(self.displays.showcase()["items"][0]["items"][0]["results"]), 17)
        # A local task must remain visible even when none of its results make the export.
        self.store.update_schedule(first["id"], {"public_results": False})
        self.assertEqual(self.displays.showcase()["items"], [])
        self.assertEqual(len(self.store.public_results()["items"]), 16)

    def test_local_limit_migration_validation_and_legacy_feed_compatibility(self):
        plan, feed = self.feed()
        local = self.displays.save({"name": "Default", "source_type": "local", "schedule_id": plan["id"]})
        self.assertEqual(local["max_results"], 20)
        for value in (0, -1, 101, True, "2", 1.5):
            with self.assertRaises(Problem): self.displays.save({"max_results": value}, local["id"])
        self.displays.save({"max_results": 7}, local["id"])
        self.assertEqual(Displays(self.store).list()["items"][0]["max_results"], 7)
        with self.store.transaction() as db:
            db.execute("ALTER TABLE displays DROP COLUMN max_results")
        self.assertEqual(Displays(self.store).list()["items"][0]["max_results"], 20)
        groups = sanitize_feed(feed)
        self.assertEqual(sanitize_feed({"version": 1, "items": groups}), groups)
        bad = copy.deepcopy(feed)
        bad["items"] *= 21
        with self.assertRaises(ValueError): sanitize_feed(bad)

    def test_http_settings_and_background_feed_refresh(self):
        _, feed = self.feed()
        engine = Engine(self.store, None, FakeGrader())
        engine.displays.fetcher = lambda _: sanitize_feed(feed)
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, "worker-token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        engine.start()
        def request(path, method="GET", body=None):
            req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", method=method,
                data=json.dumps(body).encode() if body is not None else None, headers={"Authorization": "Bearer worker-token"})
            try:
                with urllib.request.urlopen(req, timeout=5) as response: return response.status, json.load(response)
            except urllib.error.HTTPError as exc: return exc.code, json.load(exc)
        try:
            self.assertEqual(request("/execution", "PUT", {"max_concurrency": 9})[1]["max_concurrency"], 9)
            status, display = request("/displays", "POST", {"name": "External", "source_type": "remote", "remote_url": "https://external.example/feed"})
            self.assertEqual(status, 201)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline and not request("/showcase")[1]["items"][0]["items"]: time.sleep(.05)
            self.assertEqual(request("/showcase")[1]["items"][0]["items"], sanitize_feed(feed))
            self.assertEqual(request("/public-results")[1]["items"], feed["items"])
            self.assertEqual(request("/displays/" + display["id"] + "/refresh", "POST", {})[0], 202)
            self.assertEqual(request("/displays/" + display["id"], "PUT", {"enabled": False})[0], 200)
            self.assertEqual(request("/showcase")[1]["items"], [])
            self.assertEqual(request("/displays/" + display["id"], "DELETE")[0], 200)
            for path, method in (("/execution", "PUT"), ("/displays", "POST"), ("/public-results", "GET")):
                self.assertEqual(request("/users/7" + path, method, {} if method != "GET" else None)[0], 404)
        finally:
            engine.stopping.set()
            engine.wake.set()
            for worker_thread in engine.threads: worker_thread.join(10)
            engine.scheduler.join(10)
            engine.display_thread.join(10)
            server.shutdown()
            server.server_close()
            thread.join(5)
