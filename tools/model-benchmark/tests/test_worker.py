import concurrent.futures
import gc
from datetime import datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import sqlite3
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch
import urllib.error
import urllib.request
from zoneinfo import ZoneInfo
import zlib

from engine import Engine, Grader
from prepare import float_tolerance
from server import make_handler
from store import Bank, Problem, Store
from transport import RequestFailure, ResponsesClient, normalize_url, public_addresses


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        # sqlite connection contexts commit but do not close; collect fixture
        # connections before removing temporary files (required on Windows).
        self.addCleanup(gc.collect)
        self.root = Path(self.temp.name)
        bank_path = self.root / "bank.sqlite3"
        with sqlite3.connect(bank_path) as db:
            db.executescript("CREATE TABLE questions(id TEXT PRIMARY KEY,category TEXT,difficulty TEXT,title TEXT,payload BLOB); CREATE TABLE metadata(key TEXT,value TEXT);")
            db.execute("INSERT INTO metadata VALUES('manifest',?)", (json.dumps({"version": "fixture-v1"}),))
            for category, size in (("coding", 30), ("math", 10), ("reasoning", 10)):
                for i in range(size):
                    q = {"id": f"{category}:{i}", "category": category, "difficulty": "medium" if i % 2 else "hard",
                         "title": "Test question", "prompt": "Return 42"}
                    db.execute("INSERT INTO questions VALUES(?,?,?,?,?)", (q["id"], category, q["difficulty"], q["title"], zlib.compress(json.dumps(q).encode())))
        self.date = datetime(2026, 11, 11, 23, 59, tzinfo=ZoneInfo("Asia/Shanghai"))
        self.store = Store(self.root / "data", Bank(bank_path), clock=lambda: self.date)

    def create(self, **overrides):
        data = {"suite_id": self.store.state()["suite"]["id"], "base_url": "https://example.com/v1",
                "api_key": "test-secret-DO-NOT-EXPOSE", "model": "test-model"}
        data.update(overrides)
        return self.store.create(data)


