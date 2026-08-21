---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: channel-target-and-fast-control
  status: draft
  covers:
    - "main.go"
    - "main_test.go"
    - "state.go"
    - "state_test.go"
    - "management.go"
    - "management_test.go"
    - "scheduler_test.go"
    - "fast_strip_test.go"
    - "web/src/panels/KeysPanel.tsx"
    - "web/src/components/ChannelTargetEditor.tsx"
    - "web/src/api.ts"
  sync_commit: null
  supersedes: []
  superseded_by: null
---

# 渠道定向与 Fast 控制 设计

## 背景与目标

key 绑定目前只能追加模型映射规则和做访问禁用。需要两个新控制维度：(1) 把某个客户端 key 的请求**定向**到指定的 AI 供应商 / 认证文件集合（候选池内正常调度，池空报错不降级）；(2) 对每个 key 控制 **fast 模式**准入——关闭时把 fast 请求覆盖成普通请求。

**成功标准**：绑定渠道定向后，该 key 的请求只可能由所选供应商 ∪ 所选认证文件中的凭据执行；关闭 Fast 允许后，该 key 的任何 fast 标记（`speed:"fast"` body 字段或 `fast-mode-2026-02-01` beta 头）在到达上游前被剥离；管理 UI 可视化配置两者。

## 非目标

- 不实现按选择顺序的优先级降级调度（池内交给宿主内建策略）。
- 不做流式响应的模型名还原（SSE 逐块改写风险高，收益低）。
- 不在 UI 上检测/展示 Scheduler 调度权冲突状态。
- 不代理 CPA 的 auth-files 接口（前端直调宿主 API）。
- 不支持 OpenAI Responses 等非 Claude 协议的 fast 变体（CPA 宿主中 fast 是 Claude 协议专属语义）。

## 术语表

- **渠道定向（channel target）**：某 key 绑定上的一组「供应商整选 + 认证文件单选」及总开关；生效时请求只能由该集合内的凭据执行。_Avoid_：渠道绑定、通道指定。
- **候选池（candidate pool）**：定向开启时，「所选供应商下的全部可用认证文件 ∪ 所选认证文件」的并集。
- **Fast 允许（fast allowed）**：绑定级开关；false 时该 key 的 fast 标记被剥离为普通请求。_Avoid_：加速模式、turbo。
- **认证文件（auth file）**：CPA 宿主中的一条凭据记录（`GET /v0/management/auth-files` 条目），有唯一 `id` 与归属 `provider`。
- **fail-open**：插件无法识别请求上下文（拿不到 key、无绑定、功能关）时不干预、交还宿主正常流程的行为约定。

## 影响面

- **Go 后端**：`state.go`（KeyBinding 结构与校验）、`main.go`（能力注册、`handleSchedulerPick`、拦截器扩展、响应还原）、`management.go`（PATCH 扩展、preview 定向感知）。
- **依赖**：新增直接引用 `tidwall/gjson`、`tidwall/sjson`（已在 go.mod 间接依赖树中，升为 direct，无新外部依赖）；SDK 能力声明新增 `Scheduler` 与 `ResponseInterceptor`。
- **前端**：`KeysPanel.tsx` 表格列与弹窗重构、新组件 `ChannelTargetEditor.tsx`、`api.ts` 类型与新接口函数。
- **外部系统**：CPA 宿主（`v7.2.119` SDK 契约：`scheduler.pick`、`response.intercept_after`）；Scheduler 能力全宿主单实例（见风险）。

## 已确认的关键决策

- 定向机制走纯 Scheduler 能力而非 `TargetKind=provider` 路由 —— Target 单值无法表达多供应商并集；scheduler 层天然拿到带 Provider 归属的候选列表（详见 `../../adr/0006-channel-targeting-via-scheduler-not-provider-route.md`）。
- 开关粒度：每 key 一个渠道定向总开关 —— 配置保留不丢失，数据结构与 UI 都最简。
- 多认证文件语义：插件自持计数器在池内轮转（round-robin，按 ID 确定性排序）——SDK 契约下 `DelegateBuiltin` 不受候选池约束（宿主在全量分片上重挑）、钉死单个 AuthID 又无轮转，插件侧轮转是同时保住「池隔离」与「多文件分摊」的唯一实现路径；计数器驻内存、重启归零可接受。
- 选择粒度：供应商整选 + 认证文件单选可混选，取并集 —— 整选是动态语义（供应商新增文件自动入池），不做保存时展开。
- 池空行为：报错不降级（503 `auth_not_found`）——保持「定向」承诺，绝不落到池外渠道。
- 响应模型名：非流式还原 + 流式透传 —— 与 key-policy 先例一致，流式 SSE 改写风险高。
- 定向与映射关系：定向优先、跳过本插件的规则映射 —— 定向是「换执行路径」不是「改模型名」；路由决策返回 Handled=false 即自然实现。
- fast 判定范围：精准剥离 Claude 协议的 `speed:"fast"` 与 `fast-mode-2026-02-01` beta 头 —— CPA 中 fast 的唯一真实语义，广义变体属推测性设计。
- `fast_allowed` 默认开启，用 `*bool` 区分缺省与显式关闭 —— 存量状态文件零迁移。
- fast 与定向相互独立可叠加 —— 无用户场景支撑联动规则。
- preview 感知定向：dry-run 返回定向解析结果 —— 预览与生产行为同步，避免误导。
- Scheduler 共存冲突接受失效风险 —— 宿主全局单实例，UI 不检测。

