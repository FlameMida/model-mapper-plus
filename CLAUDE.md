# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

- Run all unit tests: `make test` or `go test ./...`
- Build the admin UI into `web/dist/index.html` (requires npm): `make web-build`
- Build/package targets force `web-build` first so the embedded UI is always fresh; multi-platform `make build` runs `web-build` once then compiles each OS/arch.
- Run vet: `make vet` or `go vet ./...`
- Run one plugin test: `go test . -run TestName`
- Run release packager tests: `go test .github/scripts/package-release.go .github/scripts/package-release_test.go`
- Build Windows amd64 plugin: `make build-windows-amd64`
- Build Linux amd64 plugin from Windows with Zig: `make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"`
- Build/package one platform: `make package VERSION=0.1.2 GOOS=windows GOARCH=amd64`
- Package already-built artifacts into `dist/release/`: `make package VERSION=0.1.2`
- Run live local smoke (against a running CPA): set `CPA_SMOKE_MGMT_KEY` and `CPA_SMOKE_CLIENT_KEY`, then `make smoke-local`
- Dev loop on a mac host with a docker CPA: `make dev-so` cross-compiles a linux/amd64 `.so` (via `zig`, `brew install zig`) with a fresh UI and copies it to `CPA_PLUGINS_DIR` (default `/Users/flame/CLIProxyAPI/plugins`); `make dev-ui` runs the vite dev server on :5173 proxying `/v0/management` to `CPA_HOST` (default `http://127.0.0.1:8317`). Override `ZIG`, `CPA_PLUGINS_DIR`, `CPA_HOST` as needed.
- Clean build output: `make clean`

Do not run `go test ./.github/scripts`; that directory contains multiple `package main` scripts and will collide on duplicate `main`/`run` symbols. Test script files explicitly as shown above.

## Local development & debugging

Target setup: **mac host + docker CPA (linux/amd64)**, the CPA `plugins` dir bind-mounted to `CPA_PLUGINS_DIR` (default `/Users/flame/CLIProxyAPI/plugins`). Host toolchain: `go`, `npm`, and `zig` (`brew install zig`) for cgo cross-compilation.

Prerequisites on the CPA side (its `config.yaml`): `plugins.enabled: true`, a `plugins.configs.model-mapper-plus` block, a `remote-management.secret-key` (for the admin UI + smoke), and at least one entry in `api-keys` (client key for chat requests).

### Edit → verify loop

| You changed | Run | Then |
|---|---|---|
| Go code | `make dev-so` | `docker compose restart <cpa>` (or let CPA hot-reload) |
| Frontend only | `make dev-ui` (vite on :5173) | before shipping, `make dev-so` to bake the new UI into the `.so` |
| Mapping behavior | `CPA_SMOKE_MGMT_KEY=… CPA_SMOKE_CLIENT_KEY=… make smoke-local` | reads pass/fail per case |

`make dev-so` = `web-build` (fresh `web/dist/index.html`) → zig cross-compile `linux/amd64` `.so` → `cp` into `CPA_PLUGINS_DIR/linux/amd64/`.

`make dev-ui` = vite dev server; `/v0/management` is proxied to `CPA_HOST` (default `http://127.0.0.1:8317`), so the SPA talks to the running CPA.

### Admin UI & key endpoints

- Admin page: `http://<cpa-host>:<port>/v0/resource/plugins/model-mapper-plus/index.html` (sign in with the management key; resource route is unauthenticated, data API is CPA-authenticated)
- `GET /v0/management/plugins/model-mapper-plus/state` returns the resolved absolute `state_file` path — check it when saves don't seem to persist
- Rules injection (what smoke uses): `PUT /v0/management/plugins/model-mapper-plus/rules` with `{global,claude,codex,openai}`

### Tunable variables

- Build/deploy: `ZIG` (default `zig`), `CPA_PLUGINS_DIR`, `CPA_HOST`
- Smoke: `CPA_SMOKE_MGMT_KEY`, `CPA_SMOKE_CLIENT_KEY`, `CPA_SMOKE_BASE_URL` (default `http://127.0.0.1:8317`), `CPA_SMOKE_WRONG_KEY`, and `CPA_SMOKE_MODEL_PASSTHROUGH` / `_CHAIN_SRC` / `_CHAIN_MID` / `_CHAIN_DST` (defaults `deepseek-v4-flash` / `deepseek-v4-pro` / `deepseek-v4-flash` / `gpt-5.4-mini` — set to models your upstream actually serves)

### Gotchas

- `state_file` defaults to `model-mapper-plus-state.json` resolved against the CPA process working directory (same as key-policy; the plugin cannot read `plugins.dir`). In docker that is usually `/app/`. If cwd is read-only, set an explicit writable absolute `state_file` in the plugin config.
- Success-case smoke (`openai-dedicated-chain`, `streaming`) requires the chain-end model to be actually servable by your upstream; override `CPA_SMOKE_MODEL_*` to match.
- `make dev-ui` serves the real React app but the `.so` still embeds the **last built** UI — re-run `make dev-so` before relying on the embedded page.

## Architecture overview