class StoreTests(Fixture):
    def test_daily_suite_concurrency_restart_and_rollover(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            suites = list(pool.map(lambda _: self.store.state()["suite"], range(20)))
        self.assertEqual(len({s["id"] for s in suites}), 1)
        self.assertEqual(suites[0]["total"], 30)
        self.assertEqual(len({q["id"] for q in suites[0]["questions"]}), 30)
        coding = [q for q in suites[0]["questions"] if q["category"] == "coding"]
        self.assertEqual(sum(q["difficulty"] == "medium" for q in coding), 10)
        reopened = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(reopened.state()["suite"], suites[0])
        self.date += timedelta(minutes=2)
        self.assertNotEqual(self.store.state()["suite"]["id"], suites[0]["id"])
        with self.assertRaises(Problem) as error:
            self.create(suite_id=suites[0]["id"])
        self.assertEqual(error.exception.status, 409)

    def test_retest_uses_latest_refreshed_suite_and_preserves_settings(self):
        original = self.create(effort="high")
        self.store.finish(original["id"], "completed")
        original = self.store.detail(original["id"])
        self.store.refresh({"coding": 2, "math": 0, "reasoning": 1}, original["suite_id"])
        latest = self.store.refresh()
        rerun = self.store.rerun(original["id"])
        for key in ("model", "effort", "base_url"):
            self.assertEqual(rerun[key], original[key])
        self.assertEqual(rerun["suite"], latest)
        self.assertNotEqual(rerun["suite_id"], original["suite_id"])
        self.assertEqual(rerun["counts"], latest["counts"])
        self.assertEqual(rerun["total"], 3)
        self.assertEqual(rerun["rerun_of"], original["id"])
        self.assertEqual(self.store.detail(original["id"]), original)
        self.assertEqual(self.store.state()["total"], 3)
        with self.assertRaises(Problem):
            self.store.rerun(original["id"])
        with self.assertRaises(Problem):
            self.store.refresh(expected_suite=original["suite_id"])
        claimed = self.store.claim()
        self.assertEqual(claimed["api_key"], "test-secret-DO-NOT-EXPOSE")
        self.assertEqual(claimed["question_ids"], [q["id"] for q in latest["questions"]])

    def test_retest_creates_current_daily_suite_after_midnight(self):
        original = self.create()
        self.store.finish(original["id"], "completed")
        self.date += timedelta(minutes=2)
        # No state request has created today's suite yet.
        rerun = self.store.rerun(original["id"])
        self.assertEqual(rerun["suite_day"], "2026-11-12")
        self.assertEqual(rerun["suite_revision"], 1)
        self.assertNotEqual(rerun["suite_id"], original["suite_id"])
        self.assertEqual(rerun["suite"], self.store.state()["suite"])
        self.assertEqual(self.store.detail(original["id"])["suite_day"], "2026-11-11")
        # A later refresh cannot silently change an already queued run's questions.
        self.store.refresh()
        self.assertEqual(self.store.detail(rerun["id"])["suite"], rerun["suite"])

    def test_retest_does_not_refresh_an_unchanged_daily_suite(self):
        original = self.create()
        self.store.finish(original["id"], "completed")
        rerun = self.store.rerun(original["id"])
        self.assertEqual(rerun["suite"], original["suite"])
        self.assertEqual(self.store.state()["suite"]["revision"], 1)

    def test_encryption_public_records_and_restart_recovery(self):
        queued = self.create()
        second = self.create()
        claimed = self.store.claim()
        self.assertEqual(claimed["api_key"], "test-secret-DO-NOT-EXPOSE")
        with sqlite3.connect(self.store.path) as db:
            encrypted = db.execute("SELECT credential FROM runs LIMIT 1").fetchone()[0]
        self.assertNotIn(b"test-secret", encrypted)
        self.assertNotIn("test-secret", json.dumps(self.store.list_runs()))
        self.assertNotIn("credential", json.dumps(self.store.detail(queued["id"])))
        self.store.recover()
        self.assertEqual(self.store.detail(queued["id"])["status"], "interrupted")
        self.assertEqual(self.store.detail(second["id"])["status"], "queued")
        self.assertIsNone(self.store.detail(queued["id"])["pass_rate"])

    def test_queue_limit_and_cancellation_wins_completion_race(self):
        run = self.create()
        self.store.claim()
        self.store.cancel(run["id"])
        self.store.finish(run["id"], "completed")
        self.assertEqual(self.store.detail(run["id"])["status"], "cancelled")
        for _ in range(12):
            self.create()
        self.assertEqual(self.store.list_runs()["total"], 13)

    def test_counts_validation(self):
        for counts in (None, {}, {"coding": True, "math": 5, "reasoning": 5},
                       {"coding": -1, "math": 5, "reasoning": 5}, {"coding": 31, "math": 0, "reasoning": 0},
                       {"coding": 0, "math": 0, "reasoning": 0}):
            with self.subTest(counts=counts), self.assertRaises(Problem):
                self.store.refresh(counts=counts) if counts is not None else self.store.validate_counts(counts)


class FakeGrader:
    def check_ready(self):
        pass

    def grade(self, question, answer):
        passed = answer == "42"
        return {"passed": passed, "score": int(passed), "status": "passed" if passed else "failed", "message": "graded"}


class EngineTests(Fixture):
    def test_full_run_scores_errors_usage_and_artifacts(self):
        self.store.refresh({"coding": 1, "math": 1, "reasoning": 1})
        class Client:
            def generate(_, url, key, payload):
                self.assertEqual(payload["reasoning"], {"effort": "medium"})
                for field in ("max_output_tokens", "max_tokens", "max_completion_tokens"):
                    self.assertNotIn(field, payload)
                return {"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": "42"}]}],
                        "usage": {"input_tokens": "oops", "output_tokens": -1, "total_tokens": 12}}
        run = self.create()
        Engine(self.store, Client(), FakeGrader()).execute(self.store.claim())
        detail = self.store.detail(run["id"])
        self.assertEqual(detail["status"], "completed")
        self.assertEqual(detail["pass_rate"], 100)
        self.assertEqual(detail["usage"], {"input_tokens": 0, "output_tokens": 0, "total_tokens": 36})
        artifacts = list((self.store.directory / "artifacts" / run["id"]).glob("*.json"))
        self.assertEqual(len(artifacts), 3)
        self.assertNotIn("test-secret", artifacts[0].read_text())

    def test_api_failure_is_not_a_model_failure_and_stops_scheduling(self):
        class Client:
            def generate(_, *args):
                raise RequestFailure("API returned HTTP 401", 401)
        run = self.create()
        Engine(self.store, Client(), FakeGrader()).execute(self.store.claim())
        detail = self.store.detail(run["id"])
        self.assertEqual(detail["status"], "failed")
        self.assertIsNone(detail["pass_rate"])
        self.assertLessEqual(detail["completed"], 2)
        self.assertGreater(detail["api_errors"], 0)
        self.assertTrue(all(r["attempts"] == 1 for r in detail["results"]))

    def test_malformed_output_is_api_error(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        class Client:
            def generate(_, *args):
                return {"status": "completed", "output": ["bad schema"]}
        run = self.create()
        Engine(self.store, Client(), FakeGrader()).execute(self.store.claim())
        detail = self.store.detail(run["id"])
        self.assertEqual(detail["api_errors"], 1)
        self.assertIsNone(detail["pass_rate"])

    def test_incomplete_responses_are_not_api_errors_or_wrong_answers(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        answer = [{"type": "message", "content": [{"type": "output_text", "text": "42"}]}]
        cases = [
            ({"status": "incomplete", "incomplete_details": {"reason": "max_output_tokens"}, "output": []}, "max_output_tokens", "额度耗尽"),
            ({"status": "incomplete", "incomplete_details": {"reason": "max_output_tokens"}, "output": answer}, "max_output_tokens", "被截断"),
            ({"status": "incomplete", "incomplete_details": {"reason": "content_filter"}}, "content_filter", "内容过滤"),
            ({"status": "incomplete", "incomplete_details": {"reason": "private-upstream-detail"}}, "unknown", "未完成"),
            ({"status": "incomplete", "incomplete_details": {"reason": []}}, "unknown", "未完成"),
            ({"status": "incomplete", "incomplete_details": None}, "unknown", "未完成"),
            ({"status": "completed", "output": []}, "no_answer", "最终答案"),
        ]
        for response, reason, message in cases:
            with self.subTest(reason=reason, response=response):
                response["usage"] = {"input_tokens": 20, "output_tokens": 16384, "total_tokens": 16404}
                client = Mock(generate=Mock(return_value=response))
                grader = Mock()
                run = self.create()
                Engine(self.store, client, grader).execute(self.store.claim())
                detail = self.store.detail(run["id"])
                result = detail["results"][0]
                self.assertEqual(detail["status"], "failed")
                self.assertIsNone(detail["pass_rate"])
                self.assertEqual(detail["api_errors"], 0)
                self.assertEqual(detail["grading_errors"], 0)
                self.assertEqual(detail["incomplete_responses"], 1)
                self.assertEqual(detail["error"], "响应未完成，本次不生成最终通过率")
                self.assertEqual(detail["usage"]["output_tokens"], 16384)
                self.assertEqual(result["status"], "response_incomplete")
                self.assertEqual(result["incomplete_reason"], reason)
                self.assertIn(message, result["message"])
                self.assertNotIn("private-upstream-detail", json.dumps(detail))
                client.generate.assert_called_once()
                grader.grade.assert_not_called()

    def test_long_completed_answer_is_graded_in_full_and_run_continues_after_truncation(self):
        self.store.refresh({"coding": 0, "math": 2, "reasoning": 0})
        answer = "42" * 20000
        client = Mock(generate=Mock(side_effect=[
            {"status": "incomplete", "incomplete_details": {"reason": "max_output_tokens"}},
            {"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": answer}]}],
             "usage": {"output_tokens": 20000}},
        ]))
        grader = Mock(grade=Mock(return_value={"status": "passed", "passed": True, "score": 1}))
        run = self.create()
        Engine(self.store, client, grader).execute(self.store.claim())
        detail = self.store.detail(run["id"])
        self.assertEqual(detail["completed"], 2)
        self.assertEqual(detail["passed"], 1)
        self.assertEqual(detail["api_errors"], 0)
        self.assertEqual(detail["incomplete_responses"], 1)
        self.assertIsNone(detail["pass_rate"])
        grader.grade.assert_called_once()
        self.assertEqual(grader.grade.call_args.args[1], answer)
        self.assertEqual(detail["usage"]["output_tokens"], 20000)
        artifact = json.loads((self.store.directory / "artifacts" / run["id"] / "002.json").read_text())
        self.assertNotIn("max_output_tokens", artifact["request"])
        self.assertEqual(artifact["response"]["output"][0]["content"][0]["text"], answer)

    def test_transient_error_retries_but_does_not_retry_wrong_answers(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        class Client:
            calls = 0
            def generate(self, *args):
                self.calls += 1
                if self.calls == 1:
                    raise RequestFailure("rate limited", 429)
                return {"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": "wrong"}]}]}
        client = Client()
        run = self.create()
        with patch("engine.time.sleep"):
            Engine(self.store, client, FakeGrader()).execute(self.store.claim())
        detail = self.store.detail(run["id"])
        self.assertEqual(client.calls, 2)
        self.assertEqual(detail["status"], "completed")
        self.assertEqual(detail["pass_rate"], 0)
        self.assertEqual(detail["results"][0]["attempts"], 2)


