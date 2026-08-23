---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: key-rules-admin-ui
  status: active
  covers:
    - "main.go"
    - "main_test.go"
    - "state.go"
    - "state_test.go"
    - "management.go"
    - "management_test.go"
    - "web_embed.go"
    - "web/**"
    - "Makefile"
    - ".github/workflows/build.yml"
    - "README.md"
    - "CLAUDE.md"
  sync_commit: 2f2ba474a8674d3c4171034337ecccb7c9b5dbaa
---

# Key 维度规则与 Web 管理界面 设计

## 背景与目标

model-mapper-plus 现状仅有 YAML 单行配置的四段规则，无 key 维度、无可视化管理。本特性增加两项能力：①**key 绑定**——指定客户端 API key 追加独立规则集，串联跑在顶层规则之后；②**web 管理界面**——规则管理 / Key 绑定 / 规则试跑三板块，可写直改、即时生效。思考强度控制并入规则 DSL（模型名后缀），不设独立机制（ADR-0002）。

**成功标准**：指定 key 的请求按"顶层规则集 → key 规则集"接力改写模型（含强度后缀）；web UI 完成全部配置读写且保存即生效、无需 reload CPA；state_file 与 YAML 的真相源关系有文档且可验证。

## 非目标

- 独立的思考强度映射表、请求体强度字段改写器（见 ADR-0002）
- 按思考强度路由到不同模型（强度不是路由维度）
- 规则命中统计、用量面板、ModelPicker/catalog（key-policy 特有业务）
- i18n（首版中文单语）、根 package main 目录结构全面重构

## 术语表

- **思考强度**：客户端请求的目标推理努力级别，经模型名后缀 `model(level)` 或请求体字段表达；规范值域 `none/auto/minimal/low/medium/high/xhigh/max` 或数字预算。_Avoid_：Effort、推理强度、Reasoning Level
- **规则集**：全局段 + 三个端点段（Claude Messages / Codex Responses / OpenAI Completions）的四元组；顶层与 key 绑定共用此结构。段选择：端点段非空则用端点段，否则回退全局段，不叠加
- **key 绑定**：客户端 API key 原文 → {别名、启用开关、规则集} 的关联
- **state_file**：插件持有的 JSON 状态文件，web UI 的可写真相源

## 影响面

`main.go`（routeModel 签名与 3 调用点、注册与分发）；新增 `state.go`、`management.go`、`web_embed.go`、`web/` 前端；`Makefile`（web-build）、`.github/workflows/build.yml`（Node setup）；`README.md`、`CLAUDE.md` 文档更新。插件 SDK 依赖不变。

## 已确认的关键决策

- 状态模型：state_file（JSON）为真相源，YAML 规则字段仅首次 seed —— 插件无法回写 CPA 主配置，且 key-policy 已生产验证该不变量（详见 `../../adr/0001-state-file-as-source-of-truth.md`）
- 思考强度：并入规则 DSL，经模型名后缀表达 —— CPA 提取强度时后缀覆盖请求体字段，DSL 零改动即可表达（详见 `../../adr/0002-thinking-effort-via-model-suffix-rules.md`）
- key 规则：串联跑在顶层规则输出之后 —— key 有最终话语权，接力语义覆盖"推翻端点结果/逐 key 再降档"场景（详见 `../../adr/0003-key-rules-run-after-top-level-rules.md`）
- key 规则集镜像顶层四段结构：对称心智模型，`selectRules` 段选择逻辑两处复用
- key 提取：入站请求头 `Authorization: Bearer` 优先、`x-api-key` 次之；插件不读 CPA 主配置，纯插件内闭环
- web UI key 列表来源：CPA `GET /v0/management/api-keys`（返回原文），前端以 management key 直调，不经插件
- 前端：React 19 + TypeScript 7 + Vite 8 + Semi Design，单文件内联构建 + go:embed；Semi 按需引入控制单文件体积；界面与布局全部由 Semi 组件构建（Semi Layout 承载顶部导航与内容区，不引入手写 CSS 框架）；砍 router/i18n；顶部横向导航（layout-v6 确认稿）

## ADDED Requirements

### Requirement: 客户端 key 提取

