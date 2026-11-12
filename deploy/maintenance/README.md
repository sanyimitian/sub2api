# Docker 构建缓存维护

此目录仅提供待部署的文件，不会因构建应用或启动 worker 自动执行清理。

由管理员在后续维护部署中执行（需要 root、systemd、Docker CLI 和 flock）：

```sh
sudo install -m 0755 deploy/maintenance/docker-build-cache-prune.sh /usr/local/sbin/sub2api-build-cache-prune
sudo install -m 0644 deploy/maintenance/sub2api-build-cache-prune.service /etc/systemd/system/
sudo install -m 0644 deploy/maintenance/sub2api-build-cache-prune.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now sub2api-build-cache-prune.timer
```

每周日服务器本地时间 04:00 后随机延迟最多 15 分钟清理，停机错过后可补执行一次。`flock` 避免本脚本并行运行，低优先级执行，最长 30 分钟。失败记录在 `journalctl -u sub2api-build-cache-prune.service`。

命令仅使用 `docker builder prune --force --filter 'until=168h' --keep-storage 2GB`；实际释放量由符合条件的缓存决定，2 GB 不是强制总缓存上限。正在使用的缓存不由本脚本强制删除。未使用 `system prune`、`image prune`、`volume prune`。

主应用业务日志归档使用持久化 `data/logs`，每份 100 MB、轮转后 gzip 压缩、保留 90 天、不限制份数。当前活动文件不压缩，过期归档由应用在打开/轮转日志时清理。Compose 中的 `LOG_ROTATION_*` 环境变量会覆盖旧 `config.yaml` 中的轮转设置，确保后续部署应用新策略。

主应用 Docker 标准输出仅保存近期查看副本（100 MB × 15 份、压缩），不承担三个月归档；业务日志以 `data/logs` 为准。仅 stdout/stderr 的启动/崩溃信息受 Docker 份数限制。评测 worker 日志改为 50 MB × 20 份、轮转后压缩。`json-file` 没有按天保留选项，不能用 `max-file=0` 表示无限保留。

生产环境自定义 overlay 或 `extends` 服务名称应同时合并 logging 和 LOG_* 环境变量，保留原来的应用数据卷与数据库挂载，先检查最终 Compose 配置，再走正常发布流程重建应用/worker；本维护 timer 无需重建服务。本次本地调整不自动安装或启用维护任务。
