---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
# covers: 本 spec 声称覆盖的代码路径 glob；这些路径被改动而本 spec 未同步时，
#         守卫（pre-commit / CI / Claude·Codex 的 PreToolUse hook）会拦截并提示。
spec_dev:
  version: 1
  feature: channel-target-empty-pool-error-form
  status: active           # draft | active | superseded —— 仅 active 参与漂移拦截
  covers:                  # 该特性拥有的代码 glob；无代码产物时留空数组 []
    - "main.go"
    - "main_test.go"
    - "scheduler_test.go"
  sync_commit: null        # 最近一次"代码与本 spec 已同步"的提交 SHA；由 executing-plans
                           # 收尾在合并后写入（计划最终任务的锚定步骤）。
                           # git diff <sync_commit>..HEAD -- <covers> = 此后的代码变化
  supersedes:              # 本 spec 取代的旧 spec 路径列表（仓库根相对），设计期由阶段 6 取代分流
                           # 填写；部分取代同样登记于此，粒度细节写正文「取代与共存」节；无取代留空数组。
    - ".spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md"
  superseded_by: null      # 本 spec 被取代时由交付回写填入后继 spec 路径（仓库根相对），
                           # 与 status: superseded 必须成对出现；消费方读到后沿此指针跳转后继。
---

# 渠道定向池空错误形态（503 → 429 + 结构化错误体）设计

## 背景与目标

key 经 channel target 定向到凭据集合（典型为单一订阅）时，目标凭据进入短暂不可用窗口（上游冷却退避、access token 刷新中、配额恢复期、401 后状态未翻正）会被宿主在征询插件 scheduler 前整体剔除出 Candidates；定向池因此为空，插件按「报错不降级」契约返回 HTTP 503 `auth_not_found`。503 语义误导（服务故障 ≠ 目标资源暂时受限）且错误体匿名不可诊断——客户端调用者与管理员无法从错误判断是哪个 key 的定向池空了。

本变更把池空错误形态改为 **HTTP 429 + 结构化错误体**：状态码与宿主自身凭据冷却错误形态（429 `model_cooldown`）一致，错误体携带聚合计数使偶发失败可从客户端错误直接定位。「绝不使用池外凭据」红线不变。

**成功标准 / Success criteria**：定向池空时客户端收到 HTTP 429，错误体为合法 JSON（`error.type`/`error.code`=`auth_not_found` 保持断言兼容）、message 表达「目标渠道凭据当前不可用、可稍后重试」、`error.detail` 携带可见候选数与绑定集合计数且不含任何具体凭据 ID、供应商名或 key 值；红线行为与现状完全一致。

## 非目标

- 不实现池空时借道池外凭据或任何形式的降级调度（红线保留，用户裁决 2026-09-15）。
- 不修改宿主 CPA（SDK 契约、宿主逻辑均不动；错误信息面维持现状，用户裁决 2026-09-15）。
- 不提供 `Retry-After`——插件拿不到冷却剩余时间（信息边界），不伪造数值。
- 不做「绑定指向不存在凭据」的配置错场景细分（方案 C，YAGNI 裁剪，见风险节）。
- 不改管理 UI（运行时错误形态，管理页不展示）。

## 术语表

沿用 `.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md` 既有术语（候选池、定向、suppliers/auth_ids、Candidates），无新增共享术语；本 spec 无特性局部新术语。

## 参与者与适用行为

| 参与者 | 适用行为 / 错误路径 |
|---|---|
| OpenAI/Codex 客户端调用者（绑定 key 持有者） | 池空时收到 429 + 错误体（断言 `error.code=auth_not_found`）；按可重试语义退避重试 |
| Claude 协议客户端调用者 | 同一错误出口（断言 `error.type=auth_not_found`），同一 429 形态 |
| 管理员 | 经宿主 pluginhost scheduler 适配层的 Warn 日志（`pluginhost: scheduler rejected auth pick`，含 `plugin_id` 与完整错误 message，即新结构化 JSON）与错误体 `detail` 计数定位「哪个 key 的定向池空、池外还有多少候选」 |

## 影响面