This is a single-package Go `c-shared` CLIProxyAPI native plugin. `abi_cgo.go` is the C ABI bridge: it exports `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, and `cliproxyPluginShutdown`, then forwards plugin method calls into `handleMethod` in `main.go`.

`main.go` contains the plugin logic:

- `pluginRegistration` advertises `model_router`, `executor`, `executor.execute_stream`, and `management_api` support for `openai`, `claude`, and `openai-response` formats.
- `decodeLifecycleConfig` and `decodeConfig` load plugin config from CPA lifecycle payloads. Readable config fields are `enabled`, `global_rules`, `claude_messages_rules`, `codex_responses_rules`, `openai_completions_rules`, `state_file`, `usage_keeper_url`, and `usage_keeper_password_env`. `enabled`, `state_file`, `usage_keeper_url`, and `usage_keeper_password_env` are *declared* in `Metadata.ConfigFields` — the rule segments are managed in the plugin's own admin page, not duplicated on CPA's plugin config page. Keep `& ' < > "` out of every `ConfigField` name/description: CPA runs `html.EscapeString` over plugin metadata, so those characters render as entity garbage and the plugin cannot turn the host escaping off.
- `state.go` implements the state_file persistence layer (ADR-0001: once `state_file` exists it is the source of truth for rules and key bindings; YAML rule fields are seed-only, `enabled` stays YAML-only). Writes are atomic (tmp+fsync+rename, mode 0600).
- `management.go` and `web_embed.go` implement `management.register`/`management.handle`: a resource route serves the embedded admin UI, and data routes manage rules, key bindings, and dry-run previews.
- `selectRules` selects an endpoint-specific ruleset when non-empty; otherwise it falls back to `global_rules`. Endpoint-specific rules do not stack with global rules. The same segment logic (`selectRulesFrom`) is shared by top-level and per-key rule sets.
- `parseRules` / `applyRules` implement an ordered entry DSL: entries are `find=>replace` mappings or exact standalone `\a` / `\A` ASCII case operations; `*` captures, `$1` references captures, and entries run left-to-right exactly once.
- `routeModel` chains two layers: the top-level rule set runs first, then the bound key's rule set runs on its output (ADR-0003). The client key comes from inbound `Authorization: Bearer`/`x-api-key` headers. Thinking effort is expressed via model-name suffixes inside ordinary rules (ADR-0002); no dedicated effort code path exists. Routing happens only when the final model differs from the original.
- `handleExecutorExecute` and `runStreamForward` rewrite the outbound request body to the upstream model, call CPA host execution callbacks, then restore selected response model fields to the client-requested model.
- `web/` is the React 19 + Vite 8 + Semi Design admin UI, built to a single inlined `web/dist/index.html` via `make web-build` and embedded with `go:embed` (the file is force-tracked; root `dist/` stays ignored).

Important model-rewrite invariants:

- Request rewriting intentionally changes only the top-level JSON `model` field.
- Response restoration is deliberately whitelisted to `model`, `modelVersion`, `response.model`, `response.modelVersion`, and `message.model`. Do not replace recursively through arbitrary content/tool text.
- Case operations change ASCII English letters only and do not make later mappings case-insensitive.
- Streaming responses pass through `streamChunkRewriter`, which handles complete SSE events, split SSE prefixes, unterminated SSE data at flush time, raw JSON chunks, line/space-delimited JSON values, and raw JSON that must be framed as SSE for Responses SSE clients.
- On host stream errors, pending rewritten bytes are flushed before closing the plugin stream so clients do not hang waiting for buffered output.

## Release and packaging

`pluginVersion` defaults to `0.0.0-dev` and is injected during release builds with `-X main.pluginVersion=$(VERSION)`. Keep `go.mod` module path and `pluginRegistration().Metadata.GitHubRepository` aligned with `github.com/FlameMida/cpa-model-mapper-plus`.

`.github/scripts/package-release.go` is the packaging boundary. It supports:

- single-platform mode with `-library`, `-archive`, and `-checksum`
- aggregate mode with `-version`, `-dist`, and `-out`

Release zip files are named `model-mapper-plus_<version>_<goos>_<goarch>.zip`, contain the dynamic library at zip root plus optional root `LICENSE`, and use sha256sum-format checksum lines with only the archive basename.

The GitHub Actions workflow runs tests/vet on PRs, builds all release platforms on non-PR events, and publishes only for `v*` tags. Global workflow permissions are `contents: read`; only the release job uses `contents: write`.

## Reference projects

两个本机参考仓库，改 SDK 契约、management/state/web UI 实现前应先查对：

- **`/Users/flame/cpa-plugin-key-policy`** — 同生态的 c-shared 插件。本插件的 web 管理界面（`management.register`/`management.handle` + `go:embed` 单文件 UI + session/panelAuth/themeSync）、`state_file` 原子写与 YAML-seed 真相源不变量、CPA management 路由分发模式，均参照其实现。
- **`/Users/flame/CLIProxyAPI`** — 宿主主程序。本插件依赖的 SDK（`sdk/pluginapi`、`sdk/pluginabi`）的契约、`ModelRouteRequest`/`ExecutorRequest`/`ManagementRequest` 的字段填充、`plugins.dir` 解析（`internal/config/plugin_path.go`）、`ServeManagementHTTP` 转发路径与 Windows shadow 拷贝（`internal/pluginhost/loader_windows.go`）都在这里。改 ABI/路由/host 交互前必读。

