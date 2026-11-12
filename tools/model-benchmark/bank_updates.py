"""Administrator-only staging and atomic activation of immutable question banks."""
from __future__ import annotations

import json
import sys
import threading
from contextlib import closing
from pathlib import Path

from store import Problem, now


def changes(previous, candidate):
    def rows(bank):
        with closing(bank.connect()) as db:
            return {r["id"]: r["payload"] for r in db.execute("SELECT id,payload FROM questions")}
    old, new = rows(previous), rows(candidate)
    return {"added": len(new.keys() - old.keys()), "removed": len(old.keys() - new.keys()),
            "changed": sum(old[key] != new[key] for key in old.keys() & new.keys())}


class BankUpdates:
    def __init__(self, home, store, runner, grader):
        self.home, self.store, self.runner, self.grader = Path(home), store, runner, grader
        self.lock = threading.RLock()
        self.thread = None
        self.path = store.directory / "bank-update.json"
        self.info = {"status": "idle", "message": "", "checked_at": None, "candidate": None}
        if self.path.exists():
            try:
                self.info.update(json.loads(self.path.read_text()))
                if self.info["status"] == "checking":
                    self.info.update(status="failed", message="上次更新已中断，请重新检查", candidate=None)
            except (ValueError, OSError):
                pass

    def snapshot(self):
        with self.lock:
            return json.loads(json.dumps(self.info))

    def save(self, **values):
        with self.lock:
            self.info.update(values)
            temp = self.path.with_suffix(".installing")
            temp.write_text(json.dumps(self.info, ensure_ascii=False))
            temp.chmod(0o600)
            temp.replace(self.path)

    def start(self):
        with self.lock:
            if self.thread and self.thread.is_alive():
                raise Problem("总题库正在更新，请等待完成", 409)
            if self.store.resources:
                from resources import ResourcePressure
                try:
                    self.store.resources.require_capacity(update=True)
                except ResourcePressure as exc:
                    raise Problem(str(exc), 503) from exc
            self.save(status="checking", message="正在检查官方数据、下载并验证题目", candidate=None)
            self.thread = threading.Thread(target=self.prepare, name="benchmark-bank-update", daemon=True)
            self.thread.start()
            return self.snapshot()

    def prepare(self):
        try:
            output = self.runner([sys.executable, str(Path(__file__).with_name("update_bank.py")), "--home", str(self.home)], timeout=1800)
            version = json.loads(output.splitlines()[-1])["version"]
            candidate = self.store.bank_for_version(version)
            self.grader.check_sandbox()
            # Validate supported text scorers against each answer format.
            for item in candidate.catalog:
                if item["category"] == "coding":
                    continue
                question = candidate.get(item["id"])
                truth = str(question["ground_truth"])
                answer = "\\boxed{" + truth + "}" if item["category"] == "math" else truth
                if not self.grader.grade_text(question, answer)["passed"]:
                    raise ValueError("Text scorer is incompatible with candidate bank")
            delta = changes(self.store.bank, candidate)
            updated = any(delta.values())
            self.save(status="ready" if updated else "current", checked_at=now(),
                      message="新题库已验证，可启用" if updated else "当前已是最新可用题库，没有新增或变更题目",
                      candidate={"version": version, "counts": candidate.sizes(), "coding_counts": candidate.coding_sizes(),
                                 "skipped": sum(candidate.manifest.get("skipped", {}).values()), **delta} if updated else None)
        except Exception:
            self.save(status="failed", checked_at=now(), candidate=None, message="总题库更新失败，请检查网络或数据兼容性后重试；现有题库继续使用")

    def activate(self, version):
        with self.lock:
            candidate = self.info.get("candidate")
            if self.info["status"] != "ready" or not candidate or version != candidate["version"]:
                raise Problem("待启用题库已变化，请重新检查", 409)
            suite = self.store.activate_bank(version)
            self.save(status="activated", message="新题库已启用，今日题目已生成新版本；在途测试保留原题", candidate=None)
            return suite
