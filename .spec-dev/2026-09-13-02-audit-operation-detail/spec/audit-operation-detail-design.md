---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: audit-operation-detail
  status: active
  covers:
    - "audit.go"
    - "audit_query.go"
    - "audit_management.go"
    - "audit_labels.go"
    - "audit*_test.go"
    - "management_audit_test.go"
    - "management.go"
    - "notification_management.go"
    - "channel_credentials.go"
    - "web/src/api.ts"
    - "web/src/panels/AuditPanel.tsx"
    - "web/src/panels/AuditPanel.css"
    - "web/src/panels/AuditPanel.test.tsx"
  sync_commit: 6d56980fe490ac68cdcb27e668aff5bdac7e95b4
  supersedes: []
  superseded_by: null
---

# 审计操作细粒度呈现设计（audit-operation-detail）

## 背景与目标

现行审计只记录 `object_type` + 哈希化 `object_ref` + 整块 JSON diff：Key 操作无法辨认是哪个 Key；渠道定向把 enabled/suppliers/auth_ids 混成一个字段，看不出改了哪些渠道；`/notifications/*` 写操作被误归为 `key_binding/unspecified` 且变更全部丢失；`test-send` 的 finish 事件因 `Changed=nil` 违反 `validAuditEvent` 被读侧判 invalid 丢弃。

本特性把审计升级为「模块 → 字段 → 旧值 → 新值」的细粒度呈现：事件升级 v2（module、object_label、labels 快照），查询 API 增加模块筛选与计数，管理页审计面板重设计为行内展开的字段级 diff 视图，Key 显示别名+尾号，渠道定向显示渠道自定义名称。呈现方案已经用户在 visual-preview 会话确认：详情**行内展开**、Key 对象**别名 + 尾号**（定稿 mockup：`spec/assets/audit-redesign-final.html`）。

## 非目标

- 不改变审计的写入门禁、日文件布局、原子写与 Sync 语义（ADR 沿用 2026-09-12-01 特性）。
- 不回填历史：v1 事件保持原样可读，读侧以 `object_type` 推导模块显示，不获得 v2 新字段。
- 不把 Webhook/签名密钥明文入审计：脱敏范围只扩大不缩小。
- 不在审计写路径发起任何网络调用（包括 Keeper）。

## 数据契约（前后端并行实现的锚点）

### 事件 v2（`auditEvent` 新增字段）

- `module`：`rules | key_binding | notifications | keeper_auth_name | other`（枚举，v2 start/finish 均必填）。
- `object_label`：写入时快照的对象显示名（Key 别名、全局规则、通知名、认证名等）。
- `labels`：`map[string]string`，变更中出现的渠道 ID → 显示名快照（供应商 provider → provider_label；凭据 ID → "provider_label · Key •••尾4"）。

读取合并规则：`object_label`/`labels` 以 **finish 事件优先**（创建类操作 start 时还看不到新别名），缺省回落 start 事件。

### 查询 API

`GET /audit?date=&page=&page_size=&module=`，响应新增：

- `module`（回显筛选值，未筛时缺省）
- `module_counts: {rules: n, key_binding: n, …}`（全日未筛选计数）
- items 每项新增 `module`、`object_label?`、`labels?`

`module` 非法枚举值 → 400 `invalid_audit_module`。`total` 为筛选后计数。

### ObjectRef / ObjectLabel / Changes 字段表

| 操作 | module | action | object_ref | object_label | changes 字段 |
|---|---|---|---|---|---|
| PUT /rules | rules | update | `rules` | 全局规则 | `rules.global/claude/codex/openai` |
| POST /keys | key_binding | create/update | `key:••••<尾4>` | 别名（body 优先，before 次之） | `alias`、`enabled`、`blocked`、`rules.*`、`channel_target.enabled/suppliers/auth_ids`、`fast_allowed`、`notifications`（`[{id,name,enabled}]` 投影） |
| PATCH /keys | key_binding | update | 同上 | 别名（before 优先） | 同上 |
| DELETE /keys | key_binding | delete | 同上 | 别名（before 快照） | 同上 |
| PUT /notifications/settings | notifications | update | `global` | 全局通知设置 | `notifications.enabled`、`notifications.global_default.enabled/.modules/.schedule/.platforms` |
| POST /notifications/test-send | notifications | test_send | `key:••••<尾4>` 或 `notification:<id>` 或 `notification:default` | Key 别名或通知名 | `{}`（changed=false） |
| PATCH /keeper/auth-names | keeper_auth_name | update | auth_index | body.alias（操作目标名） | `alias`（由现有 keeper 流程提供） |