class TransportTests(unittest.TestCase):
    def test_model_not_found_is_explained_without_exposing_upstream_text(self):
        secret = "private-upstream-credential"
        bodies = [
            json.dumps({"error": {"type": "model_not_found", "message": secret}}).encode(),
            json.dumps({"error": {"code": "model_not_found", "message": secret}}).encode(),
            json.dumps({"error": {"type": secret, "message": secret}}).encode(),
            json.dumps({"error": secret}).encode(),
            b"[]", b"not json", b"x" * (16 * 1024 + 1),
        ]
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_): pass
            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                body = bodies.pop(0)
                self.send_response(404)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with patch("transport.public_addresses", return_value=["127.0.0.1"]):
                for index in range(len(bodies)):
                    with self.assertRaises(RequestFailure) as error:
                        ResponsesClient().generate(f"http://never-resolve.invalid:{server.server_port}/v1", secret, {})
                    self.assertEqual(error.exception.status, 404)
                    self.assertNotIn(secret, str(error.exception))
                    self.assertEqual("请更换模型或密钥" in str(error.exception), index < 2)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_orphan_cleanup_is_scoped_to_this_worker(self):
        grader = Grader(Path("/fixture/worker-home"), "fixture-image")
        with patch("engine.subprocess.check_output", return_value="abc123\ndef456\n") as lookup, patch("engine.subprocess.run") as remove:
            grader.cleanup_stale()
        self.assertIn("label=io.sub2api.benchmark.owner=" + grader.owner, lookup.call_args.args[0])
        self.assertEqual(remove.call_args.args[0], ["docker", "rm", "-f", "abc123", "def456"])

    def test_normalize_and_deny_nonpublic_addresses(self):
        self.assertEqual(normalize_url("https://EXAMPLE.com/"), "https://example.com/v1")
        self.assertEqual(normalize_url("https://example.com/v1/responses"), "https://example.com/v1")
        for value in ("file:///etc/passwd", "https://key@example.com", "https://example.com?api_key=abc", "http://example.com/#frag"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                normalize_url(value)
        for ip in ("127.0.0.1", "169.254.169.254", "10.0.0.1", "::1", "::ffff:127.0.0.1", "224.0.0.1", "ff02::1", "64:ff9b::7f00:1", "2002:7f00:1::", "0.0.0.0", "100.64.0.1"):
            with self.subTest(ip=ip), patch("socket.getaddrinfo", return_value=[(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, 80))]):
                with self.assertRaises(RequestFailure):
                    public_addresses("example.com", 80)

    def test_mixed_dns_and_rebinding_never_bypass_public_address_checks(self):
        def info(ip): return (socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, 80))
        with patch("socket.getaddrinfo", side_effect=[[info("8.8.8.8")], [info("8.8.8.8"), info("127.0.0.1")]]):
            self.assertEqual(public_addresses("example.com", 80), ["8.8.8.8"])
            with self.assertRaises(RequestFailure): public_addresses("example.com", 80)

    def test_pinned_transport_redaction_no_redirect_and_protocol(self):
        key = 'test-secret-"slash\\'
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_): pass
            def do_POST(self):
                data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                seen.append((self.path, self.headers["Host"], self.headers["Authorization"], data))
                body = json.dumps({"echo": key, "array": [key], key: "value"}).encode()
                self.send_response(302 if data.get("redirect") else 200)
                self.send_header("Location", "http://127.0.0.1/private")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            port = server.server_port
            # Only the initial resolver is patched; connecting must use its IP,
            # while HTTP Host retains the requested (unresolvable) hostname.
            with patch("transport.public_addresses", return_value=["127.0.0.1"]):
                client = ResponsesClient()
                data = client.generate(f"http://never-resolve.invalid:{port}/v1", key, {"model": "fixture"})
                self.assertNotIn(key, json.dumps(data))
                self.assertEqual(data["echo"], "[REDACTED]")
                self.assertEqual(seen[0][0], "/v1/responses")
                self.assertEqual(seen[0][1], f"never-resolve.invalid:{port}")
                with self.assertRaises(RequestFailure) as error:
                    client.generate(f"http://never-resolve.invalid:{port}", key, {"redirect": True})
                self.assertEqual(error.exception.status, 302)
                self.assertEqual(len(seen), 2)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