## 取代与共存

- [分面共存] `.spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md`：本特性扩展同一 `request.intercept_before` handler 但新增的是非终止改写分支，blocked 短路行为与其 Requirement（「访问禁用时短路拒绝请求」「管理面可读写 blocked」「Admin UI 提供禁止访问开关」）完全不变；两 spec covers 有交集（main.go / management.go / KeysPanel.tsx），改动时以本 spec 为同步锚点即可，无需 supersedes。
- [分面共存] `.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`：「key 层接力执行」「路由判定链路」等映射行为不变（定向开启时仅整体跳过，不改变映射 DSL 语义）——其「key 层接力执行」SHALL 的无条件表述自本 spec 起收窄为「定向未开启时」；编辑表单重构不改变其 RuleSetEditor 行为契约。
- [分面共存] `.spec-dev/2026-07-27-plugin-version/spec/plugin-version-design.md`：无行为相交（版本注入链路不动）。

## 行为规范（Requirements）

### Requirement: 渠道定向候选池过滤

当某 key 的渠道定向开关开启时，该 key 发起的每个模型请求 SHALL 只能由候选池内的凭据执行：候选 = 所选供应商下宿主提供的全部可用认证记录，加上单独所选的认证记录，取并集。池内多个凭据时插件按确定性顺序（ID 排序）轮转分配。

#### Scenario: 单选认证文件命中

- **GIVEN** 绑定 key K 定向开启，auth_ids 含认证文件 F1（provider=claude），suppliers 为空
- **WHEN** K 发起一个 claude 格式请求
- **THEN** 该请求由 F1 对应凭据执行，或因 F1 不可用而收到 503 auth_not_found 错误；绝不使用其他凭据

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

### Requirement: 定向池空时显式报错不降级

宿主征询插件调度且过滤后候选池为空时（如所选凭据均不可用、或目标供应商不在本次解析结果中），请求 SHALL 以 503 状态与 `auth_not_found` 类错误失败，SHALL NOT 静默改用池外凭据。池内凭据因冷却被宿主前置排除时，请求由宿主以 429 `model_cooldown` 应答——同样不降级到池外。

#### Scenario: 池内候选全部不可用

- **GIVEN** 绑定 key K 定向开启，宿主征询调度时 Candidates 中无任何属于 K 候选池的可用凭据
- **WHEN** K 发起请求
- **THEN** 请求失败（HTTP 503 auth_not_found），绝不使用池外凭据

#### Scenario: 全部冷却走宿主原生应答

- **GIVEN** 绑定 key K 定向开启，池内唯一认证文件处于冷却状态（宿主在调度前已将其从候选中排除）
- **WHEN** K 发起请求
- **THEN** 宿主以 429 `model_cooldown`（含 Retry-After）应答，不降级到池外凭据

### Requirement: 定向开启跳过规则映射

定向开关开启时，该 key 的请求 SHALL 不执行本插件的顶层与 per-key 规则映射——上游收到的模型名等于客户端请求的模型名；未开定时的映射行为不变。

#### Scenario: 定向时模型名不被改写

- **GIVEN** 顶层规则会把模型 M 映射为 N；绑定 key K 定向开启
- **WHEN** K 请求模型 M
- **THEN** 上游执行使用的模型名仍是 M（映射被跳过），响应还原后客户端看到的也是 M

### Requirement: 定向响应的非流式模型名还原