渠道定向 diff 拆分：原单字段 `channel_target` 拆为 `channel_target.enabled`（bool）、`channel_target.suppliers`（[]string）、`channel_target.auth_ids`（[]string）。

## ADDED Requirements

### Requirement: R1 模块归属与筛选

每个 v2 审计事件 SHALL 携带模块枚举，查询端点 SHALL 支持模块筛选并返回全日模块计数；v1 历史事件 SHALL 继续可读且不入垃圾桶告警。

#### Scenario: S1 模块正确归属
- GIVEN 分别触发规则保存、Key 增改删、通知设置保存、测试发送、Keeper 名称 PATCH。
- WHEN 读取当日审计。
- THEN 每项 `module` 与字段表一致；通知操作不再是 `key_binding/unspecified`；`test-send` 的 finish 事件通过校验出现在结果中（`changed=false`）。

#### Scenario: S2 筛选与计数
- GIVEN 当日含多模块操作。
- WHEN `GET /audit?module=key_binding`。
- THEN 仅返回该模块项，`total` 为其数量，`module_counts` 仍为全日各模块计数；`module=bad` 返回 400 `invalid_audit_module`。

#### Scenario: S3 v1 兼容
- GIVEN 仅含 v1 事件的日文件。
- WHEN 读取。
- THEN 不产生 `invalid_event` 告警，项缺失 v2 字段（`module` 为空、无 `object_label/labels`），分页与 outcome 语义不变。

### Requirement: R2 字段级变更呈现

变更 SHALL 按字段拆分记录前后值：渠道定向拆为总开关/供应商/认证三字段；Key 级通知以 `{id,name,enabled}` 投影；全局通知设置拆为总开关/默认通知各字段。

#### Scenario: S4 渠道定向拆分
- GIVEN 开启渠道定向且供应商增删、认证新增的 Key 保存。
- WHEN 读取该操作。
- THEN `changes` 含 `channel_target.enabled/suppliers/auth_ids` 各自独立前后值，不再有整块 `channel_target` 字段。

#### Scenario: S5 通知设置投影与脱敏
- GIVEN 修改全局通知的平台 Webhook 与签名密钥后保存。
- WHEN 读取该操作。
- THEN 平台字段以结构化投影出现；Webhook 与签名密钥值被 `[REDACTED]` 遮盖，但字段因值变化仍出现在 diff 中；Key 级通知的 Webhook/密钥同样纳入脱敏源。

### Requirement: R3 对象可读标识

Key 操作 SHALL 在写入时快照「别名 + 尾4」标识（`object_label`=别名，`object_ref`=`key:••••<尾4>`），不再记录 SHA-256 指纹；创建操作以请求体别名为准。

#### Scenario: S6 别名快照
- GIVEN 存在别名 `prod-deepseek` 的 Key。
- WHEN 对其 PATCH 封禁、随后 DELETE。
- THEN 两条操作 `object_ref` 均为 `key:••••<尾4>`，`object_label` 均为 `prod-deepseek`（DELETE 用删除前快照）；无别名 Key 的 `object_label` 缺省，前端回落显示 `object_ref`。

### Requirement: R4 渠道名称解析

审计事件 SHALL 在写入时对变更中的供应商/认证 ID 附带显示名快照（来自 `/channel-credentials` 解析时建立的进程内标签缓存，容量有上限）；前端读取时 SHALL 以事件快照优先、实时渠道目录与 Keeper 名称次之、原始 ID 兜底。

