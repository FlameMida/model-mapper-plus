---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: channel-target-and-fast-control
  status: active
  covers:
    - "main.go"
    - "main_test.go"
    - "state.go"
    - "state_test.go"
    - "management.go"
    - "management_test.go"
    - "scheduler_test.go"
    - "fast_strip_test.go"
    - "channel_credentials*.go"
    - "testdata/channel_credentials.json"
    - "web/src/channelCredentials.test.ts"
    - "web/src/components/ChannelTargetEditor.css"
    - "web/src/components/ChannelTargetEditor.test.tsx"
    - "web/src/panels/KeysPanel.channel-target.test.tsx"
    - "web/src/panels/KeysPanel.tsx"
    - "web/src/components/ChannelTargetEditor.tsx"
    - "web/src/api.ts"
  sync_commit: 00f481055783d8bce9fcc632fbfdcab5ccd0cf39
  supersedes: []
  superseded_by: null
---

# 渠道定向与 Fast 控制 设计

> **Superseded-pending (2026-09-15)** — 本 spec 的「Requirement: 定向池空时显式报错不降级」「Requirement: 渠道定向候选池过滤」「Requirement: AI Providers 凭据目录与精确勾选」（池空错误形态相关表述）将被 .spec-dev/2026-09-15-02-channel-target-empty-pool-error-form/spec/channel-target-empty-pool-error-form-design.md 部分取代（待其交付）；新工作以新 spec 为准，本 spec 仍描述当前已实现行为。

## 背景与目标

key 绑定目前只能追加模型映射规则和做访问禁用。需要两个新控制维度：(1) 把某个客户端 key 的请求**定向**到指定的 AI 供应商 / 认证文件集合（候选池内正常调度，池空报错不降级）；(2) 对每个 key 控制 **fast 模式**准入——关闭时把 fast 请求覆盖成普通请求。

**成功标准**：绑定渠道定向后，该 key 的请求只可能由“宿主交给 Scheduler 的当前可选候选”与“所选供应商 ∪ 所选凭据”的交集中的凭据执行，交集为空时 HTTP 503 且绝不降级到池外；关闭 Fast 允许后，该 key 的任何 fast 标记（`speed:"fast"` body 字段或 `fast-mode-2026-02-01` beta 头）在到达上游前被剥离；管理 UI 可视化配置两者。

## 非目标

- 不实现按选择顺序的优先级降级调度（池内交给宿主内建策略）。
- 不做流式响应的模型名还原（SSE 逐块改写风险高，收益低）。
- 不在 UI 上检测/展示 Scheduler 调度权冲突状态。
- 不代理 CPA 的 auth-files 或 AI Providers 配置 GET 接口（前端直调宿主 API）；插件仅提供无状态的凭据 ID 解析 POST 接口。
- 不支持 OpenAI Responses 等非 Claude 协议的 fast 变体（CPA 宿主中 fast 是 Claude 协议专属语义）。
- 不修改 `/Users/flame/CLIProxyAPI` 宿主、SDK 或 RPC 适配层；宿主仓库仅作为只读契约依据。
- 不伪造宿主的 per-model cooldown 判定或 `Retry-After`；插件看不到 Scheduler 前被排除候选的完整模型状态。

## 术语表

- **渠道定向（channel target）**：某 key 绑定上的一组「供应商整选 + 凭据单选」及总开关；生效时请求只能由该集合内的凭据执行。_Avoid_：渠道绑定、通道指定。
- **候选池（candidate pool）**：定向开启时，宿主完成模型能力、可用性、cooldown 与全局最高优先级预过滤后交给 Scheduler 的 Candidates，与「所选供应商 ∪ 所选凭据」的交集。
- **Fast 允许（fast allowed）**：绑定级开关；false 时该 key 的 fast 标记被剥离为普通请求。_Avoid_：加速模式、turbo。
- **凭据（credential）**：CPA Scheduler 的一条候选记录，以 `ID` 唯一标识；来源包括认证文件和 AI Providers 配置。定向按凭据判断，不以模型名区分。
- **认证文件（auth file）**：CPA 宿主中的一条凭据记录（`GET /v0/management/auth-files` 条目），有唯一 `id` 与归属 `provider`。
- **fail-open**：插件无法识别请求上下文（拿不到 key、无绑定、功能关）时不干预、交还宿主正常流程的行为约定。