插件 SHALL 从路由与执行入参的请求头提取客户端 API key：`Authorization: Bearer <key>` 优先，缺失时取 `x-api-key`；两者皆无则该请求不参与 key 绑定匹配。

#### Scenario: Bearer 优先于 x-api-key

- **GIVEN** 请求同时携带 `Authorization: Bearer sk-a` 与 `x-api-key: sk-b`
- **WHEN** 插件提取客户端 key
- **THEN** 得到 `sk-a`

#### Scenario: 无 key 头跳过 key 层

- **GIVEN** 请求不携带任何 key 头
- **WHEN** 路由判定
- **THEN** 仅按顶层规则集判定，key 层不参与

### Requirement: key 层接力执行

对绑定存在且启用的 key，插件 SHALL 在顶层规则集的输出之上，接力执行该 key 规则集中按端点选中的段（端点段非空用端点段，否则回退该 key 全局段）。

#### Scenario: 接力降档

- **GIVEN** 顶层 Claude 段 `claude-opus-4-5(max)=>claude-opus-4-5(high)`，key K 绑定 Claude 段 `claude-opus-4-5(high)=>claude-opus-4-5(medium)` 且启用
- **WHEN** K 经 claude 端点请求 `claude-opus-4-5(max)`
- **THEN** 出站模型为 `claude-opus-4-5(medium)`

#### Scenario: 未绑定 key 不受 key 层影响

- **GIVEN** 同上配置
- **WHEN** 未绑定的 key 经 claude 端点请求 `claude-opus-4-5(max)`
- **THEN** 出站模型为 `claude-opus-4-5(high)`

#### Scenario: key 端点段留空回退其全局段

- **GIVEN** K 绑定仅有全局段 `*(max)=>$1(high)`，顶层规则无匹配
- **WHEN** K 经 openai 端点请求 `gpt-5(max)`
- **THEN** 出站模型为 `gpt-5(high)`

#### Scenario: 绑定停用时透传顶层结果

- **GIVEN** K 的绑定存在但启用开关关闭
- **WHEN** K 请求任意模型
- **THEN** 结果等于仅执行顶层规则集的输出

### Requirement: 净效果为零不路由

接力执行后最终模型等于原始请求模型时，插件 SHALL 返回不接管（Handled=false），请求走 CPA 默认路径。

#### Scenario: key 层改回原值

- **GIVEN** 顶层段 `gpt-4o=>deepseek-V3`，K 段 `deepseek-V3=>gpt-4o`
- **WHEN** K 请求 `gpt-4o`
- **THEN** 插件不接管路由

### Requirement: state_file 真相源与 YAML seed

state_file 路径 SHALL 经 `ResolveStatePath` 规范化为绝对路径（对齐 key-policy）：配置为空时默认文件名 `model-mapper-plus-state.json`；默认文件名与相对路径均相对 **CPA 进程工作目录** 解析（插件无法读取 CPA 的 `plugins.dir`，故不追逐 `.so` 位置）；绝对路径原样清理返回。state_file 存在且合法时 SHALL 作为顶层规则四段与 key 绑定的唯一真相源；不存在时 SHALL 以 YAML 规则字段为运行值，并于首次经 management API 保存时创建 state_file（纳入当时 YAML 值，并在需要时创建父目录 mode 0700）。reconfigure 把 state_file 换到一个不存在的新路径、且当前已有持久化数据时，SHALL 把当前数据（规则四段 + key 绑定）迁移到新路径（创建文件、mode 0600、原子写）后即以新路径为真相源——避免用户换路径时规则与 key 绑定看起来「丢失」；首次运行（无持久化数据）换到不存在的新路径仍回退 YAML seed、不创建文件。`enabled` 开关 SHALL 只来自 YAML，不进 state_file。`GET /state` SHALL 返回规范化后的 `state_file` 绝对路径供 UI 展示。

#### Scenario: seed 仅一次

- **GIVEN** 无 state_file，YAML `global_rules` 为 R1
- **WHEN** 插件加载后经 UI 保存一次 key 绑定
- **THEN** 绝对路径上的 state_file 创建且其 `rules.global` 为 R1；此后修改 YAML `global_rules` 不再影响运行值

#### Scenario: 默认路径基于进程工作目录

