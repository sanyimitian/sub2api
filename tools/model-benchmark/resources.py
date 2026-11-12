"""Read-only host/container resource checks for benchmark admission and dispatch."""
from __future__ import annotations

import os
import shutil
import threading
import time
from pathlib import Path

GIB = 1024 ** 3


class ResourcePressure(Exception):
    pass


class ResourceGuard:
    def __init__(self, home, *, sample=None, clock=time.monotonic):
        self.home = Path(home)
        self.sample = sample or self.read_metrics
        self.clock = clock
        self.lock = threading.RLock()
        self.memory_stop = float(os.getenv('MODEL_BENCHMARK_MEMORY_STOP_PERCENT', '85'))
        self.memory_resume = float(os.getenv('MODEL_BENCHMARK_MEMORY_RESUME_PERCENT', '80'))
        self.disk_stop = float(os.getenv('MODEL_BENCHMARK_DISK_STOP_GB', '5')) * GIB
        self.disk_resume = float(os.getenv('MODEL_BENCHMARK_DISK_RESUME_GB', '8')) * GIB
        self.disk_warn = float(os.getenv('MODEL_BENCHMARK_DISK_WARN_GB', '10')) * GIB
        if not (0 < self.memory_resume < self.memory_stop <= 100
                and 0 < self.disk_stop < self.disk_resume <= self.disk_warn < float('inf')):
            raise ValueError('Invalid benchmark resource thresholds')
        self.memory_blocked = self.disk_blocked = False
        self.checked = float('-inf')
        self.last = {}

    def read_metrics(self):
        meminfo = Path(os.getenv('MODEL_BENCHMARK_MEMINFO', '/proc/meminfo'))
        values = {line.split(':')[0]: int(line.split()[1]) * 1024
                  for line in meminfo.read_text().splitlines() if ':' in line}
        total, available = values['MemTotal'], values['MemAvailable']
        if not 0 <= available <= total or total <= 0:
            raise ValueError('Invalid host memory counters')
        host_percent = 100 * (total - available) / total
        worker_percent = None
        cgroup = Path('/sys/fs/cgroup')
        if (cgroup / 'memory.max').is_file():
            limit = (cgroup / 'memory.max').read_text().strip()
            if limit != 'max':
                current = int((cgroup / 'memory.current').read_text())
                stats = dict(line.split() for line in (cgroup / 'memory.stat').read_text().splitlines())
                worker_percent = 100 * max(0, current - int(stats.get('inactive_file', 0))) / int(limit)
        elif (cgroup / 'memory/memory.limit_in_bytes').is_file():
            limit = int((cgroup / 'memory/memory.limit_in_bytes').read_text())
            current = int((cgroup / 'memory/memory.usage_in_bytes').read_text())
            stats = dict(line.split() for line in (cgroup / 'memory/memory.stat').read_text().splitlines())
            worker_percent = 100 * max(0, current - int(stats.get('total_inactive_file', 0))) / limit
        paths = {'data': self.home}
        docker_disk = os.getenv('MODEL_BENCHMARK_DOCKER_DISK_PATH')
        if docker_disk:
            paths['docker'] = Path(docker_disk)
        disks = {name: shutil.disk_usage(path).free for name, path in paths.items()}
        return {'host_memory_percent': host_percent, 'worker_memory_percent': worker_percent,
                'disk_free_bytes': min(disks.values())}

    def snapshot(self, *, force=False):
        with self.lock:
            if not force and self.clock() - self.checked < 5:
                return dict(self.last)
            thresholds = {'memory_stop_percent': self.memory_stop, 'memory_resume_percent': self.memory_resume,
                          'disk_stop_gb': self.disk_stop / GIB, 'disk_resume_gb': self.disk_resume / GIB,
                          'disk_warn_gb': self.disk_warn / GIB}
            try:
                metrics = self.sample()
                memory = max(metrics['host_memory_percent'], metrics.get('worker_memory_percent') or 0)
                free = metrics['disk_free_bytes']
                if not 0 <= memory < float('inf') or not 0 <= free < float('inf'):
                    raise ValueError('Invalid resource metrics')
                self.memory_blocked = memory >= (self.memory_resume if self.memory_blocked else self.memory_stop)
                self.disk_blocked = free < (self.disk_resume if self.disk_blocked else self.disk_stop)
                reasons = []
                if self.memory_blocked:
                    reasons.append(f'内存使用率 {memory:.1f}%，降至 {self.memory_resume:g}% 以下后恢复')
                if self.disk_blocked:
                    reasons.append(f'磁盘可用空间 {free / GIB:.1f} GB，恢复至 {self.disk_resume / GIB:g} GB 后继续')
                blocked = bool(reasons)
                warning = free < self.disk_warn
                self.last = {**metrics, 'memory_percent': round(memory, 1), 'disk_free_gb': round(free / GIB, 1),
                             'blocked': blocked, 'status': 'blocked' if blocked else 'warning' if warning else 'ok',
                             'message': '；'.join(reasons) if blocked else
                             f'磁盘剩余 {free / GIB:.1f} GB，请及时检查日志和缓存' if warning else '',
                             'thresholds': thresholds}
            except (OSError, ValueError, KeyError, ZeroDivisionError):
                # Do not launch paid requests or sandboxes when monitoring is unavailable.
                self.last = {'blocked': True, 'status': 'unavailable',
                             'message': '无法读取服务器资源，已暂停启动测试，请管理员检查监控挂载',
                             'thresholds': thresholds}
            self.checked = self.clock()
            return dict(self.last)

    def require_capacity(self, *, update=False):
        state = self.snapshot(force=True)
        if state['blocked']:
            raise ResourcePressure(state['message'])
        # Reserve 6 GiB for downloads, 2 GiB for the candidate, plus the stop floor.
        if update and state['disk_free_bytes'] < self.disk_stop + 8 * GIB:
            raise ResourcePressure(f'更新总题库至少需要 {self.disk_stop / GIB + 8:g} GB 可用空间')