## 影响面

- **Go 后端**：`state.go`（KeyBinding 结构与校验）、`main.go`（能力注册、`handleSchedulerPick`、拦截器扩展、响应还原）、`management.go`（PATCH 扩展、preview 定向感知）。
- **依赖**：新增直接引用 `tidwall/gjson`、`tidwall/sjson`（已在 go.mod 间接依赖树中，升为 direct，无新外部依赖）；SDK 能力声明新增 `Scheduler` 与 `ResponseInterceptor`。
- **前端**：`KeysPanel.tsx` 表格列与弹窗重构、新组件 `ChannelTargetEditor.tsx`、`api.ts` 类型与新接口函数。
- **外部系统**：编译期固定 CPA `v7.2.119` SDK 契约（`scheduler.pick`、`response.intercept_after`）；兼容性验收额外对比本地只读宿主 `v7.2.139`。`/Users/flame/CLIProxyAPI` 不得产生任何修改，临时构建输出只能落在 `/tmp`；Scheduler 能力全宿主单实例（见风险）。

## 已确认的关键决策

- 定向机制走纯 Scheduler 能力而非 `TargetKind=provider` 路由 —— Target 单值无法表达多供应商并集；scheduler 层天然拿到带 Provider 归属的候选列表（详见 `../../adr/0006-channel-targeting-via-scheduler-not-provider-route.md`）。
- 开关粒度：每 key 一个渠道定向总开关 —— 配置保留不丢失，数据结构与 UI 都最简。
- 多认证文件语义：插件自持计数器在池内轮转（round-robin，按 ID 确定性排序）——SDK 契约下 `DelegateBuiltin` 不受候选池约束（宿主在全量分片上重挑）、钉死单个 AuthID 又无轮转，插件侧轮转是同时保住「池隔离」与「多文件分摊」的唯一实现路径；计数器驻内存、重启归零可接受。
- 选择粒度：供应商整选 + 凭据单选可混选，取并集 —— 整选是动态语义（供应商新增文件自动入池），不做保存时展开。
- 池空行为：报错不降级——插件 ABI 保留 `HTTPStatus=503` / `Code=auth_not_found`，并把 message 编码为同时含 `error.type` 与 `error.code` 的完整 JSON。CPA v7.2.119 的 RPC 适配层会丢弃 typed code，但会保留 HTTP 状态与 message；OpenAI/Codex 错误路径原样透传 JSON，Claude `/v1/messages` 重包装后保留 `error.type=auth_not_found`。
- 响应模型名：非流式还原 + 流式透传 —— 与 key-policy 先例一致，流式 SSE 改写风险高。
- 定向与映射关系：定向优先、跳过本插件的规则映射 —— 定向是「换执行路径」不是「改模型名」；路由决策返回 Handled=false 即自然实现。
- fast 判定范围：精准剥离 Claude 协议的 `speed:"fast"` 与 `fast-mode-2026-02-01` beta 头 —— CPA 中 fast 的唯一真实语义，广义变体属推测性设计。
- `fast_allowed` 默认开启，用 `*bool` 区分缺省与显式关闭 —— 存量状态文件零迁移。
- fast 与定向相互独立可叠加 —— 无用户场景支撑联动规则。
- preview 感知定向：dry-run 返回定向解析结果 —— 预览与生产行为同步，避免误导。
- Scheduler 共存冲突接受失效风险 —— 宿主全局单实例，UI 不检测。
- 不绕过宿主前置候选收窄 —— 目标凭据因 cooldown 或全局优先级较低而未进入 Candidates 时，插件返回 503，不尝试选择不在 Candidates 中的 AuthID。

## 取代与共存

