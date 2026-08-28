## ADDED Requirements

### Requirement: 渠道监控开关决定适用范围
系统 MUST 持久化每条渠道监控的 `use_current_service` 开关。只有管理员在新增或编辑监控时点击“使用当前服务”并成功保存后，该监控请求才可进入本能力；系统 MUST NOT 通过 endpoint 字符串、域名或 API Key 类型推断开关。

#### Scenario: 点击并保存后启用
- **WHEN** 管理员点击“使用当前服务”并保存一条渠道监控
- **THEN** 系统 MUST 将 `use_current_service` 保存为 `true`
- **AND** 后续该监控发出的探测请求 MUST 带有效的监控标记

#### Scenario: 未点击时不启用
- **WHEN** 管理员手工填写与当前服务相同的 endpoint 但没有点击“使用当前服务”
- **THEN** 系统 MUST 将 `use_current_service` 保持为 `false`
- **AND** 该监控请求 MUST NOT 进入本能力

#### Scenario: 编辑时保留真实开关
- **WHEN** 管理员打开已保存的监控编辑对话框但不点击开关
- **THEN** 表单 MUST 展示并提交服务端已有的 `use_current_service` 值

### Requirement: 监控标记必须可信且只作用于网关上下文
系统 MUST 为启用开关的探测请求生成带过期时间和签名的内部标记，并在网关入口验证后写入请求 context。无效、过期或缺失标记的请求 MUST 按普通请求处理；已验证标记 MUST NOT 被转发给外部上游。

#### Scenario: 有效标记进入监控上下文
- **WHEN** 网关收到签名正确且未过期的渠道监控标记
- **THEN** 系统 MUST 将 monitor ID 和请求 ID 写入 context
- **AND** 账号尝试 observer MUST 能读取该上下文

#### Scenario: 伪造标记不触发冷却
- **WHEN** 外部请求携带缺少签名或签名不匹配的监控 header
- **THEN** 系统 MUST 忽略该标记
- **AND** 请求的失败 MUST NOT 写入渠道监控冷却状态

### Requirement: 监控失败统一按账号冷却
带有效监控标记的每次账号探测失败 MUST 进入账号级冷却链，不区分 HTTP 状态、网络错误、空响应、超时、模型、平台或 API Key 细节。冷却键 MUST 只包含账号 ID，且状态 MUST 跨账号所属分组共享。

#### Scenario: 任意失败进入冷却
- **WHEN** 带监控标记的探测返回 HTTP 错误、网络错误、空响应或超时
- **THEN** 系统 MUST 为实际选中的账号创建或合并一个渠道监控冷却事件
- **AND** 系统 MUST 使用账号级键，不得创建模型级或故障类型级键

#### Scenario: 普通请求完全旁路
- **WHEN** 普通 API Key 请求或未点击当前服务的监控请求失败
- **THEN** 系统 MUST NOT 创建渠道监控冷却事件
- **AND** 现有普通请求错误处理 MUST 保持不变

#### Scenario: 冷却跨分组共享
- **WHEN** 同一账号属于多个分组且其一个带标记的探测失败
- **THEN** 该账号 MUST 在所有分组的后续选择中被冷却状态排除

### Requirement: 五档冷却阶梯和并发合并
系统 MUST 使用一个统一的五档账号冷却阶梯，默认时长依次为 2、5、30、60、120 分钟。活动冷却期间的并发失败 MUST 合并为同一个事件；冷却到期后的下一次失败才推进到下一档；第五档之后 MUST 保持第五档。

#### Scenario: 默认阶梯推进
- **WHEN** 同一账号在冷却到期后连续产生五次独立的带标记失败事件
- **THEN** 冷却时长 MUST 依次为 2、5、30、60、120 分钟

#### Scenario: 活动窗口内合并
- **WHEN** 100 个并发探测同时失败且首个失败建立的冷却仍有效
- **THEN** 系统 MUST 只增加一次阶梯事件
- **AND** 冷却截止时间 MUST 不被后续失败缩短或重复推进

#### Scenario: 末档保持
- **WHEN** 同一账号在第五档冷却到期后再次失败
- **THEN** 系统 MUST 继续使用 120 分钟（或当前配置的第五档）
- **AND** 不得溢出或回到第一档

#### Scenario: 成功清理账号状态
- **WHEN** 带标记的探测获得有效成功
- **THEN** 系统 MUST 清除该账号的连续失败阶梯状态
- **AND** 已经生效的冷却截止时间 MUST NOT 被成功结果提前缩短

### Requirement: 冷却策略可配置并可恢复默认
系统 MUST 提供版本化渠道监控冷却配置，字段包括五档 `cooldown_minutes`、`slow_response_threshold_seconds`、`priority_increment`、`max_priority_increase` 和 `priority_auto_recovery_seconds`。配置 MUST 支持 GET、PUT 和恢复默认操作，非法配置 MUST 被拒绝，运行时 MUST 无需重启即可使用最近一次有效配置。

