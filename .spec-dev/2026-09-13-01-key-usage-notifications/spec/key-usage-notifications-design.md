---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: key-usage-notifications
  status: active
  covers:
    - "main.go"
    - "state.go"
    - "management.go"
    - "abi_cgo.go"
    - "keeper_client.go"
    - "keeper_aliases.go"
    - "channel_credentials.go"
    - "notification_*.go"
    - "web/src/App.tsx"
    - "web/src/api.ts"
    - "web/src/panels/KeysPanel.tsx"
    - "web/src/panels/KeyNotificationsTab.tsx"
    - "web/src/panels/NotificationsPanel.tsx"
    - "web/src/components/Notification*.tsx"
    - "web/src/components/PlatformIdentityEditor.tsx"
    - "web/src/notification*.ts"
  sync_commit: caf6a6a997e1fbe374f759109f62ba89c8224879
  supersedes: []
  superseded_by: null
---

# Key 渠道用量通知设计

## 背景与目标

当前 mapper 只有 Key、渠道定向和 Keeper 别名能力，没有定时发送用量通知的配置、统计存档或投递记录。新功能在 mapper 内提供多条 Key 通知，统计数据统一来自 `/Users/maverick/cpa-usage-keeper`，并通过企业微信、飞书、钉钉群机器人发送。

成功标准：管理员可以为每个 Key 建立多条独立通知；每条通知能按配置的模块和周期生成 Token、百分比、USD 折算金额、渠道窗口和认证信息；插件重启后能恢复任务并记录明确投递结果；数据缺失、限流和平台不支持时不伪造成功或零值。

## 非目标

- 不修改 `/Users/maverick/cpa-usage-keeper` 的代码、接口或数据库。
- 不修改 CLIProxyAPI 宿主、SDK 或认证优先级。
- 不发送个人单聊，不配置或自动发现群名称；Webhook 本身代表机器人所在群。
- 不把渠道共享额度解释为 Key 独享额度。
- 不把 Keeper 的价格折算金额解释为上游账单实扣。
- 不承诺平台受理后用户一定看到消息或 @ 成功；真实群验证属于 manual 验收。

## 术语表

- **通知**：一个 Key 的独立发送单元，拥有自己的模块、模板、计划、平台身份和投递记录。
- **通知模板**：通知名称、固定消息结构、模块开关和模块周期的组合；统计数值由系统生成。
- **渠道窗口**：Keeper 返回的特定额度窗口，例如 `5H` 或 `Weekly`，包含渠道共享用量、比例和重置信息。
- **Key 渠道消耗占比**：该 Key 在该渠道同期 Token 消耗除以该渠道所有 Key 同期 Token 总消耗。
- **历史覆盖**：某一统计周期已由 Keeper 查询或 mapper 存档的日期范围；覆盖不足不等于零消耗。
- **平台目标**：某条通知在企业微信、飞书或钉钉上的 Webhook、用户唯一 ID 和签名配置（签名配置按平台支持情况保存，企业微信群机器人无独立加签），不包含目标群字段。
- **全局默认通知**：一条作用于所有未配置任何专属通知的 Key 的完整通知实体（名称、模板模块、发送计划、平台身份）；Key 一旦拥有专属通知，它对该 Key 即停止，不叠加。

## 参与者与适用行为

- 管理员通过现有 CPA 管理鉴权配置全局模板、群机器人、Key 通知和查询投递记录。
- mapper 后台通知服务在 register、reconfigure 和 shutdown 生命周期内采集数据、生成任务并发送消息。
- Keeper 提供用量、价格折算、认证名称、套餐、配额窗口和重置卡只读结果。
- 企业微信、飞书和钉钉群机器人接收对应格式的群消息。
- Key 使用者不直接登录 mapper，也不拥有修改通知配置的独立身份。

## 影响面

后端新增通知管理 API、后台任务生命周期、Keeper 统计适配、历史/任务/投递存储和三个平台适配器；state 新增全局通知配置及 Key 通知列表。前端在 Key 编辑弹窗新增「通知」页签承载该 Key 的通知列表，每条通知在 Semi SideSheet 抽屉中编辑；抽屉内提供「通知模板」和「平台身份配置」两个 Tab，后者嵌套企业微信、飞书、钉钉三个 Tab，并继续使用现有 Semi Design 风格。Keeper 的现有管理连接需补充后台可用的 CPA 管理地址及凭据环境变量名；浏览器 session 不作为后台凭据。

