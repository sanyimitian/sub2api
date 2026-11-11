# 管理员模型能力评测

管理员入口：「模型鉴定管理 → 掺水测试」，地址 `/admin/model-benchmarks`；同组「智商测试」沿用 `/admin/intelligence-status`。用户入口统一为「模型鉴定 → 掺水测试」。
输入 URL、API Key、准确模型 ID 和推理强度即可创建后台任务。默认强度 `medium`，
默认编程 20（中等 10、困难 10）、数学 5、推理 5。中等/困难题数可分别配置，自动合计编程题数；每类可设为 0，总数为 1–100，且不超过各类与各难度的题库容量。题目详情及测试记录分别显示编程难度与成绩。

「选择密钥」读取登录管理员自己账号下的 API 密钥，可按名称搜索、分页选择。选中后
自动填入站点公开设置中的 API 地址（未配置时使用当前 API 站点 `/v1`）及密钥；
禁用、过期或额度用尽的密钥不可选。修改自动填入的 API 地址会清空所选密钥。

勾选「设置定时任务」后填写名称和执行频率，保存即按计划执行（不会立即发起一次测试）。
默认固定间隔 30 分钟，可按分钟或小时配置，范围为 1 分钟至 30 天，从上次测试结束后计时；首次从保存或启用时计时。
也保留每天 09:00、每小时、每周一 09:00 和自定义五段 Cron；时区与每日题库相同，默认上海。
列表的「立刻执行」使用已保存的 URL、加密密钥、模型及强度，取当天当前题库并进入全站队列。
暂停的计划也可执行一次，保持暂停；同一计划已有排队、运行或停止中的测试时返回 409，不会重复扣费。
手动执行不改变 Cron 的下次触发时间；固定间隔则从此次测试结束后重新计时。
支持编辑名称/频率、暂停、启用、删除，接口参数需新建任务来更换。每次触发取当天当前题目及题数，
任务入队后固定该版本。可同时保留最多 50 个计划。

调度在 worker 后台运行，关闭页面不影响；每 5 秒扫描并持久化下次执行时间。同一计划尚有
排队/运行任务时，Cron 跳过本次并显示原因，固定间隔等待此次结束后再计时。重启后对错过的周期最多补发一次，
不集中补发全部历史周期。暂停/删除只停止后续触发，已有任务可在测试记录中单独停止。
密钥加密保存，调度结果标记为「定时测试」；密钥失效时记录接口错误，可暂停计划并重新配置。

## Linux 安装

评测 worker 启动时自动检查 Docker、下载固定版本题库和评分源码、构建缺失评分镜像，验证完成后自动开放测试。已有题库及历史结果保留，已有评分镜像直接复用。页面无需等待安装完成即可打开。建议至少 8 核、16 GB 内存、20 GB 空闲空间。

新服务器或尚未启用 worker 的部署，在完整仓库根目录执行一次 `sudo bash deploy/install-model-benchmark.sh --compose-file deploy/docker-compose.yml --env-file deploy/.env`。脚本自动安装缺失的 Docker Engine 与 Compose v2（Ubuntu/Debian）、生成内部通信密钥、构建带 Python 依赖的 worker 并启动。应用使用已包含本功能的发布镜像；保持原有部署配置、数据库卷和项目名，自定义项目名通过 `--project-name` 指定。仅准备 worker、不重启应用可追加 `--worker-only`。

worker 已启用后无需手动准备题库和 grader。页面显示准备阶段及进度，失败后 30 秒起自动重试、最长间隔 5 分钟，管理员可点击「立即重试准备」。用户只能查看状态；准备期间不能发起或重测，不消耗次数。服务每 30 秒检查执行环境，发现异常时暂停派发并自动恢复，保留排队任务和既有数据。

宿主机安装需要 root/sudo；网页及匿名接口没有宿主机软件安装权限。安装器只使用 Docker 官方签名软件源，不执行远程脚本。其他已装 Docker 的 Linux 也可运行；不支持的发行版缺少 Docker 时会给出说明。环境重试接口仅管理员可调用，且不接受命令、下载地址或自定义脚本。
全站同时运行上限默认 5，可在管理员测试管理页设置为 1–100；管理员手动、定时和所有用户测试共用此上限，其余按提交顺序持久化排队。
每个测试逐题执行，上游请求与评分容器并行数量不超过全站任务上限，每个评分容器 1 CPU / 2 GB。提高上限时应按任务数配置服务器资源。

