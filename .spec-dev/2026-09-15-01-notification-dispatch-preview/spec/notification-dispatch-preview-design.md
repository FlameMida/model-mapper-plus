---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: notification-dispatch-preview
  status: draft
  covers:
    - "notification_*.go"
    - "web/src/panels/NotificationsPanel.tsx"
    - "web/src/panels/KeyNotificationsTab.tsx"
    - "web/src/components/Notification*.tsx"
    - "web/src/components/PlatformIdentityEditor.tsx"
    - "web/src/notification*.ts"
    - "web/src/api.ts"
  sync_commit: null
  supersedes:
    - ".spec-dev/2026-09-13-01-key-usage-notifications/spec/key-usage-notifications-design.md"
  superseded_by: null
---

# 通知调度、预览与渠道粒度设计

## 背景与目标

用量通知已经能配置和试发，但到期后不真正发送、下次触发用 RFC3339、预览是抽屉内纯文本、全局仍要填用户唯一 ID、统计把其他 API Key 当成渠道。本特性在既有内置通知服务上修正调度时钟、发送粒度、预览形态和成员姓名。

**成功标准**：计划时间到达后会发送（重启最多补最近一次错过）；全局/Key 列表与服务卡用东八区 `YYYY-MM-DD HH:mm:ss` 展示下次发送；预览为遮罩三栏、仅启用平台、指标分行；全局可不填用户唯一 ID；全局每个认证渠道一条消息；Key 级正文只含本 Key 实际用量身份；飞书拉取成员显示姓名（权限不足时只显示 ID）。

## 非目标

- 不修改 `/Users/maverick/cpa-usage-keeper` 或 CLIProxyAPI 宿主。
- 不把渠道定向（`ChannelTarget`）接入通知采集。
- 不承诺客户端截图像素级 1:1；预览为群机器人结构还原。
- 不新增「每天固定钟点」计划类型；「每隔」不对齐墙钟。
- 不补发全部错过的计划点。
- 不引入第二进程或新第三方依赖。

## 术语表

- **认证渠道**：Keeper analysis 中 `auth_files_composition` 与 `ai_provider_composition` 的一行，`key` 为 identity/`auth_index`，`label` 为展示名。_Avoid_：客户端 API Key、企微/飞书/钉钉投递平台、CPA `ChannelTarget` 凭据 ID。
- **调度时钟**：bbolt 中每条通知在某 scope（全局或 Key 指纹）及可选渠道键上的上次成功发送时间。_Avoid_：进程内 `lastFire`。
- **结构还原**：按该平台群机器人实际 webhook 类型渲染气泡、机器人名、markdown/text 子集与 @ 文本，不使用官方未提供的 CSS token。

## 参与者与适用行为

- 管理员：配置全局/Key 通知、预览、测试发送；看到下次发送时间与成员姓名。
- 通知服务：register/reconfigure/tick 时判定到期、采集、入队、发送；shutdown 取消在途。
- Keeper：只读 analysis、api-keys/settings、quota cache；映射失败不得伪造渠道。
- 企业微信/飞书/钉钉群机器人：接收 webhook；预览不向其发送。
- 飞书通讯录：成员拉取；姓名字段依赖权限，缺失时不得把拉取判失败。

## 影响面

`notification_service.go` 调度与 identity 解析；`notification_schedule.go` 到期判定；`notification_store.go` clock bucket；`notification_keeper.go` composition 解析与 `api_key_id` 映射；`notification_template.go` 分行渲染；`notification_management.go` preview/status；`notification_members_client.go` 补姓名；`notification_types.go` 全局用户 ID 校验；前端 NotificationsPanel、KeyNotificationsTab、NotificationEditor、PlatformIdentityEditor。Keeper 与宿主不改。

## 已确认的关键决策

- 渠道 = 认证渠道（auth file ∪ AI Provider identity），不是 API Key 行或投递平台。
- 全局按认证渠道各发一条；无专属通知的 Key 不再复制全局消息（详见 `../../adr/0008-global-notifications-dispatch-per-auth-channel.md`）。
- Key 级名单 = 该 Key 在 analysis 上实际产生用量的认证身份；不读渠道定向。
- 全局用户唯一 ID 选填；仅打开「@所有人」才 @所有人，否则不 @。Key 级仍必填用户 ID。
- 错过只补最近一次；每隔 = 上次成功发送 + 间隔，不对齐钟点。
- 预览遮罩三栏并排、指标分行；定稿 `spec/assets/preview-modal-columns.html`。
- 原地修补内置服务，不新建渠道摘要调度器。
- 删除进程 `lastFire`，不留兼容开关。