运行数据使用独立的 bbolt 文件，配置 state 和运行库不混为一个恢复来源。新增依赖不改变插件对外 ABI；具体版本和跨平台编译在实施计划中锁定。本特性同时交付前端依赖升级：React、react-dom、semi-ui、semi-icons、Vite、TypeScript、vite 插件与测试工具链（vitest、jsdom）升级至实施时点的最新稳定版（其中 vitest、jsdom、vite 插件为跨大版本升级），并新增 dnd-kit（core + sortable）用于模块拖拽排序；升级以既有前端测试、typecheck、构建全绿为验收基线，不改变插件对外 ABI。

## 已确认的关键决策

- 采用插件内置通知服务；它符合当前单插件部署方式，避免引入第二个常驻进程（详见 `../../adr/0007-embedded-usage-notification-service.md`）。
- 一个 Key 可以拥有多条独立通知；同一人的不同 Key 仍分别配置。
- 全局模板是默认值；存在 Key 专属模板时优先使用。
- 每条通知独立配置模板、模块、计划、平台和投递状态。
- 企业微信、飞书、钉钉只发送群消息并 @ 用户；三个平台配置分离，平台配置不包含目标群字段。
- 统计唯一来源是 Keeper；金额使用 Keeper 当前价格和倍率折算 USD；消息末尾标注东八区实际统计时间（`统计时间：YYYY-MM-DD HH:mm:ss`），不展示「金额为 Keeper 价格折算，非上游账单」。
- 日、月、自然半年、年均支持本期累计和上一完整周期；半年按 1—6 月、7—12 月划分。
- Key 渠道占比的分母是该渠道同期所有 Key 的 Token 总消耗；分母为零或覆盖不一致时占比为未知。
- 渠道余量是各 Key 可访问渠道的共享上游额度；Key 自身消耗和渠道窗口共享用量分开显示。
- 当前渠道窗口同时展示 `5H` 和 `Weekly`（仅在 Keeper 返回对应窗口时显示）；重置时间后显示剩余天数和小时数；每张重置卡显示过期时间及剩余天数和小时数。
- Token 使用 Keeper 的 K/M/B 缩写展示，原始整数用于计算和存档。
- 计划支持固定间隔和日历定时；每月只能选月初或月末；每年只选月份、日期和时间，不设置“每几年”间隔。发送时间使用 Semi Design TimePicker。
- 全局提供默认计划，每个 Key 的每条通知可独立覆盖；限流时合并同 Key、同平台目标、同统计周期的待发重复任务，保留最新值。
- 首次启用遇到 Keeper 不可查询的旧日期时保留已知统计并标注缺失日期，不把缺失视作零。
- 认证配置自定义命名放在渠道内容最上方；套餐等级和可用重置卡数量均来自 Keeper，字段缺失或不支持时明确显示未知/未提供。
- 通知编辑交互：Key 编辑弹窗内「通知」页签放通知列表，编辑在 Semi SideSheet 抽屉（medium 尺寸，footer 为预览/取消/保存）内进行；抽屉内「通知模板」「平台身份配置」两个 Tab 取代早先“Key 编辑页两个外层 Tab”的表述（2026-09-13 视觉预览确认）。
- 模块顺序调整采用拖拽排序（dnd-kit core + sortable）；前端依赖升级与 dnd-kit 引入绑定本特性交付。
- 平台身份配置在管理界面明文回显 Webhook 与签名密钥、不做脱敏显示（2026-09-13 确认）；完整 API Key 维持既有掩码行为，预览、测试发送、投递日志仍不展示秘密。
- 企业微信面板不渲染签名密钥字段（企微群机器人无独立加签），签名密钥按平台支持情况保存（2026-09-13 确认）。
- Key 弹窗「通知」页签用 Semi Table 呈现列表：行内启用开关即时生效、行内「测试发送」按钮（仅已落库通知可测；新增/编辑后抽屉保存即落库，绑定从未保存过则置灰并提示先保存 Key 配置）、「最近投递」状态摘要列；操作列（编辑/测试发送/投递记录/删除）SHALL 单行不换行；投递记录用 Table 行展开查看（2026-09-13 确认；测试发送落库约束 2026-09-14 确认；操作列不换行 2026-09-15 确认）。
- 消息正文排版采用分节详情风格：模块以「▍」小节标题分节，每渠道独立成组、字段分行，分组间空行（2026-09-13 确认）；三平台以行式文本为最大公约数（企微 markdown 标签白名单、飞书 open_id 内联 @、钉钉 atUserIdList），适配细节在实施计划中展开。
- 视觉预览定稿 mockup 归档于 spec/assets/（notification-drawer-template、notification-platform-identity、notification-list、notification-message-style、notification-template-inherit、notification-global-panel，2026-09-13）。
- 通知表单输入框左侧带常显字段标题（不依赖占位符说明字段含义），全局默认通知名称字段与平台身份三字段同批补齐（2026-09-14 确认）。
- 通知模板与发送计划各自提供「跟随全局默认」开关（对应“分别回退”，可模板自定义而计划跟随全局，反之亦然），开关打开时对应区块只读展示全局内容；新建通知默认为跟随全局（2026-09-13 确认）。
- 通知名称同 Key 内唯一（同时作为消息标题），重复保存被拦截；表单校验错误定位到具体字段行内展示，提交时以通知弹窗（Toast）提示第一条错误并切到出错 Tab，保存键不静默禁用（2026-09-14 确认）；表单变更后提示预览可能过期；计划切换实时显示下一次发送时间，无效日历日期跳过并显示下一次有效时间（2026-09-13 确认）。
- @所有人（at_all）仅全局通知提供：全局平台身份可开启以免填用户唯一 ID；Key 级通知不渲染该开关，保存时剥离 at_all，启用平台必须填写用户唯一 ID（2026-09-14 确认）。
- 通知抽屉「保存」即落库：全局抽屉保存立即 PUT /notifications/settings；Key 级抽屉保存基于已保存绑定 postKey 仅落 notifications。行内测试发送始终针对已落库实体，避免「没有已启用的平台 / 通知不存在」这类草稿命中后端的报错（2026-09-14 确认）。
- 不存在全局平台身份回退：专属通知的平台身份必配于自身，不引用全局素材（2026-09-13 确认）。全局默认通知是一条完整通知实体（名称、模板、计划、平台身份），作用于未配置任何专属通知的 Key；Key 配置 ≥1 条专属通知后全局默认通知对该 Key 停止，不叠加（2026-09-13 确认）。Key 弹窗通知列表为空时提示当前按全局默认通知发送。

