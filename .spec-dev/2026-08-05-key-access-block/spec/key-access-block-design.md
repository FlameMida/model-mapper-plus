---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: key-access-block
  status: active
  covers:
    - "go.mod"
    - "go.sum"
    - "main.go"
    - "main_test.go"
    - "intercept_block_test.go"
    - "host_lifecycle_test.go"
    - "state.go"
    - "state_test.go"
    - "management.go"
    - "management_test.go"
    - "management_api_test.go"
    - "keybinding_test.go"
    - "web/src/panels/KeysPanel.tsx"
    - "web/src/panels/KeysPanel.test.tsx"
    - "web/src/panels/KeysPanel.blocked.test.tsx"
    - "web/src/panels/KeysPanel.delete.test.tsx"
    - "web/src/api.ts"
    - "web/src/api.test.ts"
    - "web/dist/index.html"
  sync_commit: e0d7cfee9cf71d7cf23732e85c43bfa902d04c74
---

# Key 访问禁用（blocked）设计

## 背景与目标

指定 Key 绑定面板已有「启用」开关，但仅控制是否应用该 key 的**追加规则集**，无法禁止该 key 继续调用。运维需要在本插件内对已登记 binding 的 key 做访问门禁，并对客户端返回英文「额度已用尽」风格错误（对齐 CPA 403 → `insufficient_quota`）。

**成功标准 / Success criteria**：管理员可在新建/编辑窗与列表用 Switch 设置 `blocked`；在 CLIProxyAPI `v7.2.119` 的 Home 关闭、且由标准 `BaseAPIHandler` 执行的 OpenAI/Claude/Responses **HTTP/SSE（非 WebSocket upgrade）**请求中，blocked key 可先经过纯 `model.route` 回调，随后必须由 `request.intercept_before` 返回固定 OpenAI 形 HTTP 403 JSON（message 为英文额度用尽），且不进入插件 executor、认证选择/执行或上游；既有 `enabled` 规则语义与单测行为不变；插件使用 CLIProxyAPI SDK `v7.2.119` 构建，部署的 CPA 宿主版本不低于 `v7.2.103`。

## 非目标

- 修改 CPA 宿主或 cpa-usage-keeper
- 真实额度计量 / 计费 / per-key 自定义拒绝文案
- 未在本插件 state 中建立 binding 的 CPA api-key
- Keeper 中模型名伪装（独立需求，本特性不做）
- 用 `frontend_auth` 做门禁（无法自定义额度用尽文案）
- 改动 CPA 宿主以改变 Home 模式下 self-executor route 的生命周期顺序；该模式会在 before-interceptor 前由宿主返回 503
- 对 `/v1/alpha/search` 或 `/backend-api/codex/alpha/search`（Codex direct alias）接入 request interceptor；这些端点不经过 `BaseAPIHandler` 的 request-interceptor 链
- 将带 `Upgrade: websocket` 的 `GET /v1/responses` 或 `GET /backend-api/codex/responses` 纳入固定 HTTP 403/body 契约；它们先完成 WebSocket upgrade，只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的后续消息才可被前置拒绝，宿主会将该拒绝写成 WebSocket `type:"error"` 事件，而非 HTTP DirectResponse

## 术语表

- **规则启用 `enabled`**：是否对该 key 应用追加规则集（既有语义）。_Avoid_：把「启用」说成允许访问
- **访问禁用 `blocked`**：是否禁止该 key 在本特性支持的模型请求范围内发起调用。_Avoid_：与 `enabled` 混称「禁用」
- **Key 绑定**：state 中一条 `KeyBinding`（key / alias / enabled / blocked / rules）

## 影响面

