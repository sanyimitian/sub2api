# 模型评测公开结果接口（v2）

```http
GET https://你的站点/api/v1/model-benchmarks/public-results
Accept: application/json
```

无需登录、API Key 或 Cookie。允许跨域 GET/OPTIONS；请使用 `credentials: 'omit'`。
只包含本站管理员定时任务中开启「结果是否开放展示」的已结束结果，所有任务合计最多返回最新 20 条。
按结束时间倒序排列，结束时间相同时按记录入库顺序倒序；不足 20 条时返回全部，暂无结果时返回空列表。
新任务可能尚无结果。暂停任务仍可导出已公开成绩；关闭公开或删除任务后停止导出。
手动测试、用户测试、外站导入数据均不在此接口中。响应包含完整测试网址，不含凭据、模型答案或原始错误。

响应示例：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "version": 2,
    "generated_at": "2026-11-12T12:00:00+00:00",
    "limit": 20,
    "items": [
      {
        "id": "测试记录ID",
        "schedule_id": "管理员定时任务ID",
        "schedule_name": "每日模型测试",
        "base_url": "https://api.example.com/v1",
        "model": "example-model",
        "effort": "medium",
        "status": "completed",
        "created_at": "2026-11-12T01:00:00+00:00",
        "finished_at": "2026-11-12T01:08:00+00:00",
        "total": 30,
        "completed": 30,
        "passed": 27,
        "pass_rate": 90,
        "categories": {
          "coding": { "total": 20, "completed": 20, "passed": 18 },
          "math": { "total": 5, "completed": 5, "passed": 5 },
          "reasoning": { "total": 5, "completed": 5, "passed": 4 }
        },
        "suite_day": "2026-11-12",
        "suite_revision": 1,
        "suite_fingerprint": "题库SHA256指纹",
        "elapsed_seconds": 480,
        "usage": { "input_tokens": 22000, "output_tokens": 38000, "total_tokens": 60000 },
        "api_errors": 0,
        "api_error_details": [],
        "grading_errors": 0,
        "incomplete_responses": 0
      }
    ]
  }
}
```

`items` 是扁平的结果数组，每条带所属任务的 `schedule_id` 和 `schedule_name`。
`status` 为 `completed`、`failed`、`cancelled` 或 `interrupted`。
仅 `completed` 有最终 `pass_rate`（0–100 的数字），其他状态为 `null`，不能显示为 0 分。
`effort` 为 `low`、`medium`、`high` 或 `xhigh`。只有相同题库指纹和推理强度的结果适合直接比较。
关闭公开后客户端应在下次成功获取时替换整个列表，不能只追加结果。
`usage` 是本次测试汇总的 Token 用量；`api_errors`（接口错误）、`grading_errors`（评分异常）和 `incomplete_responses`（响应未完成）分别提供数量，不返回原始错误。旧版本缺少 `incomplete_responses` 时按 0 展示。响应未完成包括上游额度截断、内容过滤及无最终答案，不作为答错，也不生成最终通过率。

可选字段 `api_error_details` 提供安全的接口错误分组摘要，例如 `[{"code":"http_error","http_status":524,"count":2}]`，
表示被测 API 返回 HTTP 524，影响 2 道题（按题目计数，不是重试次数）。卡片和详情据此显示错误原因；评分异常、响应未完成另行展示。
`code` 仅允许 `http_error`、`request_error`（请求/连接异常）、`response_error`（响应异常）、`cancelled`（测试停止）和 `unknown`。
只有 `http_error` 带 300–599 的整数 `http_status`；每组 `count` 为 1–100，分组不重复，总数不超过 `api_errors`。
不包含上游原始文本、响应头、密钥或答案，未知字段会被移除。旧记录从已保存的逐题错误生成摘要，无需重新测试；
旧来源不提供摘要或只提供部分分组时，剩余数量显示通用请求异常，不猜测错误码。接口版本仍为 v2。

浏览器调用：

```js
const response = await fetch('https://你的站点/api/v1/model-benchmarks/public-results', {
  credentials: 'omit',
  headers: { Accept: 'application/json' }
})
if (!response.ok) throw new Error(`HTTP ${response.status}`)
const body = await response.json()
if (body.code !== 0 || body.data?.version !== 2 || !Array.isArray(body.data.items)) {
  throw new Error('Unsupported result feed')
}
const results = body.data.items
// 按数组顺序逐条渲染卡片，使用框架的文本插值展示 schedule_name/model。
```

同一系统之间接入：目标站管理员打开「模型鉴定管理 → 掺水测试 → 展示配置 → 新增展示」，选择「外站结果接口」，
粘贴以上完整 URL，并设置刷新间隔（默认 60 分钟）。后台保存成功结果与更新时间，获取失败保留缓存并提示。
对方关闭公开后，会在下一次成功获取时同步移除；管理员可点「立即获取」提前同步。
外站导入同时兼容旧版 v1 的按任务分组格式，导入时统一保留全局最新 20 条。
登录后的管理员配置结果行每次加载 24 条，本地来源可继续加载保存范围内的历史结果；外站来源只能展示来源接口实际提供的缓存结果。旧外站未提供 `base_url` 时显示「来源未提供网址」，不使用结果接口地址代替。对外接口仍返回最新 20 条。

服务不可用时返回 HTTP 503 或其他非 2xx 状态及 `{ "code": ..., "message": ... }`，不要将其作为空结果覆盖缓存。
本站 worker 接口仍受内部令牌保护，对外调用应使用应用的 `/api/v1/...` 地址。

## 安全与调用限制

- 仅开放固定 GET 地址，不接受查询参数、请求体、回调函数、任意目标地址或客户端筛选条件。POST/PUT/DELETE 不能触发操作。
- 匿名请求不会携带调用方的 Cookie、Authorization 或转发头进入 worker；只访问部署配置中的固定 worker 路径，不跟随重定向。
- 公开结果在 worker 和应用出口分别使用字段白名单；应用还校验最多 20 条、ID、状态、时间、分类计数及数值范围，重新计算通过率。API Key、答案、原始错误和未知字段不会被导出；`base_url` 是公开任务的完整测试网址。
- 返回 JSON，设置 `X-Content-Type-Options: nosniff`、严格内容安全策略和 `Cache-Control: no-store`；跨域只允许无凭据读取。展示端按文本渲染外部名称和模型，不执行 HTML。
- 除站点配置的公开 IP 限流外，每个应用进程始终执行每 IP 每秒 1 次、突发 10 次；进程总计每秒 2 次、突发 20 次；最多 4 个同时进行的公开读取。IP 桶最多保存 4096 项并回收过期项。超限返回 HTTP 429 和 `Retry-After`，不占用模型测试任务名额。
- worker 请求超时 5 秒，公开响应上限 256 KiB；错误只返回通用提示，不透出内部堆栈、路径或凭据。建议接入站按配置周期刷新，收到 429 时遵守 `Retry-After`，失败时保留上次成功结果。
- 外站数据抓取只允许 HTTP/HTTPS，无账号信息；拒绝本机、内网、链路本地、组播、保留地址和 IPv4 隧道/转换地址。DNS 解析后直接连接校验过的 IP，HTTPS 验证证书，不跟随重定向、不发送凭据；响应最多 2 MiB，解析后只保留约定字段及最新 20 条。

多个应用实例共享的公开 IP 限流使用现有 Redis 配置，始终开启的并发和突发限制按进程执行。反向代理的可信地址沿用站点 `trusted_proxies` 配置；只应信任实际使用的代理。