管理员测试管理页的「允许用户自行测试」开关默认关闭，切换后自动保存。
只有开启时用户页面才展示「我的测试」（当天题目、表单、历史及详情）；关闭后服务端也拒绝用户新建和重测，且不消耗次数。
已经排队或运行的任务继续执行，历史记录保留，重新开启后可查看；管理员测试、定时任务、本站结果展示和对外公开接口不受影响。

以下手动 Compose 步骤仅作为原有部署的兼容方式；使用上述安装器后无需重复。题库准备和 grader 构建也会在 worker 启动后自动完成。

```sh
# 将以下两个变量写入 deploy/.env（不要提交）。HOME 必须是绝对路径。
# MODEL_BENCHMARK_HOME=/opt/sub2api-model-benchmark
# MODEL_BENCHMARK_WORKER_TOKEN=<openssl rand -hex 32 生成的随机值>
mkdir -p /opt/sub2api-model-benchmark
chmod 700 /opt/sub2api-model-benchmark

docker build -f tools/model-benchmark/Dockerfile.grader \
  -t sub2api-model-benchmark-grader:v1 tools/model-benchmark
docker compose --env-file deploy/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.benchmark.yml build model-benchmark
docker compose --env-file deploy/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.benchmark.yml run --rm --no-deps \
  model-benchmark python prepare.py
docker compose --env-file deploy/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.benchmark.yml up -d --no-deps model-benchmark

# 在正常应用发布流程中使 sub2api 加载 overlay 的两个 worker 环境变量。
# 例如，应用新镜像已准备好之后：
docker compose --env-file deploy/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.benchmark.yml up -d --no-deps sub2api
```

若现有部署用 `docker-compose.local.yml`，将示例中的主配置文件替换为该文件，保持
原有 Compose 项目名、环境变量和卷。不要在已有部署上换用另一套数据库卷。
不使用 Compose 时，后台应用设置 `MODEL_BENCHMARK_WORKER_URL` 和
`MODEL_BENCHMARK_WORKER_TOKEN`，worker 使用相同 token；默认只监听 `127.0.0.1:8124`。
非容器方式至少需 Python 3.11 和 `requirements.txt` 中依赖，启动 `server.py` 后自动准备题库及评分镜像。容器方式由 worker 构建自动安装 Python 依赖。

Worker 持有 Docker socket，具有宿主 Docker 管理权限；仅此服务挂载 socket，应用后端和
模型生成代码不挂载。生成代码在无网络、只读根文件系统、非 root、无 capabilities、
限制内存/CPU/PID 的短生命周期容器中执行。生产环境可把 worker 放到专用执行机，
通过私网与应用连接；不要把 worker 端口公开到互联网。

## 题目与公平性

- 使用固定版本 LiveCodeBench `test6` 的 medium/hard 题和 LiveBench 的 AIME 2024、
  zebra、spatial 题。源码 commit、数据集 revision 和内容 SHA256 保存在 `manifest.json`。
- 默认编程题中 medium/hard 各 10；数学与推理分别从对应公开题池随机抽取。
- 按 `Asia/Shanghai` 日期，在当天第一次访问或创建任务时抽题，SQLite 持久化；
  重启不会换题。可通过 `MODEL_BENCHMARK_TIMEZONE` 配置时区。
- 「刷新题目」从已安装公开题库重新抽题，不会下载未经验证的新数据；保存数量也会换题。
  每次形成新版本。随机抽样可能重复部分题目，选择整个题池时题目集合相同。
- 「重新测」使用提交时当天当前版本的题目及题数，保留原记录的 URL、模型、API Key 和强度。
  跨日时自动取当天题目，手动刷新或修改数量后取最新版本；原记录的题目和成绩保持不变。
  新建测试若在跨午夜或其他管理员换题后仍提交旧版本，会收到 409，刷新后再提交。
  新建和重测的任务入队后均固定题库版本，后续刷新不影响在途任务。
