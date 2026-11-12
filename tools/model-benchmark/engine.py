from __future__ import annotations

import concurrent.futures
import hashlib
import json
import os
import re
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path

from store import MAX_CONCURRENCY, Store
from displays import Displays
from transport import RequestFailure, ResponsesClient

INSTRUCTIONS = "You are a helpful assistant. Solve the task independently and follow the requested answer format."
RETRYABLE = (429, 500, 502, 503, 504)


class Grader:
    def __init__(self, home: Path, image: str):
        self.home, self.image = home, image
        self.owner = hashlib.sha256(str(home.resolve()).encode()).hexdigest()[:24]
        sys.path.insert(0, str(home / "sources/LiveBench"))

    def cleanup_stale(self):
        # A killed worker cannot enforce its subprocess timeout. Reclaim only
        # containers created for this exact worker directory before accepting jobs.
        containers = subprocess.check_output([
            "docker", "ps", "-aq", "--filter", f"label=io.sub2api.benchmark.owner={self.owner}"
        ], text=True, timeout=15).split()
        if containers:
            subprocess.run(["docker", "rm", "-f", *containers], check=True, capture_output=True, timeout=30)

    def check_ready(self):
        subprocess.run(["docker", "image", "inspect", self.image], check=True, capture_output=True, timeout=15)
        if not (self.home / "sources/LiveCodeBench/lcb_runner/evaluation/testing_util.py").is_file():
            raise RuntimeError("Benchmark grader sources are missing")
        from livebench.process_results.math.math_competitions.utils import aime_process_results
        from livebench.process_results.reasoning.zebra_puzzle.utils import get_zebra_puzzle_evaluator
        from livebench.process_results.reasoning.spatial.utils import spatial_process_results

    def grade(self, question: dict, answer: str) -> dict:
        answer = re.sub(r"<think>.*?</think>", "", answer, flags=re.DOTALL)
        if question["category"] != "coding":
            return self.grade_text(question, answer)
        # Same fenced-code extraction as the pinned official LiveCodeBench tool.
        lines = answer.split("\n")
        fences = [index for index, line in enumerate(lines) if "```" in line]
        code = "\n".join(lines[fences[-2] + 1:fences[-1]]) if len(fences) >= 2 else ""
        if not code.strip():
            return {"passed": False, "score": 0, "status": "failed", "message": "回答中未找到要求的代码块"}
        name = "sub2api-benchmark-" + uuid.uuid4().hex
        command = ["docker", "run", "--rm", "-i", "--name", name,
                   "--label", f"io.sub2api.benchmark.owner={self.owner}",
                   "--log-driver", "none",
                   "--network", "none", "--read-only", "--user", "65534:65534",
                   "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
                   "--pids-limit", "128", "--cpus", "1", "--memory", "2g", "--memory-swap", "2g",
                   "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m",
                   "-v", str(self.home / "sources/LiveCodeBench") + ":/lcb:ro", self.image]
        payload = {"sample": question["evaluation_sample"], "code": code, "float_tolerance": question.get("float_tolerance")}
        try:
            result = subprocess.run(command, input=json.dumps(payload), capture_output=True, text=True, timeout=600)
        except subprocess.TimeoutExpired:
            subprocess.run(["docker", "rm", "-f", name], capture_output=True, timeout=30)
            return {"passed": False, "score": 0, "status": "grading_error", "message": "评分容器超过总时限"}
        markers = [line.partition("=")[2] for line in result.stdout.splitlines() if line.startswith("BENCHMARK_RESULT=")]
        if result.returncode or not markers:
            return {"passed": False, "score": 0, "status": "grading_error", "message": "评分容器执行失败"}
        data = json.loads(markers[-1])
        if data.get("infrastructure_error"):
            return {"passed": False, "score": 0, "status": "grading_error", "message": "评分器执行异常"}
        error_code = data.get("metadata", {}).get("error_code")
        message = { -2: "输出与预期不符", -3: "代码执行超时", -4: "代码运行异常", -1: "代码编译失败" }.get(error_code, "测试未通过")
        return {"passed": data["passed"], "score": int(data["passed"]),
                "status": "passed" if data["passed"] else "failed", "message": "全部用例通过" if data["passed"] else message,
                "tests_passed": sum(type(value) in (bool, int) and value == 1 for value in data["tests"]), "test_count": data["test_count"]}

    def check_sandbox(self):
        question = {"category": "coding", "evaluation_sample": {
            "input_output": json.dumps({"inputs": ["2 3\n"], "outputs": ["5\n"], "fn_name": None})}}
        result = self.grade(question, "```python\na, b = map(int, input().split())\nprint(a + b)\n```")
        if not result.get("passed"):
            raise RuntimeError("Sandbox execution check failed")

    @staticmethod
    def grade_text(question, answer):
        from livebench.process_results.math.math_competitions.utils import aime_process_results
        from livebench.process_results.reasoning.zebra_puzzle.utils import get_zebra_puzzle_evaluator
        from livebench.process_results.reasoning.spatial.utils import spatial_process_results
        if question["category"] == "math":
            score = aime_process_results(question["ground_truth"], answer)
        elif question["task"] == "zebra_puzzle":
            score = get_zebra_puzzle_evaluator(question["release"])(question["ground_truth"], answer)
        elif question["task"] == "spatial":
            score = spatial_process_results(question["ground_truth"], answer)
        else:
            raise ValueError("Unsupported question scorer")
        return {"passed": score == 1, "score": float(score), "status": "passed" if score == 1 else "failed",
                "message": "答案正确" if score == 1 else "答案未完全正确"}


