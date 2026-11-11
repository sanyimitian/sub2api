import concurrent.futures
from datetime import datetime, timedelta
import json
import sqlite3
import threading
import time

from engine import Engine
from scheduling import next_occurrence
from store import Problem, Store
from test_worker import Fixture, FakeGrader


class ScheduleTests(Fixture):
    def plan(self, **overrides):
        return self.store.create_schedule({"name": "Daily model test", "base_url": "https://example.com/v1", "model": "test-model",
            "api_key": "schedule-private-key", "effort": "high", "cron_expression": "0 9 * * *", "enabled": True, **overrides})

    def test_intervals_validate_units_and_limits(self):
        for expression, minutes in (("@every 1m", 1), ("@every 90m", 90), ("@every 2h", 120), ("@every 720h", 43200)):
            with self.subTest(expression=expression):
                plan = self.plan(cron_expression=expression)
                self.assertEqual(datetime.fromisoformat(plan["next_run_at"]), self.date + timedelta(minutes=minutes))
        for expression in ("@every 0m", "@every -1h", "@every 1.5h", "@every 721h", "@every 43201m", "@every 1s", "@every 1h;ls"):
            with self.subTest(expression=expression), self.assertRaises(Problem):
                self.plan(cron_expression=expression)

    def test_interval_waits_for_completion_and_resumes_after_restart(self):
        plan = self.plan(cron_expression="@every 90m")
        self.date += timedelta(minutes=90)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        claimed = self.store.claim()
        self.assertEqual(self.store.list_schedules()["items"][0]["active_run_id"], claimed["id"])
        self.assertIsNone(self.store.list_schedules()["items"][0]["next_run_at"])
        self.date += timedelta(hours=4)
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.store.finish(claimed["id"], "completed")
        expected = self.date + timedelta(minutes=90)
        restarted = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        restarted.recover()
        self.assertEqual(datetime.fromisoformat(restarted.list_schedules()["items"][0]["next_run_at"]), expected)
        self.date = expected
        self.assertEqual(restarted.enqueue_due_schedules(), 1)
        self.assertEqual(restarted.claim()["schedule_id"], plan["id"])
        self.date += timedelta(minutes=10)
        restarted.recover()
        self.assertEqual(datetime.fromisoformat(restarted.list_schedules()["items"][0]["next_run_at"]), self.date + timedelta(minutes=90))

    def test_manual_trigger_is_atomic_uses_current_suite_and_saved_credentials(self):
        plan = self.plan(enabled=False, public_results=True)
        old = self.store.state()["suite"]
        latest = self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        other = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        def trigger(i):
            try:
                return (self.store if i % 2 else other).run_schedule(plan["id"])
            except Problem as exc:
                return exc.status
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(trigger, range(12)))
        self.assertEqual(results.count(409), 11)
        run = next(r for r in results if isinstance(r, dict))
        self.assertEqual(run["suite_id"], latest["id"])
        self.assertNotEqual(run["suite_id"], old["id"])
        self.assertEqual(run["schedule_id"], plan["id"])
        self.assertEqual(run["effort"], "high")
        self.assertNotIn("schedule-private-key", json.dumps(run))
        self.assertEqual(self.store.claim()["api_key"], "schedule-private-key")
        self.store.finish(run["id"], "completed")
        saved = self.store.list_schedules()["items"][0]
        self.assertFalse(saved["enabled"])
        self.assertIsNone(saved["next_run_at"])
        self.assertEqual(self.store.public_results()["items"][0]["schedule_id"], plan["id"])
        self.store.delete_schedule(plan["id"])
        with self.assertRaises(Problem) as error:
            self.store.run_schedule(plan["id"])
        self.assertEqual(error.exception.status, 404)

    def test_manual_trigger_and_due_tick_share_one_active_slot(self):
        for expression in ("* * * * *", "@every 1m"):
            with self.subTest(expression=expression):
                plan = self.plan(cron_expression=expression)
                self.date += timedelta(minutes=2)
                other = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
                def trigger(manual):
                    try:
                        return self.store.run_schedule(plan["id"]) if manual else other.enqueue_due_schedules()
                    except Problem as exc:
                        return exc.status
                with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                    list(pool.map(trigger, [True, False]))
                with self.store.transaction() as db:
                    self.assertEqual(db.execute("SELECT COUNT(*) FROM runs WHERE schedule_id=?", (plan["id"],)).fetchone()[0], 1)
                self.store.delete_schedule(plan["id"])

    def test_manual_trigger_respects_global_queue_and_preserves_cron(self):
        self.store.execution_settings({"max_concurrency": 1})
        self.create()
        active = self.store.claim()
        plan = self.plan()
        run = self.store.run_schedule(plan["id"])
        self.assertEqual(run["status"], "queued")
        self.assertIsNone(self.store.claim())
        self.assertEqual(self.store.list_schedules()["items"][0]["next_run_at"], plan["next_run_at"])
        self.store.finish(active["id"], "completed")
        self.assertEqual(self.store.claim()["id"], run["id"])

    def test_interval_edit_pause_cancel_and_credential_failure(self):
        plan = self.plan(cron_expression="@every 5m")
        run = self.store.run_schedule(plan["id"])
        changed = self.store.update_schedule(plan["id"], {"cron_expression": "@every 2h"})
        self.assertIsNone(changed["next_run_at"])
        self.store.cancel(run["id"])
        self.assertEqual(datetime.fromisoformat(self.store.list_schedules()["items"][0]["next_run_at"]), self.date + timedelta(hours=2))
        run = self.store.run_schedule(plan["id"])
        self.store.update_schedule(plan["id"], {"enabled": False})
        self.store.cancel(run["id"])
        self.assertIsNone(self.store.list_schedules()["items"][0]["next_run_at"])
        self.store.update_schedule(plan["id"], {"enabled": True})
        run = self.store.run_schedule(plan["id"])
        with self.store.transaction() as db:
            db.execute("UPDATE runs SET credential=? WHERE id=?", (b"invalid", run["id"]))
        self.assertIsNone(self.store.claim())
        self.assertEqual(self.store.detail(run["id"])["status"], "failed")
        self.assertEqual(datetime.fromisoformat(self.store.list_schedules()["items"][0]["next_run_at"]), self.date + timedelta(hours=2))

    def test_next_trigger_and_encrypted_credentials(self):
        plan = self.plan()
        self.assertEqual(plan["next_run_at"], "2026-11-12T01:00:00+00:00")
        self.assertEqual(plan["timezone"], "Asia/Shanghai")
        self.assertEqual(self.store.list_runs()["total"], 0)
        self.assertNotIn("schedule-private-key", json.dumps(self.store.list_schedules()))
        self.assertNotIn("credential", json.dumps(plan))
        with sqlite3.connect(self.store.path) as db:
            credential = db.execute("SELECT credential FROM schedules WHERE id=?", (plan["id"],)).fetchone()[0]
        self.assertNotIn(b"schedule-private-key", credential)
        self.assertEqual(self.store.cipher.decrypt(credential), b"schedule-private-key")

    def test_concurrent_ticks_enqueue_once_with_current_daily_suite(self):
        old = self.store.state()["suite"]
        plan = self.plan()
        self.date += timedelta(hours=10)
        latest = self.store.refresh({"coding": 2, "math": 1, "reasoning": 0})
        other = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            added = list(pool.map(lambda i: (self.store if i % 2 else other).enqueue_due_schedules(), range(12)))
        self.assertEqual(sum(added), 1)
        claimed = self.store.claim()
        self.assertEqual(claimed["schedule_id"], plan["id"])
        self.assertEqual(claimed["api_key"], "schedule-private-key")
        self.assertEqual(claimed["effort"], "high")
        self.assertNotEqual(claimed["suite_id"], old["id"])
        self.assertEqual(claimed["suite_id"], latest["id"])
        self.assertEqual(claimed["question_ids"], [q["id"] for q in latest["questions"]])
        self.assertEqual(self.store.detail(claimed["id"])["total"], 3)
        self.assertEqual(self.store.list_schedules()["items"][0]["last_run_id"], claimed["id"])
        self.store.refresh()
        self.assertEqual(self.store.detail(claimed["id"])["suite_id"], latest["id"])

    def test_no_overlap_queue_waiting_and_resume_next_occurrence(self):
        plan = self.plan(cron_expression="* * * * *")
        self.date += timedelta(minutes=1)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        first = self.store.list_runs()["items"][0]
        self.date += timedelta(minutes=1)
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.assertIn("尚未结束", self.store.list_schedules()["items"][0]["last_error"])
        self.store.finish(first["id"], "completed")
        for _ in range(10): self.create()
        self.date += timedelta(minutes=1)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        self.assertEqual(self.store.list_schedules()["items"][0]["last_error"], "")
        for row in self.store.list_runs()["items"]: self.store.cancel(row["id"])
        self.date += timedelta(minutes=1)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        latest = self.store.list_schedules()["items"][0]
        self.assertEqual(latest["last_error"], "")
        self.assertNotEqual(latest["last_run_id"], first["id"])
        self.assertEqual(latest["id"], plan["id"])

    def test_pause_edit_resume_delete_preserve_existing_runs(self):
        plan = self.plan(cron_expression="* * * * *")
        paused = self.store.update_schedule(plan["id"], {"enabled": False})
        self.assertIsNone(paused["next_run_at"])
        self.date += timedelta(hours=10)
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        edited = self.store.update_schedule(plan["id"], {"name": "New name", "cron_expression": "*/5 * * * *", "enabled": True})
        self.assertEqual(edited["name"], "New name")
        self.assertGreater(datetime.fromisoformat(edited["next_run_at"]), self.date)
        self.date += timedelta(minutes=5)
        self.assertEqual(self.store.enqueue_due_schedules(), 1)
        record = self.store.list_runs()["items"][0]
        self.assertEqual(self.store.delete_schedule(plan["id"]), {"deleted": True})
        self.assertEqual(self.store.list_schedules()["items"], [])
        self.date += timedelta(hours=1)
        self.assertEqual(self.store.enqueue_due_schedules(), 0)
        self.assertEqual(self.store.detail(record["id"])["status"], "queued")
        self.assertEqual(self.store.claim()["api_key"], "schedule-private-key")
        with self.assertRaises(Problem): self.store.update_schedule(plan["id"], {"enabled": True})

    def test_restart_catches_up_once_and_preserves_plan(self):
        plan = self.plan(cron_expression="*/5 * * * *")
        self.date += timedelta(days=3)
        restarted = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        restarted.recover()
        self.assertEqual(restarted.enqueue_due_schedules(), 1)
        self.assertEqual(restarted.enqueue_due_schedules(), 0)
        self.assertEqual(restarted.list_runs()["total"], 1)
        self.assertEqual(restarted.list_schedules()["items"][0]["id"], plan["id"])
        self.assertGreater(datetime.fromisoformat(restarted.list_schedules()["items"][0]["next_run_at"]), self.date)

    def test_edit_name_does_not_skip_due_occurrence(self):
        plan = self.plan(cron_expression="* * * * *")
        self.date += timedelta(minutes=3)
        renamed = self.store.update_schedule(plan["id"], {"name": "Renamed"})
        self.assertEqual(renamed["next_run_at"], plan["next_run_at"])
        self.assertEqual(self.store.enqueue_due_schedules(), 1)

    def test_validation_rejects_invalid_cron_and_mutating_credentials(self):
        for expression in (None, "", "* * * * * *", "61 * * * *", "*/0 * * * *", "0 0 31 2 *", "@daily", "H * * * *"):
            with self.subTest(expression=expression), self.assertRaises(Problem): self.plan(cron_expression=expression)
        for data in ({"name": " "}, {"enabled": "true"}, {"api_key": ""}):
            with self.subTest(data=data), self.assertRaises(Problem): self.plan(**data)
        plan = self.plan()
        with self.assertRaises(Problem): self.store.update_schedule(plan["id"], {"api_key": "new-key"})

    def test_numeric_weekly_and_steps(self):
        zone = self.store.timezone
        expression, upcoming = next_occurrence("0 9 * * 1", self.date, zone)
        self.assertEqual(expression, "0 9 * * 1")
        self.assertEqual(datetime.fromisoformat(upcoming).astimezone(zone).strftime("%Y-%m-%d %H:%M"), "2026-11-16 09:00")
        self.assertEqual(next_occurrence("*/30 * * * *", self.date, zone)[1], "2026-11-11T16:00:00+00:00")

    def test_scheduler_tick_runs_while_engine_is_busy(self):
        self.plan(cron_expression="* * * * *")
        self.create()
        entered, release = threading.Event(), threading.Event()
        engine = Engine(self.store, None, FakeGrader())
        def execute(_):
            entered.set()
            release.wait(10)
        engine.execute = execute
        engine.start()
        try:
            self.assertTrue(entered.wait(3))
            self.date += timedelta(minutes=1)
            deadline = time.monotonic() + 7
            while self.store.list_runs()["total"] < 2 and time.monotonic() < deadline: time.sleep(.05)
            self.assertEqual(self.store.list_runs()["total"], 2)
        finally:
            engine.stopping.set()
            engine.wake.set()
            release.set()
            for worker_thread in engine.threads: worker_thread.join(10)
            engine.scheduler.join(10)
            engine.display_thread.join(10)

    def test_upgrade_adds_schedule_columns_without_losing_old_run(self):
        original = self.create()
        with sqlite3.connect(self.store.path) as db:
            db.execute("DROP INDEX run_schedule_occurrence")
            db.execute("ALTER TABLE runs DROP COLUMN schedule_id")
            db.execute("ALTER TABLE runs DROP COLUMN scheduled_for")
            db.execute("DROP TABLE schedules")
        upgraded = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(upgraded.detail(original["id"])["suite_id"], original["suite_id"])
        self.assertEqual(upgraded.list_schedules(), {"items": []})
        self.assertEqual(upgraded.claim()["api_key"], "test-secret-DO-NOT-EXPOSE")
