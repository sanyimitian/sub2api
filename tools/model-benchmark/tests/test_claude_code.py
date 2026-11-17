import http.client
import json
import subprocess
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import Mock, patch
from urllib.parse import urlsplit

from claude_code import ClaudeCodeClient
from engine import Engine
from store import Problem, Store
from transport import RequestFailure
from test_worker import Fixture, FakeGrader


class ModeTests(Fixture):
    def test_default_invalid_and_retest(self):
        self.assertEqual(self.create()["evaluation_mode"], "responses")
        for mode in ("unknown", None, [], {}):
            with self.subTest(mode=mode), self.assertRaises(Problem):
                self.create(evaluation_mode=mode)
        first = self.create(evaluation_mode="claude_code")
        self.store.finish(first["id"], "failed")
        self.assertEqual(self.store.rerun(first["id"])["evaluation_mode"], "claude_code")
        reopened = Store(self.root / "data", self.store.bank, clock=lambda: self.date)
        self.assertEqual(reopened.detail(first["id"])["evaluation_mode"], "claude_code")
        self.assertEqual(reopened.user_results()["items"][0]["results"][0]["evaluation_mode"], "claude_code")

    def test_schedule_snapshots_and_execution(self):
        self.store.refresh({"coding": 0, "math": 1, "reasoning": 0})
        plan = self.store.create_schedule({"base_url": "https://example.com/v1", "api_key": "secret",
            "model": "claude-fable-5-2", "effort": "medium", "name": "CLI", "cron_expression": "@every 1h",
            "evaluation_mode": "claude_code", "public_results": True, "group_id": 12, "group_name": "Claude"})
        run = self.store.run_schedule(plan["id"])
        api, cli = Mock(), Mock()
        cli.generate.return_value = {"status": "completed", "model": "claude-fable-5-2", "client_version": "2.1.326",
            "output": [{"type": "message", "content": [{"type": "output_text", "text": "42"}]}]}
        engine = Engine(self.store, api, FakeGrader(), claude_client=cli)
        engine.execute_claimed(self.store.claim())
        api.generate.assert_not_called()
        self.assertEqual(cli.generate.call_args.kwargs, {"group_id": 12})
        saved = self.store.detail(run["id"])
        self.assertEqual(saved["status"], "completed")
        self.assertEqual(saved["results"][0]["client_version"], "2.1.326")
        self.assertEqual(self.store.public_results()["items"][0]["evaluation_mode"], "claude_code")
        self.store.update_schedule(plan["id"], {"evaluation_mode": "responses"})
        self.assertEqual(self.store.detail(run["id"])["evaluation_mode"], "claude_code")
        self.assertEqual(self.store.run_schedule(plan["id"])["evaluation_mode"], "responses")


