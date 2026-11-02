# Sub2API 公开资料出口增强版

本仓库基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 最新主线整理，额外加入一个面向中转站站长和第三方采集器的“公开资料出口”功能。

它的目标不是开放后台，也不是暴露账号池，而是把站点本来适合公开的信息标准化输出，方便 PriceAI 或其他采集器自动识别模型价格、分组倍率、缓存命中和可用性状态。

仓库的 `main` 分支采用“上游最新版 + 一个公开资料功能提交”的结构。接入方不需要复制文件或重新开发，只需把远程 `main` 作为一个功能提交合并到自己的最新版 Sub2API 分支。

## 新增能力

- 公开发现接口：`/.well-known/ai-transit.json`
- 单一公开快照接口：`/api/public/transit/snapshot`（按后台当前互斥监控模式自动返回 V1 或 V2）
- 兼容旧版本接口：`/api/public/transit/v1/snapshot`、`/api/public/transit/v2/snapshot`
- 可选公开页面：`/public/transit`
- 后台开关：`系统设置 -> 功能开关 -> 公开资料出口`
- 分组价格：充值倍率、分组倍率、平台、模型数量
- 模型明细：模型名、计费模式、输入价、输出价、缓存输入价、缓存创建价、按次/尺寸价
- 缓存指标：累计缓存命中率、缓存命中量、缓存创建量
- 可用性监测：复用现有渠道状态监测数据，展示延迟、可用率和状态
- 被动监测：从真实请求/错误日志按模型和公开分组聚合请求量、成功率、平均响应延迟和 TTFT

## 隐私边界

公开资料出口只输出标准化聚合信息，不公开以下内容：

- 上游账号、Cookie、Access Token、Refresh Token
- API Key、密钥、代理配置
- 内部渠道 ID、账号池调度细节
- 用户身份、用户余额、用户请求日志

站长可以只开放机器可读接口，不开放公开页面。

## 开关说明

公开资料出口拆成两个开关：

- `public_transit_enabled`：公开资料接口开关，默认开启。
- `public_transit_page_enabled`：公开资料页面开关，默认关闭。

如果只想让 PriceAI 或其他采集器自动抓取数据，开启接口即可；如果希望访客也能在站点页面上查看模型价格和可用性，再开启公开页面。

## 接口示例

发现接口：

```bash
curl https://your-domain.example/.well-known/ai-transit.json
```

返回示例：

```json
{
  "schema_version": "ai-transit.v1",
  "system": "sub2api",
  "snapshot_url": "https://your-domain.example/api/public/transit/snapshot",
  "snapshot_v2_url": "https://your-domain.example/api/public/transit/v2/snapshot",
  "capabilities": ["active_monitoring", "passive_monitoring"],
  "homepage_url": "https://your-domain.example/public/transit",
  "generated_at": "2026-07-07T00:00:00Z"
}
```

快照接口：

```bash
curl https://your-domain.example/api/public/transit/snapshot
```

这个接口会读取后台的互斥监控模式：选择 V1 主动探测时返回 V1 结构，选择 V2 被动监控时返回带 `passive_monitoring` 的 V2 结构。对接方只需保存这一个地址；V2 模式可以通过 `?range=90m|24h|7d|30d` 选择统计窗口。

原有的版本化地址仍然保留，用于兼容已经固定接口路径的旧采集器：

- `/api/public/transit/v1/snapshot`：固定返回 V1 主动探测结构。
- `/api/public/transit/v2/snapshot`：固定返回 V2 被动聚合结构。

被动聚合快照：

```bash
curl https://your-domain.example/api/public/transit/v2/snapshot
```

V2 保留 V1 的价格、分组和主动监测字段，并新增 `passive_monitoring`：默认统计最近 24 小时真实流量，按模型和分组输出请求数、成功数、错误数、成功率、平均响应延迟、平均 TTFT 和最近请求时间。V2 的 `mode` 固定为 `passive`，`source` 固定为 `real_traffic_aggregates`；没有可用 Ops/usage 日志时返回空数组和 warning，不会伪造探测样本。

发现接口的 `snapshot_url` 指向新的单一模式感知接口；`snapshot_v2_url` 仍作为兼容字段保留。旧采集器可以继续读取版本化地址，新接入方只需使用 `snapshot_url`。

## 接入方式

如果你的 Sub2API 版本接近上游主线，推荐直接 cherry-pick 本仓库 `main` 分支顶端的功能提交。命令不依赖固定 commit hash，仓库以后基于新版 Sub2API 重新整理提交时也仍然有效：

```bash
git remote add public-transit https://github.com/dimthink/sub2api-public-transit.git
git fetch public-transit main
git switch -c feature/public-transit
git cherry-pick public-transit/main
```

如果你的仓库改动较多，也可以下载 patch 后手动应用：

```bash
FEATURE_COMMIT=$(git rev-parse public-transit/main)
curl -L "https://github.com/dimthink/sub2api-public-transit/commit/${FEATURE_COMMIT}.patch" -o public-transit.patch
git am public-transit.patch
```

遇到冲突时，优先检查这些区域：

- 分组管理：`backend/internal/handler/admin/group_handler.go`
- 用量统计：`backend/internal/repository/usage_log_repo.go`
- 设置开关：`backend/internal/service/setting_service.go`
- 前端分组页：`frontend/src/views/admin/GroupsView.vue`
- 前端设置页：`frontend/src/views/admin/SettingsView.vue`

