## ADDED Requirements

### Requirement: 仅对当前服务监控的普通 API Key 账号启用归因
系统 MUST 只对带有效渠道监控标记的请求启用本能力，并且只接受 `AccountTypeAPIKey` 且非号池模式的实际账号。OAuth、Setup Token、Service Account、号池账号、直连外部 endpoint 的监控和普通请求 MUST 完全旁路；没有实际选中账号时 MUST 不强行归属。

#### Scenario: 当前服务监控选中普通 API Key
- **WHEN** 一个带有效监控标记的请求在网关选中普通 API Key 账号
- **THEN** 系统 MUST 为该账号登记一次监控请求尝试
- **AND** 该账号 MUST 有资格在最终失败时进入渠道监控冷却链

#### Scenario: 非目标账号旁路
- **WHEN** 请求选中 OAuth、Setup Token、Service Account 或号池账号
- **THEN** 系统 MUST 不为该账号登记可冷却尝试
- **AND** 本能力 MUST 不改变该账号现有冷却、调度和错误处理

#### Scenario: 没有选中账号
- **WHEN** 监控请求在账号选择前失败，或请求没有经过账号网关
- **THEN** 系统 MUST 不创建账号尝试记录
- **AND** 最终失败 MUST 不向任意账号写入渠道监控冷却

### Requirement: 监控请求账本必须记录实际账号尝试
系统 MUST 使用签名监控标记中的 `request_id` 作为一次模型监控请求的唯一关联标识，并为每个实际选中的目标 API Key 账号记录账号 ID、尝试终态和最终归因状态。主模型和每个附加模型 MUST 使用不同的 `request_id`；账本 MUST 通过有限 TTL 自动清理。

#### Scenario: 换号保留前序账号
- **WHEN** 一个模型监控请求先选中账号 A 并传输失败，随后重试选中账号 B
- **THEN** 同一个 `request_id` 的账本 MUST 同时包含账号 A 和账号 B 的尝试
- **AND** 账号 A 的失败终态 MUST 不得被账号 B 的后续结果覆盖

#### Scenario: 各模型账本隔离
- **WHEN** 同一渠道监控同时检测主模型和附加模型
- **THEN** 两个模型 MUST 使用不同的 `request_id`
- **AND** 一个模型的最终状态 MUST 不得归因或完成另一个模型的账号账本

#### Scenario: 账本自动过期
- **WHEN** 监控请求在最终归因前中断且账本超过保留 TTL
- **THEN** 账本 MUST 自动过期
- **AND** 系统 MUST 不把过期账本中的账号归因给之后的请求

### Requirement: 最终 CheckResult.failed/error 必须触发全量账号冷却
系统 MUST 以同一 `request_id` 对应的最终 `CheckResult.status` 作为请求级失败判定。状态为 `failed` 或 `error` 时，系统 MUST 将账本中所有实际尝试过且符合目标类型的账号各自写入一次渠道监控冷却链，不得根据 HTTP 状态码、错误文本、模型、平台或传输是否曾返回 2xx 进行缩减。

#### Scenario: HTTP 2xx 但挑战校验失败
- **WHEN** 网关向上游返回 HTTP 2xx，但 checker 因挑战值不匹配将最终状态设为 `failed`
- **THEN** 系统 MUST 将该请求实际选中的每个普通 API Key 账号写入渠道监控冷却链
- **AND** 不得因为网关没有返回传输错误而跳过冷却

#### Scenario: 客户端超时
- **WHEN** 监控客户端超时并将最终 `CheckResult.status` 设为 `error`
- **THEN** 系统 MUST 冷却该请求账本中所有实际尝试过的目标 API Key 账号
- **AND** 冷却写入 MUST 不依赖上游是否已经返回响应头

#### Scenario: 最终失败没有目标账号
- **WHEN** 最终状态为 `failed` 或 `error`，但账本中没有目标 API Key 账号
- **THEN** 系统 MUST 不创建任何账号冷却事件