class HTTPTests(Fixture):
    def test_auth_validation_and_job_lifecycle(self):
        class Client:
            def generate(_, *args):
                return {"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": "42"}]}]}
        engine = Engine(self.store, Client(), FakeGrader())
        token = "fixture-worker-token-32-characters"
        server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(self.store, engine, token))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        engine.start()
        def request(path, method="GET", body=None, authorized=True):
            req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}{path}", method=method,
                  data=json.dumps(body).encode() if body is not None else None,
                  headers={"Authorization": "Bearer " + token} if authorized else {})
            try:
                with urllib.request.urlopen(req, timeout=5) as response:
                    return response.status, json.load(response)
            except urllib.error.HTTPError as error:
                return error.code, json.load(error)
        try:
            self.assertEqual(request("/state", authorized=False)[0], 401)
            self.assertEqual(request("/config", "PUT", {})[0], 400)
            suite = request("/config", "PUT", {"counts": {"coding": 0, "math": 1, "reasoning": 0}})[1]
            status, run = request("/runs", "POST", {"suite_id": suite["id"], "base_url": "https://example.com", "model": "test", "api_key": "private-fixture-key"})
            self.assertEqual(status, 202)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                detail = request("/runs/" + run["id"])[1]
                if detail["status"] == "completed": break
                time.sleep(0.05)
            self.assertEqual(detail["pass_rate"], 100)
            self.assertNotIn("private-fixture-key", json.dumps(detail))
            latest = request("/suites/refresh", "POST", {"suite_id": suite["id"]})[1]
            status, rerun = request("/runs/" + run["id"] + "/rerun", "POST", {})
            self.assertEqual(status, 202)
            self.assertEqual(rerun["suite_id"], latest["id"])
            self.assertNotEqual(rerun["suite_id"], run["suite_id"])
            status, plan = request("/schedules", "POST", {"name": "Daily check", "base_url": "https://example.com", "api_key": "private-schedule-key", "model": "test", "cron_expression": "0 9 * * *", "enabled": False})
            self.assertEqual(status, 201)
            self.assertIsNone(plan["next_run_at"])
            self.assertNotIn("private-schedule-key", json.dumps(request("/schedules")[1]))
            endpoint = "/schedules/" + plan["id"] + "/run"
            self.assertEqual(request(endpoint, "POST", {}, authorized=False)[0], 401)
            self.assertEqual(request("/users/7" + endpoint, "POST", {})[0], 404)
            self.assertEqual(request(endpoint, "POST", {"model": "override"})[0], 400)
            status, triggered = request(endpoint, "POST", {})
            self.assertEqual(status, 202)
            self.assertEqual(triggered["schedule_id"], plan["id"])
            self.assertFalse(request("/schedules")[1]["items"][0]["enabled"])
            self.assertNotIn("private-schedule-key", json.dumps(triggered))
            self.assertEqual(request("/schedules/" + plan["id"], "PUT", {"enabled": True})[0], 200)
            self.assertEqual(request("/schedules/" + plan["id"], "PUT", {"cron_expression": "bad"})[0], 400)
            self.assertEqual(request("/schedules/" + plan["id"], "DELETE", authorized=False)[0], 401)
            self.assertEqual(request("/schedules/" + plan["id"], "DELETE")[0], 200)
            self.assertEqual(request("/schedules")[1]["items"], [])
        finally:
            engine.stopping.set()
            engine.wake.set()
            for worker_thread in engine.threads: worker_thread.join(10)
            engine.scheduler.join(10)
            engine.display_thread.join(10)
            server.shutdown()
            server.server_close()
            thread.join()