- 运行时：`main.go`（能力注册、`request.intercept_before` 分发与门禁）
- 状态：`state.go`（`KeyBinding.Blocked`、按 key 查 blocked 的查找）
- 管理 API：`management.go`（POST/PATCH/列表读写 `blocked`）
- 前端：`web/src/panels/KeysPanel.tsx`、`web/src/api.ts`
- 测试：`main_test.go` / `state_test.go` / `management_test.go` / `intercept_block_test.go` / `host_lifecycle_test.go` / 既有 `keybinding_test.go` 回归
- SDK 契约：现有 CLIProxyAPI 依赖升级并固定为 `v7.2.119`；使用 `pluginapi.RequestInterceptResponse.Terminate/StatusCode/ResponseBody` 与 `pluginabi.MethodRequestInterceptBefore`
- 宿主兼容性：CPA runtime SHALL 为 `v7.2.103` 或更高；插件升级后注册 RPC `SchemaVersion=2`（native `ABIVersion` 仍为 1），更早宿主最多支持 schema 1，无法加载该插件，也不具备 Terminate direct-response 契约

## 已确认的关键决策

- 字段：新增 `blocked`，不重定义 `enabled` —— 正交语义，兼容既有行为（详见 `../../adr/0004-key-binding-blocked-separate-from-enabled.md`）
- 门禁路径：`request.intercept_before` Terminate，不在 `model.route`/executor 拒绝；CLIProxyAPI `v7.2.119` 会先调用纯 `model.route`，这是允许的（详见 `../../adr/0005-block-key-via-request-intercept-before.md`）
- 客户端错误：HTTP 403 + 固定 JSON，`type=permission_error`，`code=insufficient_quota`，`message=Your quota has been exhausted.`
- UI：与「启用规则」并列的 Switch（新建/编辑窗 + 列表列）
- 插件 YAML/生命周期总开关 `enabled: false` 时本插件不拦截
- 门禁查找不依赖规则 `enabled`：`blocked=true` 即使 `enabled=false` 也拒绝
- 作用域：仅 state 内存在且 `blocked=true` 的 binding；删除 binding 即解除本插件禁用
- Preview dry-run：本特性不强制改 preview；运行时拒绝优先（YAGNI）
- 版本前提：构建 SDK 固定 `v7.2.119`；运行时 CPA 最低 `v7.2.103`；不新增运行时或业务第三方依赖
- 适用范围：固定 HTTP 403 仅承诺给 Home 关闭、并实际进入 `BaseAPIHandler` 的标准 OpenAI/Claude/Responses **HTTP/SSE（非 WebSocket upgrade）**执行路径；Home 模式的 self-executor route 会先得到宿主 503，`/v1/alpha/search` 与 `/backend-api/codex/alpha/search` 不经过此拦截器，均非本插件门禁范围；带 `Upgrade: websocket` 的 `GET /v1/responses` 与 `GET /backend-api/codex/responses` 只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息才可在执行前走前置拦截，宿主以 WebSocket `type:"error"` 事件承载该结果，不承诺固定 HTTP 403/body

## ADDED Requirements

### Requirement: Key 绑定持久化访问禁用标志

插件 state 中的每条 Key 绑定 SHALL 持久化布尔字段 `blocked`；缺省与旧 state 缺字段时视为 `false`。

#### Scenario: 旧 state 加载后 blocked 为 false

- **GIVEN** state_file JSON 中某 binding 无 `blocked` 字段
- **WHEN** 插件加载 state
- **THEN** 该 binding 的 `blocked` 为 `false`，且可正常参与规则匹配（若 `enabled=true`）

#### Scenario: blocked 经管理 API 写入后可再读出

- **GIVEN** 管理员对 key `sk-a` 的 binding 设置 `blocked=true` 并保存成功
- **WHEN** 再次 GET keys/state
- **THEN** 返回的该 binding 含 `"blocked": true`

### Requirement: 访问禁用时短路拒绝请求

在 Home 关闭、且宿主实际进入标准 `BaseAPIHandler` request-interceptor 链的 OpenAI/Claude/Responses **HTTP/SSE（非 WebSocket upgrade）**请求中，若插件总开关开启且客户端 API key 匹配某条 `blocked=true` 的 Key 绑定（匹配方式与既有 key 提取一致：`Authorization: Bearer` 优先，否则 `x-api-key`），插件 SHALL 在 `request.intercept_before` 终止请求。CLIProxyAPI `v7.2.119` 的既定宿主顺序会先调用纯 `model.route` 回调；该回调可以计算路由，但不得承担拒绝或发起执行。blocked 请求随后 MUST 返回相同的固定 403，且不得进入本插件 executor、认证选择/执行或上游；响应 body 为固定 JSON：