- [分面共存] `.spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md`：本特性扩展同一 `request.intercept_before` handler 但新增的是非终止改写分支，blocked 短路行为与其 Requirement（「访问禁用时短路拒绝请求」「管理面可读写 blocked」「Admin UI 提供禁止访问开关」）完全不变；两 spec covers 有交集（main.go / management.go / KeysPanel.tsx），改动时以本 spec 为同步锚点即可，无需 supersedes。
- [分面共存] `.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`：「key 层接力执行」「路由判定链路」等映射行为不变（定向开启时仅整体跳过，不改变映射 DSL 语义）——其「key 层接力执行」SHALL 的无条件表述自本 spec 起收窄为「定向未开启时」；编辑表单重构不改变其 RuleSetEditor 行为契约。
- [分面共存] `.spec-dev/2026-07-27-plugin-version/spec/plugin-version-design.md`：无行为相交（版本注入链路不动）。

## 行为规范（Requirements）

### Requirement: 渠道定向候选池过滤

当某 key 的渠道定向开关开启时，该 key 发起的每个模型请求 SHALL 只能由候选池内的凭据执行：候选 = 宿主交给 Scheduler 的 Candidates ∩（所选供应商 ∪ 单独所选凭据）。宿主在 Scheduler 前已按模型能力、可用性、cooldown 与全局最高优先级收窄 Candidates；插件 SHALL NOT 选择不在 Candidates 中的 AuthID。池内多个凭据时插件按确定性顺序（ID 排序）轮转分配。

对于宿主已判定当前请求可用并放入 Candidates 的目标凭据，插件 SHALL NOT 仅因其 `Status=error` 再次排除；该状态可能来自已结束的冷却或其他模型的历史失败，不代表当前请求不可用。

#### Scenario: 单选认证文件命中

- **GIVEN** 绑定 key K 定向开启，auth_ids 含认证文件 F1（provider=claude），suppliers 为空
- **WHEN** K 发起一个 claude 格式请求
- **THEN** F1 位于宿主 Candidates 时该请求由 F1 执行；F1 未进入 Candidates 但宿主仍征询 Scheduler 时请求收到 HTTP 503 `auth_not_found` 类错误；两种情况均绝不使用其他凭据

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
- **THEN** F1 正常参与定向池内调度，不因历史 `error` 状态返回 503，也不使用池外凭据

#### Scenario: 其他模型失败不影响当前可用模型

- **GIVEN** F1 因模型 A 失败而处于 `Status=error`，当前请求模型 B 可用；绑定 key K 按供应商或认证文件定向到 F1
- **WHEN** 宿主针对模型 B 将 F1 放入 Candidates
- **THEN** F1 正常参与 K 的定向池内调度，不受模型 A 的失败状态影响

### Requirement: 定向池空时显式报错不降级

宿主征询插件调度且过滤后候选池为空时（如目标凭据未进入 Candidates、或目标供应商不在本次解析结果中），请求 SHALL 以 HTTP 503 的 `auth_not_found` 类错误失败，SHALL NOT 静默改用池外凭据。插件 ABI SHALL 保留 `Code=auth_not_found`，且 message SHALL 是同时含 `error.type=auth_not_found` 与 `error.code=auth_not_found` 的合法 JSON；对 CPA v7.2.119 客户端，OpenAI/Codex 协议断言 `error.code`，Claude `/v1/messages` 断言 `error.type`。

#### Scenario: 池内候选全部不可用

- **GIVEN** 绑定 key K 定向开启，宿主征询调度时 Candidates 中无任何属于 K 候选池的可用凭据
- **WHEN** K 发起请求
- **THEN** 请求失败（HTTP 503）；OpenAI/Codex 错误体含 `error.code=auth_not_found`，Claude 错误体含 `error.type=auth_not_found`，且绝不使用池外凭据

#### Scenario: 目标冷却但池外仍可用

- **GIVEN** 绑定 key K 定向开启，目标池唯一认证文件 F1 处于 cooldown，池外认证文件 F2 为 active
- **WHEN** K 发起请求
- **THEN** 宿主在 Scheduler 前排除 F1，插件过滤掉 F2 并返回协议对应的 HTTP 503 `auth_not_found` 类错误；绝不使用 F2

#### Scenario: 目标被全局优先级收窄排除