- 所有请求统一用 Responses `/responses`、`max_output_tokens=16384`、`store=false`、
  相同 instructions、无工具、未指定 temperature。推理强度按所选值发送，不自动降档。
  不兼容 Responses 或强度的站点会显示接口错误。上游实际执行的模型或计算量无法仅凭接口确认。
- 通过率 = **完整通过题数 / 总题数 × 100%**。编程需通过全部用例，文本题由固定版本
  官方评分器打分且分数为 1 才通过；部分得分保留在逐题结果中。
- API 错误、响应截断、评分基础设施错误不会作为最终能力成绩；任务标记异常、通过率为空。
  暂时性连接问题及 HTTP 429/500/502/503/504 最多重试两次，模型答错不重试。
  HTTP 400/401/403/404/405 停止后续请求。停止按钮等待当前在途题目完成。
- 编程采用固定官方评分器，修正二进制标准输入 `readline` 不前进的问题；仅对题目明确规定
  浮点误差的题目启用对应容差。单用例限时 6 秒，单容器总时限 600 秒。
- 这是公开基准的自定义抽样，**不是官方完整榜单，也不能证明模型身份或是否掺水**。
  同版本、同参数比较有意义；公开旧题可能受训练数据污染，30 题分数也有抽样波动。

## 持久化与运维

目录结构：`bank.sqlite3` 为只读题库；`sources/` 为固定官方源码；`data/benchmark.sqlite3`
保存配置、题目快照、任务与结果；`data/credentials.key` 是密钥加密主密钥；
`data/artifacts/<run-id>/` 保存脱敏后的完整请求/响应，供复核；页面只返回答案前 12000 字符。
定时计划也保存在同一 SQLite 中；启动时自动增加所需表和字段，原有记录保留。
测试记录每页 20 条，支持页码和跳转。已结束的记录按结束时间保留 20 天，启动时和每小时自动清理过期记录、回答文件以及无记录引用的旧题目快照；排队和运行中的任务保留，定时任务及其密钥不随历史结果删除。备份副本由运营者独立管理，不属于在线记录清理范围。
API Key 不进入前端存储、结果接口或日志。备份时一并保存整个目录，妥善保护
`credentials.key`；主密钥丢失后不能解密历史任务凭据，需新建测试重新输入。
运行中重启会把任务标为「已中断」，排队任务保留；可手动重测。
Worker 启动时会清理带有自身目录专属标签的遗留评分容器，避免异常退出后留下旧任务。

管理员「总题库管理 → 更新总题库」在后台检查固定官方数据源的最新提交，下载数据并验证格式及评分兼容性。
目前导入 LiveCodeBench 所有可用公开分期的中等/困难题，以及 LiveBench 当前有效的 AIME、zebra_puzzle 和 spatial 题；不执行远程脚本，也不自动升级评分器。
完成后展示新增、变更、移除数量，点击「启用新题库」才切换。启用时按现有题数配置生成新的当日版本；容量不足则拒绝切换。没有变更、下载失败或校验失败时，当前题库及当日题目不变。
新题库写入 `banks/<version>.sqlite3`，活动版本记录在配置数据库。原始 `bank.sqlite3` 和旧版本保留，排队、运行中及历史记录按各自版本读取题目；重新测试使用当前版本。更新状态保存在 `data/bank-update.json`，重启中断的下载需重新检查。
只有管理员能使用 `POST /api/v1/admin/model-benchmarks/bank/update` 和 `/bank/activate`，前者不接受参数，后者只接受已验证的 `version`，不允许自定义下载地址。
`prepare.py` 保留已有题库内容。部署更新评分镜像时应同时记录镜像摘要，不混用不同评分协议的成绩。
默认禁止目标 API URL 指向本机、内网、元数据地址，禁用重定向并固定 DNS 解析地址。
`MODEL_BENCHMARK_ALLOW_PRIVATE_TARGETS=true` 仅供明确需要内网 API 的运营方配置，不能由页面改变。

## 验证

