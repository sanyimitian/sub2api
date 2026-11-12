"""Public result feeds and persistent, administrator-selected display sources."""
from __future__ import annotations

import http.client
import json
import math
import socket
import ssl
import threading
import uuid
from datetime import datetime, timezone
from urllib.parse import urlsplit, urlunsplit

from store import CATEGORIES, EFFORTS, Problem, RESULT_PAGE_SIZE, result_cursor, encode_result_cursor
from transport import RequestFailure, public_addresses


def feed_url(value):
    if not isinstance(value, str) or len(value) > 2048 or any(ord(c) < 33 or ord(c) == 127 for c in value.strip()):
        raise Problem("请输入完整的公开结果接口地址")
    try:
        u = urlsplit(value.strip())
        if u.scheme not in ("http", "https") or not u.hostname or u.username is not None or u.password is not None or u.fragment or u.port == 0:
            raise ValueError()
        host = u.hostname.encode("idna").decode("ascii")
        authority = (f"[{host}]" if ":" in host else host) + (f":{u.port}" if u.port else "")
        path = u.path or "/"
        # HTTP paths must be encoded by the caller; disallow control characters above.
        (path + u.query).encode("ascii")
        return urlunsplit((u.scheme, authority, path, u.query, ""))
    except (ValueError, UnicodeError) as exc:
        raise Problem("请输入有效的 HTTP/HTTPS 公开结果接口地址") from exc


def fetch_feed(url):
    """No cookies, auth, redirects, proxies or model credentials leave this worker."""
    u = urlsplit(feed_url(url))
    port = u.port or (443 if u.scheme == "https" else 80)
    addresses = public_addresses(u.hostname, port)
    conn = http.client.HTTPConnection(u.hostname, port, timeout=15)
    deadline = None
    try:
        sock = None
        for address in addresses[:4]:
            try:
                sock = socket.create_connection((address, port), timeout=5)
                break
            except OSError:
                continue
        if sock is None:
            raise RequestFailure("无法连接结果接口")
        if u.scheme == "https":
            try:
                sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
            except Exception:
                sock.close()
                raise
        conn.sock = sock
        sock.settimeout(15)
        def expire():
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            sock.close()
        deadline = threading.Timer(15, expire)
        deadline.daemon = True
        deadline.start()
        conn.request("GET", u.path + ("?" + u.query if u.query else ""), headers={"Accept": "application/json"})
        response = conn.getresponse()
        if response.status != 200:
            raise RequestFailure(f"结果接口返回 HTTP {response.status}")
        body = response.read(2 * 1024 * 1024 + 1)
        if len(body) > 2 * 1024 * 1024:
            raise RequestFailure("结果接口响应过大")
        return sanitize_feed(json.loads(body))
    finally:
        if deadline:
            deadline.cancel()
        conn.close()


