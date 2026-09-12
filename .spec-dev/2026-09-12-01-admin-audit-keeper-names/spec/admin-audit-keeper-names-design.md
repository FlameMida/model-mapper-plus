---
spec_dev:
  version: 1
  feature: admin-audit-keeper-names
  status: active
  covers:
    - "audit*.go"
    - "management*.go"
    - "keeper_*.go"
    - "main.go"
    - "web/src/api*"
    - "web/src/App*"
    - "web/src/keeperAuthNames*"
    - "web/src/components/ChannelTargetEditor*"
    - "web/src/components/KeeperAuthNameEditor*"
    - "web/src/panels/KeysPanel*"
    - "web/src/panels/RulesPanel*"
    - "web/src/panels/AuditPanel*"
    - "web/dist/index.html"
    - "README.md"
    - "CLAUDE.md"
  sync_commit: 72bb7caa6bc613286906e48af5a4070b517e2983
  supersedes: []
  superseded_by: null
---

# 可审计的管理操作与 Keeper 认证名称同步

## 背景与目标

为 mapper 管理操作增加直接写入每日文件的审计，并将新的 Codex 认证名称同步操作纳入同一审计边界。渠道列表默认显示 Keeper 名称，用户可以直接修改 Keeper 别名。

原始需求包含渠道定向故障；用户已自行调整认证优先级并确认恢复，该项结束。用户明确禁止修改 `/Users/maverick/CLIProxyAPI`。本特性不再修改模型路由、Scheduler 或认证优先级。

## 决策来源

本会话 2026-09-12 的明确裁决：

- 审计以提交到后端的保存、删除、名称同步请求为单位，记录成功和失败；不记录未保存草稿中的每次点击。
- 用户纠正“归档”的含义：记录直接写到文件，按天分文件；不采用 state 内待归档队列、延迟导出或补写架构。
- 审计文件无法写入时阻止新的修改操作并报错。
- 点击“同步到 Keeper”立即保存，与 Key 绑定保存独立；随后取消绑定编辑不撤销已同步的名称。
- 既有设计中的 50 字符名称限制、日期筛选、分页、变更详情、脱敏和默认保留日志继续作为实现默认值。

## 非目标

- 不修改宿主或 Keeper 源码，不调整线上配置或部署。用户在实施中明确授权“完成后提交 推送 发版”，因此验收后允许本仓库的提交、推送及版本发布。
- 不采集推理请求日志、未保存草稿点击、CPA 其他页面或 Keeper 原生页面的操作。
- 不创建管理员身份体系；不承诺跨进程共享写入同一日志目录。
- 不实现数据库、state outbox、后台导出、自动删除历史日志或跨系统事务。

## 术语与参与者

- **操作**：一次到达 mapper 已登记变更接口的提交。批量规则保存是一项操作，详情保存四段规则的前后差异。
- **操作记录**：同一 `operation_id` 的开始事件与结果事件；文件逐行保存事件，页面合并为一行。
- **日文件**：`Asia/Shanghai` 下按操作开始日期命名的 `YYYY-MM-DD.jsonl`；跨午夜的结果仍写入该操作的原日文件，事件时间各自真实记录。
- **认证 ID**：CPA `auth-files.id`，只用于现有渠道选择。
- **认证索引**：CPA `auth_index`，精确关联 Keeper `auth_type=1` 的 `identity`。
- **Keeper 身份 ID**：Keeper JSON 中的字符串 `id`，仅用于其名称 PATCH 路径，不能与上述两种标识混用。
- 管理员通过现有 CPA 管理认证提交；审计 actor 固定为 `management_api`，不将可伪造 header 当管理员身份。
- 插件重配是系统动作，只协调目录/配置切换，不新增用户未要求的系统操作审计。

## 影响面与取代共存

管理写入口、Keeper HTTP 客户端、渠道列表、App 页签和新增审计文件模块共同构成一次完整交付。