```json
{
  "error": {
    "message": "Your quota has been exhausted.",
    "type": "permission_error",
    "code": "insufficient_quota"
  }
}
```

该行为依赖 CLIProxyAPI `v7.2.103` 首次提供的 request-interceptor termination wire contract 与注册 RPC schema 2；插件 SHALL 使用 `v7.2.119` SDK 构建（`ABIVersion=1`、`SchemaVersion=2`），部署检查 SHALL 拒绝把本特性认定为已验收，若 CPA 宿主版本低于 `v7.2.103` 或无法确认版本。

Home 已开启且 `model.route` 返回 self/executor target 时，CLIProxyAPI `v7.2.119` 会在调用 `request.intercept_before` 前返回宿主 503；`/v1/alpha/search` 与 `/backend-api/codex/alpha/search`（Codex direct alias）也不会调用该 hook。以上路径不承诺本特性的固定 403 或门禁效果，若需覆盖必须修改 CPA 宿主，属于非目标。

带 `Upgrade: websocket` 的 `GET /v1/responses` 与 `GET /backend-api/codex/responses` 由 `ResponsesWebsocket` 先完成 WebSocket 握手。只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的后续消息，才会在 AuthManager/executor 前经过 `request.intercept_before`；命中 `Terminate` 时，宿主将其转换为 WebSocket 文本事件（`type:"error"`，并携带宿主生成的错误字段），而不是 HTTP 403 DirectResponse。首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、消息规范化/校验错误、native-interaction 校验、provider 解析及其他 hook 前早退分支均不经过该 hook，也不属于本特性门禁。无论上述分支如何，这两条 WebSocket 路径都不承诺 HTTP 状态为 403、HTTP body 的逐字节一致性，或本特性的固定 HTTP 403 验收；若需把 WebSocket 事件格式也纳入契约，必须改 CPA 宿主并另行设计和验收。

#### Scenario: blocked key 的 HTTP/SSE 请求被 403 拒绝且文案固定

- **GIVEN** CPA Home 关闭，插件总开关开启，binding `{key: "sk-a", blocked: true}` 存在，且非 WebSocket upgrade 的 HTTP/SSE 请求进入标准 `BaseAPIHandler` 的 before-interceptor
- **WHEN** 客户端以 `Authorization: Bearer sk-a` 发起模型请求
- **THEN** 响应状态为 403，body 与上述固定 JSON 一致（字段与 message 字符串完全一致）

#### Scenario: Responses WebSocket upgrade 不属于固定 HTTP 403 契约

- **GIVEN** CPA Home 关闭，插件总开关开启，binding `{key: "sk-a", blocked: true}` 存在，客户端以 `Upgrade: websocket` 连接 `GET /v1/responses` 或 `GET /backend-api/codex/responses`
- **WHEN** 客户端在已升级连接上发送完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的 `response.create` 或 `response.append`
- **THEN** 宿主会在 AuthManager/executor 前调用 before-interceptor；命中拒绝以 WebSocket `type:"error"` 文本事件表达，不要求 HTTP 403 或固定 HTTP body 的逐字节一致性，也不列入本特性的 HTTP/SSE 验收
- **AND** 首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、消息规范化/校验错误、native-interaction 校验、provider 解析及其他 hook 前早退分支不经过该 hook，均不属于本特性门禁或固定 HTTP 响应契约

#### Scenario: 宿主可先路由，但不得执行或访问上游

- **GIVEN** CLIProxyAPI `v7.2.119` 的 Home 关闭的 `BaseAPIHandler`、插件总开关开启，binding `{key: "sk-a", blocked: true}` 存在，且 `model.route` 可处理该模型
- **WHEN** 客户端以 `Authorization: Bearer sk-a` 发起非流式模型请求
- **THEN** 宿主依次调用一次纯 `model.route` 与一次 `request.intercept_before`；客户端得到固定 403；插件 executor 调用数为 0，认证选择/执行与上游均不发生