## 约束归属与拒绝的解读

- Keeper 的 365 天聚合查询限制由 Keeper 负责；mapper 负责存档覆盖状态和不完整标注，不声称能补回未查询到的历史。
- Keeper 的窗口 Token/金额由认证渠道负责，mapper 不改名为 Key 专属额度；Key 百分比由 mapper 使用同周期全 Key 聚合自行计算。
- 完整 API Key 不进入任何管理响应；Webhook 密钥与加签密钥在管理响应中明文回显（2026-09-13 管理员确认不做脱敏显示，管理界面由 CPA 管理鉴权保护），但仍不进入通知正文变量、投递日志和审计投影；配置文件保留在 0600 权限的受控存储中。
- 飞书自定义机器人支持 Open ID/User ID，但外部群仅 Open ID；钉钉官方群消息文档只保证内部群的 UserId @ 能力。平台不支持 @ 时，配置校验或投递结果必须明确失败，不静默伪造 @。
- 现有 `KeyBinding.Enabled`、`blocked`、`fast_allowed` 与通知开关独立；禁止访问不会抹除历史统计，关闭通知不会删除模板。
- `admin-audit-keeper-names`、`channel-target-and-fast-control`、`keeper-key-aliases` 和 `key-rules-admin-ui` 均为分面共存；本特性不登记 supersedes。通知配置保存应沿既有管理审计边界，后台投递使用独立投递记录。

## 取代与共存

无相交 active spec 被取代。与以下 active spec 分面共存：

- `.spec-dev/2026-09-12-01-admin-audit-keeper-names/spec/admin-audit-keeper-names-design.md`：共用管理写入、Keeper 客户端和 Key 页面；通知保存及测试发送纳入其管理审计协调，后台投递记录不冒充管理操作。
- `.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md`：复用 Key 和渠道凭据语义，不改变 `suppliers ∪ auth_ids` 或 Scheduler 行为。
- `.spec-dev/2026-09-05-01-keeper-key-aliases/spec/keeper-key-aliases-design.md`：复用 Keeper 超时、认证失败隔离、缓存和重配废弃旧结果约定。
- `.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`：在既有 Key 编辑窗新增通知 Tab，不改变规则编辑和 Key 保存语义。