- `main.go`：`handleSchedulerPick` 池空分支（错误体构造由常量改为函数、`HTTPStatus` 503→429）。
- `scheduler_test.go`：现有 503 断言用例（`assertChannelTargetAuthNotFound`）同步；新用例沿既有模式落此文件。
- `README.md`：面向用户的行为文档（第 109、111 行附近「过滤为空时返回 HTTP 503」「返回 503」）同步为 429 与新错误体描述。
- 旧 spec 全部池空错误形态表述（部分取代，交付时回写，范围见「取代与共存」节）+ `.spec-dev/2026-08-22-channel-target-and-fast-control/acceptance/host-version-compat.md` 断言更新。
- 无新依赖、无宿主改动、无前端改动、无 state 结构变更。

## 已确认的关键决策

- 保留「绝不使用池外凭据」红线，仅改错误形态 —— 用户裁决（2026-09-15 契约姿态题）。
- 不修改宿主 CPA —— 用户裁决；插件对状态码与错误体已有完全控制权（透传链逐跳核实：`pluginMethodError` → ABI `Envelope.Error{http_status}` → `rpcError.statusCode` → `clienterror.HTTPStatusFromError` 显式值优先 → `BuildErrorResponseBodyWithError` 对合法 JSON message 原样作为响应体）。
- 状态码选 429 而非维持 503（方案 A，用户选定）—— 语义准确（目标资源暂时受限 ≠ 服务故障）且与宿主自身凭据冷却 429 `model_cooldown` 形态一致。
- `error.type`/`error.code` 保持 `auth_not_found` —— 断言兼容（宿主/客户端按 code/type 断言），且语义比硬套 OpenAI `rate_limit` 类 code 更准（本质是目标渠道凭据暂不可用，非限流）。
- `error.detail` 仅聚合计数（`candidates`、`binding.suppliers`、`binding.auth_ids`）—— 错误体面向客户端调用者，内部配置（凭据 ID、供应商名）与 key 任何形态不外泄。
- Retry-After 不做 —— ABI `Error` 无 Headers 字段且无冷却剩余时间数据源；客户端用默认退避（重试本已发生，见风险节）。

## 约束归属与拒绝的解读

- 「绝不使用池外凭据」红线 → 负责边界：`handleSchedulerPick` 过滤逻辑（不变）→ 验证：MODIFIED Requirement 1 的 Scenario「目标冷却但池外仍可用」「目标被全局优先级收窄排除」。
- 错误体必须为合法 JSON → 负责边界：错误体构造函数 → 验证：Scenario「池内候选全部不可用」（宿主 `BuildErrorResponseBodyWithError` 仅对合法 JSON 原样透传，此为透传前提）。
- `detail` 脱敏 → 负责边界：错误体构造函数 → 验证：Scenario「错误体脱敏」。
- 拒绝的解读：「429 = rate limit，应配 `rate_limit_exceeded` 类 code」——排除。本错误本质是目标渠道凭据暂不可用；保持 `auth_not_found` 兼容既有断言且语义更准（已确认决策）。
- 拒绝的解读：「错误体应携带具体原因（冷却/token 刷新/配额）」——排除。池空时插件只见宿主过滤后的 Candidates，无法区分成因（信息边界）；message 仅枚举可能成因作提示，不声明确定原因。

## 取代与共存

- [部分取代] `.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md`。回写分两类形制：
  - **Requirement 级取代**（新 spec 差量三节给出 MODIFIED 完整新版接管，旧 spec 该 Requirement 标题下插 Superseded 标注、正文保留原文仅作历史参考）：
    - Requirement「定向池空时显式报错不降级」—— 错误形态从 HTTP 503 改为 HTTP 429 + 结构化错误体（新增脱敏约束），「报错不降级」红线本身不变。
    - Requirement「渠道定向候选池过滤」—— Scenario「单选认证文件命中」THEN 的失败形态断言 503→429；Scenario「历史失败的目标凭据恢复可用」THEN 的「返回 503」措辞改为「返回错误」；其余 Scenario 不变。
  - **关联文本同步**（旧 spec 保持该处现行有效，仅更新池空错误形态表述使其与交付后行为一致）：
    - Requirement「AI Providers 凭据目录与精确勾选」—— 正文中「保持现有保存结构、池内轮转、池空 503 及宿主预过滤边界」的「池空 503」措辞同步为 429；该 Requirement 其余行为（目录、勾选、保存结构）不变，不标注 Superseded。
    - 非 Requirement 区的 503 表述一并同步：成功标准（交集为空时 HTTP 503）、设计概述（约 70、78、334、356、358、363 行）、错误处理表（372-374 行）、验收矩阵（391-393 行）及风险节相关断言；H1 下 Superseded-pending 行移除。
