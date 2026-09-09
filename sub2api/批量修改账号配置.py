#!/usr/bin/env python3
"""批量原地修改 Codex OpenAI OAuth 账号导出文件。

运行：
    python3 批量修改账号配置.py

仅处理当前目录中名称匹配 ``codex-*-free.sub2api.json`` 的文件；
不会修改用作配置样例的 ``sub2api-account-*.json`` 文件。
"""

from __future__ import annotations

import json
import os
import stat
import sys
import tempfile
from pathlib import Path
from typing import Any


脚本目录 = Path(__file__).resolve().parent
目标文件模式 = "codex-*-free.sub2api.json"

# 与 sub2api-account-20260909100830.json 中的静态配置保持一致。
账号配置 = {
    "concurrency": 1,
    "priority": 1,
    "rate_multiplier": 0.001,
    "auto_pause_on_expired": True,
}
扩展配置 = {
    "auto_pause_7d_threshold": 0.5,
    "auto_reset_credit_5h_threshold": 1,
    "auto_reset_credit_7d_threshold": 1,
    "auto_reset_credit_enabled": False,
    "openai_long_context_billing_enabled": True,
    "openai_oauth_responses_websockets_v2_enabled": False,
    "openai_oauth_responses_websockets_v2_mode": "off",
    "privacy_mode": "training_off",
}


def 选择模型() -> str:
    """要求操作者在每次执行时选择唯一允许使用的模型。"""
    选项 = {
        "1": "gpt-5.6-luna",
        "gpt-5.6-luna": "gpt-5.6-luna",
        "luna": "gpt-5.6-luna",
        "2": "gpt-5.6-terra",
        "gpt-5.6-terra": "gpt-5.6-terra",
        "terra": "gpt-5.6-terra",
    }
    print("请选择账号唯一允许使用的模型：")
    print("  1. gpt-5.6-luna")
    print("  2. gpt-5.6-terra")
    while True:
        try:
            输入 = input("请输入 1 或 2：").strip().lower()
        except EOFError:
            print("未读取到模型选择，未修改任何文件。", file=sys.stderr)
            raise SystemExit(1)
        模型 = 选项.get(输入)
        if 模型:
            return 模型
        print("输入无效，请输入 1 或 2。")


def 加载并校验(路径: Path) -> dict[str, Any]:
    try:
        数据 = json.loads(路径.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as 异常:
        raise ValueError(f"{路径.name} 不是可读取的 JSON：{异常}") from 异常

    账号列表 = 数据.get("accounts") if isinstance(数据, dict) else None
    if not isinstance(账号列表, list) or not 账号列表:
        raise ValueError(f"{路径.name} 不包含非空 accounts 列表")
    if not all(isinstance(账号, dict) for 账号 in 账号列表):
        raise ValueError(f"{路径.name} 的 accounts 列表含有非对象项")
    return 数据


def 修改配置(数据: dict[str, Any], 模型: str) -> None:
    for 账号 in 数据["accounts"]:
        凭据 = 账号.get("credentials")
        if not isinstance(凭据, dict):
            凭据 = {}
            账号["credentials"] = 凭据
        # model_mapping 只保留本次选中的模型，以实现模型白名单。
        凭据["model_mapping"] = {模型: 模型}

        extra = 账号.get("extra")
        if not isinstance(extra, dict):
            extra = {}
            账号["extra"] = extra
        extra.update(扩展配置)
        账号.update(账号配置)


def 原子写入(路径: Path, 数据: dict[str, Any]) -> None:
    """写入临时文件后替换原文件，避免中途中断时留下半个 JSON。"""
    文件描述符, 临时文件名 = tempfile.mkstemp(
        prefix=f".{路径.name}.", suffix=".tmp", dir=路径.parent, text=True
    )
    try:
        with os.fdopen(文件描述符, "w", encoding="utf-8") as 文件:
            json.dump(数据, 文件, ensure_ascii=False, indent=2)
            文件.write("\n")
            文件.flush()
            os.fsync(文件.fileno())
        os.chmod(临时文件名, stat.S_IMODE(路径.stat().st_mode))
        os.replace(临时文件名, 路径)
    except BaseException:
        Path(临时文件名).unlink(missing_ok=True)
        raise


def main() -> None:
    目标文件 = sorted(脚本目录.glob(目标文件模式))
    if not 目标文件:
        print(f"未找到匹配 {目标文件模式!r} 的账号文件。", file=sys.stderr)
        raise SystemExit(1)

    模型 = 选择模型()

    # 先完整校验，确保发现坏文件时不会只修改一部分账号文件。
    try:
        待写入 = [(路径, 加载并校验(路径)) for 路径 in 目标文件]
    except ValueError as 异常:
        print(f"校验失败，未修改任何文件：{异常}", file=sys.stderr)
        raise SystemExit(1)

    for 路径, 数据 in 待写入:
        修改配置(数据, 模型)
        原子写入(路径, 数据)

    print(f"已原地替换 {len(待写入)} 个账号文件。")
    print(f"唯一允许模型：{模型}")
    print("已设置：账号计费倍率 0.001、长上下文计费开启、7 天自动暂停阈值 0.5。")


if __name__ == "__main__":
    main()