```sh
cd tools/model-benchmark
python -m unittest discover -s tests -v
# 已准备题库与 grader 镜像的 Linux 环境：
MODEL_BENCHMARK_TEST_HOME=/opt/sub2api-model-benchmark \
  python -m unittest discover -s tests -v
```

集成测试使用本地模拟 API，不调用付费模型。后端测试：
`go test -tags=unit ./internal/handler/admin ./internal/server/routes -run ModelBenchmark`。
前端：`pnpm exec vitest run src/views/admin/__tests__/ModelBenchmarksView.spec.ts src/router/__tests__/feature-access.spec.ts`，然后 `pnpm build`。

## 用户评测与开放展示

页面顶部仅提示「测试结果仅供参考」，展示区域标题为「本站测试结果」，今日题目以紧凑摘要放在新建测试上方。

用户入口 `/model-identification/dilution`，需要登录。上半部分只展示管理员「展示配置」中启用的数据来源；可选择本站公开任务或外站结果接口。本站来源默认显示该任务最新 20 条，可单独设置最多 1–100 条；外站来源显示接口提供的全局最新 20 条。采用横向卡片和滑块浏览，展示通过率、分类成绩、模型、推理强度、耗时、Token 用量、结束时间和题库版本，点击卡片查看详细汇总。不返回测试接口地址、密钥、原始错误或模型答案。新功能不会自动将所有公开任务添加为展示项，需要管理员选择。

下半部分仅支持手动测试、查看和停止自己的记录及使用当天题库重测，不可配置定时任务或题库。每个账号在滚动 60 分钟内最多提交 3 次（含重测）；成功入队即计次，取消或失败不退次数，校验失败不计次。额度和归属保存在 SQLite，进程重启不清零。全站运行名额由数据库事务控制，停止中的任务释放前仍占用名额。并发配置持久化、立即生效；调低上限不会中断已有任务，待名额释放后按新上限继续。

每个用户最多同时有一个未结束测试：排队中、运行中或停止中均阻止新建和重测，返回 HTTP 409，且不消耗次数。此检查在入队事务中执行，多次点击或并发请求也只能创建一个任务。页面同步禁用按钮，并提供打开当前任务的入口；任务完全结束后可继续发起。管理员任务仍按全站并发配置执行。

用户 API `/api/v1/model-benchmarks` 使用登录认证身份构造内部用户路径，忽略请求中伪造的账号信息；配置后的用户展示页仍需要登录，对外 `/public-results` 接口免登录。管理员 API 仍仅管理员可用。

### 展示来源与对外接口

管理员「展示配置」支持新增、编辑、暂停、删除来源。本站来源只能选择公开开关开启的定时任务，并设置最大展示结果条数（默认 20，范围 1–100），按结束时间倒序展示。关闭公开或删除任务后立即从本站展示和对外接口移除。旧展示配置自动迁移为默认 20 条。

外站来源填写完整结果接口地址，刷新间隔默认 60 分钟，可设 1–10080 分钟。保存后自动获取，后台独立于测试任务刷新，关闭浏览器也会继续；也可手动「立即获取」。结果、获取时间和下次刷新时间持久化保存，失败时保留上次成功结果并标记过期，成功获取空结果时清除旧成绩。修改来源地址会清空旧来源缓存；旧请求返回不能覆盖新配置。

对外接口 `GET /api/v1/model-benchmarks/public-results` 无需登录，允许浏览器跨域 GET。仅导出本站管理员公开定时任务的已结束结果，所有任务合计最多最新 20 条，按结束时间倒序排列。响应协议为 v2，`items` 是携带任务 ID 和名称的扁平结果列表，不转发导入的外站结果。外站导入仍兼容旧版 v1 分组格式。用户页的展示配置和本地最大条数不影响该导出范围。

完整字段与调用示例见 [PUBLIC_API.md](PUBLIC_API.md)。管理员设置接口为 `GET/PUT /api/v1/admin/model-benchmarks/execution`；展示接口为 `/api/v1/admin/model-benchmarks/displays`，支持 GET/POST、`/:id` PUT/DELETE、`/:id/refresh` POST。用户无权访问配置接口。