## 可直接使用的合并与生产部署提示词

把下面整段直接发给 Codex、Claude Code、Cursor Agent 或其他代码助手即可。它会先基于你的最新版主分支合并功能，再按照仓库已有的生产部署方式发布；不会替换数据库、配置或数据卷。

```text
请把 Sub2API“公开资料出口”功能合并到当前仓库，并按当前项目已有的生产部署流程发布。

功能仓库：
https://github.com/dimthink/sub2api-public-transit

执行要求：
1. 先检查当前分支、未提交改动、远程来源、当前版本和生产部署方式；不要猜测服务器路径、Compose 文件名或服务名。
2. 获取当前仓库的官方上游最新版并创建临时功能分支。保留现有二次开发、配置、数据库、数据卷和未提交改动，不允许用 `git reset --hard` 覆盖用户工作。
3. 添加或更新功能远程，然后获取功能分支：
   - `git remote add public-transit https://github.com/dimthink/sub2api-public-transit.git`
   - 如果远程已存在，则用 `git remote set-url public-transit https://github.com/dimthink/sub2api-public-transit.git`
   - `git fetch public-transit main`
4. 功能仓库 `main` 的顶端就是需要接入的单一功能提交。优先执行 `git cherry-pick public-transit/main`，不要重新实现一套类似功能。
5. 如果 cherry-pick 冲突，只处理公开资料功能与当前新版代码之间的冲突，不要重构无关代码；若当前仓库二改过深，再改用 `main.patch` 手动合并。
6. 合并后确认至少存在这些能力：
   - /.well-known/ai-transit.json
   - /api/public/transit/snapshot（按当前互斥监控模式自动兼容 V1/V2）
   - /api/public/transit/v1/snapshot（旧版兼容）
   - /api/public/transit/v2/snapshot（V2 固定兼容接口）
   - /public/transit
   - 未登录首页右上角的“公开资料”入口
   - 后台“系统设置 -> 功能开关 -> 公开资料出口”中的接口开关和页面开关
7. 执行合并验证：
   - go test ./internal/service ./internal/handler ./internal/server ./internal/repository ./internal/web -tags embed
   - pnpm --dir frontend run build
8. 发布前识别并沿用项目现有生产部署方式。若使用 Docker Compose：先备份数据库并记录当前镜像与 commit，再使用现有生产 Compose 配置重新 build/up；只重建应用服务，不删除 volume，不执行 `docker compose down -v`。
9. 发布后从生产域名验证：健康检查正常；`GET /public/transit`、`GET /.well-known/ai-transit.json`、`GET /api/public/transit/snapshot` 均返回 200；公开响应不包含账号、密钥、Cookie、Token、用户数据或内部渠道 ID；V1/V2 两种监控模式保持互斥且统一接口能自动适配。
10. 如果测试、构建、数据库迁移或健康检查失败，立即停止发布并回滚到发布前的 commit/镜像；不得删除或重建数据库和数据卷。
11. 最后输出：
   - 接入的 commit hash
   - 冲突文件和处理方式
   - 验证命令结果
   - 生产部署方式、目标服务和发布时间
   - 生产接口验证结果
   - 回滚点和仍需关注的风险

注意：公开资料出口只能公开模型价格、分组倍率、缓存命中和可用性等聚合信息，不能公开账号、密钥、Cookie、Token、内部渠道 ID 或用户数据。
```

如果只想合并代码、暂不发布，把提示词第一句中的“并按当前项目已有的生产部署流程发布”删除，并删去第 8–10 条即可。

如果你的站点已经有大量二改，建议先让 Agent 创建一个临时分支再接入：

```bash
git switch -c feature/public-transit-snapshot
```

## 验证建议

应用后建议至少执行：

```bash
cd backend
go test ./internal/service ./internal/handler ./internal/server ./internal/repository ./internal/web -tags embed

cd ..
pnpm --dir frontend run build
```

启动服务后检查：

```bash
curl -I https://your-domain.example/public/transit
curl https://your-domain.example/.well-known/ai-transit.json
curl https://your-domain.example/api/public/transit/snapshot
```

## 给 PriceAI 的站点准入建议

站点如果希望被自动收录，建议至少满足：

- `/.well-known/ai-transit.json` 可公开访问
- `/api/public/transit/snapshot` 可公开访问，并能按后台互斥监控模式自动返回对应结构
- 模型价格字段尽量完整
- 分组倍率和充值倍率真实可用
- 可用性监测保持启用
- 若选择 V2 被动数据，请同时启用 Ops 监测并保留 `usage_logs`、`ops_error_logs` 的写入；V2 只公开聚合结果，不公开账号、用户、Key、IP、渠道 ID 或原始请求内容
- 不在公开接口中泄露账号、密钥、Cookie 或内部渠道 ID

## 与原版 Sub2API 的关系

本仓库不是重新发行一个独立网关项目，而是基于原版 Sub2API 增加“公开资料出口”能力。原项目的部署方式、数据库结构和后台使用习惯尽量保持不变。

如果上游后续合并了类似功能，建议优先回到上游主线；如果上游暂未合并，可以继续基于本仓库的单一功能提交做兼容接入。