## 约束归属与拒绝的解读

- 「到时间后发送」由调度时钟 + due 纯函数负责，验证在可控时钟的 `scheduleTick` 测试；拒绝把 15s 轮询时刻当成计划锚点。
- 「本 Key 拥有的渠道」采用实际用量身份，验证在 analysis httptest；拒绝 ChannelTarget 勾选名单，拒绝全站 `api_key_composition`。
- 「1:1 样式」采用结构还原，验证在预览 Modal 与分行正文；拒绝像素级皮肤与应用卡片 SDK。
- 「不填用户 ID」= 不 @，除非 at_all；拒绝默认 @所有人。

## 取代与共存

- [部分取代] `.spec-dev/2026-09-13-01-key-usage-notifications/spec/key-usage-notifications-design.md`：Requirement「多通知配置与继承」——全局不再对无专属通知的 Key 逐 Key 发送。
- [部分取代] 同上：Requirement「Key 渠道消耗与占比」——周期行改为认证渠道，Key 级不含其他 Key。
- [部分取代] 同上：Requirement「模板与消息结构」——指标改为每行一项。
- [部分取代] 同上：Requirement「发送计划」——必须按计划触发并展示人类友好下次时间，废除 lastFire 滑动。
- [部分取代] 同上：Requirement「平台身份配置」——全局启用平台可免填用户唯一 ID。
- [部分取代] 同上：关键接口 `POST /notifications/preview`——改为分平台正文 + 遮罩弹窗。
- [分面共存] `.spec-dev/2026-09-12-01-admin-audit-keeper-names/spec/admin-audit-keeper-names-design.md`：管理写入仍走审计；本特性不改审计投影。
- [分面共存] `.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md`：渠道定向仍只服务 Scheduler，通知不读。
- [分面共存] `.spec-dev/adr/0007-embedded-usage-notification-service.md`：继续内置服务 + bbolt。

## ADDED Requirements

### Requirement: 预览遮罩与分平台结构还原

点击「预览消息」SHALL 打开遮罩弹窗，按该通知已启用平台并排展示企业微信、飞书、钉钉结构还原气泡（未启用的平台 SHALL NOT 出现）；正文指标 SHALL 每项一行。预览 SHALL NOT 向平台发送。

#### Scenario: 三平台均启用

- **GIVEN** 通知启用了企业微信、飞书、钉钉。
- **WHEN** 管理员点击预览消息。
- **THEN** 遮罩弹窗三栏并排，分别按企微 markdown、飞书 text、钉钉 markdown 结构展示同一份分行正文；钉钉栏标明会话列表标题为通知名称。

#### Scenario: 只启用飞书

- **GIVEN** 仅飞书平台启用。
- **WHEN** 预览。
- **THEN** 弹窗只出现飞书一栏，不渲染企微、钉钉空列。

#### Scenario: 预览不发送

- **GIVEN** 平台 webhook 可用。
- **WHEN** 预览成功。
- **THEN** 不产生投递记录，平台收不到请求。

### Requirement: 飞书成员显示姓名

飞书成员拉取 SHALL 为每个成员提供姓名或明确回退为纯 ID；SHALL NOT 把空姓名渲染成「 · {id}」。`Scope.List` 仅返回 ID 的用户 SHALL 通过通讯录 batch 接口补姓名。

#### Scenario: 部门用户带姓名

- **GIVEN** FindByDepartment 返回 open_id 与 name。
- **WHEN** 打开成员下拉。
- **THEN** 选项文案为「{name} · {id}」。

#### Scenario: 授权范围直授用户

- **GIVEN** Scope.List 给出 user_ids 且无 name。
- **WHEN** 拉取完成且 batch 返回姓名。
- **THEN** 这些 ID 的选项含姓名。

#### Scenario: 姓名权限不足