- **GIVEN** 配置未写 `state_file`，CPA 进程工作目录为 `/app`
- **WHEN** 解析状态路径
- **THEN** 得到 `/app/model-mapper-plus-state.json`

#### Scenario: 换路径迁移持久化数据

- **GIVEN** state_file=A（存在，含规则与 key 绑定）已加载为真相源
- **WHEN** reconfigure 把 state_file 改为一个不存在的新路径 B
- **THEN** B 被创建且内容为 A 的规则四段与 key 绑定；运行值取自 B，`persisted=true`，`GET /state` 的 `state_file` 为 B

#### Scenario: 首次运行换路径不迁移

- **GIVEN** 无持久化数据（首次运行，state_file 为默认且不存在）
- **WHEN** reconfigure 把 state_file 改为一个不存在的新路径
- **THEN** 不创建文件，回退 YAML seed，`persisted=false`

### Requirement: state_file 损坏回退

state_file 无法解析时 SHALL 回退 YAML seed 值继续运行，不中断插件加载。

#### Scenario: 非法 JSON 回退

- **GIVEN** state_file 内容为非法 JSON，YAML `global_rules` 为 R1
- **WHEN** 插件加载
- **THEN** 插件以 R1 运行，web UI 可打开并经保存修复 state_file

### Requirement: state_file 原子写

state_file 的每次写入 SHALL：必要时创建父目录 (0700)，再原子完成（写临时文件、fsync、chmod 0600、rename 替换）。

#### Scenario: 任意时刻文件完整

- **GIVEN** web UI 连续多次保存
- **WHEN** 任一时刻读取 state_file
- **THEN** 内容为完整的旧版或完整的新版之一，且权限为 0600

#### Scenario: 嵌套目录自动创建

- **GIVEN** `state_file` 指向尚不存在的嵌套目录下的文件
- **WHEN** 首次保存
- **THEN** 父目录被创建且 state 文件成功写入

### Requirement: management 能力注册与路由

插件 SHALL 声明 ManagementAPI 能力并实现 `management.register`/`management.handle`：Resource 路由 `GET /index.html` 返回嵌入管理页；数据路由含 `GET /state`、`PUT /rules`、`POST /keys`、`PATCH /keys`、`DELETE /keys`、`POST /preview`。

#### Scenario: 管理页可访问

- **GIVEN** 插件已加载
- **WHEN** 浏览器 GET `/v0/resource/plugins/model-mapper-plus/index.html`
- **THEN** 返回管理页面 HTML（资源路由不经 management 鉴权；数据 API 由 CPA 侧鉴权后转发）

### Requirement: 保存前 DSL 预校验

`PUT /rules` 与 key 绑定保存 SHALL 先对全部涉及段执行 `parseRules` 校验；任一段非法则整体拒绝、返回错误描述且不落盘。

#### Scenario: 非法规则拒绝且状态不变

- **GIVEN** 当前状态合法
- **WHEN** `PUT /rules` 携带含空白字符的段 `gpt-* => deepseek`
- **THEN** 返回 400 与错误描述，state_file 内容不变

### Requirement: 规则试跑

`POST /preview` SHALL 使用与正式 `routeModel` 相同的两层规则链与 `enabled` 开关，按给定 key（可选）、端点、模型返回分步结果：顶层输出 M₁、key 层输出 M₂、是否路由、最终出站模型。

#### Scenario: 分步结果可见

- **GIVEN** 「接力降档」Scenario 的配置
- **WHEN** `POST /preview`（key=K，format=claude，model=`claude-opus-4-5(max)`）
- **THEN** 返回 M₁=`claude-opus-4-5(high)`、M₂=`claude-opus-4-5(medium)`、routed=true

#### Scenario: 插件禁用时试跑不映射

- **GIVEN** `enabled: false` 且存在会改写模型的规则
- **WHEN** `POST /preview`
- **THEN** routed=false 且 final 等于输入模型

### Requirement: web 管理界面三板块

web UI SHALL 以顶部横向导航提供三板块：规则管理（四段有序条目编辑，条目支持上移/下移/删除，含 `\a`/`\A` 大小写操作条目）、Key 绑定（列表 + 新增/编辑弹窗，规则集编辑器与规则管理同构）、规则试跑（preview 表单与分步结果展示）。作为 CPA panel 同源 iframe 时 SHALL 跟随父页面 `data-theme`（light/white/dark）自动切换 Semi 主题。