- 无其它相交 active spec。

## ADDED Requirements

无（本次全部为既有行为的形态修改）。

## MODIFIED Requirements

### Requirement: 定向池空时显式报错不降级（改了什么：失败状态码 503→429；错误体升级为结构化 JSON——message 语义化、新增 `detail` 聚合计数与脱敏约束；红线与 code/type 不变）

宿主征询插件调度且过滤后候选池为空时（如目标凭据未进入 Candidates、或目标供应商不在本次解析结果中），请求 SHALL 以 HTTP 429 的 `auth_not_found` 类错误失败，SHALL NOT 静默改用池外凭据。插件 ABI SHALL 保留 `Code=auth_not_found`，且 message SHALL 是同时含 `error.type=auth_not_found` 与 `error.code=auth_not_found` 的合法 JSON；`error.message` SHALL 表达「绑定渠道的凭据当前均不可用、可稍后重试」的语义（可枚举冷却/刷新/配额等可能成因，但 SHALL NOT 声明确定成因——插件在池空时无法区分）。错误体 SHALL 含 `error.detail`，其内容 SHALL 仅限聚合计数：宿主可见候选数（`candidates`，恒 ≥1）与绑定集合计数（`binding.suppliers`、`binding.auth_ids`）；SHALL NOT 出现任何具体凭据 ID、供应商名或客户端 key 的任何形态。

#### Scenario: 池内候选全部不可用

- **GIVEN** 绑定 key K 定向开启，宿主征询调度时 Candidates 中无任何属于 K 候选池的可用凭据
- **WHEN** K 发起请求
- **THEN** 请求失败（HTTP 429）；OpenAI/Codex 错误体含 `error.code=auth_not_found`，Claude 错误体含 `error.type=auth_not_found`，错误体为合法 JSON 且含可重试语义的 `error.message` 与 `error.detail` 计数；绝不使用池外凭据

#### Scenario: 目标冷却但池外仍可用

- **GIVEN** 绑定 key K 定向开启，目标池唯一认证文件 F1 处于 cooldown，池外认证文件 F2 为 active
- **WHEN** K 发起请求
- **THEN** 宿主在 Scheduler 前排除 F1，插件过滤掉 F2 并返回协议对应的 HTTP 429 `auth_not_found` 类错误；绝不使用 F2

#### Scenario: 目标被全局优先级收窄排除

- **GIVEN** 绑定 key K 定向开启并只选低优先级 active 凭据 F1，池外存在更高优先级 active 凭据 F2
- **WHEN** 宿主只把全局最高优先级层 F2 交给 Scheduler
- **THEN** 插件过滤后返回协议对应的 HTTP 429 `auth_not_found` 类错误；不选择不在 Candidates 中的 F1，也不使用 F2

#### Scenario: 错误体脱敏

- **GIVEN** 绑定 key K 定向开启且定向池为空，Candidates 含池外凭据（其 ID、Provider 为非空内部值）
- **WHEN** 插件返回池空错误
- **THEN** 错误体全文（含 `detail`）不含任何传入凭据 ID、供应商名或客户端 key 值，仅含聚合计数

#### Scenario: detail 诊断计数正确

- **GIVEN** 绑定 key K 定向开启（suppliers 空、auth_ids 含 1 项），宿主征询时 Candidates 有 3 条池外凭据，过滤后定向池为空
- **WHEN** 插件返回池空错误
- **THEN** `error.detail.candidates=3`、`error.detail.binding.suppliers=0`、`error.detail.binding.auth_ids=1`

宿主全局无任何可选凭据时 MAY 在调用 Scheduler 前返回原生 429 `model_cooldown` 与 `Retry-After`；这是 CPA 宿主边界行为，不是本插件的 SHALL 承诺。变更后两侧错误形态（429）一致性更强。

### Requirement: 渠道定向候选池过滤（改了什么：Scenario「单选认证文件命中」THEN 的失败形态断言 503→429；Scenario「历史失败的目标凭据恢复可用」THEN 的「返回 503」措辞改为「返回错误」以免状态码硬编码；过滤规则与其它 Scenario 原样）