## 行为规范（Requirements）

### Requirement: 多通知配置与继承

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#多通知配置与继承；原文保留仅作历史参考。

每个 Key SHALL 支持零条或多条通知；每条通知 SHALL 独立保存名称、启用状态、模板模块、统计周期、发送计划、平台目标和投递状态。未配置专属模板或计划时 SHALL 分别回退全局默认值。

#### Scenario: 同一 Key 的多条通知互相独立

- **GIVEN** Key K 有日报通知和周报通知，二者的平台、模块和计划不同。
- **WHEN** 修改日报通知的计划或关闭周报通知。
- **THEN** 另一条通知的模板、计划、启用状态和投递任务不变。

#### Scenario: Key 专属模板优先

- **GIVEN** 全局模板存在，Key K 有专属模板，Key L 没有专属模板。
- **WHEN** K 和 L 生成相同周期的消息。
- **THEN** K 使用专属模板，L 使用全局模板。

#### Scenario: Key 无通知时按全局默认通知发送

- **GIVEN** Key X 没有任何专属通知，全局默认通知已启用。
- **WHEN** 到达全局默认通知的发送计划触发时间。
- **THEN** X 按全局默认通知的模板与平台身份生成并发送，投递记录归属全局通知与 X。

#### Scenario: 配置专属通知后全局默认通知停止

- **GIVEN** Key X 原先没有任何专属通知，按全局默认通知发送。
- **WHEN** 管理员为 X 新增第一条专属通知并保存。
- **THEN** 全局默认通知不再对 X 发送；X 的发送完全由专属通知接管，全局默认通知对其他无通知 Key 不受影响。

### Requirement: 模板与消息结构

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#模板与消息结构；原文保留仅作历史参考。

通知 SHALL 使用通知名称作为消息标题，消息开头 SHALL 自动包含认证配置的 Keeper 自定义命名和套餐等级；模板 SHALL 支持勾选模块和调整模块顺序，但 SHALL NOT 改变系统计算的 Token、百分比、金额、窗口或重置卡字段。

#### Scenario: 模块勾选实时影响消息

- **GIVEN** 日统计、年统计和 Weekly 已开启，半年统计、5H 和重置卡已关闭。
- **WHEN** 生成预览或发送文本。
- **THEN** 文本只包含通知名称标题、认证命名/套餐、日统计、年统计和 Weekly；不出现关闭模块的空行或占位符。

#### Scenario: 自定义命名在最上方

- **GIVEN** Keeper 返回自定义命名“研发主账号”和套餐“Pro 20x”。
- **WHEN** 生成消息。
- **THEN** 标题下第一段显示“研发主账号”和“Pro 20x”，后续才是统计内容。

### Requirement: 周期统计

日、月、自然半年和年统计 SHALL 支持本期累计及上一完整自然周期；自然半年 SHALL 使用 1—6 月和 7—12 月边界。

#### Scenario: 当前与上一周期

- **GIVEN** 日模块选择上一完整周期、月模块选择本期累计。
- **WHEN** 在 2026-09-13 发送。
- **THEN** 日模块查询 2026-09-12，月模块查询 2026-09-01 至发送查询时刻，并在消息中标出各自范围。

#### Scenario: 跨年历史不足

- **GIVEN** 上一完整年度包含 Keeper 当前接口无法返回的日期。
- **WHEN** 生成年报。
- **THEN** 已知数据照常展示，缺失日期明确标注“历史数据不完整”，缺失日期不计为零。

### Requirement: Key 渠道消耗与占比

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#Key-渠道消耗与占比；原文保留仅作历史参考。

每个 Key 的通知 SHALL 按当前渠道权限逐渠道展示 Token 精确数量、同期渠道占比和 USD 折算金额；占比 SHALL 使用同一统计周期的该渠道所有 Key Token 总消耗作为分母。

#### Scenario: 渠道明细

- **GIVEN** K 在渠道 A 消耗 800000 tokens，渠道 A 同期所有 Key 消耗 2000000 tokens，Keeper 返回金额可用。
- **WHEN** 生成消息。
- **THEN** 渠道 A 显示 800.00K tokens、40.00% 和对应 USD 折算金额。

