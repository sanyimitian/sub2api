import concurrent.futures
from datetime import timedelta
from http.server import ThreadingHTTPServer
import json
import sqlite3
import threading
import time
import urllib.error
import urllib.request

from engine import Engine
from server import make_handler
from store import Problem, Store
from test_worker import Fixture, FakeGrader


class UserTests(Fixture):
    def setUp(self):
        super().setUp()
        self.store.execution_settings({"user_testing_enabled": True})

    def target(self, **extra):
        return {"suite_id": self.store.state()["suite"]["id"], "base_url": "https://example.com/v1",
                "model": "test-model", "api_key": "private-user-key", **extra}

    def test_user_testing_switch_blocks_create_and_rerun_without_charging_quota(self):
        finished = self.store.create(self.target(), 7)
        self.store.finish(finished["id"], "completed")
        active = self.store.create(self.target(), 8)
        before = self.store.state(7)["quota"]
        self.store.execution_settings({"user_testing_enabled": False})
        self.assertFalse(self.store.state(7)["execution"]["user_testing_enabled"])
        for action in (lambda: self.store.create(self.target(user_testing_enabled=True), 7), lambda: self.store.rerun(finished["id"], 7)):
            with self.assertRaises(Problem) as error: action()
            self.assertEqual(error.exception.status, 403)
        self.assertEqual(self.store.state(7)["quota"], before)
        self.assertEqual(self.store.claim()["id"], active["id"])
        self.store.finish(active["id"], "completed")
        self.assertEqual(self.store.detail(finished["id"], 7)["status"], "completed")
        admin = self.create()
        self.assertEqual(admin["status"], "queued")
        plan = self.store.create_schedule({**self.target(), "name": "Admin schedule", "cron_expression": "@every 1h"})
        self.assertEqual(self.store.run_schedule(plan["id"])["status"], "queued")
        self.store.execution_settings({"user_testing_enabled": True})
        self.assertEqual(self.store.rerun(finished["id"], 7)["status"], "queued")

    def test_user_testing_setting_migration_validation_and_persistence(self):
        original = self.create()
        self.store.execution_settings({"max_concurrency": 6})
        with sqlite3.connect(self.store.path) as db:
            db.execute("ALTER TABLE config DROP COLUMN user_testing_enabled")
        upgraded = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertFalse(upgraded.execution_settings()["user_testing_enabled"])
        self.assertEqual(upgraded.execution_settings()["max_concurrency"], 6)
        self.assertEqual(upgraded.detail(original["id"])["status"], "queued")
        for value in (None, 0, 1, "true", [], {}):
            with self.subTest(value=value), self.assertRaises(Problem):
                upgraded.execution_settings({"max_concurrency": 8, "user_testing_enabled": value})
            self.assertEqual(upgraded.execution_settings()["max_concurrency"], 6)
        upgraded.execution_settings({"user_testing_enabled": True})
        restarted = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertTrue(restarted.execution_settings()["user_testing_enabled"])
        restarted.execution_settings({"max_concurrency": 8})
        self.assertTrue(restarted.execution_settings()["user_testing_enabled"])

    def test_quota_atomic_across_stores_persistent_and_rolling(self):
        other = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        data = self.target()
        def submit(i):
            try:
                return (self.store if i % 2 else other).create(data, 7)
            except Problem as exc:
                self.assertEqual(exc.status, 409)
                return None
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            accepted = [r for r in pool.map(submit, range(12)) if r]
        self.assertEqual(len(accepted), 1)
        self.assertEqual(other.state(7)["active_run"]["id"], accepted[0]["id"])
        self.assertEqual(other.state(7)["quota"]["used"], 1)
        for run in accepted: self.store.cancel(run["id"], 7)
        for _ in range(2):
            run = self.store.create(data, 7)
            self.store.cancel(run["id"], 7)
        restarted = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(restarted.state(7)["quota"]["remaining"], 0)
        self.assertEqual(restarted.state(8)["quota"]["remaining"], 3)
        self.date += timedelta(seconds=3599)
        with self.assertRaises(Problem) as error: restarted.create(self.target(), 7)
        self.assertEqual(error.exception.status, 429)
        self.date += timedelta(seconds=1)
        restarted.create(self.target(), 7)
        self.assertEqual(restarted.state(7)["quota"]["remaining"], 2)

    def test_ownership_and_rerun_use_today_and_charge_quota(self):
        run = self.store.create(self.target(owner_user_id=99), 7)
        self.assertEqual(self.store.list_runs(owner_user_id=99)["total"], 0)
        self.assertEqual(self.store.list_runs(owner_user_id=7)["total"], 1)
        for action in (self.store.detail, self.store.rerun, self.store.cancel):
            with self.assertRaises(Problem) as error: action(run["id"], 8)
            self.assertEqual(error.exception.status, 404)
        self.store.cancel(run["id"], 7)
        suite = self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        rerun = self.store.rerun(run["id"], 7)
        self.assertEqual(rerun["suite_id"], suite["id"])
        self.assertEqual(self.store.list_runs(owner_user_id=7)["total"], 2)
        self.assertEqual(self.store.state(7)["quota"]["used"], 2)
        self.store.finish(rerun["id"], "failed")
        third = self.store.rerun(rerun["id"], 7)
        self.store.cancel(third["id"], 7)
        with self.assertRaises(Problem) as error: self.store.rerun(run["id"], 7)
        self.assertEqual(error.exception.status, 429)
        self.assertEqual(self.store.state(7)["quota"]["used"], 3)

    def test_rejected_requests_do_not_consume_quota(self):
        for overrides in ({"suite_id": "stale"}, {"model": ""}, {"api_key": ""}, {"effort": "invalid"}):
            with self.assertRaises(Problem): self.store.create(self.target(**overrides), 7)
        self.assertEqual(self.store.state(7)["quota"]["used"], 0)

    def test_unfinished_user_test_blocks_create_and_rerun_until_fully_stopped(self):
        old = self.store.create(self.target(), 7)
        self.store.finish(old["id"], "completed")
        current = self.store.create(self.target(), 7)
        for status in ("queued", "running", "cancelling"):
            if status == "running": self.store.claim()
            if status == "cancelling": self.store.cancel(current["id"], 7)
            self.assertEqual(self.store.state(7)["active_run"], {"id": current["id"], "status": status})
            with self.assertRaises(Problem) as error: self.store.create(self.target(), 7)
            self.assertEqual(error.exception.status, 409)
            with self.assertRaises(Problem) as error: self.store.rerun(old["id"], 7)
            self.assertEqual(error.exception.status, 409)
        self.assertEqual(self.store.state(7)["quota"]["used"], 2)
        self.assertIsNone(self.store.state(8)["active_run"])
        self.store.create(self.target(), 8)
        self.store.finish(current["id"], "cancelled")
        self.assertIsNone(self.store.state(7)["active_run"])
        self.assertEqual(self.store.rerun(old["id"], 7)["status"], "queued")

    def test_global_five_claims_fifo_and_cancelling_holds_slot(self):
        runs = [self.store.create(self.target(), i + 1) for i in range(12)]
        other = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            claimed = [r for r in pool.map(lambda i: (self.store if i % 2 else other).claim(), range(12)) if r]
        self.assertEqual({r["id"] for r in claimed}, {r["id"] for r in runs[:5]})
        self.assertEqual(self.store.detail(runs[5]["id"])["queue_position"], 1)
        self.assertEqual(self.store.detail(runs[-1]["id"])["queue_position"], 7)
        self.store.cancel(runs[0]["id"], 1)
        self.assertIsNone(self.store.claim())
        self.store.finish(runs[0]["id"], "completed")
        self.assertEqual(self.store.claim()["id"], runs[5]["id"])

    def test_public_finished_results_hide_sensitive_data_and_switch_immediately(self):
        plan = self.store.create_schedule({**self.target(), "name": "Shared plan", "cron_expression": "* * * * *"})
        self.assertFalse(plan["public_results"])
        ids = []
        for i in range(3):
            self.date += timedelta(minutes=1)
            self.assertEqual(self.store.enqueue_due_schedules(), 1)
            run = self.store.list_runs()["items"][0]
            ids.append(run["id"])
            self.store.append(run["id"], {"index": 1, "category": "coding", "status": "passed", "passed": True, "answer": "private-answer"})
            self.store.finish(run["id"], "completed" if i < 2 else "failed", "private-error")
        self.assertEqual(self.store.public_results()["items"], [])
        before = self.store.list_schedules()["items"][0]["next_run_at"]
        self.store.update_schedule(plan["id"], {"public_results": True})
        self.assertEqual(self.store.list_schedules()["items"][0]["next_run_at"], before)
        self.date += timedelta(minutes=1)
        self.store.enqueue_due_schedules()  # In-flight result must not displace finished results.
        shared = self.store.public_results()["items"]
        self.assertEqual([r["id"] for r in shared], ids[::-1])
        self.assertIsNone(shared[0]["pass_rate"])
        self.assertEqual(shared[0]["base_url"], "https://example.com/v1")
        for secret in ("private-user-key", "private-answer", "private-error", "credential"):
            self.assertNotIn(secret, json.dumps(shared))
        self.store.update_schedule(plan["id"], {"public_results": False})
        self.assertEqual(self.store.public_results()["items"], [])
        self.store.update_schedule(plan["id"], {"public_results": True, "enabled": False})
        self.assertEqual(len(self.store.public_results()["items"]), 3)
        self.store.delete_schedule(plan["id"])
        self.assertEqual(self.store.public_results()["items"], [])

    def test_upgrade_defaults_old_schedules_private_and_runs_unowned(self):
        run = self.create()
        self.store.create_schedule({**self.target(), "name": "Old", "cron_expression": "0 9 * * *"})
        with sqlite3.connect(self.store.path) as db:
            db.execute("DROP INDEX runs_site_user_quota")
            db.execute("DROP INDEX runs_site_user_active")
            db.execute("DROP INDEX IF EXISTS runs_user_quota")
            db.execute("DROP INDEX IF EXISTS runs_user_active")
            db.execute("ALTER TABLE runs DROP COLUMN owner_user_id")
            db.execute("ALTER TABLE runs DROP COLUMN quota_at")
            db.execute("ALTER TABLE schedules DROP COLUMN public_results")
        upgraded = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(upgraded.detail(run["id"])["id"], run["id"])
        self.assertEqual(upgraded.list_runs(owner_user_id=7)["total"], 0)
        self.assertFalse(upgraded.list_schedules()["items"][0]["public_results"])

    def test_http_user_namespace_and_quota_cannot_be_spoofed(self):
        engine = Engine(self.store, None, FakeGrader())  # Leave queue pending for deterministic assertions.
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, "fixture-token"))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        def request(path, method="GET", body=None):
            req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", method=method,
                data=json.dumps(body).encode() if body is not None else None,
                headers={"Authorization": "Bearer fixture-token", "X-User-ID": "8"})
            try:
                with urllib.request.urlopen(req, timeout=5) as response: return response.status, json.load(response)
            except urllib.error.HTTPError as exc: return exc.code, json.load(exc)
        try:
            status, run = request("/users/7/runs", "POST", self.target(owner_user_id=8))
            self.assertEqual(status, 202)
            self.assertEqual(request("/users/8/runs")[1]["total"], 0)
            for method, suffix in (("GET", ""), ("POST", "/rerun"), ("POST", "/cancel")):
                self.assertEqual(request("/users/8/runs/" + run["id"] + suffix, method, {} if method == "POST" else None)[0], 404)
            for method, path in (("POST", "/schedules"), ("PUT", "/config"), ("POST", "/suites/refresh")):
                self.assertEqual(request("/users/7" + path, method, {})[0], 404)
            self.assertEqual(request("/users/7/runs", "POST", self.target())[0], 409)
            request("/users/7/runs/" + run["id"] + "/cancel", "POST", {})
            for _ in range(2):
                status, added = request("/users/7/runs", "POST", self.target())
                self.assertEqual(status, 202)
                request("/users/7/runs/" + added["id"] + "/cancel", "POST", {})
            self.assertEqual(request("/users/7/runs", "POST", self.target())[0], 429)
            self.assertEqual(request("/users/7/state")[1]["quota"]["remaining"], 0)
            self.store.execution_settings({"user_testing_enabled": False})
            self.assertEqual(request("/users/9/runs", "POST", self.target(user_testing_enabled=True))[0], 403)
            self.assertEqual(request("/users/7/runs/" + run["id"] + "/rerun", "POST", {})[0], 403)
            self.assertEqual(request("/users/7/execution", "PUT", {"user_testing_enabled": True})[0], 404)
            self.assertFalse(request("/users/7/state")[1]["execution"]["user_testing_enabled"])
            self.assertEqual(request("/users/9/state")[1]["quota"]["used"], 0)
            self.assertEqual(request("/showcase")[1], {"items": [], "next_cursor": None})
        finally:
            server.shutdown()
            server.server_close()
            thread.join(5)

    def test_engine_five_active_and_sixth_starts_after_release(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        runs = [self.store.create(self.target(), i + 1) for i in range(6)]
        gate = threading.Event()
        entered = threading.Event()
        lock = threading.Lock()
        active = peak = calls = 0
        def generate(*_):
            nonlocal active, peak, calls
            with lock:
                active += 1
                calls += 1
                peak = max(peak, active)
                if active == 5: entered.set()
            gate.wait(10)
            with lock: active -= 1
            return {"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": "42"}]}]}
        engine = Engine(self.store, type("Client", (), {"generate": staticmethod(generate)})(), FakeGrader())
        engine.start()
        try:
            self.assertTrue(entered.wait(5))
            self.assertEqual(self.store.detail(runs[-1]["id"])["status"], "queued")
            self.assertEqual(calls, 5)
            gate.set()
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline and self.store.detail(runs[-1]["id"])["status"] != "completed": time.sleep(.05)
            self.assertEqual(self.store.detail(runs[-1]["id"])["status"], "completed")
            self.assertEqual(calls, 6)
            self.assertEqual(peak, 5)
        finally:
            gate.set()
            engine.stopping.set()
            engine.wake.set()
            for worker_thread in engine.threads: worker_thread.join(10)
            engine.scheduler.join(10)
            engine.display_thread.join(10)