- **GIVEN** 绑定 key K 定向开启并只选低优先级 active 凭据 F1，池外存在更高优先级 active 凭据 F2
- **WHEN** 宿主只把全局最高优先级层 F2 交给 Scheduler
- **THEN** 插件过滤后返回协议对应的 HTTP 503 `auth_not_found` 类错误；不选择不在 Candidates 中的 F1，也不使用 F2

宿主全局无任何可选凭据时 MAY 在调用 Scheduler 前返回原生 429 `model_cooldown` 与 `Retry-After`；这是 CPA v7.2.119 的宿主边界行为，不是本插件的 SHALL 承诺。

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

### Requirement: AI Providers 凭据目录与精确勾选

认证文件和 AI Providers 凭据 SHALL 统一按 `suppliers ∪ auth_ids` 过滤，AI Providers 无特殊豁免；同模型、同供应商内的未选凭据亦不得进入候选池（供应商整选覆盖的条目除外）。保持现有保存结构、池内轮转、池空 503 及宿主预过滤边界。

前端 SHALL 读取 `auth-files` 及 `gemini-api-key`、`interactions-api-key`、`claude-api-key`、`codex-api-key`、`xai-api-key`、`openai-compatibility`、`vertex-api-key`，将配置数组按宿主原始顺序提交插件 `POST /channel-credentials`。插件用 Go 标准库复现本地 CPA `d1a024e9400bc65bd78ccd908945cf2eacc2835e` 的 ID 算法和内部 provider 命名，不修改宿主、不引入依赖、不依赖浏览器安全上下文。

ID SHALL 使用正确的类型与字段顺序、Go TrimSpace、排序后的 headers 和重复编号；先生成 ID 再排序或搜索。重复计数器在每次完整目录解析时新建；禁用的 OpenAI Compatibility 供应商不生成运行时凭据且不消耗编号，故不提供勾选项；无 API Key 的兼容供应商按宿主规则生成条目。其他宿主版本的 ID 或 provider 规则变化需重新验证，不能假定兼容。

解析接口 SHALL 仅返回 ID、内部 provider、展示名称、脱敏 Key、展示地址、来源和「已配置」状态；不发起上游请求、不写状态文件、不保留或回显原文 API Key、代理地址或自定义 headers。绑定继续仅保存供应商与凭据 ID。解析错误 SHALL 不回显原始输入。

旧宿主缺少 Interactions / xAI 接口时，其 404 SHALL 视为不支持该类型；其他读取失败或非法目录格式 SHALL 显示加载失败并保留所有勾选，不能静默显示不完整目录。401/403 沿用重新登录流程。配置变化导致 ID 变化时，旧 ID 保留并显示「当前未返回」，不猜测转绑。

#### Scenario: 同模型下按配置凭据单选

- **GIVEN** 宿主同模型 Candidates 含同供应商的两个 AI Providers 凭据及认证文件，绑定仅选择其中一个配置凭据 ID
- **WHEN** Scheduler 接收该请求
- **THEN** 仅选中 ID 可被使用；其他配置凭据和认证文件被过滤

#### Scenario: 跨来源混选与重开回显

- **GIVEN** 页面展示认证文件和 AI Providers 配置凭据
- **WHEN** 用户混选、保存绑定并重新打开
- **THEN** 两类 ID 均回显；保存体只携带绑定字段，不携带上游配置；Scheduler 仅在所选凭据间轮转

#### Scenario: 原始接口与真实宿主 ID 一致

- **GIVEN** 七类管理 GET 的响应样本与真实 ConfigSynthesizer 输出
- **WHEN** 插件解析目录
- **THEN** ID 与 provider 逐条等于宿主结果，包括重复条目后缀及无 Key 的兼容供应商；与模型名无关

### Requirement: Fast 关闭时剥离 fast 标记