- 与 `2026-09-05-01-keeper-key-aliases/spec/keeper-key-aliases-design.md` 分面共存：原 API Key 的“从 Keeper 同步”仍只填入绑定草稿；新增的是 Codex 认证的“同步到 Keeper”。外部错误隔离、5 秒超时、60 秒缓存规则复用。
- 与 `2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md` 分面共存：只扩展认证名称展示和搜索，不改变 `suppliers ∪ auth_ids`、禁用态、勾选持久化或优先级边界。
- 与 `2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md` 和 `2026-08-05-key-access-block/spec/key-access-block-design.md` 分面共存：保留原 CRUD/开关语义，新增管理写入前审计门禁与结果提示。
- ADR-0001 的 state 真相源不变；不增加 state 字段，不将审计作为配置恢复来源。ADR-0006 的 Scheduler 方案不变。

## ADDED Requirements

### Requirement: R1 认证名称精确关联与默认展示

渠道中的 Codex 认证文件 SHALL 通过认证索引精确匹配 Keeper 的活跃认证身份，并优先展示其 `displayName`，同时保留 CPA 文件名/ID 供辨认。

#### Scenario: S1 名称加载和精确匹配
- GIVEN 两个同名 Codex 文件具有不同认证索引，Keeper 为其中一个设置别名。
- WHEN 打开渠道列表并读取名称。
- THEN 仅精确匹配项采用 Keeper `displayName`；搜索可用 Keeper 名称、原名称及文件 ID，勾选值仍为 CPA ID。

#### Scenario: S2 未配置、未匹配或读取失败
- GIVEN Keeper 未配置、读取失败、新认证尚未同步或索引匹配存在歧义。
- WHEN 展示目录。
- THEN 保留 CPA 目录与选择；回退原名称并解释无法同步原因，不猜测匹配、不创建 Keeper 身份、不退出 CPA 登录。

### Requirement: R2 名称同步立即生效

名称同步 SHALL 经 mapper 后端调用 Keeper 现有 PATCH，成功后立即显示返回的名称，与绑定保存/取消独立。

#### Scenario: S3 修改及清空名称
- GIVEN 唯一匹配的 Codex 认证身份。
- WHEN 提交合法名称或空字符串。
- THEN Keeper 收到 `PATCH /api/v1/usage/identities/:id` 和 `{alias: string}`；成功立即回显，空值恢复 Keeper 默认名称，取消绑定编辑不撤销该操作。

#### Scenario: S4 校验、鉴权及并发响应
- GIVEN 超过 50 个 Unicode 字符或被 Keeper 禁止的控制字符；另有切换认证、关闭弹窗、切换配置及超时场景。
- WHEN 用户提交或旧请求返回。
- THEN 非法名称在发往 Keeper 前拒绝；PATCH 包含 JSON Content-Type 和 `X-CPA-Usage-Keeper-Request: fetch`；首次 401 登录后仅重试一次，其他网络错误不自动重试 PATCH；迟到响应不覆盖另一项或新编辑；超时标记结果未确认，不声称 Keeper 未保存。

### Requirement: R3 管理提交的完整审计范围

到达 mapper 的 `PUT /rules`、`POST/PATCH/DELETE /keys` 以及新增认证名称 PATCH 请求 SHALL 按提交记录对象、动作、时间、受控结果和结构化变更。

#### Scenario: S5 新增、覆盖、删除和无变化提交
- GIVEN 空绑定列表及随后存在的绑定。
- WHEN 依次 POST 新增、POST 覆盖、PATCH 开关、DELETE、重复保存相同值。
- THEN 分类分别为 create/update/update/delete/update；无变化提交详情明确 `changed=false`；成功修改记录实际前后值，失败不伪造 after；Key 更名的现有 POST 新 Key + DELETE 旧 Key 忠实记录为两项。

#### Scenario: S6 规则批量与验证失败
- GIVEN 四段规则及未保存草稿。
- WHEN 保存包含多行增删改的规则，或提交非法 JSON/非法规则/不存在的 Key。
- THEN 一项规则保存记录包含各变更段前后内容；失败请求有失败结果且 state 不变；取消草稿、GET、preview、凭据解析和只读名称刷新不产生操作记录。

### Requirement: R4 每日直接写盘及写入门禁