class ClaudeCodeTests(unittest.TestCase):
    def setUp(self):
        self.payload = {"model": "claude-fable-5-2", "instructions": "Solve independently.",
                        "input": [{"role": "user", "content": "Return 42"}], "reasoning": {"effort": "medium"}}
        self.result = {"subtype": "success", "is_error": False, "stop_reason": "end_turn", "result": "42",
                       "modelUsage": {"claude-fable-5-2": {}},
                       "usage": {"input_tokens": 2, "cache_read_input_tokens": 10, "cache_creation_input_tokens": 5, "output_tokens": 3}}

    def test_response_usage_redaction_and_incomplete(self):
        self.result["result"] = "answer real-secret relay-secret"
        output = ClaudeCodeClient.response(self.result, 0, self.payload["model"], "real-secret", "relay-secret")
        self.assertEqual(output["usage"], {"input_tokens": 17, "output_tokens": 3, "total_tokens": 20})
        self.assertEqual(output["output"][0]["content"][0]["text"], "answer [REDACTED] [REDACTED]")
        self.result["stop_reason"] = "max_tokens"
        output = ClaudeCodeClient.response(self.result, 0, self.payload["model"], "real-secret", "relay-secret")
        self.assertEqual(output["status"], "incomplete")
        self.assertEqual(output["incomplete_details"]["reason"], "max_output_tokens")
        self.result["modelUsage"] = {"another-model": {}}
        with self.assertRaises(RequestFailure):
            ClaudeCodeClient.response(self.result, 0, self.payload["model"], "real-secret", "relay-secret")

    def probe(self, status=200, redirect=False, group_id=12, tools=None):
        seen = []
        class Upstream(BaseHTTPRequestHandler):
            def log_message(self, *_args): pass
            def do_POST(self):
                seen.append((self.path, dict(self.headers), self.rfile.read(int(self.headers["Content-Length"]))))
                self.send_response(status)
                if redirect:
                    self.send_header("Location", "http://127.0.0.1:1/leak")
                self.end_headers()
                self.wfile.write(b'{"result":"42"}')
        server = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        def run(command, **kwargs):
            self.assertIn("--bare", command)
            self.assertEqual(command[command.index("--tools") + 1], "")
            self.assertEqual(command[command.index("--effort") + 1], "medium")
            self.assertNotIn("--dangerously-skip-permissions", command)
            self.assertEqual(kwargs["input"], b"Return 42")
            self.assertNotIn("real-secret", str(command) + str(kwargs["env"]))
            relay = urlsplit(kwargs["env"]["ANTHROPIC_BASE_URL"])
            conn = http.client.HTTPConnection(relay.hostname, relay.port, timeout=2)
            request = {"model": self.payload["model"], "tools": tools or []}
            conn.request("POST", "/v1/messages?beta=true", json.dumps(request),
                         {"x-api-key": kwargs["env"]["ANTHROPIC_API_KEY"], "User-Agent": "claude-cli/2.1.326", "x-app": "cli"})
            response = conn.getresponse()
            response.read()
            conn.close()
            kwargs["stdout"].write(json.dumps(self.result).encode())
            return subprocess.CompletedProcess(command, 0)
        with patch("claude_code.shutil.which", return_value="claude"), patch("claude_code.subprocess.run", side_effect=run):
            result = ClaudeCodeClient(allow_private=True, timeout=3).generate(
                f"http://127.0.0.1:{server.server_port}/custom/v1", "real-secret", self.payload, group_id=group_id)
        return result, seen

    def test_relay_preserves_client_headers_group_and_base_path(self):
        output, seen = self.probe()
        self.assertEqual(output["status"], "completed")
        self.assertEqual(len(seen), 1)
        path, headers, _ = seen[0]
        self.assertEqual(path, "/custom/v1/messages?beta=true")
        self.assertEqual(headers["User-Agent"], "claude-cli/2.1.326")
        self.assertEqual(headers["x-api-key"], "real-secret")
        self.assertEqual(headers["X-Sub2API-Benchmark-Group-ID"], "12")

    def test_relay_rejects_redirects_tools_and_preserves_http_status(self):
        for status in (302, 401, 409, 503):
            with self.subTest(status=status), self.assertRaises(RequestFailure) as failure:
                self.probe(status, redirect=status == 302)
            self.assertEqual(failure.exception.status, status)
            self.assertNotIn("real-secret", str(failure.exception))
        with self.assertRaises(RequestFailure):
            self.probe(tools=[{"type": "web_search_20250305", "name": "web_search"}])

    def test_private_targets_rejected_before_cli_and_timeout_reported(self):
        with patch("claude_code.shutil.which", return_value="claude"), patch("claude_code.subprocess.run") as runner:
            with self.assertRaises(RequestFailure):
                ClaudeCodeClient().generate("http://127.0.0.1/v1", "secret", self.payload)
            runner.assert_not_called()
        with patch("claude_code.shutil.which", return_value="claude"), patch("claude_code.subprocess.run", side_effect=subprocess.TimeoutExpired("claude", 1)):
            with self.assertRaises(RequestFailure):
                ClaudeCodeClient(allow_private=True).generate("http://127.0.0.1/v1", "secret", self.payload)
