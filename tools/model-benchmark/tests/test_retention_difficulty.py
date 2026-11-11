from datetime import timedelta, timezone
from store import Bank, Problem, Store
from test_worker import Fixture


class DifficultyTests(Fixture):
    def test_configured_mix_persists_and_old_suites_keep_their_questions(self):
        original = self.store.state()["suite"]
        self.assertEqual(original["coding_counts"], {"medium": 10, "hard": 10})
        updated = self.store.refresh({"coding": 8, "math": 1, "reasoning": 1}, original["id"], {"medium": 2, "hard": 6})
        self.assertEqual(updated["coding_counts"], {"medium": 2, "hard": 6})
        run = self.create()
        selected = [q for q in updated["questions"] if q["difficulty"] == "hard"][0]
        self.store.append(run["id"], {"id": selected["id"], "index": 1, "category": "coding", "status": "passed", "passed": True})
        self.assertEqual(self.store.detail(run["id"])["coding_difficulties"]["hard"], {"total": 6, "completed": 1, "passed": 1})
        reopened = Store(self.root / "data", Bank(self.root / "bank.sqlite3"), clock=lambda: self.date)
        self.assertEqual(reopened.state()["coding_counts"], {"medium": 2, "hard": 6})
        self.date += timedelta(days=1)
        self.assertEqual(reopened.state()["suite"]["coding_counts"], {"medium": 2, "hard": 6})
        self.assertEqual(self.store.detail(run["id"])["suite"]["fingerprint"], updated["fingerprint"])
        self.assertEqual(self.store.refresh()["coding_counts"], {"medium": 2, "hard": 6})

    def test_invalid_difficulty_mix_does_not_change_configuration(self):
        before = self.store.state()
        for mix in ({"medium": 2, "hard": 7}, {"medium": 20, "hard": 0}, {"medium": True, "hard": 7},
                    {"medium": -1, "hard": 9}, {"medium": 8}, {"medium": 4, "hard": 4, "easy": 0}):
            with self.subTest(mix=mix), self.assertRaises(Problem):
                self.store.refresh({"coding": 8, "math": 1, "reasoning": 1}, coding_counts=mix)
            after = self.store.state()
            self.assertEqual(after["coding_counts"], before["coding_counts"])
            self.assertEqual(after["suite"]["id"], before["suite"]["id"])
        self.assertEqual(self.store.refresh({"coding": 0, "math": 1, "reasoning": 0}, coding_counts={"medium": 0, "hard": 0})["total"], 1)

    def test_existing_configuration_migrates_without_changing_daily_suite(self):
        before = self.store.state()["suite"]
        with self.store.transaction() as db:
            db.execute("ALTER TABLE config DROP COLUMN coding_counts")
        reopened = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(reopened.state()["suite"], before)
        self.assertEqual(reopened.state()["coding_counts"], {"medium": 10, "hard": 10})


class RetentionTests(Fixture):
    def test_expiration_preserves_boundary_active_tasks_and_schedule(self):
        cutoff = self.date.astimezone(timezone.utc) - timedelta(days=20)
        old = (cutoff - timedelta(seconds=1)).isoformat()
        expired = self.create()
        boundary = self.create()
        active = self.create()
        schedule = self.store.create_schedule({"name": "Keep schedule", "base_url": "https://example.com", "model": "test", "api_key": "keep-credential", "cron_expression": "0 9 * * *", "enabled": False})
        with self.store.transaction() as db:
            db.execute("UPDATE runs SET status='failed',finished_at=? WHERE id=?", (old, expired["id"]))
            db.execute("UPDATE runs SET status='completed',finished_at=? WHERE id=?", (cutoff.isoformat(), boundary["id"]))
            db.execute("UPDATE runs SET started_at=?,finished_at=? WHERE id=?", (old, old, active["id"]))
            db.execute("UPDATE schedules SET last_run_id=? WHERE id=?", (expired["id"], schedule["id"]))
        artifacts = self.store.directory / "artifacts"
        for run in (expired, boundary, active):
            directory = artifacts / run["id"]
            directory.mkdir(parents=True)
            (directory / "001.json").write_text("private answer")
        outside = self.root / "unrelated"
        outside.mkdir()
        (outside / "keep.txt").write_text("keep")
        link = artifacts / ("a" * 32)
        link.symlink_to(outside, target_is_directory=True)
        self.assertEqual(self.store.prune_history(), 1)
        self.assertFalse((artifacts / expired["id"]).exists())
        self.assertTrue((artifacts / boundary["id"]).is_dir())
        self.assertTrue((artifacts / active["id"]).is_dir())
        self.assertTrue((outside / "keep.txt").is_file())
        self.assertTrue(link.is_symlink())
        with self.assertRaises(Problem): self.store.detail(expired["id"])
        self.assertEqual(self.store.detail(active["id"])["status"], "queued")
        plans = self.store.list_schedules()["items"]
        self.assertEqual(len(plans), 1)
        self.assertIsNone(plans[0]["last_run_id"])
        self.assertEqual(self.store.prune_history(), 0)
        # An interrupted deletion is retried without touching retained runs.
        orphan = artifacts / ("b" * 32)
        orphan.mkdir()
        self.store.prune_history()
        self.assertFalse(orphan.exists())

    def test_pagination_is_bounded_and_keeps_users_separate(self):
        owned = []
        for index in range(41):
            run = self.create()
            owner = 7 if index < 25 else 8
            if owner == 7: owned.append(run["id"])
            with self.store.transaction() as db:
                db.execute("UPDATE runs SET owner_user_id=?,status='completed' WHERE id=?", (owner, run["id"]))
        first = self.store.list_runs(1, 7)
        second = self.store.list_runs(2, 7)
        self.assertEqual(first["total"], 25)
        self.assertEqual(len(first["items"]), 20)
        self.assertEqual(len(second["items"]), 5)
        self.assertEqual([r["id"] for r in first["items"] + second["items"]], list(reversed(owned)))
        self.assertEqual(self.store.list_runs(999, 7)["page"], 2)
        self.assertEqual(self.store.list_runs(1, 999)["total"], 0)
        for page in (0, True, 1000001):
            with self.assertRaises(Problem): self.store.list_runs(page)