定向开启的 key 收到非流式响应时，响应体中的模型名字段（model / modelVersion / message.model / response.model）SHALL 被还原为客户端请求的模型名；流式响应 SHALL 原样透传不做还原；非定向请求的响应 SHALL 不被此逻辑触碰。

#### Scenario: 非流式还原

- **GIVEN** 绑定 key K 定向开启，K 发起非流式请求，上游返回体含 `"model":"upstream-real-name"`
- **WHEN** 响应经过本插件
- **THEN** 客户端收到的 body 中 model 等于 K 请求时用的模型名

#### Scenario: 流式透传

- **GIVEN** 绑定 key K 定向开启，K 发起流式请求
- **WHEN** SSE 分块经过插件
- **THEN** 各分块内容与不经插件时一致（无逐块模型名改写）

### Requirement: Fast 关闭时剥离 fast 标记

绑定的 Fast 允许为显式 false 时，该 key 的 Claude 协议请求若携带 fast 标记——body 顶层 `speed` 字段值为 fast（大小写不敏感），或请求头 anthropic-beta 列表含 fast-mode-2026-02-01——则插件 SHALL 在请求进入上游前移除这些标记（删除 speed 字段、从 beta 头各值中剔除该 token 且保留其余 beta）；无 fast 标记的请求与非 Claude 协议请求 SHALL 原样通过。Fast 允许缺省（true 或未设置）时不做任何改写。

#### Scenario: 仅 body 带 speed

- **GIVEN** 绑定 key K fast_allowed 显式 false
- **WHEN** K 发起含 `"speed":"fast"` 的 claude 请求
- **THEN** 上游收到的 body 无 speed 字段，其余字段不变

#### Scenario: 仅 beta 头标记

- **GIVEN** 同上绑定
- **WHEN** K 发起 anthropic-beta 含 `fast-mode-2026-02-01,prompt-caching-2024` 的请求
- **THEN** 上游收到的 beta 头只剩 `prompt-caching-2024`

#### Scenario: 默认放行

- **GIVEN** 绑定 key K 未设置 fast_allowed（旧数据）
- **WHEN** K 发起含 speed fast 的请求
- **THEN** 请求原样通过，上游照常执行 fast

### Requirement: KeyBinding 数据结构与校验

state 文件 SHALL 以增量方式承载新字段：`channel_target {enabled, suppliers[], auth_ids[]}` 与 `fast_allowed`；version 不变更；旧版本插件读到新字段 SHALL 忽略之。保存时 SHALL 校验 channel_target 数组元素非空且各自去重；数组允许同时为空（运行期报错，保存不拦）。`findKeyBinding` 匹配语义不变。

#### Scenario: 存量文件零迁移加载

- **GIVEN** 磁盘 state 文件由旧版写入，无 channel_target/fast_allowed 字段
- **WHEN** 新版插件启动加载
- **THEN** 加载成功，所有绑定表现为未配置定向、Fast 允许

#### Scenario: 校验拒绝重复 ID

- **GIVEN** POST /keys payload 中 auth_ids 为 ["a","a"]
- **WHEN** 保存
- **THEN** 返回 400 与去重相关错误信息，state 文件不变

### Requirement: 管理面读写与预览感知定向

PATCH /keys SHALL 支持 channel_target 与 fast_allowed 字段的局部更新；PATCH 请求体中 channel_target 为 null 或缺席 SHALL 不改动既有配置（清空语义由「关闭开关但保留配置」承担，不发明 null 清空）；POST /preview 在请求 key 定向开启时 SHALL 返回定向解析结果（enabled、resolved suppliers 与 auth_ids，即存储配置的原样回显）及 mapping_skipped=true，且 m1/m2/final 显示原始模型名；定向未开启时 preview 输出结构向后兼容。

#### Scenario: PATCH 局部更新 fast_allowed

- **GIVEN** 已存在绑定 K
- **WHEN** PATCH {fast_allowed:false}
- **THEN** 其余字段（rules 等）不变，fast_allowed=false 持久化

#### Scenario: PATCH 更新渠道定向

- **GIVEN** 已存在绑定 K（无渠道定向配置）
- **WHEN** PATCH {channel_target:{enabled:true, suppliers:["gemini"], auth_ids:["f1"]}}
- **THEN** 定向配置持久化；随后 PATCH {channel_target:{enabled:false, suppliers:["gemini"], auth_ids:["f1"]}} 后配置保留、仅开关关闭

#### Scenario: preview 显示定向

- **GIVEN** 绑定 K 定向开启（suppliers=["gemini"], auth_ids=["f1"]）
- **WHEN** POST /preview {key:K, format, model}
- **THEN** 响应含 channel_target.resolved={suppliers:["gemini"],auth_ids:["f1"]}、mapping_skipped=true、final=model 原名

