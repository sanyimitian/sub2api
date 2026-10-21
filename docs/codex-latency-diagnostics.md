# 首字慢自动切换诊断日志

用于排查请求为什么没有触发切换。

当前版本提供默认关闭的[统一备用会话池](openai-prepared-sessions.md)总开关。关闭时保留原有按需探测和共享复用，因此下文探测阶段及原因码仍适用；开启时由后台独立补充，慢请求只领取候选，不启动独立探测。备用池模式使用 shared_candidate_reserved（已领取共享候选）、shared_pool_empty_or_ineligible（无适用共享候选）、candidate_reservation_in_progress（正在领取候选）；该模式下 ProbeID / ProbeUntil 仅用于短暂的领取互斥。

接管完成额外校验 candidate_not_committed（未独占候选）与 sent_route_mismatch（实际发送线路不匹配）。旧请求晚到会记录 stale_route_result（过期线路结果），不会覆盖已建立的新绑定。

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

重连恢复、目标账号选择和待切换会话标识应用使用 `info`（普通信息）；已完成切换后的日常会话标识应用仍使用 `debug`（调试信息）。恢复请求的完成判断即使首字很快也保留普通信息级别，缓存写入失败仍保留警告级别。

## 判断字段

| 字段 | 含义 |
| --- | --- |
| `stage` | 判断阶段：`entry` 入口、`session` 会话建立、`reconnect` 重连恢复、`selection` 目标账号选择、`route` 上游会话标识应用、`observe` 请求完成统计、`state` 状态读写、`probe` 探测 |
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
| `pool_key_hash`、`pool_model` | 共享池键的摘要及池模型；跨分组请求可按同一池摘要关联 |
| `switch_wait_seconds`、`probe_reuse_seconds` | 本次探测读取的待切换等待时间、候选保留时间，单位秒 |
| `candidate_count`、`expired_candidate_count`、`ineligible_candidate_count` | 池快照的总候选数、已过期数、未过期但不符合当前权限／模型／会话标识要求的数量 |
| `eligible_candidate_count` | 未过期且通过权限／模型／会话标识检查的候选数；仍可能被预占、已承诺独占或速度不合格 |
| `available_candidate_count`、`leased_candidate_count`、`committed_candidate_count`、`invalid_latency_candidate_count` | 上述候选中可领取、有效租约预占、已承诺独占、速度不合格的互斥分类数量；不等于成功切换次数 |
| `shared_probe_remaining_ms`、`next_candidate_expiry_ms`、`next_lease_expiry_ms` | 相对池快照时刻，共享探测剩余时间、最近候选过期时间和最近有效预占到期时间；零表示无对应项或已结束 |
| `wait_duration_ms`、`wait_outcome`、`pool_snapshot_age_ms` | 实际等待耗时、退出原因、最后一次池快照距日志输出的时间；退出统计使用已有快照，不额外访问缓存 |
| `target_result_id`、`upstream_session_hash` | 候选结果编号及已应用上游会话标识的摘要；不记录原始上游会话标识 |
| `reconnect_requested`、`recovering`、`recovery_confirmed` | 状态中是否发起过重试、当前请求是否进入恢复路径、上下文是否通过当前实现的确认 |
| `switch_completion_decision` | 独立于慢请求阈值的完成判定，合并在原有统计事件中；避免被后续冷却或首字判断掩盖 |
| `completion_target_account_id`、`completion_target_result_id` | 完成判定当时读取到的目标；与后续统计读取的目标字段分开，避免成功清理后丢失关联 |

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
| `shared_pool_wait`、`shared_pool_wait_ended` | 等待共享池候选或正在运行的共享探测，以及这次等待退出；每次等待各记录一次，退出包含取消、超时、领取候选、状态变化或取得新探测资格 |
| `new_probe_started` | 已取得共享探测资格，即将新开探测 |
| `probe_account_lookup_failed`、`probe_channel_pricing_restricted`、`probe_no_eligible_accounts` | 账号读取失败、渠道价格限制，或当前账号／候选账号不符合探测条件 |
| `pending_state_not_updated` | 候选已找到，但待切换状态未更新；检查状态是否被改变以及缓存失败日志 |
| `probe_feature_disabled`、`probe_not_claimed`、`probe_interrupted`、`probe_rounds_exhausted` | 探测中功能关闭、未取得探测资格、探测中断或轮次耗尽 |
| `pending_expired`、`reconnect_non_user_request` | HTTP 待切换目标已过期，或当前请求不是用户消息而跳过恢复判断；后者可能为工具回传 |
| `reconnect_context_rejected`、`reconnect_input_empty`、`reconnect_state_not_updated` | HTTP 恢复上下文不符合要求、有效输入为空、或重试状态未成功建立 |
| `upstream_session_applied`、`upstream_session_not_applied` | 已在请求上下文设置目标／保留会话标识，或已有路由状态却未能应用；应用不等于已向上游发送，也不等于接管成功 |
| `target_account_selected`、`target_account_unavailable`、`target_selection_failed`、`target_slot_not_acquired` | 恢复时目标账号已选中、没有选到账号、选择报错、未取得并发槽位；同时记录实际选择账号是否匹配目标 |

## 定位长时间未完成

1. 用原有“已请求重连”事件的 `conversation_id`（会话摘要，与诊断中的 `scope` 对应）串联请求；HTTP 和 WebSocket 的 `openai.ws_latency_reconnect_received`（已进入恢复判断）沿用原事件名，补充服务端／客户端请求编号、分组及目标结果编号，不另记重复事件。“待切换”和“已请求重连”原事件也补充目标结果编号，便于关联到同一个候选。
2. 若出现 `reconnect_non_user_request`（非用户消息跳过恢复），随后同一请求出现 `upstream_session_applied`（会话标识已应用）且 `recovering=false`，说明代码跳过了恢复判断却应用了会话标识；不能把这类请求解释成“客户端没有请求”。
3. 请求结束后的 `switch_completion_decision`（完成判定）分别说明：`not_evaluated` 未进入判断、`not_recovering` 未进入恢复、`context_unconfirmed` 上下文未确认、`non_user_request` 非用户消息、`request_unsuccessful` 请求未成功、`target_account_mismatch` 目标账号不匹配、`upstream_session_missing` 缺少会话标识、`candidate_lease_missing` 缺少候选租约、`state_update_failed` 状态更新失败，或 `completed` 已完成。成功的首字时间和完整耗时继续看原有 `first_token_ms` 与 `duration_ms`，不增加另一条完成日志。
4. 若共享池可领取数为零、预占数大于零，检查租约到期时间和 `wait_outcome`（等待结果）。`deadline_exceeded` 表示等待超时，`canceled` 表示上下文取消，`candidate_reserved` 表示等到了候选，`state_changed` 表示会话状态变化，`new_probe_started` 表示离开等待并取得新探测资格，`probe_not_claimed` 表示其他未取得资格的退出。快照不保证在输出日志时仍完全一致。

这些日志只补充证据，不改变工具回传分类、候选预占、重试或完成判定规则；日志缺失仍可能受级别、采样或不同会话标识影响，不能单凭缺少恢复事件断言客户端没有重试。

诊断不记录对话正文、工具结果内容、密钥、完整请求头、原始缓存内容或原始错误文本。错误只记录类型及超时／取消标志。新增日志只对部署后发生的请求有效，不能补回历史请求缺失的信息。

备用池总开关开启后的后台补充、候选领取与接管失败日志，详见 [低延迟备用会话池诊断](openai-prepared-sessions.md#诊断事件)。其中探测成功、实际入池、预占、用户接管成功分别记录，不能用探测完成次数代替切换成功次数。