#### Scenario: 分母为零或覆盖不一致

- **GIVEN** 渠道同期总消耗为零，或 Key 与渠道总量的覆盖日期不一致。
- **WHEN** 生成消息。
- **THEN** Token 和已知金额仍可展示，占比显示“未知”，不显示 0% 代替未知。

### Requirement: 渠道窗口与认证信息

通知 SHALL 逐渠道展示 Keeper 自定义命名、套餐等级和可用重置卡数量；若 Keeper 返回 5 小时窗口，通知 SHALL 展示 `5H`，若返回周窗口，通知 SHALL 展示 `Weekly`；不存在的窗口 SHALL 不渲染该窗口行。

#### Scenario: 两个窗口与重置卡

- **GIVEN** 渠道有 5H 和 Weekly 窗口，5H 重置时间为 2026-09-13 19:00，当前为 17:00，存在两张卡。
- **WHEN** 生成消息。
- **THEN** 展示 `5H:`、`Weekly:`，重置时间后分别显示“还剩 0 天 02 小时”；每张卡显示自己的过期时间和剩余天数、小时数。

#### Scenario: 没有 5H

- **GIVEN** Keeper 只返回 Weekly。
- **WHEN** 生成消息。
- **THEN** 只展示 `Weekly:`，不出现 5H 空行或未知占位符。

#### Scenario: 套餐和重置卡未知

- **GIVEN** 套餐缺失或渠道不是 Codex，重置卡数量为 null。
- **WHEN** 生成消息。
- **THEN** 套餐显示“未知/未提供”，重置卡显示“未提供”；明确的 0 显示为 0 张。

### Requirement: 发送计划

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#发送计划；原文保留仅作历史参考。

每条通知 SHALL 支持固定间隔和日历定时两类计划。固定间隔 SHALL 可选秒、分、时、天；每月计划 SHALL 只能选择月初或月末；每年计划 SHALL 选择月份、日期和时间且不得配置年份间隔；发送时间 SHALL 支持秒级 TimePicker。

#### Scenario: 三类计划

- **GIVEN** 管理员在周期 Select 中选择每隔、每月、每年。
- **WHEN** 切换周期。
- **THEN** 每隔显示单位和值，每月只显示月初/月末，每年显示月份、日期和时间；字段不混用。

#### Scenario: 无效日历日期

- **GIVEN** 月末或 2 月 29 日规则遇到不存在的日期。
- **WHEN** 计算下一次触发。
- **THEN** 月末按月末触发；2 月 29 日在非闰年跳过并记录下一次有效时间，不静默改成其他日期。

### Requirement: 平台身份配置

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#平台身份配置；原文保留仅作历史参考。

每条通知 SHALL 在其编辑抽屉内提供“通知模板”和“平台身份配置”两个外层 Tab，平台身份配置内 SHALL 有企业微信、飞书、钉钉三个 Tab。每个平台 SHALL 独立保存 Webhook、用户唯一 ID、签名密钥和启用状态，其中签名密钥按平台支持情况保存（企业微信群机器人无独立加签，其面板 SHALL 明确说明“此平台无签名密钥”而不渲染该字段）；页面 SHALL 不提供目标群字段。平台身份配置打开时 SHALL 默认选中第一个平台 Tab（企业微信）；Webhook 地址、用户唯一 ID、签名密钥字段 SHALL 在输入框左侧显示常显标题，不得仅以占位符说明字段含义（2026-09-14 确认，Key 通知抽屉与全局默认通知面板共用同一编辑器、同样适用）。全局通知的平台身份 SHALL 提供「@所有人」开关，开启后该平台可免填用户唯一 ID；Key 级通知 SHALL 不提供该开关，保存时 SHALL 剥离 at_all，启用平台必须填写用户唯一 ID（2026-09-14 确认）。编辑抽屉保存校验失败时 SHALL 在出错字段行内展示原因，并以 Toast 提示第一条错误、切到对应 Tab；保存键 SHALL 保持可点击，不得静默禁用（2026-09-14 确认）。全局通知模板 Tab SHALL 展示可操作的发送计划编辑器（不跟随全局）；发送计划区块 SHALL 有常显分组标题。

#### Scenario: 平台配置隔离

