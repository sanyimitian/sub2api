"""验证增加评测网络及重建容器后，宿主机反代的来源网关保持不变。

需要 Linux Docker Engine 28+、Compose 2.33.1+ 和 python:3.12-slim 镜像。
仅创建临时项目中的探测容器，不启动应用、数据库或真实评测服务。
默认让 Docker 分配网段；地址池不足时可通过 CLIENT_IP_TEST_SUBNETS 指定
两个已确认空闲的网段，使用逗号分隔。
"""

import http.client
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


DEPLOY = Path(__file__).resolve().parent.parent
PROBE = """
from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(self.client_address[0].encode())
HTTPServer(('0.0.0.0', 8080), Handler).serve_forever()
"""


def run(*args, env=None):
    result = subprocess.run(args, env=env, text=True, capture_output=True, timeout=120)
    if result.returncode:
        raise RuntimeError(f"命令失败：{' '.join(args)}\n{result.stderr}")
    return result.stdout.strip()


def check(base):
    project = "sub2api-client-ip-test-" + uuid.uuid4().hex[:10]
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "compose.json"
        envfile = Path(directory) / ".env"
        envfile.write_text("")
        env = {**os.environ, "POSTGRES_PASSWORD": "test-only",
               "MODEL_BENCHMARK_WORKER_TOKEN": "test-only-" + "a" * 32}
        # 使用真实部署文件的合并结果，仅替换运行进程、端口和存储等无关项。
        merged = json.loads(run(
            "docker", "compose", "--env-file", str(envfile),
            "-f", str(DEPLOY / base), "-f", str(DEPLOY / "docker-compose.benchmark.yml"),
            "config", "--format", "json", env=env))
        app_networks = merged["services"]["sub2api"]["networks"]
        worker_networks = merged["services"]["model-benchmark"]["networks"]
        assert set(app_networks) == {"sub2api-network", "benchmark-network"}
        assert set(worker_networks) == {"benchmark-network"}
        networks = {name: {} for name in app_networks}
        subnets = os.environ.get("CLIENT_IP_TEST_SUBNETS", "").split(",")
        if subnets != [""]:
            assert len(subnets) == 2, "必须提供两个空闲测试网段"
            for name, subnet in zip(sorted(networks), subnets):
                networks[name] = {"ipam": {"config": [{"subnet": subnet.strip()}]}}
        app = {
            "image": "python:3.12-slim", "command": ["python", "-c", PROBE],
            "ports": ["127.0.0.1::8080"], "stop_grace_period": "1s",
            "networks": {"sub2api-network": app_networks["sub2api-network"]},
        }
        config = {"services": {"sub2api": app}, "networks": networks}
        compose = ["docker", "compose", "-p", project, "-f", str(path)]

        def verify_peer(expected=None):
            cid = run(*compose, "ps", "-q", "sub2api")
            info = json.loads(run("docker", "inspect", cid))[0]
            net = info["NetworkSettings"]
            gateway = net["Networks"][project + "_sub2api-network"]["Gateway"]
            if expected is not None:
                assert gateway == expected, "主网络网关发生了变化"
            port = net["Ports"]["8080/tcp"][0]["HostPort"]
            # 模拟宿主机 Nginx 连接回环映射端口，绕过测试机的 HTTP 代理。
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            deadline = time.monotonic() + 15
            while True:
                try:
                    with opener.open("http://127.0.0.1:" + port, timeout=1) as response:
                        peer = response.read().decode()
                    break
                except (OSError, urllib.error.URLError, http.client.HTTPException):
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(0.1)
            assert peer == gateway, f"宿主机反代来源变成 {peer}，应为主网关 {gateway}"
            return gateway

        try:
            path.write_text(json.dumps(config))
            run(*compose, "up", "-d")
            gateway = verify_peer()
            app["networks"] = app_networks
            config["services"]["model-benchmark"] = {
                "image": "python:3.12-slim", "command": ["python", "-c", PROBE],
                "networks": worker_networks, "stop_grace_period": "1s",
                "healthcheck": {
                    "test": ["CMD", "python", "-c", "import urllib.request; "
                             "urllib.request.urlopen('http://127.0.0.1:8080',timeout=1).read()"],
                    "interval": "1s", "timeout": "2s", "retries": 15,
                },
            }
            path.write_text(json.dumps(config))
            run(*compose, "up", "-d", "--wait", "--wait-timeout", "30")
            verify_peer(gateway)
            run(*compose, "exec", "-T", "sub2api", "python", "-c",
                "import urllib.request; "
                "r=urllib.request.urlopen('http://model-benchmark:8080',timeout=5); "
                "assert r.status == 200")
            run(*compose, "up", "-d", "--no-deps", "--force-recreate", "sub2api")
            verify_peer(gateway)
            print(f"{base}：增加评测网络、评测服务通信和重建后的来源网关均通过验证")
        finally:
            run(*compose, "down", "--timeout", "1", "--volumes")


if __name__ == "__main__":
    for filename in ("docker-compose.yml", "docker-compose.local.yml"):
        check(filename)