#### Scenario: S7 写入时快照
- GIVEN UI 刚解析过渠道目录（provider_label 已知），随后保存渠道定向变更。
- THEN 事件 `labels` 含新增/移除 ID → 显示名映射；缓存未命中或纯 curl 场景 `labels` 缺省，读侧兜底不报错。

#### Scenario: S8 读取时富化
- GIVEN 历史事件无 `labels` 或渠道已改名。
- WHEN 审计面板渲染。
- THEN 实时目录命中则显示当前名称，未命中显示原始 ID；富化请求失败不阻塞面板，静默降级。

### Requirement: R5 面板行内展开与主题适配

审计面板 SHALL 以行内展开呈现字段级 diff（bool 用开关态、数组用增/删/保留行、规则段用行级 diff、脱敏值有专属样式），模块 SHALL 可筛选；所有颜色 SHALL 使用 Semi Design 主题变量，深浅两主题下均无硬编码色值。

#### Scenario: S9 行内展开与摘要
- GIVEN 含渠道定向变更的当日审计。
- WHEN 点击该行。
- THEN 行内展开显示操作元信息与三字段 diff（供应商/认证增删行带名称）；收起时行内摘要 chips 显示 `供应商 +2 −1` 等概要；失败操作行内直接展示错误码含义。

#### Scenario: S10 主题适配
- GIVEN 系统处于深色主题。
- WHEN 打开审计面板。
- THEN 模块标签、diff 行、脱敏标记、分隔线均跟随 Semi 深色变量，无浅色残留。

### Requirement: R6 读侧合并与版本校验

`validAuditEvent` SHALL 按版本分流：v1 维持原规则；v2 额外要求 `module` 非空。`DisallowUnknownFields` 保持开启。

#### Scenario: S11 混合版本文件
- GIVEN 同一日文件内 v1 与 v2 事件混合。
- WHEN 读取。
- THEN 两者均被接受且各自按其规则校验，v2 项带新字段，v1 项不带。

## 验收矩阵

| 验收点 | 优先级 | 验证方式 |
|---|---|---|
| S1/S2/S3 模块归属、筛选计数、v1 兼容 | high | `go test . -run '^TestAudit' -count=1` 新增用例 |
| S4 渠道定向拆分 | high | `go test . -run '^TestManagementAudit' -count=1` 扩展 |
| S5 通知投影与脱敏 | high | 同上 + `auditSecrets` 单测 |
| S6 别名快照（create/update/delete） | high | 管理端到端单测 |
| S7 标签缓存 | medium | `audit_labels` 单测（含容量上限） |
| S8 前端富化降级 | medium | `AuditPanel.test.tsx`（富化失败不阻塞） |
| S9 行内展开/摘要/失败展示 | high | `AuditPanel.test.tsx` |
| S10 深浅主题 | medium | CSS 审查仅用 `--semi-color-*` 变量；浏览器 QA |
| S11 混合版本 | high | `audit_query` 单测 |
| 回归 | high | `go test . -run '^Test(Audit|ManagementAudit|Keeper|ManagementKeeper|Notification)' -count=1`、`go test -race ./...`、`npm --prefix web run typecheck`、`npm --prefix web test`、`make web-build` |

## 与既有 spec 的共存

- 修订 `2026-09-12-01-admin-audit-keeper-names` R5 中「Key 使用 SHA-256 指纹及掩码」及查询响应形状条款为 v2 语义（别名+尾4、模块筛选），其余门禁/日文件/串行约束不变。
- 与 key-usage-notifications、channel-target、key-rules-admin-ui 各 spec 分面共存：不改变保存语义、渠道定向取值空间与通知投递行为。

## 决策记录

- 名称快照写入 finish 事件并由读侧「finish 优先」合并：创建类操作 start 时尚无新别名，且避免在审计门禁里做任何网络调用。
- 渠道标签缓存放进程内、容量上限 4096：重启后旧事件已带快照，未命中场景由前端实时富化兜底。
- `test_send` 动作独立枚举而非复用 `sync`：前端动词映射（测试发送）与统计语义不同。