默认配置 MUST 为：冷却 `[2,5,30,60,120]` 分钟、慢响应阈值 `12` 秒、优先级增量 `1`、最多增加 `3`、自动恢复 `3600` 秒。

#### Scenario: 读取默认配置
- **WHEN** 系统尚未保存渠道监控冷却配置
- **THEN** GET 接口和运行时 MUST 返回并使用上述默认值

#### Scenario: 保存合法配置后即时生效
- **WHEN** 管理员保存五个正整数且严格递增的阶梯及合法的慢响应参数
- **THEN** PUT MUST 返回规范化配置
- **AND** 后续探测 MUST 使用新配置而无需重启

#### Scenario: 拒绝非法配置
- **WHEN** 配置阶梯不是五项、不是严格递增、含非正数，或其他字段超出服务端边界
- **THEN** PUT MUST 返回校验错误
- **AND** 最近一次有效配置 MUST 保持不变

#### Scenario: 恢复默认值
- **WHEN** 管理员点击设置页的“恢复默认值”并确认
- **THEN** 系统 MUST 持久化并返回默认配置
- **AND** 页面 MUST 显示 2、5、30、60、120 分钟、12 秒、+1、+3、1 小时对应值

#### Scenario: 配置存储暂时不可用
- **WHEN** 运行时读取设置存储失败
- **THEN** 系统 MUST 优先使用最近一次有效配置
- **AND** 从未成功读取时 MUST 使用内置默认配置

### Requirement: 慢响应临时增加账号优先级
带有效监控标记的探测响应耗时超过配置阈值时，系统 MUST 将实际账号的有效优先级增加一个配置的 `priority_increment`，每个临时周期最多增加 `max_priority_increase` 次。第一次增加时 MUST 记录起始时间，后续增加不得重置起始时间。

#### Scenario: 超阈值逐次增加
- **WHEN** 同一账号连续完成三次超过 12 秒（或当前配置阈值）的探测，且期间未恢复
- **THEN** 账号有效优先级 MUST 依次增加 +1、+2、+3（或对应配置增量）
- **AND** 增加次数 MUST 不超过配置上限

#### Scenario: 达到上限后不再增加
- **WHEN** 账号已达到 `max_priority_increase`
- **AND** 后续探测仍超过阈值
- **THEN** 系统 MUST 保持当前临时加成，不得继续增加

#### Scenario: 正常响应立即恢复
- **WHEN** 账号下一次带标记探测耗时不超过阈值
- **THEN** 系统 MUST 立即清除临时加成并恢复探测前的基准优先级
- **AND** 增加次数和起始时间 MUST 一并清理

#### Scenario: 一小时后自动恢复
- **WHEN** 从第一次增加优先级开始已经达到 `priority_auto_recovery_seconds`（默认 1 小时）
- **THEN** 系统 MUST 自动清除临时加成并恢复基准优先级
- **AND** 必须清理该临时周期的计数和起始时间

#### Scenario: 自动恢复后再次超时
- **WHEN** 临时周期已自动恢复且当前新的探测再次超过阈值
- **THEN** 系统 MUST 将当前探测作为新的第一次增加
- **AND** MUST 从当前时间重新开始自动恢复计时

### Requirement: 优先级状态持久化与并发边界
临时优先级状态 MUST 在进程重启后可恢复，并 MUST 与账号调度快照同步。并发探测更新时，临时加成不得超过配置上限；管理员修改基准优先级时，恢复操作 MUST 回到新的基准值。

#### Scenario: 重启后继续临时状态
- **WHEN** 进程在账号已产生临时加成后重启
- **THEN** 调度器 MUST 从持久化状态恢复有效优先级
- **AND** 到期时间判断 MUST 继续基于第一次增加时间

#### Scenario: 并发更新不超上限
- **WHEN** 多个监控请求并发报告同一账号慢响应
- **THEN** 持久化后的增加次数 MUST 不超过配置上限
- **AND** 调度缓存中的有效优先级 MUST 与持久化结果一致

#### Scenario: 基准优先级变更
- **WHEN** 临时加成期间管理员把账号基准优先级改为新值
- **THEN** 系统 MUST 以新基准值加当前临时加成为有效优先级
- **AND** 临时状态清理后 MUST 恢复到新基准值

### Requirement: 其他账号与生命周期行为保持兼容
本能力 MUST 只影响带有效渠道监控标记的账号尝试。客户端取消、普通请求、未使用当前服务的监控和非账号探测失败 MUST NOT 因本能力产生冷却或优先级变化；现有调度、重试和错误返回语义 MUST 保持。

#### Scenario: 客户端取消不触发
- **WHEN** 客户端在探测上游完成前主动取消请求
- **THEN** 系统 MUST NOT 因客户端取消新增渠道监控冷却
- **AND** 系统 MUST NOT 增加账号临时优先级

#### Scenario: 其他账号类型不受影响
- **WHEN** 请求使用 OAuth、Setup Token、API Key 号池或其他非目标账号类型
- **THEN** 系统 MUST 不套用本能力的冷却和优先级规则
- **AND** 原有账号类型逻辑 MUST 继续生效