每项操作 SHALL 在执行任何配置或 Keeper 变更前直接向日文件追加并 Sync 开始事件；开始事件写入失败时拒绝该操作；执行结束后直接追加并 Sync 结果事件。

#### Scenario: S7 实时日文件与午夜
- GIVEN 可写的 state 同级 `model-mapper-plus-audit/` 目录。
- WHEN 提交操作及一项跨越北京时间午夜的操作。
- THEN 每个事件即时写为完整 JSONL 行；目录权限 0700、文件权限 0600；操作按开始日期归属日文件，跨日结果仍可配对；state 内不存在审计队列；重启后可读取历史记录。

#### Scenario: S8 开始写入失败阻止副作用
- GIVEN 日志目录是普通文件、无法打开日志或开始事件 Write/Sync 失败。
- WHEN 提交任何受审计的变更。
- THEN 返回 HTTP 503、受控 `audit_unavailable` 错误；本地 state 和 Keeper 均不发生该次变更，不回显原始请求或 OS 错误中的敏感输入。

#### Scenario: S9 结果写入失败与崩溃边界
- GIVEN 开始事件已成功落盘，业务操作执行后发生结果写入失败或进程中断。
- WHEN 查询日文件或本次 HTTP 请求仍能返回。
- THEN 无完整结果的记录显示 `unknown`（当前进程明确仍在运行者为 `running`）；HTTP 若业务结果已知则保留该结果，额外返回 `audit.recorded=false` 提示，不把已成功变更说成已回滚；后续新操作仍必须先通过真实 Begin 写入。不得自动重放原业务操作。

### Requirement: R5 审计查询和脱敏

“规则试跑”之后的“操作审计”页 SHALL 按日查询合并后的操作，支持倒序分页、动作/结果展示、变更详情和异常提示，并且不暴露认证秘密。

#### Scenario: S10 查询、分页与异常数据
- GIVEN 空目录、多页记录、未完成事件及损坏/截断的行。
- WHEN 切换日期、翻页、刷新或请求非法日期/分页参数。
- THEN 空日返回空列表；有效数据倒序稳定分页（时间及 operation_id 排序），损坏数据显式提示而非当作无记录；非法路径日期/分页返回 400，禁止目录穿越；切换后的迟到响应不覆盖当前日期。

#### Scenario: S11 敏感数据与热重配
- GIVEN 包含完整 Key、Keeper 密码、Cookie/Token 的请求以及并发保存和 state 路径重配。
- WHEN 写入和查询审计。
- THEN 文件和响应只保存白名单投影；Key 使用 SHA-256 指纹及掩码，HTTP headers/完整 URL/raw body/Keeper 响应体不入日志；规则和别名中的已知秘密字面值也替换为掩码。重配与管理变更串行协调，开始与结果始终写入同一目录，不混用新旧配置；历史目录不搬迁或删除。

## 接口与实现边界

新增管理路径均相对 `/v0/management/plugins/model-mapper-plus`：

- `GET /keeper/auth-names` 与 `POST /keeper/auth-names/refresh`：`{status: ready|disabled|unavailable, items: [{identity_id, auth_index, alias, display_name}], fetched_at?, error_code?}`。仅返回活跃 Codex Auth File 名称投影，未知类型和重复冲突不可写。
- `PATCH /keeper/auth-names`：输入 `{auth_index, alias}`；后端强制刷新并重新解析对应 Keeper ID，不信任浏览器提供的 Keeper ID。返回 `{status: ready|not_found|invalid|unavailable|unknown, item?, error_code?, audit?}`，只有 `ready` 含更新 item。除了审计 Begin 失败为 503，其余应用结果统一以 HTTP 200 封装，避免外部 401/403 触发 CPA 登出。审计结果必须消费应用 status，不能把 HTTP 200 一律算成功；PATCH 已发出但没有可确认响应时为 unknown，发出前失败为 failed。
- `GET /audit?date=YYYY-MM-DD&page=1&page_size=20`：默认北京时间当天，page>=1，1<=page_size<=100；返回 `{date, timezone, page, page_size, total, items, warnings}`，`Cache-Control: no-store`。items 按 operation_id 合并事件，有 `action, object_type, object_ref, started_at, finished_at?, outcome, changed, changes, error_code?`。
- 所有变更响应增加可选 `audit: {operation_id, recorded, error_code?}`；原成功/错误响应字段保留。终态写入失败时，业务结果已知的界面保留原结果并提示“审计结果未写入”；Keeper 结果为 unknown 时提示“同步结果未确认，审计结果未写入”，不宣称已保存或已回滚，不自动重试。