class Engine:
    def __init__(self, store: Store, client: ResponsesClient, grader: Grader):
        self.store, self.client, self.grader = store, client, grader
        self.stopping = threading.Event()
        self.available = threading.Event()
        self.available.set()
        self.wake = threading.Event()
        self.displays = Displays(store)
        self.threads = [threading.Thread(target=self.loop, name="benchmark-queue", daemon=True)]
        self.scheduler = threading.Thread(target=self.schedule_loop, name="benchmark-schedules", daemon=True)
        self.display_thread = threading.Thread(target=self.display_loop, name="benchmark-displays", daemon=True)

    def start(self):
        self.store.prune_history()
        self.store.recover()
        self.scheduler.start()
        self.display_thread.start()
        for thread in self.threads:
            thread.start()

    def schedule_loop(self):
        next_cleanup = time.monotonic() + 3600
        while not self.stopping.is_set():
            if time.monotonic() >= next_cleanup:
                try:
                    self.store.prune_history()
                    next_cleanup = time.monotonic() + 3600
                except Exception:
                    next_cleanup = time.monotonic() + 60
            try:
                if self.store.enqueue_due_schedules():
                    self.wake.set()
            except Exception:
                # Retry next tick; failed transactions leave schedules due.
                pass
            self.stopping.wait(5)

    def display_loop(self):
        # Independent of task execution: a slow source never delays cron or tests.
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            pending = set()
            while not self.stopping.is_set():
                pending = {future for future in pending if not future.done()}
                try:
                    while len(pending) < 2:
                        item = self.displays.claim_refresh()
                        if item is None:
                            break
                        pending.add(pool.submit(self.displays.fetch, item))
                except Exception:
                    pass  # A database failure must not kill future refreshes.
                self.stopping.wait(2)

    def loop(self):
        # Threads are allocated on demand. SQLite owns the live site-wide limit,
        # including admin, user and scheduled jobs. Lowering it drains active work.
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_CONCURRENCY) as pool:
            self.dispatch(pool)

    def dispatch(self, pool):
        while not self.stopping.is_set():
            if not self.available.is_set():
                self.stopping.wait(1)
                continue
            try:
                run = self.store.claim()
            except Exception:
                self.stopping.wait(5)
                continue
            if run is None:
                self.wake.wait(2)
                self.wake.clear()
                continue
            pool.submit(self.execute_claimed, run)

    def execute_claimed(self, run):
        try:
            self.execute(run)
        except Exception:
            # Do not log exception values: client errors may contain keys.
            self.store.finish(run["id"], "failed", "评测任务异常结束，请检查评测服务状态")
        finally:
            run.pop("api_key", None)
            self.wake.set()

    def wait_for_resources(self, run_id):
        while self.store.resources and self.store.resources.snapshot(force=True)['blocked']:
            if self.store.cancelled(run_id) or self.stopping.wait(2):
                return False
        return not self.store.cancelled(run_id) and not self.stopping.is_set()

    def execute(self, run):
        self.grader.check_ready()
        results = []
        # One question per run keeps request/grader concurrency within the same
        # site-wide task limit across manual and scheduled work.
        fatal = False
        for index, question_id in enumerate(run["question_ids"], 1):
            # Keep the run and completed answers; no new request/grade under pressure.
            if not self.wait_for_resources(run['id']):
                break
            result = self.one(run, index, question_id)
            self.store.append(run["id"], result)
            results.append(result)
            if result.get("fatal"):
                fatal = True
                break
        if self.store.cancelled(run["id"]) or self.stopping.is_set():
            self.store.finish(run["id"], "cancelled")
        elif fatal:
            self.store.finish(run["id"], "failed", results[-1]["message"] + "；已停止后续题目，请检查密钥、模型及接口兼容性")
        elif any(result["status"] in ("grading_error", "api_error", "response_incomplete") for result in results):
            issues = [label for status, label in (("api_error", "接口错误"), ("grading_error", "评分异常"),
                                                  ("response_incomplete", "响应未完成"))
                      if any(result["status"] == status for result in results)]
            self.store.finish(run["id"], "failed", "、".join(issues) + "，本次不生成最终通过率")
        else:
            self.store.finish(run["id"], "completed")

    def one(self, run, index, question_id):
        started = time.monotonic()
        question = self.store.bank_for_version(run.get("bank_version", self.store.bank.manifest["version"])).get(question_id)
        result = {"index": index, "id": question_id, "category": question["category"], "title": question["title"],
                  "difficulty": question.get("difficulty", ""), "passed": False, "score": 0, "attempts": 0}
        payload = {"model": run["model"], "instructions": INSTRUCTIONS,
                   "input": [{"role": "user", "content": question["prompt"]}],
                   "reasoning": {"effort": run["effort"]}, "store": False}
        data = None
        for attempt in range(3):
            if not self.wait_for_resources(run['id']):
                result.update(status='api_error', message='测试已停止')
                break
            result["attempts"] += 1
            try:
                data = self.client.generate(run["base_url"], run["api_key"], payload)
                break
            except RequestFailure as exc:
                if attempt < 2 and (exc.status in RETRYABLE or exc.status == 0) and not self.store.cancelled(run["id"]):
                    time.sleep(2 ** (attempt + 1))
                    continue
                result.update(status="api_error", message=str(exc), fatal=exc.status in (400, 401, 403, 404, 405))
                break
        if data is not None:
            # A response may arrive after resource pressure changed. Keep it in
            # memory until it is safe to persist and launch a grading container.
            if not self.wait_for_resources(run['id']):
                result.update(status='api_error', message='测试已停止', elapsed_seconds=round(time.monotonic() - started, 2))
                return result
            # Persist the sanitized response for reproducible audits; no headers
            # or credentials are stored with these artifacts.
            artifacts = self.store.directory / "artifacts" / run["id"]
            artifacts.mkdir(parents=True, exist_ok=True, mode=0o700)
            (artifacts / f"{index:03d}.json").write_text(json.dumps({"request": payload, "response": data}, ensure_ascii=False), encoding="utf-8")
            malformed_output = False
            try:
                answer = "\n".join(content.get("text", "") for item in data.get("output", []) if item.get("type") == "message"
                                   for content in item.get("content", []) if content.get("type") == "output_text")
            except (TypeError, AttributeError):
                answer = ""
                malformed_output = True
            usage = data.get("usage") or {}
            if not isinstance(usage, dict):
                usage = {}
            result.update(answer=answer[:12000], answer_truncated=len(answer) > 12000,
                          returned_model=str(data.get("model", ""))[:200],
                          usage={k: usage[k] if type(usage.get(k)) is int and usage[k] >= 0 else 0
                                 for k in ("input_tokens", "output_tokens", "total_tokens")})
            if data.get("status") == "incomplete":
                details = data.get("incomplete_details")
                reason = details.get("reason") if isinstance(details, dict) else None
                messages = {"max_output_tokens": "上游输出额度耗尽，响应被截断，未获得完整答案",
                            "content_filter": "上游内容过滤导致响应未完成"}
                reason = reason if isinstance(reason, str) and reason in messages else "unknown"
                result.update(status="response_incomplete", incomplete_reason=reason,
                              message=messages.get(reason, "上游标记响应未完成，未获得完整答案"))
            elif data.get("status") != "completed" or malformed_output:
                result.update(status="api_error", message="模型响应状态异常或响应格式不正确")
            elif not answer.strip():
                result.update(status="response_incomplete", incomplete_reason="no_answer",
                              message="上游未返回可评分的最终答案")
            else:
                try:
                    if self.wait_for_resources(run['id']):
                        result.update(self.grader.grade(question, answer))
                    else:
                        result.update(status='api_error', message='测试已停止')
                except Exception:
                    result.update(status="grading_error", message="评分器执行异常")
        result["elapsed_seconds"] = round(time.monotonic() - started, 2)
        return result