#### Scenario: 规则关闭仍拒绝访问

- **GIVEN** 插件总开关开启，binding `{key: "sk-a", enabled: false, blocked: true}`
- **WHEN** 客户端以 `sk-a` 发起模型请求
- **THEN** 仍返回 403 与固定额度用尽 JSON（门禁不依赖 `enabled`）

#### Scenario: 未 blocked 的 binding 不拦截

- **GIVEN** 插件总开关开启，binding `{key: "sk-a", blocked: false, enabled: true, rules: ...}`
- **WHEN** 客户端以 `sk-a` 发起模型请求
- **THEN** 请求不被本门禁 Terminate，继续既有路由/映射行为

#### Scenario: 无 binding 的 key 不拦截

- **GIVEN** 插件总开关开启，state 中无 `sk-orphan` 的 binding
- **WHEN** 客户端以 `sk-orphan` 发起模型请求
- **THEN** 本插件门禁不 Terminate

#### Scenario: 插件总开关关闭时不拦截

- **GIVEN** 插件总开关关闭，binding `{key: "sk-a", blocked: true}` 仍在 state 中
- **WHEN** 客户端以 `sk-a` 发起模型请求
- **THEN** 本插件门禁不 Terminate

#### Scenario: 无客户端 key 头不拦截

- **GIVEN** 插件总开关开启，存在任意 blocked binding
- **WHEN** 请求不携带 Bearer 与 x-api-key
- **THEN** 本插件门禁不 Terminate

### Requirement: 管理面可读写 blocked

管理 API 的 Key 绑定创建（POST）、局部更新（PATCH）与列表/状态读取 SHALL 支持 `blocked` 字段；管理员 SHALL 能将 `blocked` 从 `true` 改回 `false` 以解禁，不因当前为 blocked 而拒绝编辑。

#### Scenario: PATCH 解禁后请求放行

- **GIVEN** binding `sk-a` 当前 `blocked=true`
- **WHEN** 管理员 PATCH `blocked=false` 成功后，客户端再以 `sk-a` 请求
- **THEN** 本插件门禁不再 Terminate 该 key

#### Scenario: 新建时可直接 blocked

- **GIVEN** 管理员 POST 新建 binding，body 含 `key`、`blocked: true`
- **WHEN** 保存成功且客户端立即用该 key 请求
- **THEN** 返回 403 与固定额度用尽 JSON

### Requirement: Admin UI 提供禁止访问开关

Keys 面板的新建/编辑窗与列表 SHALL 提供与「启用（规则）」区分的「禁止访问」Switch，绑定字段 `blocked`；保存时将当前 `blocked` 提交至管理 API。

#### Scenario: 编辑窗可切换禁止访问并保存

- **GIVEN** 管理员打开某 key 的编辑窗
- **WHEN** 打开「禁止访问」Switch 并保存
- **THEN** 后续 GET 该 binding 的 `blocked` 为 `true`，且 UI 不再与「启用规则」共用同一控件语义

#### Scenario: 列表可直接切换禁止访问

- **GIVEN** Keys 列表显示 binding `sk-a`，其 `blocked=false`
- **WHEN** 管理员打开该行「禁止访问」Switch
- **THEN** UI PATCH `sk-a` 的 payload 为 `{"blocked": true}`，且列表 Switch 有区别于「启用规则」的可访问名称

## MODIFIED Requirements

### Requirement: 规则启用语义保持不变（明确与 blocked 正交）

`KeyBinding.Enabled` SHALL 继续仅表示是否应用该 key 的追加规则集；`enabled=false` 时该 key 的规则不参与路由，请求仍可访问（除非 `blocked=true`）。本特性不得将 `enabled=false` 解释为拒绝访问。

#### Scenario: 仅关闭规则不拒绝