### Requirement: Admin UI 表单重构（Tabs 三页 + 双区块定向编辑器）

编辑/新增绑定弹窗 SHALL 采用 Tabs 分页：「基础」（key Select、别名、启用规则/禁止访问/Fast 允许三 Switch、同 key 冲突提示）、「渠道定向」（总开关 + AI 供应商 CheckboxGroup + 认证文件按供应商折叠分组多选，含组头全选与每项状态标识；开关关闭时区块禁用置灰、配置保留）、「规则集」（现有 RuleSetEditor）。表格 SHALL 新增渠道定向摘要列与 Fast 状态列。全程使用 Semi Design 组件；auth-files 数据加载失败 SHALL 显示可重试的错误提示。

#### Scenario: 双区块混选回显

- **GIVEN** 绑定 K 已存 suppliers=["claude"]、auth_ids=["gemini-main"]
- **WHEN** 打开编辑弹窗切到渠道定向页
- **THEN** claude 供应商复选框勾选、gemini 分组下 gemini-main 勾选，其余未选

#### Scenario: 总开关关闭置灰

- **GIVEN** 编辑弹窗中渠道定向总开关被关闭
- **WHEN** 用户查看两个选项区块
- **THEN** 区块呈现禁用态不可勾选，已勾选项保留，重新打开开关后恢复可选

#### Scenario: auth-files 加载失败

- **GIVEN** CPA auth-files 接口不可达
- **WHEN** 打开渠道定向页
- **THEN** 页面显示错误提示与重试按钮，不显示空白列表也不崩溃

## 方案设计

### 架构与组件

三个正交机制挂接宿主钩子：

| 功能 | 宿主钩子 | 插件侧 |
|---|---|---|
| 渠道定向 | `scheduler.pick` | `Scheduler` 能力 + `handleSchedulerPick` |
| Fast 覆盖 | `request.intercept_before`（已有） | 现有 handler 内追加非终止改写分支 |
| 响应还原 | `response.intercept_after`（新注册） | `ResponseInterceptor` 能力 + 薄还原 |

- `handleSchedulerPick`：过滤（usable 状态 ∧ 池内归属）→ 空池报 503 → 非空按 ID 确定性排序后由插件自持计数器轮转选一个（round-robin；计数器驻内存，绑定池变化时归零）。不用 `DelegateBuiltin`——它在宿主全量凭据分片上重挑、会绕开候选池。Candidates 由宿主做过模型能力与可用性预过滤，插件的 usable 过滤是防御性冗余。
- fast 剥离：blocked 检查之后追加；判定与改写仅在「绑定存在且 fast_allowed 显式 false」时发生（零开销路径）。
- `handleResponseInterceptAfter`：Stream 直接透传；否则提取 key、确认定向开启后调 `rewriteModelFields(body, RequestedModel)`。
- 客户端 key 识别统一走 `apiKeyFromHeaders`（Authorization Bearer / x-api-key），scheduler 从 `Options.Headers`、response 从 `RequestHeaders` 提取；提取失败一律 fail-open。

### 数据流

```go
type ChannelTarget struct {
    Enabled   bool     `json:"enabled"`
    Suppliers []string `json:"suppliers,omitempty"`
    AuthIDs   []string `json:"auth_ids,omitempty"`
}

type KeyBinding struct {
    // …现有五字段…
    ChannelTarget *ChannelTarget `json:"channel_target,omitempty"`
    FastAllowed   *bool          `json:"fast_allowed,omitempty"`
}
```

定向请求路径：客户端 → routeModel 返回 Handled=false（跳过映射）→ 宿主按原始模型名解析 providers → conductor 调 scheduler.pick → 插件过滤候选（usable 状态 ∧ (Provider∈Suppliers ∨ ID∈AuthIDs)，Provider 大小写不敏感比较）→ 空→503 / 非空→插件轮转计数器选 AuthID。usable 排除表照抄 key-policy：disabled/error/expired/revoked/invalid/unavailable/cooldown/cooling_down/quota_exhausted/exhausted/blocked。注意宿主在调度前已按可用性与最高优先级层收窄 Candidates（`availableAuthsForSelector`），故「全部冷却」子情形由宿主以 429 原生应答、不经过插件。

### 关键接口