- **GIVEN** batch/FindByDepartment 未返回 name。
- **WHEN** 渲染选项。
- **THEN** 选项只显示 ID，拉取本身仍为 ready，不以缺姓名判失败。

## MODIFIED Requirements

### Requirement: 多通知配置与继承（全局改为按认证渠道发送，不再按 Key 复制）

每个 Key SHALL 仍支持零条或多条专属通知。全局通知 SHALL 按认证渠道生成消息：每个认证渠道、每条启用的全局通知、每个启用平台至多一条待发任务。无专属通知的 Key SHALL NOT 再收到一份复制的全局消息。Key 一旦拥有专属通知，发送完全由专属通知接管。

#### Scenario: 全局两渠道两条正文

- **GIVEN** 全局通知已启用，Keeper analysis 含认证渠道 A 与 B。
- **WHEN** 到达该通知的发送时间。
- **THEN** 入队两条不同正文（分别只含 A、只含 B），再按启用平台拆 job；投递记录归属该全局通知与对应渠道，不归属某个客户端 Key。

#### Scenario: 配置专属通知后不受全局渠道循环影响

- **GIVEN** Key K 已有专属通知。
- **WHEN** 全局渠道循环触发。
- **THEN** 不因 K 再多发一份；K 只按其专属通知与计划发送。

#### Scenario: 同一 Key 的多条专属通知互相独立

- **GIVEN** Key K 有日报和周报，平台与计划不同。
- **WHEN** 修改日报计划或关闭周报。
- **THEN** 另一条的模板、计划、启用状态和任务不变。

### Requirement: 模板与消息结构（指标每行一项）

通知 SHALL 使用通知名称作为标题。周期模块下每个认证渠道 SHALL 先独占一行写展示名，随后用量、占比、折算金额各占一行；窗口模块的已用、重置时间、剩余时间各占一行。SHALL NOT 用间隔点把用量、占比、金额挤在同一行。关闭的模块 SHALL NOT 出现空行占位。

#### Scenario: 周期渠道分行

- **GIVEN** 日统计开启，渠道 Codex 有 tokens、占比与金额。
- **WHEN** 生成预览或发送文本。
- **THEN** 文本含独立行「Codex」「用量 … tokens」「占比 …」「折算 ≈ $…」，同一渠道这三项不出现在同一行。

### Requirement: Key 渠道消耗与占比（仅本 Key 实际用量身份）

Key 级通知的周期统计 SHALL 只把该客户端 Key 实际产生用量的认证渠道行写入正文（不得出现其他客户端 Key 的展示名或 tokens）。占比分母 SHALL 为同期该认证渠道的全站 Token 总量（可从不过滤的 composition 同一 identity 读取），SHALL NOT 因单 Key 过滤把占比显示成 100% 来代替渠道份额。全局通知单条渠道消息 SHALL 只含该渠道。映射不到 Keeper 数字 `api_key_id` 且无法用完整 `apiKey` 对齐 settings 列表时 SHALL 以闭码失败，SHALL NOT 回退为全站 API Key 行。

#### Scenario: Key 级不含其他 Key

- **GIVEN** analysis 全站含 Key K 与 Key L 的用量，K 的 `api_key_id` 过滤结果只有认证渠道 Codex。
- **WHEN** 为 K 的专属通知生成正文。
- **THEN** 周期节不出现 L 的展示名或 tokens。

#### Scenario: 占比用渠道全站分母

- **GIVEN** Key K 在认证渠道 A 消耗 800000，渠道 A 全站同期 2000000。
- **WHEN** 生成 K 的专属通知正文。
- **THEN** 渠道 A 显示 800.00K tokens 与 40.00%，不显示 100.00%。

#### Scenario: 映射失败不发全站数据

- **GIVEN** `/usage/api-keys/settings` 中没有与绑定 Key 匹配的完整 `apiKey`。
- **WHEN** 采集 Key 级统计。
- **THEN** 任务记录闭码，正文不包含其他 Key 的渠道行，也不把失败写成 0 tokens。

### Requirement: 发送计划（按计划触发、人类友好下次时间、错过只补最近一次）