def sanitize_feed(data):
    """Accept flat v2 / legacy grouped v1, whitelist fields and normalize groups."""
    def text(value, maximum=200):
        if not isinstance(value, str) or not value or len(value) > maximum or any(ord(c) < 32 for c in value):
            raise ValueError("Invalid text")
        return value

    def integer(value, maximum=100):
        if type(value) is not int or not 0 <= value <= maximum:
            raise ValueError("Invalid count")
        return value

    def timestamp(value):
        text(value, 64)
        date = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if date.tzinfo is None:
            raise ValueError("Timestamp needs timezone")
        return date.astimezone(timezone.utc).isoformat()

    if isinstance(data, dict) and "data" in data:
        if data.get("code") != 0:
            raise ValueError("Unsuccessful feed")
        data = data["data"]
    if not isinstance(data, dict) or type(data.get("version")) is not int or data["version"] not in (1, 2):
        raise ValueError("Unsupported result protocol")
    items = data.get("items")
    if data["version"] == 2:
        if not isinstance(items, list) or len(items) > 20:
            raise ValueError("Invalid feed limit")
        grouped, result_ids = {}, set()
        for record in items:
            if not isinstance(record, dict):
                raise ValueError("Invalid result")
            key = text(record["schedule_id"], 100)
            result_id = text(record["id"])
            if result_id in result_ids:
                raise ValueError("Duplicate result")
            result_ids.add(result_id)
            group = grouped.setdefault(key, {"id": key, "name": text(record["schedule_name"], 100),
                "model": record["model"], "effort": record["effort"], "results": []})
            group["results"].append(record)
        items = list(grouped.values())
    if not isinstance(items, list) or len(items) > 50:
        raise ValueError("Invalid schedules")
    output, ids = [], set()
    for item in items:
        if not isinstance(item, dict):
            raise ValueError("Invalid schedule")
        schedule = {k: text(item[k], 100 if k in ("id", "name") else 200) for k in ("id", "name", "model", "effort")}
        if schedule["id"] in ids or schedule["effort"] not in EFFORTS:
            raise ValueError("Invalid schedule")
        ids.add(schedule["id"])
        if not isinstance(item["results"], list) or len(item["results"]) > 20:
            raise ValueError("Invalid result count")
        results = []
        for result in item["results"]:
            if not isinstance(result, dict):
                raise ValueError("Invalid result")
            record = {k: text(result[k]) for k in ("id", "model", "effort", "status", "suite_day", "suite_fingerprint")}
            if result.get("base_url"):
                record["base_url"] = feed_url(result["base_url"])
            if record["effort"] not in EFFORTS or record["status"] not in ("completed", "failed", "cancelled", "interrupted"):
                raise ValueError("Invalid finished result")
            record["suite_revision"] = integer(result["suite_revision"], 1000000)
            record["created_at"] = timestamp(result["created_at"])
            record["finished_at"] = timestamp(result["finished_at"])
            record["categories"] = {}
            for category in CATEGORIES:
                values = {key: integer(result["categories"][category][key]) for key in ("total", "completed", "passed")}
                if not values["passed"] <= values["completed"] <= values["total"]:
                    raise ValueError("Inconsistent counts")
                record["categories"][category] = values
            for key in ("total", "completed", "passed"):
                record[key] = sum(v[key] for v in record["categories"].values())
                if integer(result[key]) != record[key]:
                    raise ValueError("Inconsistent aggregate")
            if not 1 <= record["total"] <= 100 or (record["status"] == "completed" and record["completed"] != record["total"]):
                raise ValueError("Incomplete score")
            record["pass_rate"] = record["passed"] / record["total"] * 100 if record["status"] == "completed" else None
            elapsed = result.get("elapsed_seconds", 0)
            if type(elapsed) not in (int, float) or not math.isfinite(elapsed) or not 0 <= elapsed <= 31536000:
                raise ValueError("Invalid duration")
            record["elapsed_seconds"] = elapsed
            if "usage" in result:
                record["usage"] = {k: integer(result["usage"][k], 10**12) for k in ("input_tokens", "output_tokens", "total_tokens")}
            for key in ("api_errors", "grading_errors"):
                if key in result:
                    record[key] = integer(result[key])
            results.append(record)
        schedule["results"] = sorted(results, key=lambda r: r["finished_at"], reverse=True)
        output.append(schedule)
    # Legacy feeds can contain up to 50 groups. Cap imported results globally too.
    latest = sorted([(group, result) for group in output for result in group["results"]],
                    key=lambda pair: pair[1]["finished_at"], reverse=True)[:20]
    selected = {id(result) for _, result in latest}
    for group in output:
        group["results"] = [result for result in group["results"] if id(result) in selected]
    return [group for group in output if group["results"]]