- 注册能力新增：`Capabilities.Scheduler=true`、`ResponseInterceptor=true`；dispatchMethod 增加 `scheduler.pick`、`response.intercept_after` 分支。
- `PATCH /keys` patch 结构增加 `*ChannelTarget`（json tag `channel_target`）与 `*bool`（`fast_allowed`）。
- `POST /preview` 响应增加 `channel_target`（{enabled,resolved:{suppliers,auth_ids}}）与 `mapping_skipped` 字段（omitempty，向后兼容）。
- 前端新增 `api.listCpaAuthFiles(): Promise<CpaAuthFile[]>` 直调 `GET /v0/management/auth-files`（条目字段 id/provider/status/disabled/label）。

### 错误处理

| 场景 | 行为 |
|---|---|
| 宿主征询调度时池内无可用候选 | 503 `auth_not_found`（ErrorEnvelope），绝不放行池外 |
| 池内凭据全部冷却（宿主前置排除） | 宿主原生 429 `model_cooldown` + Retry-After，同样不降级 |
| scheduler/response 回调拿不到 key | Handled=false / 原样返回（fail-open） |
| 目标供应商不在本次解析结果 | 该部分候选缺席，等效池收窄；全空同上报错 |
| auth-files 接口失败 | UI 错误提示+重试；已存配置仍可查看 |
| 旧版插件读新 state 字段 | JSON 反序列化自然忽略 |

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| 单选认证文件命中 | unit | 任务内 TDD | 测试通过 |
| 供应商整选动态入池 | unit | 任务内 TDD | 测试通过 |
| 池内多凭据轮转分摊 | unit | 任务内 TDD | 测试通过 |
| 无法识别上下文时 fail-open | unit | 任务内 TDD | 测试通过 |
| 池内候选全部不可用 / 全部冷却走宿主原生应答 | unit | 任务内 TDD | 测试通过 |
| 定向时模型名不被改写 | unit | 任务内 TDD | 测试通过 |
| 非流式还原 / 流式透传 | unit | 任务内 TDD | 测试通过 |
| 仅 body 带 speed / 仅 beta 头 / 默认放行 | unit | 任务内 TDD | 测试通过 |
| 存量文件零迁移加载 / 校验拒绝重复 ID | unit | 任务内 TDD | 测试通过 |
| PATCH 局部更新 / preview 显示定向 | unit | 任务内 TDD | 测试通过 |
| 双区块混选回显 / 总开关置灰 / 加载失败 | component (vitest) | 任务内 TDD | 测试通过 |
| 定向请求实际落在目标认证文件 | e2e | 验收任务 (D) | smoke-local 通过（CPA 日志断言凭据） |
| 池内凭据全冷却时收到 429 且不落池外 | e2e | 验收任务 (D) | smoke-local 通过 |
| fast 关闭后上游收到普通请求 | e2e | 验收任务 (D) | smoke-local 通过 |
| 定向 + fast 组合叠加 | e2e | 验收任务 (D) | smoke-local 通过 |
| 编辑表单全流程人工审查 | visual | 验收任务 (D) | 截图/录屏归档 acceptance/ |

## 风险与边缘情况

1. **Scheduler 单实例冲突**：宿主全局只认第一个声明 Scheduler 的插件；与 cpa-plugin-key-policy 同时启用时本插件的定向静默失效。spec 与 README 注明，不做运行时检测（已裁决接受）。另有一个全局副作用：宿主检测到插件 scheduler 后所有请求走 `pickNextLegacy` 慢路径（放弃内建 fast-path，语义不变）。
2. **Headers 通路未实测**：scheduler.pick 经 `Options.Headers`、response.intercept_after 经 `RequestHeaders` 拿客户端 Authorization 头均基于源码推断（两处同源：`modelExecutionHeaders(ctx, …)`），实施第一个任务对两个钩子一并联调验证；不通则回设计评审升级方案。
3. **供应商键大小写**：宿主侧 provider 键统一小写；存储保留宿主原值、匹配用 EqualFold。
4. **count_tokens 路径**会过 intercept_before：fast 剥离对 count 请求同样生效（无害、语义一致）。
5. **定向开启但模型名未被任何供应商注册**：宿主在 scheduler 之前即报 unknown model——这是正确行为（客户端请求了不存在的模型），spec 不额外兜底。

## 开放问题

- smoke case 断言「请求落在目标认证文件」需确认 CPA 日志中凭据标识的字段名，实施时从 request-log 输出确定。
- Semi Collapse 分组头内嵌 Checkbox（组头全选）的交互细节（点击区域、半选态样式）实施时按 Semi 组件能力微调。
