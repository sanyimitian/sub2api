# 实施协调记录

## 基线

- 变更：fix-channel-monitor-failure-cooldown
- Schema：spec-driven
- 源分支：main
- original HEAD：14b9071d6c11d14d4814b4dc9096ece3b8e23c52
- 起始工作区：仅有未跟踪的变更产物目录 openspec/changes/fix-channel-monitor-failure-cooldown/
- 实施前备份工作树：/home/pan/桌面/code/fix-channel-monitor-failure-cooldown-实施前备份-20260907205155
- 实施前备份分支：fix-channel-monitor-failure-cooldown-pre-apply-20260907205155
- 质量命令：go test ./...、go test -race ./...、golangci-lint run ./...、go build ./cmd/server、openspec validate --changes --strict、git diff --check

## 串行实施顺序与依赖

| 单元 | 小项 | 前置依赖 | 预期修改范围 | 目标测试 | 状态 | 验证与规格核对 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 1.1-1.4 | 无 | 监控账本模型、Redis 存储、测试 | 账本单元/Redis 测试 | completed | 账本模型、Lua 原子脚本、TTL、重复登记/完成、失败优先和不可用测试通过；规格核对通过 |
| 2 | 2.1-2.4 | 1 | observer、网关出口、handler 测试 | 受影响网关测试 | completed | 目标账号旁路、Begin/Finish 全协议出口、传输终态 sticky 和成功延后测试通过；规格核对通过 |
| 3 | 3.1-3.5 | 1、2 | checker 最终归因、服务测试 | checker/服务测试 | completed | 独立 request ID、最终失败/非失败归因、重复回调、2xx/空响应/取消和模型隔离测试通过；规格核对通过 |
| 4 | 4.1-4.4 | 1、3 | 冷却回归、管理端投影、故障降级测试 | 冷却/管理端/Redis 故障测试 | completed | 现有冷却及管理端投影回归通过；Redis 登记/归因失败旁路测试通过；规格核对通过 |
| 5 | 5.1-5.4 | 1-4 | 集成、全量验证、任务与快照事务 | 全量测试、构建、竞态、严格校验 | completed | 受影响测试与目标竞态测试通过；完整测试仅实施前已存在的 3 个 Aliyun Captcha 本地 HTTP 超时失败，完整竞态仅既有 content moderation/Gin 并行测试竞态失败；全量 go vet、编译构建、空测试编译、严格校验和 diff-check 通过；golangci-lint 未安装；最终规格核对和快照事务均已完成 |

## TDD 与质量门禁

每个适用行为小项均执行 RED（先测试并验证失败）→ GREEN（最小实现并验证通过）→ REFACTOR（再次验证）。纯文档/校验项执行适用命令。每个单元完成后记录目标测试、受影响范围验证、轻量规格核对和状态；未通过不得勾选任务。

## 阻塞与规格核对

- 当前验证限制：`go test ./...` 的 3 个 Aliyun Captcha 测试在实施前备份与当前工作树均因本地 HTTP 等待响应头超时；`go test -race ./...` 的失败集中在既有 content moderation 全局配置和 Gin `SetMode` 并行测试；`golangci-lint` 未安装。受影响包、目标竞态、构建、全量 go vet、空测试编译、严格校验和 diff-check 均通过。
- 最终规格核对：通过。账本按 monitor/request 隔离并设置 TTL；observer 只登记有效签名下的普通 API Key 非号池账号；传输失败状态 sticky 且成功延后到最终归因；最终 `failed/error` 对所有目标尝试执行失败冷却，其他状态仅保留传输失败、按代次清理成功、未知不清理；Redis 失败请求级旁路且记录不含凭据的诊断；现有账号级冷却键、阶梯、并发合并、迟到成功保护和管理端原因投影保持兼容。
- 快照事务：已完成。快照工作树 `/home/pan/桌面/code/fix-channel-monitor-failure-cooldown-apply-20260907233535`，分支 `fix-channel-monitor-failure-cooldown-apply-20260907233535`，本地备份提交已创建；源工作区改动保留未提交。已从 original HEAD 生成并校验 `/home/pan/桌面/code/fix-channel-monitor-failure-cooldown-apply-20260907233535.patch`，补丁反向应用检查通过。
