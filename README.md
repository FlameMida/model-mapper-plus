# CPA Model Mapper Plus Plugin

`model-mapper-plus` is a CLIProxyAPI (CPA) native plugin. It maps text-generation request model names before CPA selects the upstream execution path, then restores supported response model fields back to the client-requested model only when a mapping matched or a case operation executed and the final model differs from the original.

When CPA invokes the plugin executor callbacks, the same mapping applies across non-streaming HTTP responses, SSE streams, and WebSocket-backed CPA streams that arrive as raw JSON chunks through the existing stream bridge.

The plugin registers `management.register`/`management.handle`: a resource route serves the embedded admin UI (unauthenticated static page), and data routes under `/v0/management/plugins/model-mapper-plus/` manage rules, key bindings, and dry-run previews after CPA-side management authentication.

## Configuration

```yaml
plugins:
  enabled: true
  configs:
    model-mapper-plus:
      enabled: true
      priority: 1
      global_rules: ""
      claude_messages_rules: ""
      codex_responses_rules: ""
      openai_completions_rules: ""
      state_file: ""  # optional, defaults to model-mapper-plus-state.json
      usage_keeper_url: ""  # optional, Keeper base URL including its deployment subpath
      usage_keeper_password_env: "CPA_KEEPER_LOGIN_PASSWORD"
```

The plugin's own `enabled` field defaults to `true`. Empty rule fields mean the request is skipped and CPA behaves normally.

`enabled`, `state_file`, `usage_keeper_url`, and `usage_keeper_password_env` are declared to CPA (`Metadata.ConfigFields`) and can be edited on the CPA plugin settings page. The four `*_rules` keys above are still read from YAML as the first-run seed — they are just managed in the plugin's own admin page instead of being duplicated in CPA's UI.

## Web admin UI

The plugin serves an admin page at `http://<cpa-host>:<api-port>/v0/resource/plugins/model-mapper-plus/index.html`, signed in with the CPA management key. It has three panels:

- **Rules**: edit the global and per-endpoint ordered rule entries (including `\a`/`\A` case operations).
- **Key bindings**: attach an extra rule set to a specific client API key, chained after the top-level rules for that key's requests.
- **Preview**: dry-run a (key, endpoint, model) triple and inspect the M→M₁→M₂ rewrite steps.