绑定的 Fast 允许为显式 false 时，该 key 的 Claude 协议请求若携带 fast 标记——body 顶层 `speed` 字段值为 fast（大小写不敏感），或请求头 anthropic-beta 列表含 fast-mode-2026-02-01——则插件 SHALL 在请求进入上游前移除这些标记（删除 speed 字段、从 beta 头各值中剔除该 token 且保留其余 beta）；无 fast 标记的请求与非 Claude 协议请求 SHALL 原样通过。Fast 允许缺省（true 或未设置）时不做任何改写。同一次 `request.intercept_before` 的 blocked 与 Fast 判定 SHALL 使用同一份不可变状态快照，不得在热更新窗口内混用两个版本。

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

#### Scenario: 热更新不混用绑定快照

- **GIVEN** 同一 key 的 blocked/Fast 配置在请求检查期间发生热更新
- **WHEN** `request.intercept_before` 处理该请求
- **THEN** handler 只读取一次 rule source，blocked 与 Fast 行为完整对应该快照，不产生两版配置的混合结果

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

### Requirement: Admin UI 表单重构（Tabs 三页 + B 版定向编辑器）

编辑/新增绑定弹窗 SHALL 采用 Tabs 分页：「基础」（key Select、别名、启用规则/禁止访问/Fast 允许三 Switch、同 key 冲突提示）、「渠道定向」（总开关 + 左侧供应商导航 + 右侧认证文件列表；右侧包含供应商整选、名称/ID 搜索、当前结果全选及每项状态标识；开关关闭时选择控件禁用置灰、配置保留，仍可切换供应商查看）、「规则集」（现有 RuleSetEditor）。窄屏时供应商导航横向排列在列表上方；编辑弹窗保留桌面 860px 宽度并限制最大宽度为视口减 32px，防止凭据标签与开关被裁切。表格 SHALL 新增渠道定向摘要列与 Fast 状态列，并必须容忍 wire JSON 因 `omitempty` 缺少 `suppliers` / `auth_ids` 或两者的合法形状。provider 从存量绑定与 auth-files 合并时 SHALL 先 trim、按大小写不敏感 canonical key 去重并保留第一个展示值；auth ID SHALL 先 trim、按大小写敏感值去重。列表、分组、回显、缺失判定、组选与保存 SHALL 共用这两套规范化语义。全程使用 Semi Design 组件；加载提示图标与「正在加载凭据…」SHALL 单行显示，加载失败提示、详情与「重新加载」按钮文字 SHALL 统一为 14px。目录合并 auth-files 与 AI Providers 配置凭据，加载、搜索、计数、摘要统一称为「凭据」；每项标注「认证文件」或「AI Providers」。AI Providers 条目可显示名称、脱敏 Key 和去除 userinfo/query/fragment 的地址；状态显示「已配置」，不伪称实时可用。搜索支持名称、地址与 ID。自定义 OpenAI Compatibility 展示配置名称，供应商整选保存 Scheduler 内部 provider key。

#### Scenario: 供应商导航切换与混选回显

- **GIVEN** 绑定 K 已存 suppliers=["claude"]、auth_ids=["gemini-main"]
- **WHEN** 打开编辑弹窗切到渠道定向页
- **THEN** 查看 claude 时供应商整选复选框勾选；切换至 gemini 时 gemini-main 勾选，其余未选；切换不修改任何已选值

#### Scenario: 搜索后的批选与整选独立

- **GIVEN** 已选择其他供应商文件、当前搜索范围之外的文件，以及 CPA 当前未返回的文件
- **WHEN** 在当前供应商中按名称或 ID 搜索并全选/取消全选当前结果
- **THEN** 只增删当前可见文件的显式 ID；筛选外、跨供应商与未返回 ID 保留，suppliers 不变；取消供应商整选不移除显式文件选择

#### Scenario: 搜索无结果与目录刷新

- **GIVEN** 当前供应商搜索无匹配项，或目录刷新后当前供应商已不再存在
- **WHEN** 用户查看列表
- **THEN** 无匹配项时显示对应提示且禁用批选；供应商不存在时回退到首个有效供应商，不修改绑定配置；主动切换供应商清空搜索

#### Scenario: 总开关关闭置灰

- **GIVEN** 编辑弹窗中渠道定向总开关被关闭
- **WHEN** 用户查看供应商整选与文件列表
- **THEN** 选择控件呈现禁用态不可勾选，搜索禁用，已勾选项保留，重新打开开关后恢复可选

