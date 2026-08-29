# add-apikey-failure-cooldown 实施协调记录

## 前置状态

- 源分支：`add-apikey-failure-cooldown-new`
- original HEAD：`efb46db0a960fdad94502b1c3a982a0051cf5245`
- 起始工作区：仅有本变更目录的未跟踪 OpenSpec 产物；无已跟踪差异
- 实施前备份工作树：`/home/pan/桌面/code/add-apikey-failure-cooldown-实施前备份-20260828212816`
- 实施前备份分支：`add-apikey-failure-cooldown-pre-apply-20260828212816`
- 备份校验：源与备份的已跟踪差异为空，未跟踪文件清单一致
- 实施原则：所有实现、测试、任务勾选和规格核对均在源工作区串行完成；备份工作树只读

## 质量门禁

- 后端目标测试：`go test ./internal/service ./internal/repository ./internal/handler/...`（在 `backend` 目录执行）
- 后端全量测试：`make -C backend test`
- 后端竞态：`make -C backend test-race`（若仓库提供该目标；否则记录等价命令）
- 前端目标测试：`pnpm --dir frontend exec vitest run <相关测试>`
- 前端检查：`pnpm --dir frontend run lint:check`、`pnpm --dir frontend run typecheck`、`pnpm --dir frontend run build`
- 全量构建：`make build`
- OpenSpec 校验：`openspec validate --change "add-apikey-failure-cooldown" --strict`
- 差异检查：`git diff --check`

## 实施顺序与状态

状态值：`pending`、`in_progress`、`completed`、`blocked`、`failed`。

| 单元 | 小项 | 前置依赖 | 预期与允许修改范围 | 测试/校验 | 状态 | 验证与规格核对 |
|---|---|---|---|---|---|---|
| 1.1 | 配置模型、默认值、服务端校验 | 无 | 后端设置/领域模型 | 配置单测 | completed | `go test -tags=unit ./internal/service -run 'TestChannelMonitorCooldownSettings'`；默认值、严格递增和边界已核对 |
| 1.2 | 设置 GET/PUT/reset、运行时缓存与回退 | 1.1 | 设置服务、管理路由、DTO | handler/service 单测 | completed | 设置 handler/service 目标测试通过；运行时回退逻辑已核对 |
| 1.3 | `use_current_service` 持久化字段与迁移 | 无 | Ent schema、模型、DTO、仓储、迁移 | Ent/迁移测试 | completed | `go generate ./ent` 后目标包编译通过，迁移 `228_channel_monitor_use_current_service.sql` 已核对 |
| 1.4 | 配置与监控开关后端 TDD 测试 | 1.1-1.3 | 后端测试文件 | 目标测试 | completed | `go test -tags=unit ./internal/service ./internal/handler/...` 通过 |
| 2.1 | 监控表单按钮、endpoint 与开关语义 | 1.3 | Channel Monitor 服务/表单 | 后端/前端目标测试 | completed | Channel Monitor 表单和 API 已保留服务端开关并清除手改 endpoint |
| 2.2 | 管理端 API 类型、表单、文案 | 2.1 | 前端 API、视图、i18n | Vitest/typecheck | completed | API 类型、表单和中英文文案已加入；前端依赖缺失待 6.3 环境验证 |
| 2.3 | 签名标记生成、校验、context 注入、转发清理 | 1.1, 2.1 | 后端标记包、网关中间件 | 标记单测 | completed | `go test -tags=unit ./internal/service -run 'TestChannelMonitorProbeMarker'` 通过，消费前删除 header |
| 2.4 | checker 按开关携带标记及伪造测试 | 2.3 | checker、请求测试 | 目标测试 | completed | `TestRunCheckForModel_CurrentServiceProbeAddsSignedMarkerOnly` 通过，普通 endpoint 不携带标记 |
| 3.1 | 监控 context 账号尝试 observer | 2.3 | 网关/服务生命周期 | observer 单测 | completed | observer 仅接受有效 context，`sync.Once` 保证单次终态，取消旁路；目标单测通过 |
| 3.2 | Redis 五档账号冷却原子状态 | 3.1 | Redis store/脚本 | Redis 单测/集成 | completed | miniredis 并发合并、到期推进和迟到成功保护测试通过 |
| 3.3 | 发送前共享冷却守卫与 Redis 降级 | 3.2 | 调度/网关 | 调度测试 | completed | OpenAI/兼容调度候选过滤启用账号键守卫，Redis 错误旁路；目标包编译通过 |
| 3.4 | 统一带标记失败并旁路其他账号类型 | 3.1-3.3 | 网关错误出口 | 集成/回归测试 | completed | |
| 3.5 | 跨分组、并发、恢复、重启、降级测试 | 3.2-3.4 | 后端集成测试 | Redis/服务集成 | completed | |
| 4.1 | 账号临时优先级状态原子更新与缓存同步 | 3.1 | 账号扩展、仓储、缓存 | 仓储单测 | completed | |
| 4.2 | 调度排序使用基准优先级加成 | 4.1 | 调度服务 | 调度单测 | completed | |
| 4.3 | 慢响应加成、恢复、自动周期 | 4.1-4.2 | observer/优先级服务 | 服务测试 | completed | |
| 4.4 | 优先级边界与并发测试 | 4.1-4.3 | 后端测试 | 目标/竞态测试 | completed | |
| 5.1 | 前端配置 API 类型、默认值、映射、reset | 1.2 | 前端 API | API 单测 | completed | |
| 5.2 | 设置页表单、单位、非法值阻止 | 5.1 | SettingsView/组件 | 组件测试 | completed | |
| 5.3 | 中英文文案与反馈/错误展示 | 5.2 | i18n/组件 | 组件测试 | completed | |
| 5.4 | 前端 API/组件/页面测试 | 5.1-5.3 | 前端测试 | Vitest/typecheck | completed | |
| 6.1 | 后端受影响范围、迁移、Redis、竞态 | 1-4 | 测试与修复 | 后端全量/竞态 | completed | |
| 6.2 | 普通请求、账号类型、取消与现有错误回归 | 3-4 | 回归测试 | 后端回归 | completed | |
| 6.3 | 前端检查与构建 | 5 | 前端质量门禁 | lint/typecheck/test/build | completed | |
| 6.4 | 全量验证、OpenSpec、差异与运维说明 | 6.1-6.3 | 文档、tasks、最终核对、快照 | 全量命令 | completed | |