当某 key 的渠道定向开关开启时，该 key 发起的每个模型请求 SHALL 只能由候选池内的凭据执行：候选 = 宿主交给 Scheduler 的 Candidates ∩（所选供应商 ∪ 单独所选凭据）。宿主在 Scheduler 前已按模型能力、可用性、cooldown 与全局最高优先级收窄 Candidates；插件 SHALL NOT 选择不在 Candidates 中的 AuthID。池内多个凭据时插件按确定性顺序（ID 排序）轮转分配。

对于宿主已判定当前请求可用并放入 Candidates 的目标凭据，插件 SHALL NOT 仅因其 `Status=error` 再次排除；该状态可能来自已结束的冷却或其他模型的历史失败，不代表当前请求不可用。

#### Scenario: 单选认证文件命中

- **GIVEN** 绑定 key K 定向开启，auth_ids 含认证文件 F1（provider=claude），suppliers 为空
- **WHEN** K 发起一个 claude 格式请求
- **THEN** F1 位于宿主 Candidates 时该请求由 F1 执行；F1 未进入 Candidates 但宿主仍征询 Scheduler 时请求收到 HTTP 429 `auth_not_found` 类错误；两种情况均绝不使用其他凭据

#### Scenario: 供应商整选动态入池

- **GIVEN** 绑定 key K 定向开启且 suppliers 含 gemini
- **WHEN** 管理员向 CPA 新增一个 gemini 认证文件后 K 再发起请求
- **THEN** 新文件无需修改绑定即参与 K 的候选池

#### Scenario: 池内多凭据轮转分摊

- **GIVEN** 绑定 key K 定向开启，候选池含同优先级的 3 个可用认证文件
- **WHEN** K 连续发起多个请求
- **THEN** 请求在这 3 个凭据间轮转分摊（每个都承担流量），且绝不落在池外凭据上

#### Scenario: 无法识别上下文时 fail-open

- **GIVEN** 插件收到 scheduler.pick 回调但无法从请求头提取客户端 key（或该 key 无绑定）
- **WHEN** 宿主等待调度决策
- **THEN** 插件返回 Handled=false，宿主按其默认策略自由调度（不报错）

#### Scenario: 历史失败的目标凭据恢复可用

- **GIVEN** 绑定 key K 按供应商或认证文件定向到 F1；F1 的凭据级或当前模型冷却已经结束，但 `Status` 仍为 `error`
- **WHEN** 宿主将 F1 作为当前请求的可用 Candidates 交给插件
- **THEN** F1 正常参与定向池内调度，不因历史 `error` 状态返回错误，也不使用池外凭据

#### Scenario: 其他模型失败不影响当前可用模型

- **GIVEN** F1 因模型 A 失败而处于 `Status=error`，当前请求模型 B 可用；绑定 key K 按供应商或认证文件定向到 F1
- **WHEN** 宿主针对模型 B 将 F1 放入 Candidates
- **THEN** F1 正常参与 K 的定向池内调度，不受模型 A 的失败状态影响

## REMOVED Requirements

无。

## 方案设计

### 架构与组件

改动集中在 `main.go` 的 `handleSchedulerPick` 池空分支：

- `channelTargetAuthNotFoundMessage` 常量（main.go:808）→ 函数 `channelTargetUnavailableBody(candidateCount int, target ChannelTarget) string`：序列化结构化错误体 JSON（纯字符串/整数字段，序列化不可失败）。
- 池空分支（main.go:837-843）：`HTTPStatus` 由 `http.StatusServiceUnavailable` 改为 `http.StatusTooManyRequests`；`Message` 改用上述函数，入参 `len(req.Candidates)` 与 `binding.ChannelTarget` 均在作用域内。

### 数据流

```
codex/Claude 请求 → CPA pickNextLegacy
  → availableAuthsForSelector（宿主剔除冷却/过期/401，仅最高优先级层）
  → pickViaPluginScheduler → handleSchedulerPick
      定向池空 → pluginMethodError{Code:auth_not_found,
                  Message:结构化 JSON, HTTPStatus:429}
  → ABI Envelope.Error → rpcError{statusCode:429}
  → conductor 上浮（pick 失败不进 MarkResult，无凭据误伤）
  → WriteErrorResponse → body=message JSON 原样、status=429
  → 客户端按 429 默认退避重试
```

### 关键接口

错误体形态（message 字符串，宿主原样透传为响应体）：