每条启用通知 SHALL 按自身（或跟随的全局默认）计划在东八区触发。固定间隔 SHALL 从该通知该 scope 上次成功发送起算间隔，不对齐日内钟点；从未成功发送过则到期一次。每月/每年 SHALL 按日历墙钟。插件重启或挂起后若错过计划点，SHALL 只补最近一次，SHALL NOT 把轮询时刻写成计划锚点。全局列表、Key 列表每一行以及服务卡的下次触发 SHALL 展示东八区 `YYYY-MM-DD HH:mm:ss`，SHALL NOT 使用 RFC3339 的 `T` 与数字时区偏移作为唯一展示。

#### Scenario: 每隔在第二拍仍发送

- **GIVEN** 通知为每隔 60 秒，上次成功发送为 T。
- **WHEN** 时钟到达 T+60 秒且之后的轮询到来。
- **THEN** 生成新的待发任务（同平台无在途 pending 时）。

#### Scenario: 轮询不再推迟每隔

- **GIVEN** 每隔 1 天，服务持续运行。
- **WHEN** 15 秒轮询多次发生。
- **THEN** 下次发送时间保持「上次成功 + 1 天」，不随每次轮询向后滑动。

#### Scenario: 重启只补最近一次

- **GIVEN** 每月月初 09:00 的计划，上次成功早于本月月初，当前已过本月月初。
- **WHEN** 服务启动并完成一轮 scheduleTick。
- **THEN** 补发本月这一次，不补更早月份。

#### Scenario: 总开关关闭不生成

- **GIVEN** `Notifications.Enabled` 为 false，某通知计划已到期。
- **WHEN** scheduleTick 运行。
- **THEN** 不入队新任务。

#### Scenario: 列表展示人类友好时间

- **GIVEN** 下次触发为 2026-09-15 08:44:47 东八区。
- **WHEN** 打开全局或 Key 通知列表。
- **THEN** 该行显示 `2026-09-15 08:44:47`，不显示 `2026-09-15T08:44:47+08:00` 作为唯一文案。

### Requirement: 平台身份配置（全局用户唯一 ID 改为选填）

全局启用平台 SHALL 仍要求合法 Webhook；用户唯一 ID SHALL 为选填。仅当该平台「@所有人」开启时 SHALL @所有人；未开启且未填用户 ID 时 SHALL 只发群消息、不 @。Key 级通知 SHALL 不提供 @所有人，启用平台必须填写用户唯一 ID，保存时 SHALL 剥离 at_all。

#### Scenario: 全局空用户 ID 可保存并发送

- **GIVEN** 全局通知启用飞书，已填 Webhook，用户唯一 ID 为空，@所有人关闭。
- **WHEN** 保存并到期发送。
- **THEN** 保存成功；发出的飞书 text 不含 `<at>`；不因缺用户 ID 失败。

#### Scenario: 打开 @所有人

- **GIVEN** 全局飞书 @所有人开启。
- **WHEN** 发送。
- **THEN** 消息含官方 @all 语法。

#### Scenario: Key 级仍要用户 ID

- **GIVEN** Key 级启用飞书且用户 ID 为空。
- **WHEN** 保存。
- **THEN** 字段行内报错，保存被拒绝。

### Requirement: 投递身份解析（按通知实体取 webhook）

定时发送 SHALL 使用该 job 对应通知实体上、该平台的 Webhook、签名与用户 ID。SHALL NOT 在全配置里按平台 kind 取第一条启用身份。

#### Scenario: 两条通知不同 webhook

- **GIVEN** 全局通知 G 与 Key 通知 K 都启用飞书，Webhook 分别为 U1、U2。
- **WHEN** 到期发送 K 的飞书 job。
- **THEN** HTTP 请求打到 U2，不打到 U1。

## REMOVED Requirements

无整条删除。原「Key 无通知时按全局默认通知对该 Key 发送」场景由「多通知配置与继承」的新版本取代，不再作为有效行为。

## 方案设计

### 架构与组件