### Requirement: 传输失败账号不得因后续成功而丢失
网关明确记录为传输失败的目标账号 MUST 在最终归因时进入冷却，即使同一监控请求后续换号成功或最终状态不是 `failed/error`。传输成功的账号只有在最终状态不是 `failed/error` 且携带匹配观测代次时才可执行现有成功清理；未知终态 MUST 不主动清理。

#### Scenario: 前序失败后续成功
- **WHEN** 账号 A 传输失败，账号 B 随后成功，最终 checker 状态为 `operational`
- **THEN** 账号 A MUST 进入渠道监控冷却链
- **AND** 账号 B MAY 按现有生成代次规则清理其可清理状态

#### Scenario: 单账号最终挑战失败
- **WHEN** 账号 A 的网关传输成功但最终 checker 状态为 `failed`
- **THEN** 账号 A MUST 按最终失败归因写入冷却
- **AND** 网关传输成功 MUST 不得提前清除或阻止该冷却事件

### Requirement: 冷却写入必须幂等并沿用现有阶梯
系统 MUST 以请求账本最终归因状态和账号冷却存储的原子操作保证重复回调、并发完成和重试不会重复推进冷却阶梯。冷却 MUST 沿用现有账号级键、配置阶梯、活动窗口合并、到期后推进、末档保持、生成代次保护和成功不提前缩短活动冷却的规则。

#### Scenario: 重复最终回调
- **WHEN** 同一个 `request_id` 的最终 `CheckResult` 被归因器处理两次
- **THEN** 第二次处理 MUST 识别已完成状态并不重复增加账号冷却阶梯

#### Scenario: 并发失败合并
- **WHEN** 多个监控请求在同一账号活动冷却期间并发完成失败归因
- **THEN** 系统 MUST 合并为现有冷却事件
- **AND** 不得因为请求账本各自独立而重复推进阶梯或缩短截止时间

#### Scenario: 迟到成功保护
- **WHEN** 旧请求的成功回调晚于同一账号的新失败事件到达
- **THEN** 旧成功 MUST 不得删除或缩短新失败创建的冷却事件

### Requirement: Redis 不可用时本次请求完全跳过冷却
系统发现 Redis 账本、脚本或冷却读写不可用后 MUST 将该请求标记为不做冷却处理；本次请求后续 MUST 不再尝试任何冷却写入，不得使用进程内兜底状态，不得暂停监控或阻塞原始请求，并 MUST 记录包含请求 ID、监控 ID 和失败阶段的结构化错误日志或指标，且不得记录 API Key 内容。

#### Scenario: 尝试登记时 Redis 不可用
- **WHEN** 网关登记账号尝试时 Redis 不可用
- **THEN** 原始监控请求 MUST 继续按原有流程执行
- **AND** 该请求最终不得向任何账号写入渠道监控冷却

#### Scenario: 最终归因时 Redis 不可用
- **WHEN** 账号尝试已记录但 checker 最终归因时 Redis 读写失败
- **THEN** 系统 MUST 放弃该请求的全部冷却处理
- **AND** 原始 `CheckResult` 返回语义 MUST 不受影响

### Requirement: 管理端继续投影渠道监控冷却来源
账号列表和详情中的冷却状态 MUST 能识别由本能力创建的账号级冷却，并显示现有原因值 `channel_monitor_cooldown`。本变更 MUST 不新增或改变“冷却阶梯（分钟）”等管理配置契约。

#### Scenario: 展示渠道监控冷却
- **WHEN** 目标 API Key 账号因监控最终失败进入冷却链
- **THEN** 管理端账号列表和详情 MUST 显示其处于渠道监控冷却及对应原因

#### Scenario: 非监控冷却原因保持
- **WHEN** 账号因其他既有机制处于冷却
- **THEN** 管理端 MUST 保留原有冷却来源和展示语义
- **AND** 本能力 MUST 不覆盖其他来源的状态