## 规格核对记录

- 1.x：已完成并通过后端目标测试；运行时配置与迁移字段已核对
- 2.x：已完成并通过标记/checker 测试；前端依赖已安装，类型检查和构建通过
- 3.1-3.3：已完成；冷却状态仅按账号 ID，普通 context 旁路
- 3.4-3.5：已完成；通用 Gemini/Anthropic 与 OpenAI 发送出口均接入 observer，Redis 跨分组、并发、五档、重启和降级测试通过
- 4.x：已完成；数据库行锁事务、调度缓存同步、并发上限、基准变更和恢复周期已核对
- 5.x：已完成；API、设置表单、双语文案及页面/API 测试通过
- 6.x：已完成；前端全量通过，后端全量仅有既有 Aliyun Captcha 本地 HTTP 超时，OpenSpec strict、构建和差异检查通过

## 阻塞与决策

- 当前无阻塞；历史同名快照实现属于不同规格，未复用。

## 服务器部署验证

### 管理端状态投影修复

- 发现渠道监控冷却仅存在 Redis 账号级状态，管理端账号 DTO 仍只读取数据库 `temp_unschedulable_*` 字段，导致实际被调度器过滤的账号显示为“正常”。
- 修复 `AccountHandler`：管理端列表和临时不可调度详情统一读取渠道监控冷却状态，并投影到现有 `temp_unschedulable_until/reason` 字段；不修改数据库 `schedulable`，冷却到期自动恢复。
- 新增管理端回归测试，验证账号列表和详情均显示渠道监控冷却。
- 验证：`go test ./internal/handler/admin -run 'TestAccountHandler(Project|List|TempUnschedulable)'` 通过；仓储全量测试仍仅受既有 Aliyun Captcha 本地 HTTP 超时影响。

- 服务器工作区：`/opt/sub2api-staging`，分支 `add-apikey-failure-cooldown-apply-20260828235406`，提交 `e4ce1d846efa96259ce005fbd18db8a95bac7e22`，Git 工作区干净。
- 已从该提交构建镜像 `sub2api:add-apikey-failure-cooldown-e4ce1d846`（摘要 `sha256:3d44f5abf2445220265d236da2edcfcaa0e38e7abb86f01f4a391ca2342aef79`），并通过 Compose 覆盖仅重建应用容器。
- 首次部署错误使用 `deploy/docker-compose.yml`，创建并接入空的 `deploy_postgres_data`、`deploy_redis_data`、`deploy_sub2api_data` 命名卷，导致原业务数据在页面中不可见；原数据未被删除。
- 已将部署恢复为服务器原有的 `deploy/docker-compose.local.yml` 目录挂载：`deploy/data`、`deploy/postgres_data`、`deploy/redis_data`。应用继续运行上述源码镜像；误建命名卷保留且未删除。
- 恢复前备份位于 `/opt/sub2api-recovery-backup-20260829-0705`，包含原目录归档、误建空库导出和 SHA-256（文件校验值），权限为仅 root 可读写。
- 恢复后确认原库包含 2 个用户、28 个账号、3 个 API Key、3 个渠道监控及历史用量数据。
- `curl http://127.0.0.1:3000/health` 返回 HTTP 200 与 `{"status":"ok"}`。
- 既有全量后端测试仅有 Aliyun Captcha 本地 HTTP 测试超时，详见 6.x 验证记录；与本变更无关。

### 持续监测

- 恢复原数据挂载后重新连续监测 10 分钟，每分钟检查一次，共 10 次。健康接口、仪表盘快照、用户趋势、用户排行接口每次均返回 HTTP 200。
- 10 次检查中应用、PostgreSQL、Redis 始终为 `running/healthy`；原库历史用量从 4759 条增长至 4786 条，证明读写持续正常。
- 监测窗口内未发现 panic、fatal、数据库、迁移、字段或数据表缺失等严重日志。监测使用的临时登录会话已撤销，临时响应文件已清理。

### 本次重新部署验证（2026-08-29）

- 首次构建因服务器 Node 默认堆上限 1536 MiB 触发内存溢出；未执行重启，既有服务持续运行。
- 临时将服务器构建阶段 Node 堆上限提高到 4096 MiB 后构建成功，随后仅重建 `sub2api` 应用容器；PostgreSQL、Redis 目录挂载和数据均未变更。
- `ai777-0.06`（ID 29）数据库字段仍为 `schedulable=true` 且 `temp_unschedulable_until=NULL`；Redis 冷却状态为第 5 档，管理端列表投影为 `temp_unschedulable_reason=channel_monitor_cooldown`，详情接口返回 `active=true`。
- 连续 10 分钟每分钟检查一次：健康接口 10/10 返回 HTTP 200，应用、PostgreSQL、Redis 始终 `running/healthy`，严重日志计数均为 0，冷却 TTL 正常递减。
- 临时构建修改已恢复，服务器 Git 工作区保持干净。