#### Scenario: key 下拉来自 CPA

- **GIVEN** CPA 配置 `api-keys: [sk-a, sk-b]`
- **WHEN** 打开新增绑定弹窗
- **THEN** key 下拉列出 sk-a、sk-b（前端以 management key 调 `GET /v0/management/api-keys`）

#### Scenario: 跟随 panel 深色主题

- **GIVEN** UI 嵌入 CPA panel 且父文档 `data-theme=dark`
- **WHEN** 页面加载
- **THEN** iframe 应用 dark 主题（body `theme-mode=dark`）

## MODIFIED Requirements

### Requirement: 路由判定链路（改：单层规则 → 两层接力）

handleModelRoute SHALL 以 `SourceFormat`、`RequestedModel`、`Headers`、`Body` 为输入：先执行顶层规则集得 M₁，再对启用绑定的 key 接力 key 层得 M₂；仅当 M₂ ≠ RequestedModel 时返回 `Handled=true`、`TargetKind=self`。当 `SourceFormat=openai-response`、RequestedModel 不含显式后缀且 `Body.reasoning.effort` 是受支持的离散强度时，插件 SHALL 先以 `RequestedModel(effort)` 作为虚拟输入执行同一规则链；若虚拟输入未产生映射，再回退到 RequestedModel 执行原有规则链。executor 执行前的二次路由判定 SHALL 使用相同逻辑，确保路由判定与实际出站模型一致；显式模型后缀始终优先于请求体强度。

#### Scenario: 两层接力触发路由

- **GIVEN** 「接力降档」Scenario 的配置
- **WHEN** K 经 claude 端点请求 `claude-opus-4-5(max)`
- **THEN** 返回 Handled=true，目标为插件自身 executor，出站模型 `claude-opus-4-5(medium)`

#### Scenario: Codex 请求体强度触发后缀规则

- **GIVEN** Codex Responses 规则 `gpt-5.6-sol(xhigh)=>gpt-5.6-sol(medium)`
- **WHEN** 客户端请求模型 `gpt-5.6-sol`，且请求体含 `reasoning.effort=xhigh`
- **THEN** 路由返回 Handled=true，executor 实际出站模型为 `gpt-5.6-sol(medium)`，CPA 以该后缀覆盖请求体中的 xhigh

#### Scenario: 强度规则未命中时保留普通映射

- **GIVEN** Codex Responses 规则 `gpt-5.6-sol=>mapped-model`
- **WHEN** 客户端请求模型 `gpt-5.6-sol`，且请求体含 `reasoning.effort=xhigh`
- **THEN** 虚拟输入未命中后回退裸模型规则，实际出站模型为 `mapped-model`

### Requirement: 插件注册能力（改：新增 ManagementAPI 与 state_file 配置项）

pluginRegistration SHALL 在 `model_router`、`executor`、`executor.execute_stream` 之外声明 ManagementAPI 能力；`Metadata.ConfigFields` SHALL 只声明 `enabled` 与 `state_file`（默认 `model-mapper-plus-state.json`）两项——规则四段改由插件自己的管理页维护，不在 CPA 插件配置页重复暴露，但 YAML 侧仍按上文 seed 语义读取（`decodeConfig`/`decodeLifecycleConfig` 保持兼容，既有配置不失效）。ConfigFields 的 `Name` 与 `Description` SHALL 不含 `& ' < > "`：CPA 渲染插件元数据时逐字段跑 `html.EscapeString`，这五个字符会成为实体乱码且插件侧无法关闭。

#### Scenario: 注册包含 management 声明

- **GIVEN** 插件初始化
- **WHEN** CPA 查询注册信息
- **THEN** 能力列表含 ManagementAPI，ConfigFields 恰为 `enabled` 与 `state_file`，且两者文案均不含被宿主转义的字符

## REMOVED Requirements

无。

## 方案设计

### 架构与组件

