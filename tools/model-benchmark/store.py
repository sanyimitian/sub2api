from __future__ import annotations

import base64
import hashlib
import ipaddress
import json
import math
import os
import re
import secrets
import shutil
import sqlite3
import threading
import uuid
import zlib
from contextlib import closing, contextmanager
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlsplit
from zoneinfo import ZoneInfo

from cryptography.fernet import Fernet, InvalidToken

from transport import normalize_url
from scheduling import interval_duration, next_occurrence

CATEGORIES = ("coding", "math", "reasoning")
DEFAULT_COUNTS = {"coding": 20, "math": 5, "reasoning": 5}
EFFORTS = ("low", "medium", "high", "xhigh")
ACTIVE = ("queued", "running", "cancelling")
DEFAULT_CONCURRENCY = 5
MAX_CONCURRENCY = 100
HOURLY_LIMIT = 3
CODING_DIFFICULTIES = ("medium", "hard")
RETENTION_DAYS = 20
RESULT_PAGE_SIZE = 24


def mask_result_url(value):
    """只保留协议、主机末尾一个字符和域名后缀；不泄露端口及路径。"""
    parsed = urlsplit(value)
    host = (parsed.hostname or "").rstrip(".")
    try:
        ipaddress.ip_address(host)
        masked = "****"
    except ValueError:
        name, dot, suffix = host.rpartition(".")
        masked = "*" * max(3, len(name) - 1) + name[-1:] + dot + suffix if name and dot else "****"
    return f"{parsed.scheme}://{masked}"


def result_cursor(value, scope):
    if not value:
        return None
    try:
        if len(value) > 1024:
            raise ValueError()
        decoded = json.loads(base64.urlsafe_b64decode(value + "=" * (-len(value) % 4)))
        kind, *key = decoded
        if kind != scope or len(key) != (4 if scope == "site" else 3):
            raise ValueError()
        stamp, *identifiers = key
        if type(stamp) not in (int, float) or not math.isfinite(stamp) or not all(isinstance(v, str) and len(v) <= 200 for v in identifiers):
            raise ValueError()
        return tuple(key)
    except (ValueError, TypeError, UnicodeError, OverflowError):
        raise Problem("分页位置不正确") from None


def encode_result_cursor(key, scope):
    return base64.urlsafe_b64encode(json.dumps([scope, *key]).encode()).decode().rstrip("=")


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


class Problem(Exception):
    def __init__(self, message: str, status: int = 400):
        super().__init__(message)
        self.status = status


