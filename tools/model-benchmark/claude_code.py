"""Official Claude Code, one isolated, tool-free session per question.

The CLI talks only to a per-request loopback relay. The relay pins validated DNS,
injects the target key, rejects redirects and preserves benchmark group checks.
"""
from __future__ import annotations

import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import secrets
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
from urllib.parse import urlsplit

from transport import RequestFailure, normalize_url, public_addresses

CLAUDE_CODE_VERSION = "2.1.326"
MAX_BYTES = 8 * 1024 * 1024


class ClaudeCodeClient:
    def __init__(self, allow_private=False, timeout=600, executable="claude"):
        self.allow_private, self.timeout, self.executable = allow_private, timeout, executable

    def generate(self, base_url, key, payload, *, group_id=None):
        executable = shutil.which(self.executable)
        if not executable:
            raise RequestFailure("评测服务未安装 Claude Code，请更新 worker 镜像", 400, code="request_error")
        target = urlsplit(normalize_url(base_url))
        port = target.port or (443 if target.scheme == "https" else 80)
        addresses = public_addresses(target.hostname, port, self.allow_private)
        relay_key = secrets.token_hex(32)
        timeout = self.timeout
        failures = []
        connections = set()
        lock = threading.Lock()
        stopped = threading.Event()

        class Relay(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                conn = None
                try:
                    path = urlsplit(self.path)
                    if path.path not in ("/v1/messages", "/v1/messages/count_tokens") or path.query not in ("", "beta=true"):
                        self.send_error(404)
                        return
                    if self.headers.get("x-api-key") != relay_key or self.headers.get("Transfer-Encoding"):
                        self.send_error(403)
                        return
                    length = int(self.headers.get("Content-Length", "0"))
                    if not 0 < length <= MAX_BYTES:
                        self.send_error(413)
                        return
                    self.connection.settimeout(timeout)
                    body = self.rfile.read(length)
                    request = json.loads(body)
                    # Never permit tool use or model fallback, including server tools.
                    if not isinstance(request, dict) or request.get("tools") or request.get("model") != payload["model"]:
                        raise RequestFailure("Claude Code 请求偏离所选模型或启用了工具", 400, code="request_error")
                    conn = http.client.HTTPConnection(target.hostname, port, timeout=timeout)
                    sock = None
                    for address in addresses:
                        try:
                            sock = socket.create_connection((address, port), timeout=min(30, timeout))
                            break
                        except OSError:
                            continue
                    if sock is None:
                        raise RequestFailure("无法连接 API 服务")
                    conn.sock = sock
                    if target.scheme == "https":
                        conn.sock = ssl.create_default_context().wrap_socket(sock, server_hostname=target.hostname)
                    conn.sock.settimeout(timeout)
                    with lock:
                        if stopped.is_set():
                            return
                        connections.add(conn)
                    headers = {k: v for k, v in self.headers.items() if k.lower() in (
                        "user-agent", "content-type", "accept", "anthropic-version", "anthropic-beta", "x-app",
                        "x-stainless-lang", "x-stainless-package-version", "x-stainless-os", "x-stainless-arch",
                        "x-stainless-runtime", "x-stainless-runtime-version", "x-stainless-retry-count",
                        "anthropic-dangerous-direct-browser-access")}
                    headers.update({"x-api-key": key, "Authorization": "Bearer " + key})
                    if group_id is not None:
                        headers["X-Sub2API-Benchmark-Group-ID"] = str(group_id)
                    upstream_path = target.path + path.path.removeprefix("/v1")
                    if path.query:
                        upstream_path += "?" + path.query
                    conn.request("POST", upstream_path, body=body, headers=headers)
                    response = conn.getresponse()
                    if response.status != 200:
                        message = f"API 返回 HTTP {response.status}"
                        if response.status == 409 and group_id is not None:
                            message = "本站测试密钥已变更分组，请编辑定时任务重新选择分组和模型"
                        raise RequestFailure(message, response.status)
                    self.send_response(200)
                    self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
                    self.send_header("Connection", "close")
                    self.end_headers()
                    total = 0
                    while chunk := response.read1(65536):
                        total += len(chunk)
                        if total > MAX_BYTES:
                            raise RequestFailure("API 响应超过大小限制", 400, code="response_error")
                        self.wfile.write(chunk)
                        self.wfile.flush()
                except (OSError, ValueError, http.client.HTTPException, RequestFailure) as exc:
                    failure = exc if isinstance(exc, RequestFailure) else RequestFailure("API 请求超时、连接中断或响应格式不正确")
                    failures.append(failure)
                    try:
                        self.send_error(502, "Benchmark upstream request failed")
                    except OSError:
                        pass
                finally:
                    if conn:
                        with lock:
                            connections.discard(conn)
                        conn.close()
                    self.close_connection = True

        server = ThreadingHTTPServer(("127.0.0.1", 0), Relay)
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.1}, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory(prefix="benchmark-claude-") as directory:
                # No inherited credentials, proxy, hooks, project or user settings.
                env = {name: os.environ[name] for name in ("PATH", "SystemRoot", "WINDIR", "TMP", "TEMP") if name in os.environ}
                env.update(ANTHROPIC_API_KEY=relay_key, ANTHROPIC_BASE_URL=f"http://127.0.0.1:{server.server_port}",
                           CLAUDE_CONFIG_DIR=directory, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
                           DISABLE_AUTOUPDATER="1", API_MAX_RETRIES="0")
                command = [executable, "--print", "--bare", "--tools", "", "--strict-mcp-config",
                           "--mcp-config", '{"mcpServers":{}}', "--setting-sources", "", "--disable-slash-commands",
                           "--no-session-persistence", "--output-format", "json", "--max-turns", "1",
                           "--model", payload["model"], "--effort", payload["reasoning"]["effort"],
                           "--append-system-prompt", payload["instructions"]]
                prompt = "\n".join(item["content"] for item in payload["input"])
                # Files avoid unbounded subprocess output in worker memory.
                with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
                    try:
                        result = subprocess.run(command, input=prompt.encode(), stdout=stdout, stderr=stderr,
                                                cwd=directory, env=env, timeout=timeout)
                    except subprocess.TimeoutExpired as exc:
                        raise RequestFailure("Claude Code 请求超时") from exc
                    except OSError as exc:
                        raise RequestFailure("无法启动 Claude Code，请检查 worker 安装", 400, code="request_error") from exc
                    if failures:
                        raise failures[0]
                    stdout.seek(0)
                    raw = stdout.read(MAX_BYTES + 1)
                    if len(raw) > MAX_BYTES:
                        raise RequestFailure("Claude Code 响应超过大小限制", 400, code="response_error")
                    try:
                        data = json.loads(raw)
                    except ValueError as exc:
                        raise RequestFailure("Claude Code 返回格式不正确", 400, code="response_error") from exc
                    return self.response(data, result.returncode, payload["model"], key, relay_key)
        finally:
            stopped.set()
            with lock:
                for conn in connections:
                    try:
                        if conn.sock:
                            conn.sock.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass
                    conn.close()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    @staticmethod
    def response(data, returncode, model, key, relay_key):
        if not isinstance(data, dict):
            raise RequestFailure("Claude Code 返回格式不正确", 400, code="response_error")
        if returncode or data.get("is_error") or data.get("subtype") != "success":
            raise RequestFailure("Claude Code 未成功完成请求", 400, code="response_error")
        models = data.get("modelUsage")
        if not isinstance(models, dict) or set(models) != {model}:
            raise RequestFailure("Claude Code 返回的模型与所选模型不一致", 400, code="response_error")
        answer = data.get("result")
        if not isinstance(answer, str):
            raise RequestFailure("Claude Code 未返回有效答案", 400, code="response_error")
        for secret in (key, relay_key):
            answer = answer.replace(secret, "[REDACTED]")
        usage = data.get("usage") or {}
        if not isinstance(usage, dict):
            raise RequestFailure("Claude Code 返回用量格式不正确", 400, code="response_error")
        def count(name):
            value = usage.get(name, 0)
            return value if type(value) is int and value >= 0 else 0
        inputs = sum(count(name) for name in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"))
        outputs = count("output_tokens")
        stop = data.get("stop_reason")
        complete = stop == "end_turn"
        reason = "max_output_tokens" if stop == "max_tokens" else "unknown"
        return {"model": model, "status": "completed" if complete else "incomplete",
                "incomplete_details": None if complete else {"reason": reason},
                "output": [{"type": "message", "content": [{"type": "output_text", "text": answer}]}],
                "usage": {"input_tokens": inputs, "output_tokens": outputs, "total_tokens": inputs + outputs},
                "evaluation_mode": "claude_code", "client_version": CLAUDE_CODE_VERSION}