- **GIVEN** 企业微信、飞书、钉钉分别填写了不同的 Webhook 和唯一 ID。
- **WHEN** 切换内嵌平台 Tab 或发送通知。
- **THEN** 每个平台只使用自己的配置，不读取其他平台的 ID 或密钥。

#### Scenario: 缺少平台身份

- **GIVEN** 通知启用了飞书但没有飞书用户唯一 ID。
- **WHEN** 保存或发送。
- **THEN** 管理界面在「用户唯一 ID」字段行内提示缺少字段，并以 Toast 弹出该错误；发送任务不伪造 @ 或成功状态。

#### Scenario: @所有人仅全局通知

- **GIVEN** 分别打开全局通知抽屉与 Key 级通知抽屉的平台身份配置。
- **WHEN** 查看企业微信/飞书/钉钉面板。
- **THEN** 仅全局通知渲染「@所有人」开关；Key 级不渲染该开关。保存 Key 绑定时后端剥离 at_all，仅有 at_all、无用户唯一 ID 的 Key 级平台配置被拒绝。

#### Scenario: 抽屉保存即落库

- **GIVEN** 管理员在全局或已保存 Key 的通知抽屉内改完配置。
- **WHEN** 点击抽屉「保存」。
- **THEN** 全局立即 PUT settings 落库；Key 级基于已保存绑定 postKey 仅落 notifications。此后行内「测试发送」命中已保存实体，不再报「没有已启用的平台」或「通知不存在」。

#### Scenario: 未落库通知不能测试发送

- **GIVEN** Key 绑定尚未保存，或某条通知仍只在编辑草稿中。
- **WHEN** 查看该行「测试发送」。
- **THEN** 按钮禁用并提示需先保存 Key 配置 / 先在抽屉保存。

#### Scenario: 默认页签与字段标题

- **GIVEN** 打开任一平台身份配置（Key 通知编辑抽屉或全局默认通知面板）。
- **WHEN** 未手动切换平台 Tab。
- **THEN** 默认选中企业微信 Tab；各平台面板的 Webhook 地址、用户唯一 ID、签名密钥（如平台支持）输入框左侧显示常显字段标题，不随占位符消失。

### Requirement: 数据采集与历史存档

后台 SHALL 使用 Keeper 现有统计、身份和配额接口；套餐与窗口采集 SHALL 每轮先触发 Keeper 配额刷新（60 秒节流）再读取配额缓存，并在有限预算内轮询等待刷新落盘；缓存仍无数据且已配置 CPA 管理端配额接口（cpa_management_url / cpa_management_key_env）时，SHALL 以同等节流降级查询 CPA 管理端配额接口。每次存档 SHALL 记录 Key 指纹、认证索引、渠道、统计周期、Token、金额、金额可用性、套餐、窗口、重置卡、查询时间和覆盖状态。首次启用 SHALL 回填 Keeper 可查询范围，并对超出范围标注缺失。

#### Scenario: 认证索引精确关联

- **GIVEN** CPA 目录同时返回宿主认证 ID 和 auth_index，Keeper 身份以 auth_index 关联。
- **WHEN** 采集渠道数据。
- **THEN** 使用明确的 auth_index 桥接，不用名称、模型名或宿主 ID 猜测身份。

#### Scenario: 套餐与窗口的刷新优先与降级采集

- **GIVEN** 通知未配置任何窗口模块，或 Keeper 配额缓存没有已完成条目。
- **WHEN** 采集套餐与窗口数据。
- **THEN** 未配置窗口模块的通知仍获取套餐（窗口模块不再是套餐采集的前置条件）；采集顺序为触发刷新 → 读缓存并限时轮询 →（已配置时）CPA 管理端配额接口；全部来源无数据时套餐保持「未知/未提供」，纯统计模块通知照常发送，不因套餐缺失而失败。

#### Scenario: Keeper 失败

- **GIVEN** Keeper 返回认证失败、限流、超时或字段缺失。
- **WHEN** 采集或刷新。
- **THEN** 任务记录受控错误码和未知字段，已有存档不被清空，通知正文不把失败转成零值。

### Requirement: 投递、限流与恢复

> **Superseded (2026-09-15)** — by .spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md#投递限流与恢复；原文保留仅作历史参考。

通知 SHALL 在发送前持久化任务状态，发送后持久化平台受理结果；同 Key、同平台目标、同统计周期的待发任务 SHALL 合并并保留最新值。已发成功任务 SHALL 不自动重发；已开始但无确认结果的任务 SHALL 恢复为 unknown。