class Displays:
    def __init__(self, store, fetcher=fetch_feed):
        self.store, self.fetcher = store, fetcher
        with store.transaction() as db:
            db.execute("""CREATE TABLE IF NOT EXISTS displays (
                id TEXT PRIMARY KEY, name TEXT NOT NULL, source_type TEXT NOT NULL,
                schedule_id TEXT, remote_url TEXT NOT NULL DEFAULT '', refresh_minutes INTEGER NOT NULL DEFAULT 60,
                enabled INTEGER NOT NULL DEFAULT 1, revision INTEGER NOT NULL DEFAULT 1,
                cache TEXT NOT NULL DEFAULT '[]', last_attempt_at TEXT, last_success_at TEXT,
                last_error TEXT NOT NULL DEFAULT '', next_fetch_at REAL NOT NULL DEFAULT 0,
                lease_token TEXT, lease_until REAL NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL, updated_at TEXT NOT NULL)""")
            if "max_results" not in {row["name"] for row in db.execute("PRAGMA table_info(displays)")}:
                db.execute("ALTER TABLE displays ADD COLUMN max_results INTEGER NOT NULL DEFAULT 20")

    def timestamp(self):
        return self.store.clock().astimezone(timezone.utc).isoformat()

    def public_config(self, row):
        result = {k: row[k] for k in ("id", "name", "source_type", "schedule_id", "remote_url", "refresh_minutes", "max_results",
                  "last_attempt_at", "last_success_at", "last_error", "created_at", "updated_at")}
        result["enabled"] = bool(row["enabled"])
        result["fetching"] = bool(row["lease_token"]) and row["lease_until"] > self.store.clock().timestamp()
        result["next_fetch_at"] = datetime.fromtimestamp(row["next_fetch_at"], timezone.utc).isoformat() if row["enabled"] and row["source_type"] == "remote" and row["next_fetch_at"] > 0 else None
        return result

    def list(self):
        with self.store.transaction() as db:
            return {"items": [self.public_config(row) for row in db.execute("SELECT * FROM displays ORDER BY rowid")]}

    def save(self, data, display_id=None):
        allowed = {"name", "source_type", "schedule_id", "remote_url", "refresh_minutes", "enabled", "max_results"}
        if set(data) - allowed:
            raise Problem("展示配置包含不支持的字段")
        with self.store.transaction() as db:
            old = db.execute("SELECT * FROM displays WHERE id=?", (display_id,)).fetchone() if display_id else None
            if display_id and old is None:
                raise Problem("展示配置不存在", 404)
            fields = {**(dict(old) if old else {"refresh_minutes": 60, "enabled": True, "max_results": 20}), **data}
            name, source = fields.get("name"), fields.get("source_type")
            if not isinstance(name, str) or not name.strip() or len(name) > 100 or any(ord(c) < 32 for c in name):
                raise Problem("展示名称应为 1 至 100 个字符")
            enabled = data.get("enabled", bool(old["enabled"]) if old else True)
            minutes = fields["refresh_minutes"]
            max_results = fields["max_results"]
            if type(max_results) is not int or not 1 <= max_results <= 100:
                raise Problem("最大展示条数必须为 1 至 100 的整数")
            if type(enabled) is not bool or type(minutes) is not int or not 1 <= minutes <= 10080:
                raise Problem("刷新间隔应为 1 至 10080 分钟，启用状态应为布尔值")
            schedule_id, url = None, ""
            if source == "local":
                schedule_id = fields.get("schedule_id")
                if not isinstance(schedule_id, str) or db.execute("SELECT id FROM schedules WHERE id=? AND public_results=1 AND deleted=0", (schedule_id,)).fetchone() is None:
                    # Still allow disabling a source whose plan was made private/deleted.
                    if enabled or not old or schedule_id != old["schedule_id"]:
                        raise Problem("请选择已开启开放展示的本站定时任务")
            elif source == "remote":
                url = feed_url(fields.get("remote_url"))
            else:
                raise Problem("请选择本站任务或外站接口作为数据来源")
            stamp = self.timestamp()
            if old:
                changed = (source, schedule_id, url) != (old["source_type"], old["schedule_id"], old["remote_url"])
                db.execute("""UPDATE displays SET name=?,source_type=?,schedule_id=?,remote_url=?,refresh_minutes=?,enabled=?,
                    revision=revision+1,updated_at=?,next_fetch_at=0,lease_token=NULL,lease_until=0,
                    cache=?,last_success_at=?,last_error=?,max_results=? WHERE id=?""",
                    (name.strip(), source, schedule_id, url, minutes, int(enabled), stamp,
                     "[]" if changed else old["cache"], None if changed else old["last_success_at"], "" if changed else old["last_error"], max_results, display_id))
            else:
                if db.execute("SELECT COUNT(*) FROM displays").fetchone()[0] >= 50:
                    raise Problem("最多可创建 50 个展示配置", 409)
                display_id = uuid.uuid4().hex
                db.execute("""INSERT INTO displays(id,name,source_type,schedule_id,remote_url,refresh_minutes,enabled,created_at,updated_at,max_results)
                    VALUES(?,?,?,?,?,?,?,?,?,?)""", (display_id, name.strip(), source, schedule_id, url, minutes, int(enabled), stamp, stamp, max_results))
            return self.public_config(db.execute("SELECT * FROM displays WHERE id=?", (display_id,)).fetchone())

    def delete(self, display_id):
        with self.store.transaction() as db:
            if not db.execute("DELETE FROM displays WHERE id=?", (display_id,)).rowcount:
                raise Problem("展示配置不存在", 404)
        return {"deleted": True}

    def request_refresh(self, display_id):
        with self.store.transaction() as db:
            row = db.execute("SELECT * FROM displays WHERE id=?", (display_id,)).fetchone()
            if row is None:
                raise Problem("展示配置不存在", 404)
            if row["source_type"] != "remote" or not row["enabled"]:
                raise Problem("仅启用的外站来源可手动刷新")
            db.execute("UPDATE displays SET next_fetch_at=0 WHERE id=?", (display_id,))
            return self.public_config(db.execute("SELECT * FROM displays WHERE id=?", (display_id,)).fetchone())

    def claim_refresh(self):
        current = self.store.clock().timestamp()
        with self.store.transaction() as db:
            row = db.execute("""SELECT * FROM displays WHERE source_type='remote' AND enabled=1
                AND next_fetch_at<=? AND lease_until<=? ORDER BY next_fetch_at,rowid LIMIT 1""", (current, current)).fetchone()
            if row is None:
                return None
            token = uuid.uuid4().hex
            db.execute("UPDATE displays SET lease_token=?,lease_until=?,last_attempt_at=? WHERE id=?", (token, current + 120, self.timestamp(), row["id"]))
            return {**dict(row), "lease_token": token}

    def fetch(self, item):
        try:
            results = self.fetcher(item["remote_url"])
            error = ""
        except Exception:
            results = None
            error = "获取失败，请检查接口地址、服务可用性及结果格式"
        with self.store.transaction() as db:
            # A response from an old URL, deleted plan or expired lease is never installed.
            current = db.execute("SELECT * FROM displays WHERE id=? AND revision=? AND lease_token=? AND enabled=1", (item["id"], item["revision"], item["lease_token"])).fetchone()
            if current is None:
                return
            stamp = self.timestamp()
            db.execute("""UPDATE displays SET cache=?,last_success_at=?,last_error=?,lease_token=NULL,lease_until=0,next_fetch_at=? WHERE id=?""",
                (json.dumps(results) if results is not None else current["cache"], stamp if results is not None else current["last_success_at"],
                 error, self.store.clock().timestamp() + current["refresh_minutes"] * 60, item["id"]))

    def showcase(self, cursor=""):
        """所有启用来源共享一页额度；本地历史不受旧展示数量配置截断。"""
        before = result_cursor(cursor, "site")
        with self.store.transaction() as db:
            sources, candidates = {}, []
            for row in db.execute("SELECT * FROM displays WHERE enabled=1 ORDER BY rowid"):
                source = {"id": row["id"], "name": row["name"], "source_type": row["source_type"],
                    "last_success_at": row["last_success_at"], "stale": bool(row["last_error"]),
                    "pending": row["source_type"] == "remote" and row["last_success_at"] is None and not row["last_error"], "items": []}
                if row["source_type"] == "local":
                    schedule = db.execute("SELECT * FROM schedules WHERE id=? AND public_results=1 AND deleted=0", (row["schedule_id"],)).fetchone()
                    if schedule is None:
                        continue
                    query = "SELECT *,julianday(finished_at) AS sort_time FROM runs WHERE schedule_id=? AND finished_at IS NOT NULL"
                    params = (schedule["id"],)
                    if before:
                        query += " AND (julianday(finished_at),id,?,?) < (?,?,?,?)"
                        params += (row["id"], schedule["id"], *before)
                    results = db.execute(query + " ORDER BY sort_time DESC,id DESC LIMIT ?", (*params, RESULT_PAGE_SIZE + 1)).fetchall()
                    for result in results:
                        candidates.append(((result["sort_time"], result["id"], row["id"], schedule["id"]), source, schedule, result))
                else:
                    for schedule in sanitize_feed({"version": 1, "items": json.loads(row["cache"])}):
                        for result in schedule["results"]:
                            stamp = db.execute("SELECT julianday(?)", (result["finished_at"],)).fetchone()[0]
                            key = (stamp, result["id"], row["id"], schedule["id"])
                            if before is None or key < before:
                                candidates.append((key, source, schedule, result))
                sources[row["id"]] = source
            candidates.sort(key=lambda item: item[0], reverse=True)
            for key, source, schedule, row in candidates[:RESULT_PAGE_SIZE]:
                result = {**self.store._shared_result(db, row), "base_url": row["base_url"]} if source["source_type"] == "local" else row
                group = next((item for item in source["items"] if item["id"] == schedule["id"]), None)
                if group is None:
                    group = {"id": schedule["id"], "name": schedule["name"], "model": result["model"], "effort": result["effort"], "results": []}
                    source["items"].append(group)
                group["results"].append(result)
            more = len(candidates) > RESULT_PAGE_SIZE
            return {"items": list(sources.values()), "next_cursor": encode_result_cursor(candidates[RESULT_PAGE_SIZE - 1][0], "site") if more else None}