After the first save, rules and key bindings live in `state_file`. When unset, the default basename is `model-mapper-plus-state.json`, resolved against the CPA process working directory (same as key-policy; the plugin cannot read CPA's `plugins.dir`). Parent dirs are created as needed (mode 0700); the file is mode 0600. Once present, the state file is the single source of truth and the YAML rule fields no longer take effect (`enabled` still comes from YAML only). Delete the state file to fall back to YAML configuration. Set an explicit absolute `state_file` in config when you want a specific directory (e.g. a writable volume). The admin UI follows the CPA panel light/dark theme when embedded.

If an existing state file cannot be parsed or fails validation, it is renamed to `<state_file>.corrupt` and the plugin falls back to the YAML seed. The reason is logged and returned as `load_error` from `GET .../state`, and the admin UI shows a banner — the quarantined file keeps its key bindings recoverable instead of being overwritten by the next save.

## Keeper API Key 别名

可在 **CPA 插件设置页**编辑 `usage_keeper_url` 和 `usage_keeper_password_env`，它们保存到 CPA 的 `config.yaml` 中 `plugins.configs.model-mapper-plus` 下，**不写入 state 文件**。

```yaml
plugins:
  configs:
    model-mapper-plus:
      usage_keeper_url: "http://cpa-usage-keeper:8080"
      usage_keeper_password_env: "CPA_KEEPER_LOGIN_PASSWORD"
```

`usage_keeper_url` 留空关闭接入；地址必须能从 CPA 进程/容器内访问。Keeper 部署在子路径时，将前缀包含在地址中，例如 `https://keeper.example.com/cpa`。Docker 中可用同一网络的 Keeper 服务名；容器内的 `localhost` 指向容器自身。

实际密码通过 **CPA 进程环境变量**提供。以 Compose 为例，在 CPA 服务的 `environment` 中合并：

```yaml
environment:
  CPA_KEEPER_LOGIN_PASSWORD: "${CPA_KEEPER_LOGIN_PASSWORD}"
```

环境变量值应为 Keeper 的管理员登录密码（不是 CPA management key 或客户端 API Key）。变量变更后重启 CPA。插件通过 Keeper 现有登录接口建立内存 Cookie 会话；密码和 Cookie 不返回插件页面。Keeper 关闭认证时读取接口无需密码。

- Key 绑定和规则试跑使用相同显示规则：**已保存插件别名 → Keeper 别名 → 脱敏 Key**；有别名时显示“别名 · 脱敏 Key”。搜索支持两边别名和 Key，选择值始终为完整 Key。
- 下拉成员来自 CPA；Keeper 只补充别名。绑定页仍可手动输入未列出的 Key。
- **刷新别名**更新展示数据，不修改绑定。成功结果缓存 60 秒；手动刷新绕过成功缓存。
- **从 Keeper 同步**强制读取当前 Key 的最新非空别名并替换编辑框内容，可继续手改；只有点击编辑弹窗的 **确定** 保存 才写入 `KeyBinding.alias`。取消编辑会放弃该草稿。绑定列表的别名列显示已保存值。
- Keeper 没有对应 Key、别名为空或读取失败时保留输入并提示原因；等待期间改 Key、改别名或关闭编辑窗会丢弃旧同步结果。
- Keeper 超时/认证失败不会退出 CPA 登录，也不影响规则和 Key 选择。一次读取最多 5 秒；认证失败冷却 60 秒，限流遵守 Keeper 的 `Retry-After`。配置切换会清理接入会话及缓存。

排查时先看选择器下方状态：配置错误检查 URL 和 CPA 环境变量；认证失败检查 Keeper 登录密码；无对应 Key 检查 Keeper 是否已同步同一 CPA 的当前 Key。不要用两边的脱敏 Key 进行人工自动匹配。关闭接入不会删除已经保存的本地别名。

## Key bindings and thinking-effort control

A key binding runs one more structurally identical rule set on top of the top-level output for requests carrying that client key (endpoint segment wins; the binding's global segment is the fallback). Thinking effort is expressed directly through model-name suffixes, for example `claude-opus-4-5(max)=>claude-opus-4-5(high)` — CPA resolves the suffix and it overrides effort fields in the request body. For Codex Responses requests whose model has no explicit suffix, `reasoning.effort` also participates in suffix matching: a request for `gpt-5.6-sol` with `reasoning.effort=xhigh` matches `gpt-5.6-sol(xhigh)=>gpt-5.6-sol(medium)`. An explicit model suffix wins; when the effort-qualified form does not match, the plugin retries the bare model so existing mappings keep working. Note that `*` captures swallow the suffix too (`claude-*` captures `opus-4-5(max)`).

### 渠道定向与 Fast 控制

渠道目录同时展示认证文件与 Gemini、Interactions、Claude、Codex、xAI、OpenAI Compatibility、Vertex 配置凭据，支持按名称、地址和 ID 搜索。AI Providers 显示脱敏 Key、地址及「已配置」状态；是否可用于当前请求仍由 CPA 判断。自定义供应商显示名称，整选保存内部 provider 标识。

前端直读宿主管理接口，再调用本插件 `POST /channel-credentials` 计算 ID；原文配置不写入插件状态。算法已与本地 CPA `d1a024e` 的真实管理响应及凭据生成结果验证，宿主修改 ID/provider 算法后需同步适配。禁用的 OpenAI Compatibility 供应商不生成运行时凭据，因此不提供勾选项；配置变更造成旧 ID 缺失时保留原选择，不自动转绑。目录读取失败会提示重试并保留勾选，旧版不存在的 Interactions/xAI 接口除外。

每条 key 绑定可独立开启渠道定向：供应商整选与凭据单选取并集，认证文件和 AI Providers 配置凭据统一按勾选控制，池内按凭据 ID 确定性轮转。定向开启后该 key 跳过模型映射；候选池是 CPA 交给 Scheduler 的当前 Candidates 与所选集合的交集，过滤为空时返回 HTTP 503 且不会降级到池外凭据。OpenAI/Codex 错误体使用 `error.code=auth_not_found`，Claude `/v1/messages` 错误体使用 `error.type=auth_not_found`。

CPA 会在 Scheduler 前过滤 cooldown 与全局较低优先级凭据。目标凭据因此缺席、但池外仍有 active 候选时，本插件过滤池外候选并返回 503；只有 CPA 全局无任何候选时，宿主才可能在插件前返回原生 429 `model_cooldown` 与 `Retry-After`。插件不会模拟该 429 分支。

“Fast 允许”默认开启。关闭后，Claude 请求的 `speed:"fast"` 与 `fast-mode-2026-02-01` beta token 会在上游执行前被删除；其他协议不受影响。

CPA 的 Scheduler 能力为宿主全局单实例。若同时启用另一个声明 Scheduler 的插件（例如 `cpa-plugin-key-policy`），只有宿主选择的首个 Scheduler 生效；本插件不提供冲突探测，请在部署配置中只保留一个 Scheduler 插件。

## Rule syntax

Each ruleset is a `;`-separated ordered list of entries. An entry is either a `find=>replace` mapping or an exact standalone case operation: `\a` lowercases ASCII English letters and `\A` uppercases them. Whitespace and quotes are invalid inside the decoded rule value.

- Mappings remain case-sensitive and apply to the complete current model name; later mappings see the value produced by every earlier entry.
- `\a` changes only `A` through `Z` to `a` through `z`; `\A` changes only `a` through `z` to `A` through `Z`. Non-ASCII bytes, digits, punctuation, and separators are unchanged.
- Case operations must be complete standalone entries. They are not additional backslash escapes for `find` or `replace`.
- In `find`, `*` captures zero or more characters, including `/`, and captures are numbered from left to right. Each capture stops at the first occurrence of the next literal, and backtracks to a later occurrence when the rest of the pattern does not fit — `*-pro` matches `vendor-pro-pro`, capturing `vendor-pro`. Two adjacent `*` are allowed but degenerate: the first takes everything up to the next literal and the second captures the empty string. `$` is literal.
- In `replace`, `$1`, `$2`, and later numbers reuse captures. `*` is literal.
- Characters such as `@`, `/`, `[`, `]`, parentheses, dots, hyphens, and underscores are literal and need no escaping.
- Entries are order-sensitive: the selected ruleset runs left to right exactly once, and later entries see the model produced by earlier entries.
- Put more specific wildcard rules before broader fallback rules.
- `\` escapes `*`, `;`, `$`, `\`, or `=>` on both sides of a mapping; escaping `$` in `find` is accepted but unnecessary.
- An entry whose captures would collapse the model name to the empty string is skipped, leaving the previous value in place.

YAML may single-quote the whole rule value; single quotes preserve backslashes, so `\a` and `\A` survive as case operations. Do **not** use double quotes for values containing backslashes — YAML reads `"\a"` as the BEL control character, and the plugin rejects it with a message pointing back here. Quote characters inside the decoded value remain invalid:

```yaml
global_rules: '@cf/zai-org/glm-4.7-flash=>glm-4.7-flash;deepseek-v4-pro[1m]=>deepseek-v4-pro'
```

Endpoint-specific rules override `global_rules` and do not stack with it:

- `claude` uses `claude_messages_rules` when non-empty.
- `openai-response` uses `codex_responses_rules` when non-empty.
- `openai` uses `openai_completions_rules` when non-empty.
- Other formats use `global_rules`.

### Examples

Claude-family fallback rules, useful in `claude_messages_rules`:

```text
claude-haiku-*=>gpt-5.4-mini;claude-sonnet-*=>gpt-5.4;claude-*=>gpt-5.5
```

Effects:

- `claude-haiku-4.5` -> `gpt-5.4-mini`
- `claude-sonnet-5` -> `gpt-5.4`
- `claude-opus-4` -> `gpt-5.5`

Compact OpenAI alias removal, useful in `codex_responses_rules` or `openai_completions_rules`:

```text
gpt-*-openai-compact=>gpt-$1
```

Effects:

- `gpt-5.5-openai-compact` -> `gpt-5.5`
- `gpt-5.4-mini-openai-compact` -> `gpt-5.4-mini`

Chained mapping runs in the written order:

```text
deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini
```

Effects:

- `deepseek-v4-pro` -> `gpt-5.4-mini`
- `deepseek-v4-flash` -> `gpt-5.4-mini`

Reversing those rules changes the result because rules do not loop back:

```text
deepseek-v4-flash=>gpt-5.4-mini;deepseek-v4-pro=>deepseek-v4-flash
```

Effects:

- `deepseek-v4-pro` -> `deepseek-v4-flash`
- `deepseek-v4-flash` -> `gpt-5.4-mini`

Ordered ASCII case operations can normalize an incoming alias, feed a case-sensitive mapping, transform its output, and continue mapping:

```text
\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A
```

For `GPT-X`, the values are processed as:

```text
GPT-X -> gpt-x -> deepseek-V3 -> DEEPSEEK-V3 -> gpt-5.5 -> GPT-5.5
```

Use YAML single quotes so the DSL backslashes are preserved:

```yaml
global_rules: '\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A'
```

## Common use cases

- Use GPT or other upstream models from Claude-compatible clients, such as Claude Code, without changing the client-requested Claude model names.
- Keep client configuration stable while moving execution to newer, cheaper, or provider-specific model names.
- Expose compact or local aliases to clients, then strip the alias suffix before upstream execution.
- Chain temporary migrations, for example routing an old provider model name through an intermediate alias before its final upstream model.

## Build

```powershell
make test
make vet
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC=<cross-compiler>
make package VERSION=0.1.0
```

Full-platform release builds run in GitHub Actions for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`
- `freebsd/amd64`

Local artifacts commonly used for smoke checks:

- `dist/windows_amd64/model-mapper-plus.dll`
- `dist/linux_amd64/model-mapper-plus.so`

## Deploy

Windows CPA:

```text
<CPA directory>/plugins/windows/amd64/model-mapper-plus.dll
```

Linux amd64 CPA:

```text
<CPA directory>/plugins/linux/amd64/model-mapper-plus.so
```

## Smoke test

Live smoke runs against an **already running CPA** (e.g. your docker-compose instance with the plugin loaded). It injects each case's rules via the plugin's own management API (`PUT /v0/management/plugins/model-mapper-plus/rules`) and asserts the rewrite result on `/v1/chat/completions`.

Required environment variables:

- `CPA_SMOKE_MGMT_KEY` — CPA management key (`remote-management.secret-key`), used for `PUT /rules`
- `CPA_SMOKE_CLIENT_KEY` — a valid client api-key for `/v1/chat/completions`

Optional (defaults shown):

- `CPA_SMOKE_BASE_URL` defaults to `http://127.0.0.1:8317`
- `CPA_SMOKE_WRONG_KEY` defaults to `wrong-local-smoke-key`
- `CPA_SMOKE_MODEL_PASSTHROUGH` defaults to `deepseek-v4-flash` — a model your upstream actually serves
- `CPA_SMOKE_MODEL_CHAIN_SRC` / `_CHAIN_MID` / `_CHAIN_DST` default to `deepseek-v4-pro` / `deepseek-v4-flash` / `gpt-5.4-mini` — set these to models your docker CPA can serve so the success cases pass

Run (no `.test-cpa/` state is created anymore):

```bash
CPA_SMOKE_MGMT_KEY=... CPA_SMOKE_CLIENT_KEY=... make smoke-local
```

Or persist them: copy `.env.example` to `.env`, fill in the keys (and override the `CPA_SMOKE_MODEL_*` defaults to models your upstream serves), then just `make smoke-local` — it auto-loads `.env`. `.env` is gitignored.

Open the admin UI at `http://127.0.0.1:8317/v0/resource/plugins/model-mapper-plus/index.html` (sign in with the management key).

## License

The Unlicense.

## Model list modification

Model-list modification is not implemented in this release. See `docs/model-list-modification-plan.md` for the required future CPA host hook.