#### Scenario: auth-files 加载失败

- **GIVEN** CPA auth-files 接口不可达
- **WHEN** 打开渠道定向页
- **THEN** 页面显示「凭据加载失败，已选配置已保留。」、错误详情与「重新加载」按钮，文字均为 14px；已选值保留，加载中或失败时不将未出现 ID 标为「当前未返回」；目录成功返回后才作该缺失提示

#### Scenario: 合法缺失数组的表格回显

- **GIVEN** 管理 API 返回 `channel_target={enabled:true}`，或仅包含 `suppliers` / `auth_ids` 其中一个数组
- **WHEN** Key 绑定表格首次渲染
- **THEN** 页面不崩溃，缺失数组按 0 项显示摘要

#### Scenario: provider 与 auth ID 规范化一致

- **GIVEN** 存量绑定为 `suppliers=[" Gemini "]`、`auth_ids=[" f1 "]`，宿主 auth-files 返回 `provider="gemini"`、`id="f1"`
- **WHEN** 用户打开渠道定向编辑器并触发选择/保存
- **THEN** provider 只显示一个分组且已勾选，f1 已勾选且不误报缺失，`onChange` 输出 trim 后且无 canonical 重复值

## 方案设计

### 架构与组件

三个正交机制挂接宿主钩子：

| 功能 | 宿主钩子 | 插件侧 |
|---|---|---|
| 渠道定向 | `scheduler.pick` | `Scheduler` 能力 + `handleSchedulerPick` |
| Fast 覆盖 | `request.intercept_before`（已有） | 现有 handler 内追加非终止改写分支 |
| 响应还原 | `response.intercept_after`（新注册） | `ResponseInterceptor` 能力 + 薄还原 |

- `handleSchedulerPick`：过滤（usable 状态 ∧ 池内归属）→ 空池报 503 → 非空按 ID 确定性排序后由插件自持计数器轮转选一个（round-robin；计数器驻内存，绑定池变化时归零）。不用 `DelegateBuiltin`——它在宿主全量凭据分片上重挑、会绕开候选池。Candidates 由宿主做过模型能力、可用性、cooldown 与全局最高优先级预过滤，插件仅防御性排除显式不可用状态；`error` 可能只是历史失败，必须保留宿主判定可用的此类候选。空池时插件仍设置 typed `Code=auth_not_found`，同时把 message 编码为合法 JSON `{"error":{"type":"auth_not_found","code":"auth_not_found","message":"…"}}`，以兼容宿主 RPC 丢失 typed code 后的三协议错误转换路径。
- fast 剥离：blocked 检查之后追加；handler 开始时只加载一次不可变 rule-source 快照，blocked 与 Fast 都基于该快照判定。改写仅在「绑定存在且 fast_allowed 显式 false」时发生。
- `handleResponseInterceptAfter`：Stream 直接透传；否则提取 key、确认定向开启后调 `rewriteModelFields(body, RequestedModel)`。
- 客户端 key 识别统一走 `apiKeyFromHeaders`（Authorization Bearer / x-api-key），scheduler 从 `Options.Headers`、response 从 `RequestHeaders` 提取；提取失败一律 fail-open。
- Admin UI 入口先把 wire 形状归一化为缺失数组等于 `[]`；编辑器内部 provider 以 `strings.TrimSpace` 等价语义生成大小写不敏感 canonical key、保留首次展示值，auth ID 则 trim 后按大小写敏感值去重。所有列表、分组、选中、缺失、组操作和保存路径复用同一套 helper。

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

定向请求路径：客户端 → routeModel 返回 Handled=false（跳过映射）→ 宿主按原始模型名解析 providers → 宿主按模型能力、可用性、cooldown 与全局最高优先级层预过滤 → conductor 调 scheduler.pick → 插件过滤候选（usable 状态 ∧ (Provider∈Suppliers ∨ ID∈AuthIDs)，Provider 大小写不敏感比较）→ 空→503 / 非空→插件轮转计数器选 AuthID。usable 排除表为 disabled/expired/revoked/invalid/unavailable/cooldown/cooling_down/quota_exhausted/exhausted/blocked；不排除宿主已判定可用的历史 `error` 状态。

