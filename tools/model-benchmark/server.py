from __future__ import annotations

import argparse
import fcntl
import hmac
import json
import os
import re
import signal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from store import Problem


def make_handler(store, engine, token, environment=None):
    class Handler(BaseHTTPRequestHandler):
        server_version = "ModelBenchmark"

        def log_message(self, *_):
            pass  # No query strings, credentials, or model output in access logs.

        def send_json(self, status, data):
            body = json.dumps(data, ensure_ascii=False).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(body)

        def handle_api(self):
            current_store = environment.store if environment else store
            current_engine = environment.engine if environment else engine
            self.handle_request(current_store, current_engine)

        def handle_request(self, store, engine):
            supplied = self.headers.get("Authorization", "").encode("utf-8")
            if not hmac.compare_digest(supplied, ("Bearer " + token).encode()):
                self.send_json(401, {"message": "Unauthorized"})
                return
            try:
                data = {}
                if self.command in ("POST", "PUT"):
                    if self.headers.get("Transfer-Encoding"):
                        raise Problem("Unsupported request encoding")
                    length = int(self.headers.get("Content-Length", "0"))
                    if not 0 <= length <= 16384:
                        raise Problem("请求内容过大")
                    data = json.loads(self.rfile.read(length)) if length else {}
                    if not isinstance(data, dict):
                        raise Problem("请求格式不正确")
                parsed = urlsplit(self.path)
                path = parsed.path
                owner_user_id = None
                user_match = re.fullmatch(r"/users/([1-9][0-9]{0,18})(/.*)", path)
                if user_match:
                    owner_user_id = int(user_match[1])
                    path = user_match[2]
                    if not re.fullmatch(r"/(?:state|runs(?:/[a-f0-9]{32}(?:/(?:rerun|cancel))?)?)", path):
                        raise Problem("Not found", 404)
                if environment:
                    setup = environment.snapshot()
                    if self.command == "GET" and path == "/state":
                        state = store.state(owner_user_id) if store else environment.pending_state()
                        state.update(ready=setup["status"] == "ready", message=setup["message"], environment=setup)
                        if owner_user_id is None and environment.bank_updates:
                            state["bank_update"] = environment.bank_updates.snapshot()
                        self.send_json(200, state)
                        return
                    if path == "/environment/retry" and self.command == "POST" and owner_user_id is None:
                        if data:
                            raise Problem("环境重试不接受配置参数")
                        self.send_json(202, environment.retry())
                        return
                    if not store or not engine:
                        if self.command == "GET" and path in ("/runs", "/schedules", "/displays", "/showcase"):
                            self.send_json(200, {"items": [], "total": 0, "page": 1, "page_size": 20})
                            return
                        raise Problem(setup["message"], 503)
                    if setup["status"] != "ready" and self.command in ("POST", "PUT") and not path.endswith("/cancel"):
                        raise Problem("评测环境正在自动准备，请就绪后重试", 503)
                if self.command == "GET" and path == "/state":
                    state = store.state(owner_user_id)
                    try:
                        engine.grader.check_ready()
                        state.update(ready=True, message="")
                    except Exception:
                        state.update(ready=False, message="评测环境尚未就绪，请完成题库和评分镜像安装")
                    self.send_json(200, state)
                elif path in ("/bank/update", "/bank/activate") and self.command == "POST" and owner_user_id is None:
                    if not environment or not environment.bank_updates:
                        raise Problem("题库服务尚未就绪", 503)
                    if path == "/bank/update":
                        if data:
                            raise Problem("更新总题库不接受自定义地址或参数")
                        self.send_json(202, environment.bank_updates.start())
                    else:
                        if set(data) != {"version"} or not isinstance(data["version"], str):
                            raise Problem("请选择已验证的题库版本")
                        self.send_json(200, environment.bank_updates.activate(data["version"]))
                elif self.command == "PUT" and path == "/config":
                    counts = store.validate_counts(data.get("counts"))
                    self.send_json(200, store.refresh(counts, data.get("suite_id"), data.get("coding_counts")))
                elif self.command == "POST" and path == "/suites/refresh":
                    self.send_json(200, store.refresh(expected_suite=data.get("suite_id")))
                elif self.command == "GET" and path == "/runs":
                    page = int(parse_qs(parsed.query).get("page", ["1"])[0])
                    self.send_json(200, store.list_runs(page, owner_user_id))
                elif self.command == "POST" and path == "/runs":
                    try:
                        engine.grader.check_ready()
                    except Exception:
                        raise Problem("评测环境尚未就绪，未创建任务", 503)
                    result = store.create(data, owner_user_id)
                    engine.wake.set()
                    self.send_json(202, result)
                elif self.command == "GET" and path == "/showcase":
                    self.send_json(200, engine.displays.showcase())
                elif self.command == "GET" and path == "/public-results":
                    self.send_json(200, store.public_results())
                elif path == "/execution" and self.command in ("GET", "PUT"):
                    self.send_json(200, store.execution_settings(data if self.command == "PUT" else None))
                    engine.wake.set()
                elif path == "/displays" and self.command == "GET":
                    self.send_json(200, engine.displays.list())
                elif path == "/displays" and self.command == "POST":
                    self.send_json(201, engine.displays.save(data))
                elif re.fullmatch(r"/displays/[a-f0-9]{32}(?:/refresh)?", path):
                    display_id = path.split("/")[2]
                    if path.endswith("/refresh") and self.command == "POST":
                        self.send_json(202, engine.displays.request_refresh(display_id))
                    elif not path.endswith("/refresh") and self.command == "PUT":
                        self.send_json(200, engine.displays.save(data, display_id))
                    elif not path.endswith("/refresh") and self.command == "DELETE":
                        self.send_json(200, engine.displays.delete(display_id))
                    else:
                        raise Problem("Method not allowed", 405)
                elif self.command == "GET" and path == "/schedules":
                    self.send_json(200, store.list_schedules())
                elif self.command == "POST" and path == "/schedules":
                    self.send_json(201, store.create_schedule(data))
                elif re.fullmatch(r"/schedules/[a-f0-9]{32}/run", path) and self.command == "POST":
                    if data:
                        raise Problem("立刻执行使用已保存的定时任务配置，不接受其他参数")
                    try:
                        engine.grader.check_ready()
                    except Exception:
                        raise Problem("评测环境尚未就绪，未创建任务", 503)
                    result = store.run_schedule(path.split("/")[2])
                    engine.wake.set()
                    self.send_json(202, result)
                elif re.fullmatch(r"/schedules/[a-f0-9]{32}", path):
                    schedule_id = path.rsplit("/", 1)[1]
                    if self.command == "PUT":
                        self.send_json(200, store.update_schedule(schedule_id, data))
                    elif self.command == "DELETE":
                        self.send_json(200, store.delete_schedule(schedule_id))
                    else:
                        raise Problem("Method not allowed", 405)
                else:
                    match = re.fullmatch(r"/runs/([a-f0-9]{32})(?:/(rerun|cancel))?", path)
                    if not match:
                        raise Problem("Not found", 404)
                    run_id, action = match.groups()
                    if self.command == "GET" and not action:
                        self.send_json(200, store.detail(run_id, owner_user_id))
                    elif self.command == "POST" and action == "rerun":
                        try:
                            engine.grader.check_ready()
                        except Exception:
                            raise Problem("评测环境尚未就绪，未创建任务", 503)
                        result = store.rerun(run_id, owner_user_id)
                        engine.wake.set()
                        self.send_json(202, result)
                    elif self.command == "POST" and action == "cancel":
                        self.send_json(200, store.cancel(run_id, owner_user_id))
                    else:
                        raise Problem("Method not allowed", 405)
            except Problem as exc:
                self.send_json(exc.status, {"message": str(exc)})
            except (ValueError, TypeError, json.JSONDecodeError):
                self.send_json(400, {"message": "请求格式不正确"})
            except Exception:
                self.send_json(500, {"message": "评测服务内部错误"})

        do_GET = do_POST = do_PUT = do_DELETE = handle_api

    return Handler


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--home", type=Path, default=Path(os.getenv("MODEL_BENCHMARK_HOME", "/opt/sub2api-model-benchmark")))
    parser.add_argument("--host", default=os.getenv("MODEL_BENCHMARK_LISTEN", "127.0.0.1"))
    parser.add_argument("--port", type=int, default=int(os.getenv("MODEL_BENCHMARK_PORT", "8124")))
    args = parser.parse_args()
    token = os.getenv("MODEL_BENCHMARK_WORKER_TOKEN", "")
    if len(token) < 32:
        raise SystemExit("MODEL_BENCHMARK_WORKER_TOKEN must contain at least 32 characters")
    os.umask(0o077)
    data = args.home / "data"
    data.mkdir(parents=True, exist_ok=True)
    # One process owns the persistent queue; never recover another live worker's jobs.
    lock = (data / "worker.lock").open("a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    from environment import Environment
    environment = Environment(args.home, os.getenv("MODEL_BENCHMARK_GRADER_IMAGE", "sub2api-model-benchmark-grader:v1"),
                              os.getenv("MODEL_BENCHMARK_TIMEZONE", "Asia/Shanghai"))
    server = ThreadingHTTPServer((args.host, args.port), make_handler(None, None, token, environment))
    server.daemon_threads = True
    environment.start()

    def stop(*_):
        environment.close()
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        environment.close()
        server.server_close()


if __name__ == "__main__":
    main()
