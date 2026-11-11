"""Idempotent, local-only preparation of the benchmark execution environment."""
from __future__ import annotations

import os
import signal
import subprocess
import sys
import threading
from datetime import datetime, timedelta, timezone
from pathlib import Path

from engine import Engine, Grader
from store import Bank, DEFAULT_COUNTS, Store
from transport import ResponsesClient


class Environment:
    def __init__(self, home, image, timezone_name, *, runner=None, activate=None, retry_seconds=30):
        self.home = Path(home)
        self.image = image
        self.timezone_name = timezone_name
        self.source = Path(__file__).resolve().parent
        self.stop_event = threading.Event()
        self.wake = threading.Event()
        self.lock = threading.RLock()
        self.store = self.engine = None
        self.bank_updates = None
        self.thread = None
        self.runner = runner or self.run
        self.activate = activate or self.start_engine
        self.retry_seconds = retry_seconds
        self.info = {"status": "preparing", "stage": "checking", "progress": 5, "attempt": 0,
                     "message": "正在检查评测环境，缺失组件将自动准备", "next_retry_at": None}

    def snapshot(self):
        with self.lock:
            return dict(self.info)

    def update(self, stage, progress, message, **extra):
        with self.lock:
            self.info.update(stage=stage, progress=progress, message=message, **extra)

    def start(self):
        with self.lock:
            if self.thread and self.thread.is_alive():
                return
            self.thread = threading.Thread(target=self.loop, name="benchmark-environment", daemon=True)
            self.thread.start()

    def retry(self):
        with self.lock:
            if self.info["status"] == "failed":
                self.wake.set()
        return self.snapshot()

    def run(self, args, *, timeout=30, cwd=None):
        # Only server-owned argument arrays are accepted; no shell or browser commands.
        process = subprocess.Popen(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                   start_new_session=True)
        import time
        deadline = time.monotonic() + timeout
        try:
            while True:
                try:
                    output, _ = process.communicate(timeout=min(1, max(.01, deadline-time.monotonic())))
                    if process.returncode:
                        raise RuntimeError("Environment command failed")
                    return output.decode(errors="replace").strip()
                except subprocess.TimeoutExpired:
                    if self.stop_event.is_set() or time.monotonic() >= deadline:
                        raise RuntimeError("Environment command interrupted")
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()

    def prepare(self):
        self.update("docker", 10, "正在检查 Docker 及执行权限")
        self.runner(["docker", "info", "--format", "{{.ServerVersion}}"], timeout=20)
        self.update("questions", 35, "正在下载固定版本题库并准备评分源码，首次准备可能需要数分钟")
        # prepare.py preserves existing banks and verifies/restores absent source directories.
        self.runner([sys.executable, str(self.source / "prepare.py"), "--home", str(self.home)], timeout=1800)
        self.update("image", 70, "正在检查并构建隔离评分镜像")
        try:
            digest = self.runner(["docker", "image", "inspect", self.image, "--format", "{{.Id}}"], timeout=30)
        except Exception:
            if self.stop_event.is_set():
                raise
            self.runner(["docker", "build", "--quiet", "-f", str(self.source / "Dockerfile.grader"), "-t", self.image, str(self.source)], timeout=1800)
            digest = self.runner(["docker", "image", "inspect", self.image, "--format", "{{.Id}}"], timeout=30)
        self.update("verifying", 90, "正在验证题库和评分器，完成后自动开放测试")
        if self.stop_event.is_set():
            raise RuntimeError("Environment stopped")
        self.activate(digest)
        self.update("ready", 100, "评测环境已就绪", status="ready", next_retry_at=None)

    def start_engine(self, digest):
        grader = Grader(self.home, digest)
        grader.check_ready()
        grader.check_sandbox()
        if self.engine is None:
            grader.cleanup_stale()
            bank = Bank(self.home / "bank.sqlite3")
            store = Store(self.home / "data", bank, self.timezone_name)
            client = ResponsesClient(os.getenv("MODEL_BENCHMARK_ALLOW_PRIVATE_TARGETS", "false").lower() == "true")
            engine = Engine(store, client, grader)
            from bank_updates import BankUpdates
            updates = BankUpdates(self.home, store, self.run, grader)
            engine.start()
            with self.lock:
                self.store, self.engine = store, engine
                self.bank_updates = updates
        else:
            # Existing tasks finish with their original immutable grader digest.
            while self.store.execution_settings()["running"]:
                if self.stop_event.wait(1):
                    raise RuntimeError("Environment stopped")
            self.engine.grader.image = digest
            self.engine.available.set()
            self.engine.wake.set()

    def loop(self):
        failures = 0
        while not self.stop_event.is_set():
            with self.lock:
                self.info.update(status="preparing", next_retry_at=None, attempt=self.info["attempt"]+1)
            if self.engine:
                self.engine.available.clear()
            try:
                self.prepare()
                failures = 0
                while not self.stop_event.wait(30):
                    self.engine.grader.check_ready()
                break
            except Exception:
                if self.stop_event.is_set():
                    break
                failures += 1
                delay = min(self.retry_seconds * 2 ** min(failures-1, 4), 300)
                stage = self.snapshot()["stage"]
                messages = {
                    "docker": "Docker 不可用或缺少执行权限，请检查部署服务及 Docker socket；稍后自动重试",
                    "questions": "题库或评分源码准备失败，请检查网络及磁盘空间；稍后自动重试",
                    "image": "评分镜像准备失败，请检查镜像仓库连接及磁盘空间；稍后自动重试",
                    "verifying": "评分环境验证失败，请检查依赖与目录权限；稍后自动重试",
                    "ready": "评分环境暂时不可用，正在自动恢复",
                }
                self.update(stage, self.snapshot()["progress"], messages.get(stage, "环境准备失败，稍后自动重试"),
                            status="failed", next_retry_at=(datetime.now(timezone.utc)+timedelta(seconds=delay)).isoformat())
                if self.engine:
                    self.engine.available.clear()
                self.wake.wait(delay)
                self.wake.clear()

    def close(self):
        self.stop_event.set()
        self.wake.set()
        if self.engine:
            self.engine.stopping.set()
            self.engine.wake.set()
        if self.thread:
            self.thread.join(5)
        if self.bank_updates and self.bank_updates.thread:
            self.bank_updates.thread.join(5)

    def pending_state(self):
        return {"ready": False, "message": self.snapshot()["message"], "environment": self.snapshot(),
                "counts": dict(DEFAULT_COUNTS), "capacities": {k: 0 for k in DEFAULT_COUNTS},
                "timezone": self.timezone_name, "suite": None, "active_run": None, "quota": None}
