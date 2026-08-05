---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: key-access-block
  status: active
  covers:
    - "main.go"
    - "main_test.go"
    - "state.go"
    - "state_test.go"
    - "management.go"
    - "management_test.go"
    - "keybinding_test.go"
    - "web/src/panels/KeysPanel.tsx"
    - "web/src/api.ts"
  sync_commit: null
---

# Key 访问禁用（blocked）设计

## 背景与目标

指定 Key 绑定面板已有「启用」开关，但仅控制是否应用该 key 的**追加规则集**，无法禁止该 key 继续调用。运维需要在本插件内对已登记 binding 的 key 做访问门禁，并对客户端返回英文「额度已用尽」风格错误（对齐 CPA 403 → `insufficient_quota`）。

**成功标准 / Success criteria**：管理员可在新建/编辑窗与列表用 Switch 设置 `blocked`；`blocked=true` 的 key 在插件启用时被 `request.intercept_before` 短路拒绝，HTTP 403，body 为固定 OpenAI 形 JSON（message 英文额度用尽）；既有 `enabled` 规则语义与单测行为不变。

## 非目标

- 修改 CPA 宿主或 cpa-usage-keeper
- 真实额度计量 / 计费 / per-key 自定义拒绝文案
- 未在本插件 state 中建立 binding 的 CPA api-key
- Keeper 中模型名伪装（独立需求，本特性不做）
- 用 `frontend_auth` 做门禁（无法自定义额度用尽文案）

## 术语表

- **规则启用 `enabled`**：是否对该 key 应用追加规则集（既有语义）。_Avoid_：把「启用」说成允许访问
- **访问禁用 `blocked`**：是否禁止该 key 发起模型请求（本特性）。_Avoid_：与 `enabled` 混称「禁用」
- **Key 绑定**：state 中一条 `KeyBinding`（key / alias / enabled / blocked / rules）

## 影响面

- 运行时：`main.go`（能力注册、`request.intercept_before` 分发与门禁）
- 状态：`state.go`（`KeyBinding.Blocked`、按 key 查 blocked 的查找）
- 管理 API：`management.go`（POST/PATCH/列表读写 `blocked`）
- 前端：`web/src/panels/KeysPanel.tsx`、`web/src/api.ts`
- 测试：`main_test.go` / `state_test.go` / `management_test.go` / 既有 `keybinding_test.go` 回归
- SDK 契约：沿用已有 `pluginapi` RequestInterceptor / `pluginabi.MethodRequestInterceptBefore`，无新依赖

## 已确认的关键决策

- 字段：新增 `blocked`，不重定义 `enabled` —— 正交语义，兼容既有行为（详见 `../../adr/0004-key-binding-blocked-separate-from-enabled.md`）
- 门禁路径：`request.intercept_before` Terminate，不在 `model.route`/executor 拒绝（详见 `../../adr/0005-block-key-via-request-intercept-before.md`）
- 客户端错误：HTTP 403 + 固定 JSON，`type=permission_error`，`code=insufficient_quota`，`message=Your quota has been exhausted.`
- UI：与「启用规则」并列的 Switch（新建/编辑窗 + 列表列）
- 插件 YAML/生命周期总开关 `enabled: false` 时本插件不拦截
- 门禁查找不依赖规则 `enabled`：`blocked=true` 即使 `enabled=false` 也拒绝
- 作用域：仅 state 内存在且 `blocked=true` 的 binding；删除 binding 即解除本插件禁用
- Preview dry-run：本特性不强制改 preview；运行时拒绝优先（YAGNI）

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

当插件总开关开启，且请求客户端 API key 匹配某条 `blocked=true` 的 Key 绑定（匹配方式与既有 key 提取一致：`Authorization: Bearer` 优先，否则 `x-api-key`）时，插件 SHALL 在 `request.intercept_before` 终止请求：不进入模型路由、不进入本插件 executor、不调用上游；HTTP 状态码为 403；响应 body 为固定 JSON：

```json
{
  "error": {
    "message": "Your quota has been exhausted.",
    "type": "permission_error",
    "code": "insufficient_quota"
  }
}
```

#### Scenario: blocked key 被 403 拒绝且文案固定

- **GIVEN** 插件总开关开启，binding `{key: "sk-a", blocked: true}` 存在
- **WHEN** 客户端以 `Authorization: Bearer sk-a` 发起模型请求
- **THEN** 响应状态为 403，body 与上述固定 JSON 一致（字段与 message 字符串完全一致）

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

`routeModel` / executor **不**实现拒绝逻辑。

### 数据流

```
Client request (Bearer/x-api-key)
  → CPA request.intercept_before (model-mapper)
       plugin off / no match / blocked=false → pass
       blocked=true → DirectResponse 403 + fixed JSON
  → model.route / executor / upstream（仅 pass 后）
```

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
- `dispatchMethod` 处理 `request.intercept_before`；`request.intercept_after` 若 ABI/注册要求实现则空放行（不重复门禁）

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
- 流式与非流式均在 intercept 阶段结束，不进入 stream 转发

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| 旧 state 加载 blocked=false | unit | 任务内 TDD | 测试通过 |
| blocked 管理 API 读写 | unit | 任务内 TDD | 测试通过 |
| blocked key 403 + 固定 JSON | unit | 任务内 TDD | 测试通过 |
| enabled=false 仍拒绝 | unit | 任务内 TDD | 测试通过 |
| 未 blocked / 无 binding / 插件关 / 无 key 头 | unit | 任务内 TDD | 测试通过 |
| PATCH 解禁后放行 | unit | 任务内 TDD | 测试通过 |
| 新建直接 blocked | unit | 任务内 TDD | 测试通过 |
| 仅关闭规则不拒绝（回归） | unit | 任务内 TDD / 既有用例 | 测试通过 |
| UI 禁止访问 Switch 与类型 | unit（前端若有）/ 手工 | 任务内或验收 | 构建通过 + 手工确认 |
| `make test` + `make web-build` | integration | 验收任务 | 命令成功 |

## 风险与边缘情况

- **多插件顺序**：客户端可能先看到其它插件的 401/拒绝，而非本 403 文案
- **产品语义**：对外伪装为额度用尽，非真实计费；UI 文案用「禁止访问」，对外 message 用额度句
- **作用域**：只能禁本插件已登记 binding 的 key
- **key 比较**：与既有 binding 查找一致使用常量时间比较，避免密钥时序泄漏

## 开放问题

- 无阻塞实施的开放问题。列表列是否使用 danger 色为视觉细节，实施时可按 Semi 惯例选择。