## Local state and documentation

Live smoke creates ignored local state under `.test-cpa/`; builds create ignored artifacts under `dist/`. Do not treat either directory as source.

`docs/solutions/` stores documented solutions to past project problems, organized by category with YAML frontmatter. Search it before changing documented areas such as release automation or model/stream rewriting. `CONCEPTS.md` defines project-specific vocabulary used by these docs.

## Keeper alias integration

- Keeper URL and password environment-variable name live in CPA plugin configuration (`usage_keeper_url`, `usage_keeper_password_env`) and are declared as ConfigFields. State persists only the existing saved binding alias.
- `keeper_client.go` owns the standard-library HTTP/Cookie client; `keeper_aliases.go` owns a 60-second cache, 5-second request deadline, coalescing and stable failure states. Keeper IO must stay outside config/state locks and every inference hook.
- Management GET `/keeper/key-aliases` and POST `/keeper/key-aliases/refresh` return an HTTP 200 status envelope for Keeper failures, so external 401/403 cannot clear CPA management authentication.
- `useKeyOptions` is owned by App; both panels consume the same options. `keyOptions.ts` keeps values as exact full keys, local alias first, Keeper alias second. Do not add Keeper-only keys to the CPA list.
- API Key editor synchronization updates draft alias only. Guard edit session/key/alias changes before applying asynchronous results; saving uses the existing binding path.
- Run `go test . -run '^Test(Keeper|ManagementKeeper)'`, `go test -race ./...`, `npm --prefix web run typecheck`, and `npm --prefix web test`. CI also runs the frontend tests.
- UI component tests disable Select motion after installing the canvas shim because jsdom does not execute popup exit animations. Verify actual selection/clear behavior with motion enabled in browser QA.

## Keeper auth names and operation audit

- Codex authentication names are a separate workflow: match CPA `auth_index` to Keeper identity, display its name, and PATCH Keeper immediately. Canceling the outer key-binding editor does not undo a completed name update. Names remain in Keeper, not state; selection continues to use CPA `id`.
- `keeper_auth_names.go` owns identity projection/cache and the name PATCH authentication stages. Keep the entire preflight/login/PATCH under one five-second context; never automatically retry an uncertain write. Cache generations prevent an older read from replacing a published update.
- `audit.go` and `audit_query.go` append start/finish JSONL records directly to `model-mapper-plus-audit/YYYY-MM-DD.jsonl` beside state. The day is the operation start date in Asia/Shanghai. No state outbox, delayed export, business replay or automatic log deletion.
- Audit events are version 2: each carries a `module` enum (`rules|key_binding|notifications|keeper_auth_name|other`), an `object_label` (alias/global-name snapshot) and `labels` (channel ID → display-name snapshot). `validAuditEvent` branches per version — v2 requires a non-empty module, v1 history stays readable without the new fields. The reader merges start+finish with finish winning for `object_label`/`labels` (creations only know their alias after the mutation ran).
- Key operations record `object_ref` as `key:••••<last4>` (short keys stay fully masked) plus the alias snapshot as `object_label`; POST prefers the body alias, PATCH/DELETE snapshot the binding at operation time. `channel_target` diffs split into `channel_target.enabled/.suppliers/.auth_ids`; key notifications project to `{id,name,enabled}` rows.
- `audit_labels.go` keeps a process-local channel label cache (cap 4096, first label wins, no eviction) fed by `/channel-credentials` resolutions; the audit writer snapshots labels for IDs that actually changed. No network IO on the audit write path.
- Notification settings project into `notifications.*` fields; platform webhooks and signature secrets (global and key-level) are collected as known secrets, so the diff shows the field changed without echoing values. `test-send` records `action=test_send` with explicit `changed=false`.
- `GET /audit` accepts `module` filtering (validated against the enum, 400 `invalid_audit_module`) and returns `module_counts` over the whole day; v1 items map to modules via `object_type` for filtering only.
- `audit_management.go` gates management mutations and reconfiguration. Begin must Write+Sync before side effects; Finish errors retain the real business result with an additional audit warning. In-process unconfirmed finishes appear unknown; after restart, parse the records that actually survived.
- Only audit projections are redacted; do not change the existing full-Key state API. Never serialize raw headers/body/upstream responses into audit records. A Keeper HTTP 200 envelope may still describe failure or an unknown result.
- Related checks: `go test . -run '^Test(Audit|ManagementAudit|Keeper|ManagementKeeper)' -count=1`, frontend target files, and final race/typecheck/build. The opt-in `MAPPER_ACCEPTANCE_SERVE=1 go test . -run '^TestAdminAcceptanceServe$' -count=1 -v -timeout=30m` serves a loopback-only browser fixture; its default Skip and server exit are not browser acceptance results.