#### Scenario: 限流合并

- **GIVEN** 同一 Key 的同一日报通知在同一群已有多个待发版本。
- **WHEN** 平台限流或网络延迟。
- **THEN** 只保留最新版本，记录合并次数和延迟；不同统计周期的任务不合并。

#### Scenario: 重启恢复

- **GIVEN** 插件在任务处于 pending 或 sending 时重启。
- **WHEN** 新通知服务启动。
- **THEN** pending 任务按计划恢复；sending 且结果不明的任务标记 unknown，不盲目重发。

#### Scenario: 平台返回业务失败

- **GIVEN** HTTP 请求成功但平台返回错误码，或平台签名校验失败。
- **WHEN** 处理发送结果。
- **THEN** 记录平台错误，不显示为平台已受理；符合重试条件的任务退避，其他任务等待人工重试。

### Requirement: 生命周期与安全

通知服务 SHALL 在 register 时启动、reconfigure 时停止旧实例并启动新实例、shutdown 时取消并等待后台任务退出；通知网络 IO SHALL 不持有 state 锁、推理锁或 bbolt 事务。敏感配置 SHALL 只在受控存储中保存，管理响应、模板变量和投递日志 SHALL 脱敏。

#### Scenario: 热重配

- **GIVEN** 服务正在采集或发送，管理员重配 state 或 Keeper 地址。
- **WHEN** 新配置生效。
- **THEN** 旧实例取消在途请求，迟到结果不能写入新配置；新实例使用新配置启动。

#### Scenario: 服务初始化失败

- **GIVEN** 通知运行库、Keeper 连接或 CPA 管理凭据不可用。
- **WHEN** 插件注册或重配。
- **THEN** 通知状态显示受控错误，已有模型路由和 Key 规则能力不受影响。

## 方案设计

### 架构与组件

- `notification_service.go`：服务生命周期、计划计算、任务调度、并发控制和版本检查。
- `notification_store.go`：bbolt 配置外的统计快照、覆盖区间、任务和投递记录；事务更新 pending/sending/terminal 状态。
- `notification_keeper.go`：复用现有 Keeper 客户端，调用 Overview/Analysis、身份、quota cache 和 reset-credits 查询，做 auth_index 映射。
- `notification_template.go`：白名单变量、模块生成、Token 缩写、金额/未知状态和平台安全转义。
- `notification_platform.go` 及三个平台适配器：构造 text/markdown 请求、签名、@ 结构、字节限制、限流和受控错误。
- `management.go` 和 `web/src/panels/KeysPanel.tsx`：管理 API、Key 编辑页、通知列表和两个层级的 Tab。

### 数据流

管理员保存全局或 Key 配置后，服务创建配置版本。调度器在固定间隔或日历触发时读取版本，先取得当前 Key 渠道目录和 Keeper 统计，再从存档补齐可查询窗口之外的日期，计算同周期渠道总量和 Key 占比，生成通知文本，按平台拆分并写入任务。发送器按目标平台限流提交，记录平台受理结果；重配、关闭、删除和重启都通过版本与持久任务状态协调。

运行库的逻辑分区为 config-revision、snapshots、coverage、jobs、deliveries。快照不保存完整 Key、Webhook、签名密钥、代理地址或原始响应，只保存 Key 指纹、认证索引、展示投影、统计数值和来源时间。

### 关键接口

接口均相对现有管理路径，并沿用 CPA 管理鉴权：

- `GET/PUT /notifications/settings`：全局默认通知实体（名称、模板模块、发送计划、平台身份）和服务开关。
- 现有 Key 保存接口的 `notification[]`：该 Key 的多条通知及其覆盖配置。
- `GET /notifications/status`：服务、缓存、历史覆盖、待发任务和错误状态。
- `POST /notifications/preview`：提交草稿并返回最终平台文字、模块、字节数和缺失提示，不发送。
- `POST /notifications/test-send`：显式发送当前保存的某条通知。未落库的草稿不得作为发送对象。
- `GET /notifications/deliveries`：按 Key 指纹、通知 ID、平台、周期和状态查询记录。
- `POST /notifications/deliveries/:id/retry`：管理员显式重试指定记录。