```json
{"error":{"type":"auth_not_found","code":"auth_not_found",
  "message":"all credentials in the channel target are currently unavailable (cooling down, refreshing, or quota-limited); retry later",
  "detail":{"candidates":3,"binding":{"suppliers":0,"auth_ids":1}}}}
```

- `candidates`：宿主本次征询传入的 Candidates 数量，恒 ≥1（宿主 `pickViaPluginScheduler` 仅在 Candidates 非空时征询插件）。
- `binding.suppliers` / `binding.auth_ids`：该 key 绑定集合的元素计数。

### 错误处理

- suppliers 绑定（非 auth_ids）池空：同形态 429，`detail.binding` 反映 `{suppliers:N, auth_ids:0}`。
- 宿主全局无凭据：宿主在征询插件前已返回自身错误（429 `model_cooldown` / `auth_unavailable`），插件不参与（现状不变）。
- 同请求 failover 重挑（绑定凭据已 tried）：非 Home 模式宿主优先返回上游 `lastErr`（现状不变）；若冒出亦为新形态。
- 宿主日志：pluginhost scheduler 适配层以 Warn 级记录 `pluginhost: scheduler rejected auth pick`（含 `plugin_id` 与完整错误 message），新结构化 JSON 直接入日志，可诊断性自动受益。

## 测试与验收策略

### 测试落点声明

- 公共接口：既有单测入口——直接调用 `handleSchedulerPick(raw []byte) ([]byte, error)`（main_test.go / scheduler_test.go 既有模式），经返回的 `*pluginMethodError` 断言 `HTTPStatus` 与 `Message`（JSON 解析各字段）。不新增接口。
- 覆盖 Scenario：MODIFIED Requirement 1 全部 5 个 Scenario、MODIFIED Requirement 2 的 Scenario「单选认证文件命中」失败分支。
- 允许替换的外部依赖：无（纯构造 + 既有调度入口；宿主透传不在单测面，由验收任务覆盖）。
- 来源：2026-09-15 阶段 5 完整设计获批。

### 验收矩阵

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| 池内候选全部不可用（429 + 结构体断言） | unit | 任务内 TDD（fast） | 测试通过 |
| 目标冷却但池外仍可用（candidates 无绑定项 → 429，池外凭据不用） | unit | 任务内 TDD（fast） | 测试通过 |
| 目标被全局优先级收窄排除（同构造面，断言同一 429 形态） | unit | 任务内 TDD（fast，可与上行共用用例不同子测） | 测试通过 |
| 单选认证文件命中（未进 Candidates 失败分支 → 429） | unit | 任务内 TDD（fast） | 测试通过 |
| 错误体脱敏（全文不含凭据 ID/供应商名/key 值） | unit | 任务内 TDD（fast） | 测试通过 |
| detail 诊断计数正确（candidates/binding 计数；含 suppliers 整选 `{suppliers:N, auth_ids:0}` 对称性子测） | unit | 任务内 TDD（fast） | 测试通过 |
| 宿主链路 429 透传到客户端（codex 与 claude 协议路径） | integration | 验收任务（D，manual/冒烟 + host-version-compat 断言更新重跑） | 验收报告 |

注：MODIFIED Requirement 2 中本次未变更的 Scenario（供应商整选动态入池、池内多凭据轮转分摊、无法识别上下文时 fail-open、历史失败的目标凭据恢复可用、其他模型失败不影响当前可用模型）沿用既有验收资产，不在本矩阵重复立项。

## 风险与边缘情况

- codex 客户端对 429 与 503 的重试节奏差异未读其源码验证——重试在 503 下已实际发生（症状中的 Reconnecting），最坏情况是退避节奏变化，非行为退化；如实告知，不阻塞。
- 429 无 `Retry-After`：客户端用默认退避（已裁决：无数据源，不伪造；宿主自身的 429 `model_cooldown` 有 Retry-After 是宿主层信息，插件无法获得）。
- 断言兼容面：宿主/客户端对 `error.code`（OpenAI/Codex）与 `error.type`（Claude）的断言不变；`.spec-dev/2026-08-22-channel-target-and-fast-control/acceptance/host-version-compat.md` 中涉及 503 的断言需随交付更新并重跑。
- 未来扩展（本次裁剪，方案 C）：经管理 API 反查 + 缓存区分「绑定指向不存在凭据」（配置错，恒失败）与「短暂窗口」——记录为非目标，需求出现时按新特性立项。

## 开放问题

无。