- **GIVEN** binding `{key: "sk-a", enabled: false, blocked: false}`，顶层规则可将 `a` 映射为 `b`
- **WHEN** 客户端以 `sk-a` 请求模型 `a`
- **THEN** 请求不被门禁拒绝，且 key 层规则不应用（与既有 `TestRouteModelBindingDisabled` 一致：可仍得顶层结果 `b`）

## 方案设计

### 架构与组件

| 单元 | 职责 | 依赖 |
|------|------|------|
| `KeyBinding.Blocked` | 持久化访问禁用 | state 原子写 |
| blocked 查找函数 | key 常量时间匹配且 `Blocked==true`，**不**要求 `Enabled` | KeyBindings |
| `handleRequestInterceptBefore` | 总开关 + 取 key + blocked → Terminate 403 | 上两者、现有 header 取 key |
| Management POST/PATCH/GET | 读写 `blocked` | state |
| `KeysPanel` + `api.ts` | 「禁止访问」Switch | management API |

`routeModel` / executor **不**实现拒绝逻辑。`routeModel` 在这项门禁中的作用仅限纯规则计算；它可在宿主的 before-interceptor 之前运行，但在本特性适用范围内不能导致执行或上游访问。

### 数据流

```
Client HTTP/SSE request (Bearer/x-api-key; standard BaseAPIHandler path, Home disabled; no WebSocket upgrade)
  → CPA model.route (pure model-mapper callback; v7.2.119 may run it first)
  → CPA request.intercept_before (model-mapper)
       plugin off / no match / blocked=false → executor / credential selection / upstream
       blocked=true → DirectResponse 403 + fixed JSON; stop before executor / credential selection / upstream
```

带 `Upgrade: websocket` 的两个 Responses `GET` 路径不使用上图的 HTTP DirectResponse 分支：连接先以 `101 Switching Protocols` 升级；只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的后续消息才可能命中前置拒绝，并由宿主写为 WebSocket `type:"error"` 事件。本地 synthetic prewarm、校验、provider 解析及其他 hook 前早退分支不经过该 hook。

### 关键接口

**State**

```go
type KeyBinding struct {
    Key     string  `json:"key"`
    Alias   string  `json:"alias"`
    Enabled bool    `json:"enabled"`
    Blocked bool    `json:"blocked"`
    Rules   RuleSet `json:"rules"`
}
```

**Intercept 响应（命中时）**

- `Terminate: true`
- `StatusCode: 403`
- `ResponseBody`: 上文固定 JSON 的 UTF-8 字节（常量）

**能力注册**