这个顺序意味着插件只能在宿主已提供的 Candidates 内收窄，无法召回因 cooldown 或较低全局优先级而缺席的目标凭据：若池外仍有 active 候选，插件收到的交集为空并返回 503；只有宿主全局无任何候选且未调用 Scheduler 时，宿主才可能直接返回原生 429 `model_cooldown` 与 `Retry-After`。插件不模拟该分支。

### 关键接口

- 注册能力新增：`Capabilities.Scheduler=true`、`ResponseInterceptor=true`；dispatchMethod 增加 `scheduler.pick`、`response.intercept_after` 分支。
- Scheduler 空池错误：`HTTPStatus=503`、typed `Code=auth_not_found`，`Message` 为同时包含 `error.type`、`error.code` 与人类可读 `error.message` 的合法 JSON；OpenAI/Codex 路径使用 JSON 内的 code，Claude 路径使用 JSON 内的 type。
- `PATCH /keys` patch 结构增加 `*ChannelTarget`（json tag `channel_target`）与 `*bool`（`fast_allowed`）。
- `POST /preview` 响应增加 `channel_target`（{enabled,resolved:{suppliers,auth_ids}}）与 `mapping_skipped` 字段（omitempty，向后兼容）。
- 前端 `listCpaCredentials()` 合并 `listCpaAuthFiles()` 和七类配置接口，调用插件 `POST /channel-credentials` 解析后返回统一目录。原有 `CpaAuthFile` 类型扩展可选 `source`、`provider_label`、`base_url` 字段以兼容旧文件目录。

### 错误处理

| 场景 | 行为 |
|---|---|
| 宿主征询调度时池内无可用候选 | HTTP 503；OpenAI/Codex 响应含 `error.code=auth_not_found`，Claude 响应含 `error.type=auth_not_found`；绝不放行池外 |
| 目标凭据 cooldown、池外仍有 active 候选 | 目标在 Scheduler 前被排除，插件过滤池外候选后返回上述 503 |
| 目标凭据优先级低于池外 active 候选 | 目标被全局最高优先级层排除，插件过滤池外候选后返回上述 503 |
| 宿主全局无任何可选凭据 | 宿主 MAY 在 Scheduler 前原生返回 429 `model_cooldown` + `Retry-After`；插件不承诺、不模拟 |
| scheduler/response 回调拿不到 key | Handled=false / 原样返回（fail-open） |
| 目标供应商不在本次解析结果 | 该部分候选缺席，等效池收窄；全空同上报错 |
| auth-files 接口失败 | UI 错误提示+重试；已存配置仍可查看 |
| 旧版插件读新 state 字段 | JSON 反序列化自然忽略 |

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| 七类宿主目录 ID/provider 一致、配置输入校验、无敏感值泄漏、混选轮转 | unit/integration | Go fixture + Scheduler ABI | 与真实宿主输出逐条比较 |
| 配置目录加载、搜索、混选保存与重开回显 | component | Vitest + 浏览器受控页面 | 测试与浏览器记录 |
| 单选认证文件命中 | unit | 任务内 TDD | 测试通过 |
| 供应商整选动态入池 | unit | 任务内 TDD | 测试通过 |
| 池内多凭据轮转分摊 | unit | 任务内 TDD | 测试通过 |
| 无法识别上下文时 fail-open | unit | 任务内 TDD | 测试通过 |
| 池内候选全部不可用时返回协议兼容 JSON 错误 | unit | 任务内 TDD | typed code、JSON type/code 与 HTTP 503 断言通过 |
| 目标 cooldown、池外 active 时不越池 | unit | 任务内 TDD | 503 且未选择池外 AuthID |
| 目标低优先级、池外高优先级时不越池 | unit | 任务内 TDD | 503 且未选择池外 AuthID |
| 定向时模型名不被改写 | unit | 任务内 TDD | 测试通过 |
| 非流式还原 / 流式透传 | unit | 任务内 TDD | 测试通过 |
| 仅 body 带 speed / 仅 beta 头 / 默认放行 | unit | 任务内 TDD | 测试通过 |
| blocked 与 Fast 热更新使用单一快照 | unit | 任务内 TDD | loader 只调用一次且行为来自同一版本 |
| 存量文件零迁移加载 / 校验拒绝重复 ID | unit | 任务内 TDD | 测试通过 |
| PATCH 局部更新 / preview 显示定向 | unit | 任务内 TDD | 测试通过 |
| B 版导航混选 / 搜索批选 / 总开关置灰 / 加载失败 | component (vitest) | 任务内 TDD | 测试通过 |
| wire 缺失数组与 provider/auth ID 规范化 | component (vitest) | 任务内 TDD | 不崩溃、回显/缺失/保存语义一致 |
| 定向请求实际落在目标认证文件 | e2e | 验收任务 (D) | smoke-local 通过（CPA 日志断言凭据） |
| 目标 cooldown、池外 active 时收到 503 且不落池外 | e2e | 验收任务 (D) | 有可安全恢复的测试凭据时执行，否则以原因和替代证据标记 DEFERRED |
| 目标低优先级、池外高优先级时收到 503 且不落池外 | integration/e2e | 验收任务 (D) | 可控 fixture 或 smoke-local 证明宿主预过滤边界 |
| v7.2.119 与本地 v7.2.139 三协议错误兼容对比 | integration/e2e | 验收任务 (D)；宿主只读，构建输出在 `/tmp` | 两版本均断言 HTTP 503；OpenAI/Codex 断言 `error.code`，Claude 断言 `error.type`；差异单独记录 |
| fast 关闭后上游收到普通请求 | e2e | 验收任务 (D) | smoke-local 通过 |
| 定向 + fast 组合叠加 | e2e | 验收任务 (D) | smoke-local 通过 |
| 编辑表单全流程人工审查 | visual | 验收任务 (D) | 截图/录屏归档 acceptance/ |