平台配置响应明文返回已保存的 Webhook 与签名密钥（2026-09-13 确认管理界面不做脱敏回显）；完整 API Key 仍不返回。预览与发送都使用保存后的配置版本；平台实际受理只表示平台接口返回成功，不表示群成员已读或 @ 一定生效。

### 错误处理

Keeper 认证失败、超时、限流、字段缺失、历史覆盖不足、平台签名失败、平台业务错误、平台限流、Webhook 超时和文件事务错误均使用稳定错误码。已知数据保留，未知数据明确标注；不把空列表、零值、旧缓存或模型自报状态解释为成功。

## 测试与验收策略

### 测试落点声明

公共测试落点为现有管理分发入口 `dispatchManagement`/`handleManagement`、真实临时 state 与 bbolt 文件、register/reconfigure/shutdown 生命周期入口，以及 React Key 编辑交互。允许替换的边界仅为 Keeper/CPA/平台 HTTP 客户端、可控时钟和真实临时文件故障；不替换统计计算、模板渲染、继承、占比或去重逻辑。模块顺序调整以启用模块序列的数据断言为准，不在 jsdom 中模拟原生拖拽事件；dnd-kit 键盘传感器交互仅作可选 UI 冒烟。

| Scenario / 检查项 | Lane | 执行方式 | 验收证据 |
|---|---|---|---|
| 多通知隔离、全局回退、模块开关、通知名称标题 | fast | 管理 API + React 行为测试 | 配置与生成文字断言 |
| Key 渠道占比、零分母、金额可用性、K/M/B 缩写 | fast | Keeper httptest 固定响应 | 原始整数、百分比和未知状态断言 |
| 5H/Weekly 缺失、重置时间倒计时、套餐和重置卡 | fast | quota/identity fixtures + 可控时钟 | 窗口行显示与剩余时间断言 |
| 每隔/每月/每年字段切换和非法日期 | fast | 计划纯函数 + React 组件测试 | 下一次触发时间和 Select 状态 |
| 365/366 天、历史覆盖、Keeper 失败和 auth_index 桥接 | fast | httptest + 真实临时 bbolt | 覆盖区间、缺失标记和不清空旧值 |
| 重启、重配、限流合并、unknown 和人工重试 | fast | 真实临时 bbolt + 可控时钟/网络 | 事务状态、任务数量和投递记录 |
| 三平台请求格式、签名、字节限制和拆分 | fast | 三个平台 HTTP 替身 | 请求体、签名字段、错误码 |
| 管理页面桌面/移动布局与 Tab 联动 | final | 本地服务浏览器验收 | 页面记录和截图 |
| Go race/vet、前端全测/typecheck/build、发布目标编译 | final | 项目现有命令 | 命令、退出码和原始日志 |
| 真实三平台群消息和实际 @ | manual | 获得当轮授权后执行 | 平台消息截图/记录；未授权保持 manual-pending |

## 风险与边缘情况

- Keeper 聚合接口只保证最近 365 个自然日，上一自然年和闰年可能存在不可查询日期；覆盖状态必须进入正文和发送记录。
- Keeper 的 quota 窗口是认证渠道共享值；窗口 Token/金额可能为空或金额完整性未确认，不得换算为 Key 独享余量。
- Keeper 的 Analysis 渠道组成可能不包含已删除身份；无法关联的历史用量保留为未归属项，不删除 Key 总量。
- 飞书外部群只能使用 Open ID；钉钉官方群消息文档明确内部群 UserId @，配置不满足平台条件时必须显示受控失败。
- 机器人平台可能在 HTTP 成功后仍未实际 @；投递记录使用“平台已受理”，不写成“用户已收到”。
- 多个 CPA 实例不能共享同一个通知运行库并保证只发送一次；bbolt 文件锁冲突时通知服务应进入受控 unavailable 状态。
- Keeper 价格规则变化后，历史快照保留采集时 USD 折算值；实时和历史数据混合时标出采集时间。
- Key 删除、重命名、通知关闭和模板改版不回写历史快照；旧任务由配置版本检查取消或标记 superseded。
- 预览和测试发送不读取或显示完整 Webhook、签名密钥、Key、代理地址或原始 Keeper 响应。

## 开放问题

无阻塞性开放问题。bbolt 具体版本、管理 API 的字段命名、平台请求拆分上限和适配器文件组织在 writing-plans 阶段按现有代码与依赖版本锁定；不改变本 spec 的行为口径。