- 声明 RequestInterceptor 能力
- `dispatchMethod` 同时处理 `request.intercept_before` 与 `request.intercept_after`；注册 RequestInterceptor 后宿主会调用两者，after 必须空放行（不重复门禁）
- `go.mod` 固定 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.119`；插件注册使用 `ABIVersion=1`、`SchemaVersion=2`；CPA runtime 最低 `v7.2.103`

**Management**

- 列表/详情 JSON 含 `blocked`
- POST body 接受 `blocked`（默认 false）
- PATCH 接受可选 `blocked *bool`（与 `enabled` 同模式）

**固定错误常量（实现一字不差）**

```text
message = "Your quota has been exhausted."
type    = "permission_error"
code    = "insufficient_quota"
```

### 错误处理

- 门禁为业务拒绝，非插件 RPC 失败 envelope
- 与其它插件（如 key-policy）并存时，宿主链上更早的拒绝可能先于本插件；本特性不保证文案在多插件下一定到达客户端
- 在本特性适用的 HTTP/SSE 范围内，流式与非流式均可先经过纯 `model.route`，但在 intercept 阶段结束，不进入 plugin executor、认证选择/执行、stream 转发或上游
- Home 已开启的 self-executor route 在 interceptor 前被宿主返回 503；`/v1/alpha/search` 与 `/backend-api/codex/alpha/search` 不调用 request interceptor。这些路径明确不在本插件门禁范围内
- `GET /v1/responses` 与 `GET /backend-api/codex/responses` 的 WebSocket upgrade 会在前置拒绝前完成握手；只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息可在 AuthManager/executor 前拒绝，对客户端是 `type:"error"` WebSocket 事件。首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、校验、native-interaction、provider 解析及其他 hook 前早退分支不经过 hook；固定 HTTP 403/body 对全部 WebSocket 分支都不适用

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| 旧 state 加载 blocked=false | unit | 任务内 TDD | 测试通过 |
| blocked 管理 API 读写 | unit | 任务内 TDD | 测试通过 |
| blocked key 的 HTTP/SSE 403 + 固定 JSON | unit | 任务内 TDD | 测试通过 |
| enabled=false 仍拒绝 | unit | 任务内 TDD | 测试通过 |
| 未 blocked / 无 binding / 插件关 / 无 key 头 | unit | 任务内 TDD | 测试通过 |
| PATCH 解禁后放行 | unit | 任务内 TDD | 测试通过 |
| 新建直接 blocked | unit | 任务内 TDD | 测试通过 |
| 仅关闭规则不拒绝（回归） | unit | 任务内 TDD / 既有用例 | 测试通过 |
| UI 编辑窗禁止访问 Switch 与保存 payload | unit | 任务内 TDD（Vitest + Testing Library） | 测试通过 |
| UI 列表禁止访问 Switch 与 PATCH payload | unit | 任务内 TDD（Vitest + Testing Library） | 测试通过 |
| Home 关闭的宿主生命周期允许纯路由、但阻止 executor / credential execution / 上游 | integration | 本地 `BaseAPIHandler` 非流式与流式回归 | self-executor 与 provider 分支的 `TestBlockedKeyHost*Lifecycle*` 通过，并验证真实 Gin 请求头 |
| CPA runtime 版本不低于 `v7.2.103` | compatibility | 验收任务 | `X-CPA-VERSION` 可解析且版本满足下限 |
| Home 关闭的 live CPA `POST /v1/responses` blocked key 返回固定 403 | e2e | 验收任务 | HTTP/SSE 403 + body 字节一致；无 live CPA 或无法确认 Home 关闭时明确 DEFERRED（executor/upstream 不变量由本地生命周期回归证明；不含 WebSocket `GET` upgrade） |
| `make test` + `npm test` + `npm run typecheck` + `make web-build` | integration | 验收任务 | 全部命令 exit 0 |

## 风险与边缘情况

- **多插件顺序**：客户端可能先看到其它插件的 401/拒绝，而非本 403 文案
- **宿主生命周期顺序与范围**：固定 SDK `v7.2.119` 在 Home 关闭的标准 `BaseAPIHandler` HTTP/SSE 路径先调用纯 `model.route` 再调用 before-interceptor；若未来宿主顺序改变，`host_lifecycle_test.go` 会给出回归信号。在 Home 模式的 self-executor route，宿主在 hook 前返回 503；`/v1/alpha/search` 与 `/backend-api/codex/alpha/search` 不进该 hook，均不在此插件门禁范围。`GET /v1/responses` 与 `GET /backend-api/codex/responses` 的 WebSocket upgrade 会先返回 101；只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息可命中 before-interceptor，并由 WebSocket `type:"error"` 事件承载。首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、校验、native-interaction、provider 解析及其他 hook 前早退分支不经过 hook，全部 WebSocket 分支均不属于固定 HTTP 403 契约
- **旧宿主不兼容**：CPA `< v7.2.103` 仅支持注册 RPC schema 1，而插件升级后声明 schema 2，插件注册会被拒绝；即便绕过注册兼容检查，旧 wire contract 也不支持 Terminate direct-response 字段
- **产品语义**：对外伪装为额度用尽，非真实计费；UI 文案用「禁止访问」，对外 message 用额度句
- **作用域**：只能禁本插件已登记 binding 的 key
- **key 比较**：与既有 binding 查找一致使用常量时间比较，避免密钥时序泄漏

## 开放问题

- 无阻塞实施的开放问题。列表列是否使用 danger 色为视觉细节，实施时可按 Semi 惯例选择。
