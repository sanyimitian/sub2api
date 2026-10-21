# 首字慢自动切换诊断日志

用于排查请求为什么没有触发探测或切换。诊断只增加可观测性，不改变现有统计、阈值、共享池和切换规则。

## 查找方法

在应用标准输出或已配置的应用日志文件中搜索 `openai.latency_diagnostic`（首字慢诊断事件），再用用量记录对应的 `request_id`（服务端请求编号）筛选。也可按 `client_request_id`（客户端请求编号）或 `scope`（内部会话范围的摘要）关联。

HTTP（普通请求）的诊断对应本次请求。WebSocket（长连接）使用连接请求编号，并结合 `turn`（轮次）定位连接内的具体请求。后台探测继承触发请求的关联信息，`probe_id`（探测编号）可以关联触发判断与探测过程。

探测完成、待切换、已请求重连、切换完成这四种原有状态事件也增加请求编号和分组编号。诊断日志不写入运维数据库，不展示为新的切换记录状态；请查看应用日志。

## 日志级别

- 首字已达到慢阈值的请求：通常使用 `info`（普通信息），包括因工具回传、输出速度、连续次数或状态阻止而跳过的请求。
- 正常快速请求、预热、功能关闭和不支持的 HTTP 入口：使用 `debug`（调试信息），需要启用调试级别才能看到。
- 状态读取、解析、写入或并发更新失败：使用 `warn`（警告）。
- 日志沿用现有输出级别和采样配置。若需逐笔核对，需确保普通信息级别已开启，并检查是否启用了日志采样。

每次进入统计判断通常只输出一条最终诊断。共享池等待只记录开始等待及退出等节点，不按每次轮询刷日志。

## 判断字段

| 字段 | 含义 |
| --- | --- |
| `stage` | 判断阶段：`entry` 入口、`session` 会话建立、`observe` 请求完成统计、`state` 状态读写、`probe` 探测 |
| `reason` | 本次最终决定的固定原因码 |
| `user_id`、`api_key_id`、`group_id`、`account_id` | 用户、密钥记录、分组和本系统账号编号；不包含密钥内容 |
| `request_kind`、`user_request` | 输入分类及是否按现有规则计为用户请求；按最后一项输入分类，不记录正文 |
| `first_token_ms`、`slow_threshold_ms` | 实测首字毫秒数、当前首字阈值 |
| `first_token_threshold_met` | 实测首字是否大于或等于首字阈值 |
| `output_tps`、`output_tps_known` | 输出词元数除以请求总耗时得到的每秒输出速度，以及该速度是否可用 |
| `min_output_tps`、`output_tps_threshold_met` | 配置的最低速度；未配置时视为通过，配置后要求速度已知且严格低于最低值 |
| `consecutive_required` | 配置要求的连续慢次数 |
| `slow_count_before`、`slow_count_after` | 状态更新前后计数；达到次数触发探测后，原实现将计数清零；写入失败时不声称计数已保存 |
| `threshold_decision` | 对可计数用户请求的速度判断；`thresholds_met` 表示速度门槛满足，连续次数及冷却等判断查看最终原因码 |
| `state_updated` | 状态是否成功写入；等待或冷却阻止时也会为假，不一定表示故障 |
| `cooldown_remaining_ms`、`pending_remaining_ms`、`probe_remaining_ms` | 冷却、待切换及正在探测的剩余有效时间，最低为零 |
| `target_account_id`、`active_probe_id` | 已保留目标账号、当前状态中的探测编号 |
| `state_key_hash`、`operation`、`attempt` | 状态键的摘要、失败操作及尝试次数，用于排查缓存问题 |

## 常见原因码

| 原因码 | 含义 |
| --- | --- |
| `tool_output_excluded` | 按现有规则，工具结果回传不参与连续慢次数统计；两个速度门槛满足也会跳过 |
| `prewarm_excluded`、`empty_input`、`non_user_request` | 预热、空输入或其他非用户请求，未参与统计 |
| `first_token_below_threshold`、`first_token_unknown` | 首字未达到慢阈值，或缺少首字测量值 |
| `output_tps_not_below_minimum`、`output_tps_unknown` | 输出速度未低于配置最低值，或无法计算输出速度 |
| `consecutive_not_reached` | 本次满足速度条件，但连续慢次数尚不足 |
| `cooldown_active`、`switch_pending`、`probe_in_progress` | 冷却中、已经有待切换目标、已经有探测在进行；多个状态同时有效时优先显示待切换、探测、冷却 |
| `request_failed`、`request_unsuccessful`、`result_missing` | 转发报错、上游未成功完成或结果缺失 |
| `recovery_request_unsuccessful` | 接管请求未成功，不能确认切换完成 |
| `session_hash_missing`、`session_scope_missing` | 缺少建立切换状态所需的会话标识 |
| `session_dependency_missing`、`state_cache_unavailable` | 缺少服务、身份、设置依赖或可用状态缓存 |
| `account_missing`、`turn_classification_missing` | 未获得可统计的账号或轮次分类 |
| `feature_disabled`、`http_entry_excluded`、`execution_lane_or_kind_excluded` | 功能关闭，或入口、执行通道、请求类别不受支持 |
| `state_update_failed` | 请求统计状态未能更新，结合相同请求的状态阶段日志定位 |
| `state_read_failed`、`state_decode_failed`、`state_write_failed`、`state_conflict_exhausted` | 状态读取、解析、写入失败，或并发更新重试耗尽 |
| `probe_triggered` | 已满足条件并成功写入探测状态；后台仍会先尝试复用共享候选 |
| `candidate_reserved` | 已保留候选；`reused_probe_result=true` 表示复用已有探测结果，并不表示切换已经成功 |
| `shared_pool_wait`、`shared_pool_wait_ended` | 等待共享池候选或正在运行的共享探测，或等待上下文已结束 |
| `new_probe_started` | 已取得共享探测资格，即将新开探测 |
| `probe_account_lookup_failed`、`probe_channel_pricing_restricted`、`probe_no_eligible_accounts` | 账号读取失败、渠道价格限制，或当前账号／候选账号不符合探测条件 |
| `pending_state_not_updated` | 候选已找到，但待切换状态未更新；检查状态是否被改变以及缓存失败日志 |
| `probe_feature_disabled`、`probe_not_claimed`、`probe_interrupted`、`probe_rounds_exhausted` | 探测中功能关闭、未取得探测资格、探测中断或轮次耗尽 |

诊断不记录对话正文、工具结果内容、密钥、完整请求头、原始缓存内容或原始错误文本。错误只记录类型及超时／取消标志。新增日志只对部署后发生的请求有效，不能补回历史请求缺失的信息。
