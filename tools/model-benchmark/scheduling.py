"""Persistent schedules support completion-based intervals and five-field cron."""
from datetime import datetime, timedelta, timezone
import re

from croniter import croniter


def interval_duration(expression):
    if not isinstance(expression, str) or not expression.strip().startswith("@every"):
        return None
    match = re.fullmatch(r"@every\s+([0-9]{1,5})(m|h)", expression.strip())
    if not match:
        raise ValueError("固定间隔应为整数分钟或小时，例如 @every 30m")
    value = int(match[1])
    minutes = value * (60 if match[2] == "h" else 1)
    if not 1 <= minutes <= 43200:
        raise ValueError("固定间隔范围为 1 分钟至 30 天")
    return timedelta(minutes=minutes)


def next_occurrence(expression, current, zone):
    duration = interval_duration(expression)
    if duration is not None:
        return " ".join(expression.split()), (current.astimezone(timezone.utc) + duration).isoformat()
    if not isinstance(expression, str) or len(expression) > 100 or len(expression.split()) != 5:
        raise ValueError("请填写五段 Cron 表达式（分 时 日 月 周）")
    expression = " ".join(expression.split())
    # Disallow croniter's hashed/random syntax: plans must have deterministic times.
    if not re.fullmatch(r"[0-9*/ ,\-]+", expression):
        raise ValueError("Cron 仅支持数字、星号、范围、列表和步长")
    try:
        value = croniter(expression, current.astimezone(zone), max_years_between_matches=5).get_next(datetime)
    except (ValueError, KeyError, OverflowError) as exc:
        raise ValueError("Cron 表达式无效或未来五年内无执行时间") from exc
    return expression, value.astimezone(timezone.utc).isoformat()