@unittest.skipUnless(os.getenv("MODEL_BENCHMARK_TEST_HOME"), "Set MODEL_BENCHMARK_TEST_HOME for real Docker grading")
class RealGraderTests(unittest.TestCase):
    def setUp(self):
        self.home = Path(os.environ["MODEL_BENCHMARK_TEST_HOME"])
        self.grader = Grader(self.home, "sub2api-model-benchmark-grader:v1")
        self.grader.check_ready()

    def test_binary_readline_and_wrong_answer(self):
        question = {"category": "coding", "evaluation_sample": {"input_output": json.dumps({"inputs": ["2\n3\n", "8\n9\n"], "outputs": ["5\n", "17\n"], "fn_name": None})}}
        correct = "```python\nimport sys\na=int(sys.stdin.buffer.readline())\nb=int(sys.stdin.buffer.readline())\nprint(a+b)\n```"
        result = self.grader.grade(question, correct)
        self.assertTrue(result["passed"], result)
        self.assertEqual(result["tests_passed"], 2)
        wrong = self.grader.grade(question, "```python\nprint(0)\n```")
        self.assertFalse(wrong["passed"])
        self.assertEqual(wrong["status"], "failed")

    def test_float_tolerance_only_when_declared(self):
        question = {"category": "coding", "float_tolerance": 1e-8,
                    "evaluation_sample": {"input_output": json.dumps({"inputs": [""], "outputs": ["0.3333333333333333"], "fn_name": None})}}
        answer = "```python\nprint(0.3333333333)\n```"
        self.assertTrue(self.grader.grade(question, answer)["passed"])
        self.assertFalse(self.grader.grade(question, "```python\nprint(0.34)\n```")["passed"])
        question.pop("float_tolerance")
        self.assertFalse(self.grader.grade(question, answer)["passed"])
        self.assertEqual(float_tolerance("absolute or relative error must not exceed 10^{-8}."), 1e-8)

    def test_real_bank_and_official_text_grading(self):
        bank = Bank(self.home / "bank.sqlite3")
        self.assertEqual(bank.sizes(), {"coding": 132, "math": 29, "reasoning": 100})
        floating = bank.get("lcb:atcoder:abc392_d")
        self.assertEqual(floating["float_tolerance"], 1e-8)
        question = {"category": "math", "ground_truth": "42"}
        self.assertTrue(self.grader.grade(question, "The answer is \\boxed{042}.")["passed"])
        self.assertFalse(self.grader.grade(question, "The answer is \\boxed{001}.")["passed"])


if __name__ == "__main__":
    unittest.main()