- `notification_schedule.go`：保留 `nextTrigger`；新增 due 判定（结合 last_success 与 now）。
- `notification_store.go`：新增 clock bucket；成功投递后写入 last_success。
- `notification_service.go`：删除 `lastFire`；每次 tick 读 `loadedStateSnapshot()`；全局循环渠道 × 通知，Key 循环绑定 × 专属通知；`identityFor` 改为按 NotificationID（及 Key 指纹）解析。
- `notification_keeper.go`：解析 `auth_files_composition` 与 `ai_provider_composition`；Key 级经 `/usage/api-keys/settings` 映射 `api_key_id`。
- `notification_template.go`：分行渲染。
- `notification_management.go`：preview 返回 `platforms[]`；settings/status 带人类友好 `next_fire`。
- `notification_members_client.go`：空名走 users/batch。
- 前端：Semi Modal 三栏；列表下次发送列；全局校验放宽；成员选项空名回退。

### 数据流

分析时间窗 → 渠道集合 → due(clock, plan, now) → collect/render（全局单渠道 / Key 级该 Key 全部身份）→ 每启用平台 UpsertJob → send → accepted 则更新 clock。预览走同一渲染、不入队。

clock 键：`notification_id` + scope（`global` 或 Key 指纹）+ 渠道 identity（Key 级整包发送可用占位 `-`）。

### 关键接口

- `GET/PUT /notifications/settings`：每条全局通知增加 `next_fire`（人类友好东八区字符串，未启用可空）。
- Key 绑定的 `notifications[]` 每条同样带 `next_fire`（由 GET 投影，不必客户端保存）。
- `GET /notifications/status`：`next_fire` 改为同一人类友好格式。
- `POST /notifications/preview`：`{key?, notification_id}`；Key 级必须带 `key`。响应 `{platforms:[{kind,text,warnings}], warnings, bytes}`，仅启用平台。
- Keeper：`GET /api/v1/usage/analysis`（可选 `api_key_id`）；`GET /api/v1/usage/api-keys/settings`（完整 apiKey 映射）。不改 Keeper。

### 错误处理

Keeper 映射/认证/超时：闭码，不更新 clock，不发零值。渠道集合为空：跳过入队。飞书补名失败：成员 ready 但姓名缺失。平台失败：clock 不前进。总开关关闭时不生成任务。

## 测试与验收策略

### 测试落点声明

公共入口：`nextTrigger` 与 due 纯函数、`startNotificationService`/`scheduleTick`（可控时钟、临时 bbolt）、`collectForPeriod`（httptest 固定 composition）、`dispatchManagement` preview/settings、飞书成员 httptest、`NotificationEditor`/`NotificationsPanel`/`KeyNotificationsTab`/`PlatformIdentityEditor`。允许替换：Keeper/平台 HTTP、时钟、临时文件。不替换分行渲染、due 判定、占比是否混入其他 Key。

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|---|---|---|---|
| 每隔第二拍仍发送；轮询不滑动 | unit | 任务内 TDD | due + scheduleTick |
| 重启只补最近一次 | integration | 任务内 TDD | 临时 bbolt clock |
| 全局两渠道两条正文 | integration | 任务内 TDD | job payload |
| Key 级不含其他 Key；映射失败闭码 | integration | httptest | 正文/错误码 |
| 定时使用实体 webhook | integration | 任务内 TDD | 请求 URL |
| 预览三栏、未启用不出现、不发送 | unit + 组件 | 任务内 TDD | preview JSON + Modal |
| 全局空用户 ID 可保存 | unit + 组件 | 任务内 TDD | 校验与 PUT |
| 飞书空名补齐或纯 ID | integration + 组件 | httptest + vitest | members.name |
| 列表人类友好时间 | 组件 | 任务内 TDD | 行文案无 `T`/`+08:00` 唯一展示 |
| 桌面/窄屏预览弹窗 | visual | 验收任务 | 浏览器 |
| 真实三平台群消息 | manual | 授权后 | 截图；未授权则 pending |

## 风险与边缘情况

- `/usage/api-keys/settings` 含完整 Key，映射仅进程内比对，不写入通知正文或审计。
- 零用量认证渠道不会出现在全局循环（与「实际用量身份」一致）。
- 官方无气泡 CSS；UI 标明结构还原。
- interval 的 periodKey 不再用每次 tick 的 RFC3339 当合并键，避免无法去重；改为 last_success 锚点上的稳定键。
- 旧 lastFire 路径删除；进行中的进程升级后首次 interval 视为从未成功，会立即发一次。

## 开放问题

无阻塞项。`clock` JSON 字段名与 preview `platforms` 字段名在实施计划中与现有 Go/TS 类型对齐即可。
