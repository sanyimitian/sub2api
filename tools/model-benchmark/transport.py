"""Responses requests with checked, pinned DNS and no redirect/proxy forwarding."""
from __future__ import annotations

import http.client
import ipaddress
import json
import socket
import ssl
import threading
from urllib.parse import urlsplit, urlunsplit


class RequestFailure(Exception):
    def __init__(self, message: str, status: int = 0, *, code: str = ""):
        super().__init__(message)
        self.status = status
        self.code = code or ("http_error" if status else "request_error")


def normalize_url(value: str) -> str:
    if not isinstance(value, str) or len(value) > 2048 or any(ord(c) < 33 for c in value.strip()):
        raise ValueError("API URL 格式不正确")
    u = urlsplit(value.strip())
    if u.scheme not in ("http", "https") or not u.hostname or u.username or u.password or u.query or u.fragment:
        raise ValueError("API URL 必须为不含账号、查询参数的 HTTP/HTTPS 地址")
    try:
        port = u.port
        host = u.hostname.encode("idna").decode("ascii")
    except (ValueError, UnicodeError) as exc:
        raise ValueError("API URL 格式不正确") from exc
    if ":" in host:
        host = f"[{host}]"
    authority = host + (f":{port}" if port else "")
    path = u.path.rstrip("/")
    if path.endswith("/responses"):
        path = path[:-10]
    if not path:
        path = "/v1"
    return urlunsplit((u.scheme, authority, path, "", ""))


def public_addresses(host: str, port: int, allow_private: bool = False) -> list[str]:
    try:
        addresses = list(dict.fromkeys(info[4][0] for info in socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)))
    except OSError as exc:
        raise RequestFailure("无法解析 API 域名") from exc
    def safe_address(value):
        ip = ipaddress.ip_address(value)
        if not ip.is_global or ip.is_multicast or ip.is_reserved or ip.is_unspecified:
            return False
        if isinstance(ip, ipaddress.IPv6Address):
            # Translation/tunnelling can conceal a private IPv4 destination.
            if ip.ipv4_mapped or ip.sixtofour or ip.teredo:
                return False
            if ip in ipaddress.ip_network("64:ff9b::/96") or ip in ipaddress.ip_network("64:ff9b:1::/48"):
                return False
        return True
    if not addresses or (not allow_private and any(not safe_address(ip) for ip in addresses)):
        raise RequestFailure("API 地址不能指向本机或内网", 400, code="request_error")
    return addresses


class ResponsesClient:
    def __init__(self, allow_private: bool = False, timeout: int = 600):
        self.allow_private = allow_private
        self.timeout = timeout

    def generate(self, base_url: str, key: str, payload: dict) -> dict:
        u = urlsplit(normalize_url(base_url))
        port = u.port or (443 if u.scheme == "https" else 80)
        addresses = public_addresses(u.hostname, port, self.allow_private)
        # Connect to the validated IP directly; retain the original hostname for
        # TLS certificate verification/SNI and the HTTP Host header.
        conn = http.client.HTTPConnection(u.hostname, port, timeout=self.timeout)
        deadline = None
        try:
            sock = None
            for address in addresses:
                try:
                    sock = socket.create_connection((address, port), timeout=30)
                    break
                except OSError:
                    continue
            if sock is None:
                raise RequestFailure("无法连接 API 服务")
            if u.scheme == "https":
                try:
                    sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
                except Exception:
                    sock.close()
                    raise
            sock.settimeout(self.timeout)
            conn.sock = sock
            # A socket read timeout alone can be kept alive by a drip response.
            # Close this exact connection on the wall deadline as well.
            def expire():
                try:
                    sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                sock.close()
            deadline = threading.Timer(self.timeout, expire)
            deadline.daemon = True
            deadline.start()
            conn.request("POST", u.path + "/responses", body=json.dumps(payload).encode(), headers={
                "Authorization": "Bearer " + key, "Content-Type": "application/json",
            })
            response = conn.getresponse()
            if response.status != 200:
                # Inspect only a bounded, structured error code. Raw upstream
                # text may contain credentials and must never be exposed.
                if response.status == 404:
                    try:
                        raw = response.read(16 * 1024 + 1)
                        data = json.loads(raw) if len(raw) <= 16 * 1024 else None
                        error = data.get("error") if isinstance(data, dict) else None
                        if isinstance(error, dict) and any(error.get(field) == "model_not_found" for field in ("type", "code")):
                            raise RequestFailure("API 返回 HTTP 404：当前密钥未提供所填模型，请更换模型或密钥", 404)
                    except (OSError, http.client.HTTPException, ValueError):
                        pass
                raise RequestFailure(f"API 返回 HTTP {response.status}", response.status)
            body = response.read(8 * 1024 * 1024 + 1)
            if len(body) > 8 * 1024 * 1024:
                raise RequestFailure("API 响应超过大小限制", code="response_error")
            data = json.loads(body)
            if not isinstance(data, dict):
                raise RequestFailure("API 返回格式不正确", code="response_error")
            # Ensure even an upstream echo never stores the submitted key.
            def redact(value):
                if isinstance(value, str):
                    return value.replace(key, "[REDACTED]")
                if isinstance(value, list):
                    return [redact(item) for item in value]
                if isinstance(value, dict):
                    return {redact(k): redact(v) for k, v in value.items()}
                return value
            return redact(data)
        except RequestFailure:
            raise
        except (OSError, http.client.HTTPException, ValueError) as exc:
            raise RequestFailure("API 请求超时、连接中断或响应格式不正确") from exc
        finally:
            if deadline is not None:
                deadline.cancel()
            conn.close()