class Bank:
    def __init__(self, path: Path):
        self.path = path
        if not path.is_file():
            raise RuntimeError("Question bank is missing; run prepare.py first")
        with closing(self.connect()) as db:
            self.catalog = [dict(r) for r in db.execute("SELECT id,category,difficulty,title FROM questions ORDER BY id")]
            self.manifest = json.loads(db.execute("SELECT value FROM metadata WHERE key='manifest'").fetchone()[0])

    def connect(self):
        db = sqlite3.connect(f"{self.path.resolve().as_uri()}?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        return db

    def get(self, question_id: str) -> dict:
        with closing(self.connect()) as db:
            row = db.execute("SELECT payload FROM questions WHERE id=?", (question_id,)).fetchone()
        if row is None:
            raise RuntimeError("A frozen suite question is missing from the bank")
        return json.loads(zlib.decompress(row[0]))

    def sizes(self) -> dict:
        return {category: sum(q["category"] == category for q in self.catalog) for category in CATEGORIES}

    def coding_sizes(self) -> dict:
        return {difficulty: sum(q["category"] == "coding" and q["difficulty"] == difficulty for q in self.catalog)
                for difficulty in CODING_DIFFICULTIES}


class Store:
    def __init__(self, directory: Path, bank: Bank, tz: str = "Asia/Shanghai", clock=None, resources=None):
        directory.mkdir(parents=True, exist_ok=True)
        self.directory, self.bank = directory, bank
        self.resources = resources
        self.original_bank = bank
        self.banks = {bank.manifest["version"]: bank}
        self.timezone = ZoneInfo(tz)
        self.clock = clock or (lambda: datetime.now(self.timezone))
        self.lock = threading.RLock()
        self.path = directory / "benchmark.sqlite3"
        key_path = directory / "credentials.key"
        if not key_path.exists():
            fd = os.open(key_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, "wb") as f:
                f.write(Fernet.generate_key())
        self.cipher = Fernet(key_path.read_bytes())
        with self.transaction() as db:
            db.executescript("""
                CREATE TABLE IF NOT EXISTS config (id INTEGER PRIMARY KEY CHECK(id=1), counts TEXT NOT NULL);
                CREATE TABLE IF NOT EXISTS suites (
                    id TEXT PRIMARY KEY, day TEXT NOT NULL, revision INTEGER NOT NULL,
                    counts TEXT NOT NULL, question_ids TEXT NOT NULL, fingerprint TEXT NOT NULL,
                    bank_version TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(day,revision));
                CREATE TABLE IF NOT EXISTS runs (
                    id TEXT PRIMARY KEY, suite_id TEXT NOT NULL REFERENCES suites(id),
                    rerun_of TEXT, base_url TEXT NOT NULL, model TEXT NOT NULL, effort TEXT NOT NULL,
                    credential BLOB NOT NULL, created_at TEXT NOT NULL, started_at TEXT,
                    finished_at TEXT, status TEXT NOT NULL, results TEXT NOT NULL DEFAULT '[]',
                    error TEXT NOT NULL DEFAULT '');
                CREATE INDEX IF NOT EXISTS runs_created ON runs(created_at DESC);
                CREATE TABLE IF NOT EXISTS schedules (
                    id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL,
                    model TEXT NOT NULL, effort TEXT NOT NULL, credential BLOB NOT NULL,
                    cron_expression TEXT NOT NULL, enabled INTEGER NOT NULL,
                    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
                    next_run_at TEXT, last_attempt_at TEXT, last_run_id TEXT,
                    last_error TEXT NOT NULL DEFAULT '', deleted INTEGER NOT NULL DEFAULT 0);
            """)
            columns = {row["name"] for row in db.execute("PRAGMA table_info(runs)")}
            for column in ("schedule_id", "scheduled_for"):
                if column not in columns:
                    db.execute(f"ALTER TABLE runs ADD COLUMN {column} TEXT")
            for column, kind in (("owner_user_id", "INTEGER"), ("quota_at", "REAL")):
                if column not in columns:
                    db.execute(f"ALTER TABLE runs ADD COLUMN {column} {kind}")
            schedule_columns = {row["name"] for row in db.execute("PRAGMA table_info(schedules)")}
            if "public_results" not in schedule_columns:
                db.execute("ALTER TABLE schedules ADD COLUMN public_results INTEGER NOT NULL DEFAULT 0")
            db.execute("CREATE INDEX IF NOT EXISTS runs_user_quota ON runs(owner_user_id,quota_at)")
            db.execute("CREATE INDEX IF NOT EXISTS runs_user_active ON runs(owner_user_id,status)")
            db.execute("CREATE INDEX IF NOT EXISTS runs_finished_page ON runs(julianday(finished_at) DESC,id DESC) WHERE finished_at IS NOT NULL")
            db.execute("CREATE INDEX IF NOT EXISTS runs_schedule_page ON runs(schedule_id,julianday(finished_at) DESC,id DESC) WHERE finished_at IS NOT NULL")
            db.execute("CREATE UNIQUE INDEX IF NOT EXISTS run_schedule_occurrence ON runs(schedule_id,scheduled_for)")
            config_columns = {row["name"] for row in db.execute("PRAGMA table_info(config)")}
            if "max_concurrency" not in config_columns:
                db.execute(f"ALTER TABLE config ADD COLUMN max_concurrency INTEGER NOT NULL DEFAULT {DEFAULT_CONCURRENCY}")
            if "coding_counts" not in config_columns:
                db.execute("ALTER TABLE config ADD COLUMN coding_counts TEXT")
            if "bank_version" not in config_columns:
                db.execute("ALTER TABLE config ADD COLUMN bank_version TEXT")
            if "user_testing_enabled" not in config_columns:
                db.execute("ALTER TABLE config ADD COLUMN user_testing_enabled INTEGER NOT NULL DEFAULT 0")
            db.execute("INSERT OR IGNORE INTO config(id,counts) VALUES(1,?)", (json.dumps(DEFAULT_COUNTS),))
            active_version = db.execute("SELECT bank_version FROM config WHERE id=1").fetchone()[0]
            if active_version:
                self.bank = self.bank_for_version(active_version)
            config = db.execute("SELECT counts,coding_counts FROM config WHERE id=1").fetchone()
            if config["coding_counts"] is None:
                db.execute("UPDATE config SET coding_counts=? WHERE id=1",
                           (json.dumps(self.default_coding_counts(json.loads(config["counts"])["coding"])),))
        os.chmod(self.path, 0o600)

    def bank_for_version(self, version):
        with self.lock:
            if version not in self.banks:
                if not re.fullmatch(r"public-v[12]-[a-f0-9]{64}", version or ""):
                    raise Problem("题库版本不存在", 404)
                bank = Bank(self.original_bank.path.parent / "banks" / (version + ".sqlite3"))
                if bank.manifest["version"] != version:
                    raise Problem("题库版本校验失败")
                self.banks[version] = bank
            return self.banks[version]

    def activate_bank(self, version):
        with self.lock:
            previous = self.bank
            candidate = self.bank_for_version(version)
            if candidate is previous:
                return self.state()["suite"]
            try:
                with self.transaction() as db:
                    self.bank = candidate
                    config = db.execute("SELECT counts,coding_counts FROM config WHERE id=1").fetchone()
                    counts = self.validate_counts(json.loads(config["counts"]))
                    self.validate_coding_counts(json.loads(config["coding_counts"]), counts["coding"])
                    db.execute("UPDATE config SET bank_version=? WHERE id=1", (version,))
                    suite = self._suite(db, refresh=True)
            except Exception:
                self.bank = previous
                raise
            return self.suite_public(suite)

    @contextmanager
    def transaction(self):
        with self.lock:
            db = sqlite3.connect(self.path, timeout=30)
            db.row_factory = sqlite3.Row
            db.execute("PRAGMA foreign_keys=ON")
            db.execute("PRAGMA journal_mode=WAL")
            try:
                db.execute("BEGIN IMMEDIATE")
                yield db
                db.commit()
            except Exception:
                db.rollback()
                raise
            finally:
                db.close()

    def validate_counts(self, counts: dict) -> dict:
        if not isinstance(counts, dict) or set(counts) != set(CATEGORIES):
            raise Problem("请填写编程、数学、推理三类题目数量")
        sizes = self.bank.sizes()
        if any(type(counts[k]) is not int or counts[k] < 0 or counts[k] > sizes[k] for k in CATEGORIES):
            raise Problem("题目数必须是非负整数，且不能超过题库容量")
        if not 1 <= sum(counts.values()) <= 100:
            raise Problem("总题量应为 1 至 100 道")
        return {key: counts[key] for key in CATEGORIES}

    def default_coding_counts(self, total):
        sizes = self.bank.coding_sizes()
        medium = min((total + 1) // 2, sizes["medium"])
        hard = min(total - medium, sizes["hard"])
        return {"medium": total - hard, "hard": hard}

    def validate_coding_counts(self, counts, total):
        sizes = self.bank.coding_sizes()
        if (not isinstance(counts, dict) or set(counts) != set(CODING_DIFFICULTIES)
                or any(type(counts[k]) is not int or not 0 <= counts[k] <= sizes[k] for k in CODING_DIFFICULTIES)
                or sum(counts.values()) != total):
            raise Problem("请填写中等和困难编程题数，合计须等于编程题总数且不能超过各难度题库容量")
        return {key: counts[key] for key in CODING_DIFFICULTIES}

    def _suite(self, db, refresh=False, counts=None) -> dict:
        day = self.clock().astimezone(self.timezone).date().isoformat()
        previous = db.execute("SELECT * FROM suites WHERE day=? ORDER BY revision DESC LIMIT 1", (day,)).fetchone()
        if previous is not None and not refresh:
            return dict(previous)
        counts = counts or json.loads(db.execute("SELECT counts FROM config WHERE id=1").fetchone()[0])
        self.validate_counts(counts)
        coding_counts = self.validate_coding_counts(json.loads(db.execute("SELECT coding_counts FROM config WHERE id=1").fetchone()[0]), counts["coding"])
        rng = secrets.SystemRandom()
        selected = []
        for category in CATEGORIES:
            pool = [q for q in self.bank.catalog if q["category"] == category]
            if category == "coding":
                for difficulty in CODING_DIFFICULTIES:
                    selected.extend(rng.sample([q for q in pool if q["difficulty"] == difficulty], coding_counts[difficulty]))
            else:
                selected.extend(rng.sample(pool, counts[category]))
        ids = [q["id"] for q in selected]
        if previous and ids == json.loads(previous["question_ids"]) and len(ids) > 1:
            ids = ids[1:] + ids[:1]
        version = self.bank.manifest["version"]
        fingerprint = hashlib.sha256(json.dumps({"ids": ids, "bank": version}, sort_keys=True).encode()).hexdigest()
        suite = dict(id=uuid.uuid4().hex, day=day, revision=(previous["revision"] + 1 if previous else 1),
                     counts=json.dumps(counts), question_ids=json.dumps(ids), fingerprint=fingerprint,
                     bank_version=version, created_at=now())
        db.execute("INSERT INTO suites VALUES(:id,:day,:revision,:counts,:question_ids,:fingerprint,:bank_version,:created_at)", suite)
        return suite

    def suite_public(self, suite: dict) -> dict:
        result = dict(suite)
        result["counts"] = json.loads(result["counts"])
        ids = json.loads(result.pop("question_ids"))
        lookup = {q["id"]: q for q in self.bank_for_version(suite["bank_version"]).catalog}
        result["questions"] = [lookup[qid] for qid in ids]
        result["coding_counts"] = {level: sum(q["category"] == "coding" and q["difficulty"] == level for q in result["questions"])
                                   for level in CODING_DIFFICULTIES}
        result["total"] = len(ids)
        return result

    def state(self, owner_user_id=None) -> dict:
        with self.transaction() as db:
            suite = self._suite(db)
            counts = json.loads(db.execute("SELECT counts FROM config WHERE id=1").fetchone()[0])
            coding_counts = json.loads(db.execute("SELECT coding_counts FROM config WHERE id=1").fetchone()[0])
            quota = self._quota(db, owner_user_id) if owner_user_id is not None else None
            execution = self._execution_settings(db)
            active = self._active_user_run(db, owner_user_id) if owner_user_id is not None else None
            bank = self.bank
        return {"counts": counts, "total": sum(counts.values()), "timezone": str(self.timezone),
                "capacities": bank.sizes(), "coding_counts": coding_counts, "coding_capacities": bank.coding_sizes(),
                "retention_days": RETENTION_DAYS, "suite": self.suite_public(suite),
                "bank_version": bank.manifest["version"], "efforts": list(EFFORTS), "quota": quota, "execution": execution,
                "active_run": dict(active) if active else None}

    def _active_user_run(self, db, owner_user_id):
        return db.execute("SELECT id,status FROM runs WHERE owner_user_id=? AND status IN ('queued','running','cancelling') ORDER BY rowid LIMIT 1", (owner_user_id,)).fetchone()

    def _execution_settings(self, db):
        return {"max_concurrency": db.execute("SELECT max_concurrency FROM config WHERE id=1").fetchone()[0],
                "user_testing_enabled": bool(db.execute("SELECT user_testing_enabled FROM config WHERE id=1").fetchone()[0]),
                "running": db.execute("SELECT COUNT(*) FROM runs WHERE status IN ('running','cancelling')").fetchone()[0],
                "queued": db.execute("SELECT COUNT(*) FROM runs WHERE status='queued'").fetchone()[0]}

    def execution_settings(self, data=None):
        with self.transaction() as db:
            if data is not None:
                if not data or set(data) - {"max_concurrency", "user_testing_enabled"}:
                    raise Problem("请选择要修改的执行设置")
                if "max_concurrency" in data and (type(data["max_concurrency"]) is not int or not 1 <= data["max_concurrency"] <= MAX_CONCURRENCY):
                    raise Problem(f"全站最大并发必须为 1 至 {MAX_CONCURRENCY} 的整数")
                if "user_testing_enabled" in data and type(data["user_testing_enabled"]) is not bool:
                    raise Problem("用户自行测试开关必须为布尔值")
                if "max_concurrency" in data:
                    db.execute("UPDATE config SET max_concurrency=? WHERE id=1", (data["max_concurrency"],))
                if "user_testing_enabled" in data:
                    db.execute("UPDATE config SET user_testing_enabled=? WHERE id=1", (int(data["user_testing_enabled"]),))
            return self._execution_settings(db)

    def _quota(self, db, owner_user_id):
        timestamps = [r[0] for r in db.execute(
            "SELECT quota_at FROM runs WHERE owner_user_id=? AND quota_at>? ORDER BY quota_at",
            (owner_user_id, self.clock().timestamp() - 3600))]
        return {"limit": HOURLY_LIMIT, "used": len(timestamps), "remaining": max(0, HOURLY_LIMIT - len(timestamps)),
                "next_available_at": datetime.fromtimestamp(timestamps[0] + 3600, timezone.utc).isoformat() if timestamps else None}

    def _owned_run(self, db, run_id, owner_user_id):
        row = db.execute("SELECT * FROM runs WHERE id=?", (run_id,)).fetchone()
        if row is None or (owner_user_id is not None and row["owner_user_id"] != owner_user_id):
            raise Problem("测试记录不存在", 404)
        return row

    def _queue_position(self, db, record):
        record["queue_position"] = db.execute(
            "SELECT COUNT(*) FROM runs WHERE status='queued' AND rowid <= (SELECT rowid FROM runs WHERE id=?)",
            (record["id"],)).fetchone()[0] if record["status"] == "queued" else None
        return record

    def refresh(self, counts=None, expected_suite=None, coding_counts=None) -> dict:
        if counts is not None:
            counts = self.validate_counts(counts)
        with self.transaction() as db:
            current = self._suite(db)
            if expected_suite and current["id"] != expected_suite:
                raise Problem("题库已更新，请刷新页面后重试", 409)
            if counts is not None:
                if coding_counts is None:
                    coding_counts = json.loads(db.execute("SELECT coding_counts FROM config WHERE id=1").fetchone()[0])
                    if sum(coding_counts.values()) != counts["coding"]:
                        coding_counts = self.default_coding_counts(counts["coding"])
                coding_counts = self.validate_coding_counts(coding_counts, counts["coding"])
                db.execute("UPDATE config SET counts=? WHERE id=1", (json.dumps(counts),))
                db.execute("UPDATE config SET coding_counts=? WHERE id=1", (json.dumps(coding_counts),))
            suite = self._suite(db, refresh=True, counts=counts)
        return self.suite_public(suite)

    def validate_target(self, data: dict):
        try:
            base_url = normalize_url(data.get("base_url", ""))
        except ValueError as exc:
            raise Problem(str(exc)) from exc
        model, key = data.get("model", ""), data.get("api_key", "")
        effort = data.get("effort", "medium")
        if not isinstance(model, str) or not model.strip() or len(model) > 200 or any(ord(c) < 32 for c in model):
            raise Problem("模型名称格式不正确")
        if not isinstance(key, str) or not key.strip() or len(key) > 4096 or any(not 33 <= ord(c) <= 126 for c in key.strip()):
            raise Problem("API Key 格式不正确")
        if effort not in EFFORTS:
            raise Problem("推理强度不受支持")
        return base_url, model.strip(), effort, self.cipher.encrypt(key.strip().encode())

    def create(self, data: dict, owner_user_id=None) -> dict:
        base_url, model, effort, credential = self.validate_target(data)
        with self.transaction() as db:
            suite = self._suite(db)
            if data.get("suite_id") != suite["id"]:
                raise Problem("当天题库已变更，请刷新后再开始测试", 409)
            run_id = self._insert_run(db, suite["id"], base_url, model, effort, credential, owner_user_id=owner_user_id)
        return self.detail(run_id)

    def _insert_run(self, db, suite_id, base_url, model, effort, credential, rerun_of=None, schedule_id=None, scheduled_for=None, owner_user_id=None):
        if owner_user_id is not None and not db.execute("SELECT user_testing_enabled FROM config WHERE id=1").fetchone()[0]:
            raise Problem("管理员暂未开放用户自行测试", 403)
        if owner_user_id is not None and self._active_user_run(db, owner_user_id):
            raise Problem("你已有未结束的测试（含排队和正在停止），请等待结束后再发起", 409)
        if owner_user_id is not None and self._quota(db, owner_user_id)["remaining"] == 0:
            raise Problem("每小时最多测试 3 次（含重新测试），请等待额度恢复", 429)
        if self.resources:
            from resources import ResourcePressure
            try:
                self.resources.require_capacity()
            except ResourcePressure as exc:
                raise Problem(str(exc), 503) from exc
        run_id = uuid.uuid4().hex
        db.execute("INSERT INTO runs(id,suite_id,rerun_of,base_url,model,effort,credential,created_at,status,schedule_id,scheduled_for,owner_user_id,quota_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
                   (run_id, suite_id, rerun_of, base_url, model, effort, credential, now(), "queued", schedule_id, scheduled_for, owner_user_id, self.clock().timestamp()))
        return run_id

    def rerun(self, run_id: str, owner_user_id=None) -> dict:
        with self.transaction() as db:
            row = self._owned_run(db, run_id, owner_user_id)
            if row["status"] in ACTIVE:
                raise Problem("该测试尚未结束", 409)
            child = db.execute("SELECT id FROM runs WHERE rerun_of=? AND status IN ('queued','running','cancelling')", (run_id,)).fetchone()
            if child:
                raise Problem("该记录已在重新测试，请等待完成", 409)
            # Resolve today's latest suite in the same transaction as enqueueing.
            # Retain connection settings, but never reuse a historical suite.
            suite = self._suite(db)
            new_id = self._insert_run(db, suite["id"], row["base_url"], row["model"], row["effort"], row["credential"], run_id, owner_user_id=owner_user_id)
        return self.detail(new_id)

    def _public_run(self, row, suite, detail=False):
        record = {k: row[k] for k in ("id", "suite_id", "rerun_of", "schedule_id", "scheduled_for", "base_url", "model", "effort", "created_at", "started_at", "finished_at", "status", "error")}
        results = json.loads(row["results"])
        counts = json.loads(suite["counts"])
        total = sum(counts.values())
        passed = sum(r.get("passed") is True for r in results)
        record.update(total=total, completed=len(results), passed=passed,
                      pass_rate=(passed / total * 100 if row["status"] == "completed" else None),
                      counts=counts, suite_day=suite["day"], suite_revision=suite["revision"], suite_fingerprint=suite["fingerprint"])
        record["categories"] = {category: {"total": counts[category], "passed": sum(r.get("passed") is True for r in results if r["category"] == category),
            "completed": sum(r["category"] == category for r in results)} for category in CATEGORIES}
        questions = {q["id"]: q for q in self.suite_public(dict(suite))["questions"] if q["category"] == "coding"}
        record["coding_difficulties"] = {level: {
            "total": sum(q["difficulty"] == level for q in questions.values()),
            "completed": sum(questions.get(r.get("id"), {}).get("difficulty") == level for r in results),
            "passed": sum(r.get("passed") is True and questions.get(r.get("id"), {}).get("difficulty") == level for r in results),
        } for level in CODING_DIFFICULTIES}
        record["api_errors"] = sum(r["status"] == "api_error" for r in results)
        record["grading_errors"] = sum(r["status"] == "grading_error" for r in results)
        record["usage"] = {key: sum(r.get("usage", {}).get(key, 0) or 0 for r in results) for key in ("input_tokens", "output_tokens", "total_tokens")}
        if row["started_at"]:
            end = datetime.fromisoformat(row["finished_at"]) if row["finished_at"] else datetime.now(timezone.utc)
            record["elapsed_seconds"] = max(0, round((end - datetime.fromisoformat(row["started_at"])).total_seconds(), 1))
        else:
            record["elapsed_seconds"] = 0
        if detail:
            record["results"] = sorted(results, key=lambda r: r["index"])
            record["suite"] = self.suite_public(dict(suite))
        return record

    def detail(self, run_id: str, owner_user_id=None) -> dict:
        with self.transaction() as db:
            row = self._owned_run(db, run_id, owner_user_id)
            suite = db.execute("SELECT * FROM suites WHERE id=?", (row["suite_id"],)).fetchone()
            return self._queue_position(db, self._public_run(row, suite, True))

    def list_runs(self, page=1, owner_user_id=None, *, users_only=False, model='') -> dict:
        if type(page) is not int or not 1 <= page <= 1000000:
            raise Problem("页码不正确")
        if not isinstance(model, str) or len(model) > 200 or any(ord(c) < 32 for c in model):
            raise Problem("模型搜索条件不正确")
        if owner_user_id is not None and (type(owner_user_id) is not int or not 0 < owner_user_id <= 9223372036854775807):
            raise Problem("用户 ID 不正确")
        with self.transaction() as db:
            conditions, params = [], []
            if users_only:
                conditions.append('owner_user_id IS NOT NULL')
            if owner_user_id is not None:
                conditions.append('owner_user_id=?')
                params.append(owner_user_id)
            if model.strip():
                # Literal substring matching: %, _ and quotes are ordinary data.
                conditions.append('instr(lower(model), lower(?)) > 0')
                params.append(model.strip())
            where = ' WHERE ' + ' AND '.join(conditions) if conditions else ''
            total = db.execute("SELECT COUNT(*) FROM runs" + where, params).fetchone()[0]
            page = min(page, max(1, (total + 19) // 20))
            rows = db.execute("SELECT * FROM runs" + where + " ORDER BY rowid DESC LIMIT 20 OFFSET ?", (*params, (page - 1) * 20)).fetchall()
            items = [self._queue_position(db, self._public_run(row, db.execute("SELECT * FROM suites WHERE id=?", (row["suite_id"],)).fetchone())) for row in rows]
            if users_only:
                for item, row in zip(items, rows):
                    item['owner_user_id'] = row['owner_user_id']
        return {"items": items, "total": total, "page": page, "page_size": 20}

    def prune_history(self):
        """Expire finished runs and their artifacts, never queued/running work."""
        cutoff = (self.clock().astimezone(timezone.utc) - timedelta(days=RETENTION_DAYS)).isoformat()
        today = self.clock().astimezone(self.timezone).date().isoformat()
        with self.transaction() as db:
            expired = "SELECT id FROM runs WHERE status IN ('completed','failed','cancelled','interrupted') AND finished_at < ?"
            db.execute("UPDATE schedules SET last_run_id=NULL WHERE last_run_id IN (" + expired + ")", (cutoff,))
            removed = db.execute("DELETE FROM runs WHERE id IN (" + expired + ")", (cutoff,)).rowcount
            db.execute("DELETE FROM suites WHERE day < ? AND NOT EXISTS (SELECT 1 FROM runs WHERE suite_id=suites.id)", (today,))
        # Also retry cleanup of orphan artifacts left by an interrupted cleanup.
        artifacts = self.directory / "artifacts"
        if artifacts.is_dir() and not artifacts.is_symlink():
            for path in artifacts.iterdir():
                if not re.fullmatch(r"[a-f0-9]{32}", path.name) or path.is_symlink() or not path.is_dir():
                    continue
                with self.transaction() as db:
                    exists = db.execute("SELECT 1 FROM runs WHERE id=?", (path.name,)).fetchone()
                    if not exists:
                        shutil.rmtree(path)
        return removed

    def recover(self):
        with self.transaction() as db:
            interrupted = db.execute("SELECT id FROM runs WHERE status IN ('running','cancelling')").fetchall()
            db.execute("UPDATE runs SET status='interrupted',finished_at=?,error=? WHERE status IN ('running','cancelling')",
                       (now(), "评测进程重启，任务已中断；可点击重新测"))
            for row in interrupted:
                self._resume_interval(db, row["id"])

    def _resume_interval(self, db, run_id):
        row = db.execute("""SELECT s.* FROM schedules s JOIN runs r ON r.schedule_id=s.id
                            WHERE r.id=? AND s.deleted=0 AND s.enabled=1 AND s.last_run_id=r.id""", (run_id,)).fetchone()
        if row is not None and interval_duration(row["cron_expression"]) is not None:
            _, upcoming = next_occurrence(row["cron_expression"], self.clock(), self.timezone)
            db.execute("UPDATE schedules SET next_run_at=? WHERE id=?", (upcoming, row["id"]))

    def _schedule_fields(self, data):
        name = data.get("name", "")
        enabled = data.get("enabled", True)
        public_results = data.get("public_results", False)
        if not isinstance(name, str) or not name.strip() or len(name) > 100 or any(ord(c) < 32 for c in name):
            raise Problem("任务名称应为 1 至 100 个字符")
        if type(enabled) is not bool:
            raise Problem("启用状态必须为布尔值")
        if type(public_results) is not bool:
            raise Problem("开放展示状态必须为布尔值")
        try:
            expression, upcoming = next_occurrence(data.get("cron_expression"), self.clock(), self.timezone)
        except ValueError as exc:
            raise Problem(str(exc)) from exc
        return name.strip(), expression, enabled, upcoming if enabled else None, public_results

    def _public_schedule(self, row):
        result = {key: row[key] for key in ("id", "name", "base_url", "model", "effort", "cron_expression", "created_at", "updated_at", "next_run_at", "last_attempt_at", "last_run_id", "last_error")}
        result.update(enabled=bool(row["enabled"]), public_results=bool(row["public_results"]), timezone=str(self.timezone))
        return result

    def list_schedules(self):
        with self.transaction() as db:
            rows = db.execute("SELECT * FROM schedules WHERE deleted=0 ORDER BY created_at DESC").fetchall()
            active = {row["schedule_id"]: row["id"] for row in db.execute(
                "SELECT id,schedule_id FROM runs WHERE schedule_id IS NOT NULL AND status IN ('queued','running','cancelling')")}
            return {"items": [{**self._public_schedule(row), "active_run_id": active.get(row["id"])} for row in rows]}

    def create_schedule(self, data):
        base_url, model, effort, credential = self.validate_target(data)
        name, expression, enabled, upcoming, public_results = self._schedule_fields(data)
        schedule_id, created = uuid.uuid4().hex, now()
        with self.transaction() as db:
            if db.execute("SELECT COUNT(*) FROM schedules WHERE deleted=0").fetchone()[0] >= 50:
                raise Problem("最多可创建 50 个定时任务", 409)
            db.execute("""INSERT INTO schedules(id,name,base_url,model,effort,credential,cron_expression,enabled,created_at,updated_at,next_run_at,public_results)
                          VALUES(?,?,?,?,?,?,?,?,?,?,?,?)""",
                       (schedule_id, name, base_url, model, effort, credential, expression, int(enabled), created, created, upcoming, int(public_results)))
            return self._public_schedule(db.execute("SELECT * FROM schedules WHERE id=?", (schedule_id,)).fetchone())

    def update_schedule(self, schedule_id, data):
        if not data or set(data) - {"name", "cron_expression", "enabled", "public_results"}:
            raise Problem("仅可修改任务名称、执行频率、启用和开放展示状态")
        with self.transaction() as db:
            row = db.execute("SELECT * FROM schedules WHERE id=? AND deleted=0", (schedule_id,)).fetchone()
            if row is None:
                raise Problem("定时任务不存在", 404)
            merged = {"name": row["name"], "cron_expression": row["cron_expression"], "enabled": bool(row["enabled"]), "public_results": bool(row["public_results"]), **data}
            name, expression, enabled, upcoming, public_results = self._schedule_fields(merged)
            if expression == row["cron_expression"] and enabled == bool(row["enabled"]):
                upcoming = row["next_run_at"]
            if enabled and interval_duration(expression) is not None and db.execute(
                    "SELECT id FROM runs WHERE schedule_id=? AND status IN ('queued','running','cancelling')", (schedule_id,)).fetchone():
                upcoming = None
            db.execute("UPDATE schedules SET name=?,cron_expression=?,enabled=?,next_run_at=?,updated_at=?,public_results=? WHERE id=?",
                       (name, expression, int(enabled), upcoming, now(), int(public_results), schedule_id))
            return self._public_schedule(db.execute("SELECT * FROM schedules WHERE id=?", (schedule_id,)).fetchone())

    def delete_schedule(self, schedule_id):
        with self.transaction() as db:
            result = db.execute("UPDATE schedules SET deleted=1,enabled=0,next_run_at=NULL,credential=X'',updated_at=? WHERE id=? AND deleted=0", (now(), schedule_id))
            if result.rowcount == 0:
                raise Problem("定时任务不存在", 404)
        return {"deleted": True}

    def run_schedule(self, schedule_id):
        # The same transaction lock protects manual triggers and scheduler ticks.
        with self.transaction() as db:
            row = db.execute("SELECT * FROM schedules WHERE id=? AND deleted=0", (schedule_id,)).fetchone()
            if row is None:
                raise Problem("定时任务不存在", 404)
            if db.execute("SELECT id FROM runs WHERE schedule_id=? AND status IN ('queued','running','cancelling')", (schedule_id,)).fetchone():
                raise Problem("该定时任务已有未结束的测试，请等待完成后再执行", 409)
            suite = self._suite(db)
            run_id = self._insert_run(db, suite["id"], row["base_url"], row["model"], row["effort"], row["credential"], schedule_id=schedule_id)
            upcoming = None if interval_duration(row["cron_expression"]) is not None else row["next_run_at"]
            db.execute("UPDATE schedules SET last_run_id=?,last_attempt_at=?,last_error='',next_run_at=? WHERE id=?",
                       (run_id, self.clock().astimezone(timezone.utc).isoformat(), upcoming, schedule_id))
        return self.detail(run_id)

    def enqueue_due_schedules(self):
        # Persist next occurrence and run together. Multiple scanners/restarts
        # cannot enqueue the same occurrence twice. At most one active run per plan.
        if self.resources and self.resources.snapshot(force=True)['blocked']:
            return 0
        current = self.clock().astimezone(timezone.utc)
        added = 0
        with self.transaction() as db:
            due = db.execute("SELECT * FROM schedules WHERE deleted=0 AND enabled=1 AND next_run_at<=? ORDER BY next_run_at,id", (current.isoformat(),)).fetchall()
            for row in due:
                try:
                    _, upcoming = next_occurrence(row["cron_expression"], current, self.timezone)
                except ValueError:
                    db.execute("UPDATE schedules SET enabled=0,next_run_at=NULL,last_error=? WHERE id=?", ("执行频率无效或已无后续执行时间，请编辑任务", row["id"]))
                    continue
                error = ""
                active = db.execute("SELECT id FROM runs WHERE schedule_id=? AND status IN ('queued','running','cancelling')", (row["id"],)).fetchone()
                if active:
                    error = "上次任务尚未结束，本次已跳过"
                if interval_duration(row["cron_expression"]) is not None:
                    upcoming = None  # The interval starts only after this run finishes.
                if not error:
                    suite = self._suite(db)
                    run_id = self._insert_run(db, suite["id"], row["base_url"], row["model"], row["effort"], row["credential"],
                                              schedule_id=row["id"], scheduled_for=row["next_run_at"])
                    db.execute("UPDATE schedules SET last_run_id=? WHERE id=?", (run_id, row["id"]))
                    added += 1
                # One catch-up run after downtime, never a burst for every missed interval.
                db.execute("UPDATE schedules SET next_run_at=?,last_attempt_at=?,last_error=? WHERE id=?", (upcoming, current.isoformat(), error, row["id"]))
        return added

    def claim(self):
        if self.resources and self.resources.snapshot(force=True)['blocked']:
            return None
        with self.transaction() as db:
            limit = db.execute("SELECT max_concurrency FROM config WHERE id=1").fetchone()[0]
            if db.execute("SELECT COUNT(*) FROM runs WHERE status IN ('running','cancelling')").fetchone()[0] >= limit:
                return None
            row = db.execute("SELECT * FROM runs WHERE status='queued' ORDER BY rowid LIMIT 1").fetchone()
            if row is None:
                return None
            db.execute("UPDATE runs SET status='running',started_at=? WHERE id=?", (now(), row["id"]))
            suite = db.execute("SELECT question_ids,bank_version FROM suites WHERE id=?", (row["suite_id"],)).fetchone()
            try:
                key = self.cipher.decrypt(row["credential"]).decode()
            except InvalidToken:
                db.execute("UPDATE runs SET status='failed',finished_at=?,error=? WHERE id=?",
                           (now(), "凭据无法解密，请新建测试并重新输入 API Key", row["id"]))
                self._resume_interval(db, row["id"])
                return None
            return {**dict(row), "question_ids": json.loads(suite[0]), "bank_version": suite[1], "api_key": key}

    def append(self, run_id: str, result: dict):
        with self.transaction() as db:
            row = db.execute("SELECT results FROM runs WHERE id=?", (run_id,)).fetchone()
            results = json.loads(row[0])
            results.append(result)
            db.execute("UPDATE runs SET results=? WHERE id=?", (json.dumps(results), run_id))

    def finish(self, run_id, status, error=""):
        with self.transaction() as db:
            current = db.execute("SELECT status FROM runs WHERE id=?", (run_id,)).fetchone()[0]
            if current not in ACTIVE:
                return
            if current in ("cancelling", "cancelled"):
                status, error = "cancelled", ""
            db.execute("UPDATE runs SET status=?,finished_at=?,error=? WHERE id=?", (status, now(), error, run_id))
            self._resume_interval(db, run_id)

    def cancelled(self, run_id):
        with self.transaction() as db:
            return db.execute("SELECT status FROM runs WHERE id=?", (run_id,)).fetchone()[0] in ("cancelling", "cancelled")

    def cancel(self, run_id, owner_user_id=None):
        with self.transaction() as db:
            row = self._owned_run(db, run_id, owner_user_id)
            if row["status"] == "queued":
                db.execute("UPDATE runs SET status='cancelled',finished_at=? WHERE id=?", (now(), run_id))
                self._resume_interval(db, run_id)
            elif row["status"] == "running":
                db.execute("UPDATE runs SET status='cancelling' WHERE id=?", (run_id,))
        return self.detail(run_id)

    def _shared_result(self, db, row):
        # Explicit whitelist: never publish targets, credentials, raw errors or answers.
        fields = ("id", "model", "effort", "status", "created_at", "finished_at", "total", "completed", "passed",
                  "pass_rate", "categories", "suite_day", "suite_revision", "suite_fingerprint", "elapsed_seconds",
                  "usage", "api_errors", "grading_errors")
        suite = db.execute("SELECT * FROM suites WHERE id=?", (row["suite_id"],)).fetchone()
        record = self._public_run(row, suite)
        return {key: record[key] for key in fields}

    def user_results(self, viewer_user_id=None, cursor=""):
        before = result_cursor(cursor, "users")
        with self.transaction() as db:
            query = "SELECT *,julianday(finished_at) AS sort_time FROM runs WHERE owner_user_id IS NOT NULL AND finished_at IS NOT NULL"
            params = ()
            if before:
                query += " AND (julianday(finished_at),id) < (?,?)"
                params = before[:2]
            rows = db.execute(query + " ORDER BY sort_time DESC,id DESC LIMIT ?", (*params, RESULT_PAGE_SIZE + 1)).fetchall()
            items = []
            for row in rows[:RESULT_PAGE_SIZE]:
                visible = viewer_user_id is None or row["owner_user_id"] == viewer_user_id
                result = {**self._shared_result(db, row), "base_url": row["base_url"] if visible else mask_result_url(row["base_url"]), "url_masked": not visible}
                items.append({"id": row["id"], "name": "", "model": row["model"], "effort": row["effort"], "results": [result]})
            last = rows[RESULT_PAGE_SIZE - 1] if len(rows) > RESULT_PAGE_SIZE else None
            return {"items": items, "next_cursor": encode_result_cursor((last["sort_time"], last["id"], ""), "users") if last else None}

    def public_results(self):
        # 对外订阅协议保留原来的全局条数限制，页面历史另行分页。
        with self.transaction() as db:
            rows = db.execute("""SELECT r.*,s.name AS schedule_name FROM runs r JOIN schedules s ON s.id=r.schedule_id
                WHERE s.public_results=1 AND s.deleted=0 AND r.finished_at IS NOT NULL
                ORDER BY julianday(r.finished_at) DESC,r.rowid DESC LIMIT 20""").fetchall()
            items = [{**self._shared_result(db, row), "base_url": row["base_url"], "schedule_id": row["schedule_id"], "schedule_name": row["schedule_name"]} for row in rows]
            return {"version": 2, "generated_at": now(), "limit": 20, "items": items}