## 风险与边缘情况

1. **Scheduler 单实例冲突**：宿主全局只认第一个声明 Scheduler 的插件；与 cpa-plugin-key-policy 同时启用时本插件的定向静默失效。spec 与 README 注明，不做运行时检测（已裁决接受）。另有一个全局副作用：宿主检测到插件 scheduler 后所有请求走 `pickNextLegacy` 慢路径（放弃内建 fast-path，语义不变）。
2. **宿主前置过滤不可逆**：Scheduler 看不到因 cooldown 或全局较低优先级而缺席的目标凭据，也拿不到足以安全重建 per-model cooldown/`Retry-After` 的状态；混合池场景只能返回 503。验收必须覆盖 cooldown 与优先级两类缺席，且严禁越池选择。
3. **RPC typed code 丢失**：CPA v7.2.119 RPC 适配只保留 HTTP status 与 message。插件以 JSON message 携带 type/code 兼容三协议；该方案依赖宿主错误转换行为，须在固定 v7.2.119 与本地 v7.2.139 上对比验证。若新版行为不同，只记录兼容差异并回到设计评审，不得修改宿主仓库。
4. **协议错误外形不同**：OpenAI/Codex 可稳定断言 `error.code`，Claude 重包装只保留 `error.type`，不能要求三个入口返回完全相同 JSON。
5. **供应商键规范化**：宿主侧 provider 常为小写，但存量配置可能含空白或大小写变体；后端匹配使用 trim + EqualFold，前端 canonical key 大小写不敏感并保留首次展示值。auth ID 只 trim，仍大小写敏感。
6. **count_tokens 路径**会过 intercept_before：fast 剥离对 count 请求同样生效（无害、语义一致）。
7. **定向开启但模型名未被任何供应商注册**：宿主在 scheduler 之前即报 unknown model——这是正确行为（客户端请求了不存在的模型），spec 不额外兜底。

## 开放问题

无。宿主修改边界、空池错误兼容策略、cooldown/优先级语义、前端规范化与 Fast 状态快照均已裁决；无法安全操纵真实 cooldown 凭据时允许将对应 live case 标记为 DEFERRED，但必须保留可复跑步骤与替代测试证据。