文件记录 envelope 为 `version=1, operation_id, phase=start|finish, occurred_at` 加白名单业务字段；只增加新行，不回写旧行。每项操作的结果写入其开始时固定的目录和日期。生成 operation_id 使用 Go 标准库随机值。

审计 outcome 为 `running|succeeded|failed|unknown`，changed 为 `true|false|null`：成功后据快照比较，副作用前失败为 false，结果不明为 null。读取 Keeper 预检失败与实际 PATCH 失联需要分开，整个预检、登录和 PATCH 共用 5 秒期限。正式 PATCH 不因超时或 5xx 重试。

`audit.go` 负责真实 Append/Sync、文件权限、文件内互斥和读取配对；`audit_management.go` 负责全管理变更互斥、脱敏投影与前后快照；`keeper_auth_names.go` 负责名称缓存/解析/同步。使用现有 HTTP Cookie 客户端并补 PATCH 头；IO 不进入推理 hooks，不持有配置或 state 锁访问 Keeper。

直接日文件的 start/finish 是审计事实，不能用来恢复配置。文件只存在 start 时无法可靠区分副作用未执行与已执行，因此明确为 unknown。开始记录写不进时不可能保证错误本身也写入该文件，HTTP 错误和受控 stderr 是此边界的诊断出口。

## 测试与验收策略

公共测试落点为现有 `dispatchManagement(pluginapi.ManagementRequest) pluginapi.ManagementResponse`、`handleManagement` ABI、真实临时 state/JSONL 文件以及 React 组件交互。替换边界仅限 Keeper HTTP（httptest）、浏览器 fetch、可控时钟、文件写入/Sync 故障；不替换审计分类、匹配或变更逻辑。

| Scenario | Lane | 执行方式 | 证据 |
|---|---|---|---|
| S1-S4 名称读写、匹配、校验、缓存、鉴权及竞态 | fast | 任务内 TDD | 管理 API + httptest，React 行为测试 |
| S5-S6 CRUD、规则批量、失败和无变化 | fast | 任务内 TDD | 管理 API 结果、state、日文件联合断言 |
| S7-S9 日界、不可写门禁、结果失败、重启 | fast | 任务内 TDD | 真实临时目录、可控时钟和 Write/Sync 故障 |
| S10-S11 查询、脱敏、目录切换 | fast | 任务内 TDD | 查询端点、文件内容及重配并发测试 |
| 名称立即生效且取消绑定不撤销，审计页顺序/详情，390px及桌面明暗主题 | final | 验收任务 | 本地受控服务及浏览器记录 |
| Go race/vet、前端全测/typecheck/build、发布打包脚本测试 | final | 最终任务 | 命令、退出码与原始日志 |
| 真实线上 Keeper 名称写入和线上部署 | manual | 明确另行授权后 | 当前 manual-pending，不自动修改真实名称 |

## 风险与边缘情况

Keeper 并发编辑没有 CAS；此功能提交明确的新值，Keeper 最后写入者生效。不自动重试不确定结果，不许将回读相同值当作本次请求成功的证明。

本地 state 和审计日文件不是原子事务，Keeper 更不是本地事务。开始记录先持久化和明确 unknown 是可实现的保证；不承诺磁盘故障下终态必达或操作回滚。截断尾行保留并显示警告；下一次追加前用换行分隔新事件，不静默删除损坏内容。

日志持续占用磁盘，当前按用户要求保留，磁盘满触发同一写入门禁。日期/目录切换不自动迁移历史，运维可以直接读取旧目录文件。
