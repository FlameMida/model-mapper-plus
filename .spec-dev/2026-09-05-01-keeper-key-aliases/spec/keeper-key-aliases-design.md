---
spec_dev:
  version: 1
  feature: keeper-key-aliases
  status: active
  covers:
    - "keeper_*.go"
    - "management_keeper_test.go"
    - "main.go"
    - "management.go"
    - "web/src/api.ts"
    - "web/src/keyOptions*"
    - "web/src/useKeyOptions*"
    - "web/src/components/ApiKeySelect*"
    - "web/src/panels/KeysPanel*"
    - "web/src/panels/PreviewPanel*"
    - "web/src/App.tsx"
    - ".github/workflows/build.yml"
  sync_commit: 56f57363473ffab77d1a1e8e2735257d0f7079b9
  supersedes: []
  superseded_by: null
---

# Keeper API Key 别名接入设计

用户已在会话中批准完整计划，并明确要求先记录计划再执行。本文固定该批准版本。来源为两边当前源码，不假定线上配置已就绪。

## R1 配置及认证

CPA `config.yaml` 的 `plugins.configs.model-mapper-plus` 增加 `usage_keeper_url`（默认空，关闭接入）和 `usage_keeper_password_env`（默认 `CPA_KEEPER_LOGIN_PASSWORD`）。两字段同时注册进插件 ConfigFields，可在 CPA 插件设置中编辑；不写入 state 文件。实际密码从 CPA 进程环境变量读取，不返回浏览器或写入日志。环境变量外部变更需重启 CPA。

URL 支持 http/https 及 APP_BASE_PATH，拒绝 userinfo、query、fragment。禁止跟随重定向。由插件后端访问 `<base>/api/v1/usage/api-keys/settings`。首次 401 时调用 `<base>/api/v1/auth/login`，请求 JSON 为 `{password: <环境变量值>}`，附 `X-CPA-Usage-Keeper-Request: fetch`；Cookie Jar 保存管理员会话，并最多重试一次读取。关闭认证的 Keeper 首次读取直接成功。整个读取、登录和重试共享 5 秒期限。

#### Scenario: 插件设置可编辑 Keeper 配置
设置地址/环境变量名后应用配置；管理接入生效，已有规则与绑定不变化。环境变量缺失且 Keeper 要求登录时显示配置错误。
#### Scenario: Keeper 会话复用和更新
首次读取 401 后登录成功；后续使用 Cookie；会话失效时只重新登录一次；密码错误和 429 不产生循环登录。

## R2 管理接口与失败隔离

管理前缀 `/v0/management/plugins/model-mapper-plus` 新增 GET `/keeper/key-aliases` 与 POST `/keeper/key-aliases/refresh`（无请求体）。响应使用 HTTP 200 表达接入状态，CPA 管理认证失败仍由宿主处理。返回 `Cache-Control: no-store`。

```ts
type KeeperAliasesResponse =
  | { status: 'ready'; items: Array<{key: string; alias: string}>; fetched_at: string }
  | { status: 'disabled'; items: [] }
  | { status: 'unavailable'; items: []; error_code: 'configuration_error' | 'authentication_failed' | 'rate_limited' | 'timeout' | 'invalid_response' | 'connection_failed'; retry_after_seconds?: number }
```

成功缓存 60 秒，手动刷新跳过成功缓存；并发刷新共享在途请求。失败返回空外部映射，不把过期值伪装成最新。认证失败冷却 60 秒，429 遵守 Retry-After（缺失/非法时 60 秒）；手动请求也遵守冷却。配置切换废弃会话、缓存和旧实例的在途结果。网络 IO 不持有规则/配置状态锁，不进入推理路径。

上游响应为 items 数组，严格校验 apiKey/keyAlias 字符串；同一 Key 冲突别名判定无效响应。同值重复可去重。原始 Key 精确关联，别名 trim，空别名保留为匹配记录。管理层沿用宿主 HTML 转义的既有对称解码。

#### Scenario: Keeper 故障隔离
超时、认证失败、无效响应均只改变接入状态；插件不退出 CPA 登录，规则保存、Key 列表和模型处理正常。
#### Scenario: 缓存刷新与配置切换
60 秒内 GET 复用缓存；手动 POST 读取最新；并发合并；旧配置迟到响应不可污染新配置。

## R3 选择器增强

CPA GET `/v0/management/api-keys` 决定下拉成员。两页统一显示优先级为已保存插件 alias > Keeper alias > 脱敏 Key。存在别名时显示 `别名 · 脱敏 Key`。搜索包含两边别名和 Key，但 value 始终为完整 Key。保留绑定页 allowCreate、试跑页可清空选择；Keeper 独有 Key 不加入列表。CPA 与 Keeper 独立加载，外部失败不清空 CPA 列表。两页共享映射，退出 CPA 登录清理内存数据。绑定列表别名列只显示已保存 alias。

#### Scenario: 选择器优先级及搜索
三种优先级均正确；同名别名/相同掩码仍为独立完整 Key；被本地别名覆盖的 Keeper 别名仍可搜索；中文和 HTML 字符准确展示。

## R4 编辑框同步

别名输入框旁增加“从 Keeper 同步”。未选择 Key/未配置时禁用并说明原因。点击强制获取最新，匹配当前完整 Key 的非空别名，填入 editing.alias；已有手填值可以主动覆盖。填入后可继续手动修改，只有保存绑定才调用原有 postKey 链路并持久化 alias。取消编辑不保存。无匹配、空别名、读取失败均保留当前输入并提示具体原因。

同步时记录编辑会话 ID、Key 和别名编辑版本；切换 Key（包括 A→B→A）、手改别名、关闭重开弹窗使旧响应无效。函数式更新只改 alias，保留同期规则/渠道/开关变化。防止重复同步。

#### Scenario: 同步填入与保存
同步能覆盖旧 alias；同步本身不保存；之后手改并保存回显最终值；刷新/重启 CPA 后仍存在。
#### Scenario: 同步失败及竞态
空 alias、无匹配、错误均保留输入；迟到请求不能串 Key、覆盖新编辑或修改重新打开的弹窗。

## R5 交付与验证

增加前端 typecheck/test CI 步骤；更新 README/CLAUDE 中配置位置、环境变量、同步/保存区别和故障排查。构建更新 tracked `web/dist/index.html`。

| Scenario / 检查项 | Lane | 执行方式 | 预期 |
|---|---|---|---|
| 选择器优先级、搜索、组件同步及竞态 | fast | 任务内 TDD | 全通过，mock 网络 |
| Keeper HTTP 认证、缓存、管理接口、持久化 | PR | 任务内 TDD | httptest/临时 state 验证通过 |
| Go race、vet、TS、完整测试、UI 构建 | PR | 验收任务 | 全通过 |
| Linux amd64 .so + 真实 CPA/Keeper | 手动交付 | 验收任务 | 认证、两页、同步保存/取消、重启持久化、故障隔离 |
| 浅色/深色、中文/长别名、现有规则与渠道/Fast | 手动交付 | 验收任务 | 正常可用，留证据 |

## 取代与共存

扩展管理 UI 展示及配置；原规则、Key 绑定状态格式、保存链路、路由和 Scheduler 契约保持。复用 Keeper 已有接口，其仓库只读。
