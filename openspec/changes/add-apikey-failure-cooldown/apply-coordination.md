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