| 单元 | 职责 | 依赖 |
|------|------|------|
| `main.go`（改） | `routeModel` 扩为两层接力；`apiKeyFromHeaders`；注册加 ManagementAPI；`handleMethod` 增两方法分发 | pluginapi SDK |
| `state.go`（新） | `State` 结构、loadState、YAML seed 合并、atomicWriteState | 标准库 |
| `management.go`（新） | management.register/handle；`/state`、`/rules`、`/keys`、`/preview` 各 handler；资源前缀分发 | state.go、规则 DSL |
| `web_embed.go`（新） | `//go:embed web/dist/index.html` 与 Serve（含占位页先行提交） | 标准库 |
| `web/`（新） | React 19 + TS 7 + Vite 8 + Semi Design 单文件前端；三板块；session/panelAuth 平移 key-policy | npm 构建链 |

### 数据流

```
CPA lifecycle(config_yaml) → decodeLifecycleConfig → Config(YAML 值)
                                                ↘ 无 state_file 时作为 seed
state_file(JSON) → loadState → State(顶层四段 + key 绑定) → routeModel/apiKeyFromHeaders 读取
web UI → management API → 校验 → 更新内存 State → atomicWriteState 落盘（即时生效，无 reload）
```

State JSON：`{version, rules:{global,claude,codex,openai}, key_bindings:[{key,alias,enabled,rules:{global,claude,codex,openai}}], updated_at}`

### 关键接口

```go
func apiKeyFromHeaders(h http.Header) string            // Bearer 优先，x-api-key 次之
func routeModel(cfg Config, st *State, format, model, apiKey string) (string, bool)
```

management API（CPA 挂载于 `/v0/management/plugins/model-mapper-plus/`）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/state` | 全量状态（返回 key 原文——management API 已由 CPA 鉴权；前端展示时掩码） |
| PUT | `/rules` | 更新顶层四段（逐段 parseRules 预校验） |
| POST | `/keys` | 新增绑定（已存在同 key 则按 upsert） |
| PATCH | `/keys` | 更新别名/启用/规则集（以 key 原文标识目标绑定，从 query/body 取，不用 path template） |
| DELETE | `/keys` | 删除绑定（同上以 key 原文标识） |
| POST | `/preview` | 试跑：{key?, format, model} → {m1, m2, routed, final} |

### 错误处理

DSL 非法 → 400 拒绝不落盘；state_file 损坏 → 回退 seed；state_file 写失败 → API 500 且内存状态回滚；请求无 key 头 → 跳过 key 层；宿主回调失败维持现状语义；web 嵌入页缺失 → 返回占位说明页。

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| Bearer 优先于 x-api-key | unit | 任务内 TDD | 测试通过 |
| 无 key 头跳过 key 层 | unit | 任务内 TDD | 测试通过 |
| 接力降档 / 未绑定不受影响 / 留空回退 / 停用透传 | unit | 任务内 TDD | 测试通过 |
| 净效果为零不路由 | unit | 任务内 TDD | 测试通过 |
| seed 仅一次 / 损坏回退 / 原子写完整性 | unit | 任务内 TDD | 测试通过 |
| 管理页可访问 / 非法规则拒绝 / 试跑分步 | unit | 任务内 TDD | 测试通过 |
| key 下拉来自 CPA | integration | 验收任务 | smoke 脚本输出 |
| 端到端：绑定 key 请求经接力改写且响应模型字段恢复 | e2e | 验收任务（make smoke-local 增 key 维度用例） | smoke 通过 |
| web 构建嵌入 .so | build | 任务内验证 | make build 产物含页面 |

## 风险与边缘情况

- **`*` 捕获吞后缀**：`claude-*` 的 `*` 会捕获 `opus-4-5(max)` 整体——README 与 UI 提示说明，试跑面板可验证
- **key 明文存 state_file**：0600 权限，风险与 CPA config.yaml 明文 api-keys 同级；README 明确
- **状态双真相**：state_file 存在即真相——README/CLAUDE.md 显著标注，避免用户改 YAML 不生效的困惑
- **CPA panel iframe 嵌入**：panelAuth 同源复用方案平移自 key-policy；跨源 iframe 无法复用属预期安全行为
- **响应模型恢复**：白名单不变，恢复为客户端原始请求模型（不含任一层改写）；`streamChunkRewriter` 零改动

## 开放问题

- 前端 vitest 覆盖范围：首版可仅构建冒烟，后续按需补组件测试
- CPA panel 主题/语言同步（themeSync/langSync）是否一并平移：实施时若成本低于半天则保留
