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
- Run live local smoke: set `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN`, then `make smoke-local`
- Clean build output: `make clean`

Do not run `go test ./.github/scripts`; that directory contains multiple `package main` scripts and will collide on duplicate `main`/`run` symbols. Test script files explicitly as shown above.

## Architecture overview

This is a single-package Go `c-shared` CLIProxyAPI native plugin. `abi_cgo.go` is the C ABI bridge: it exports `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, and `cliproxyPluginShutdown`, then forwards plugin method calls into `handleMethod` in `main.go`.

`main.go` contains the plugin logic:

- `pluginRegistration` advertises `model_router`, `executor`, `executor.execute_stream`, and `management_api` support for `openai`, `claude`, and `openai-response` formats.
- `decodeLifecycleConfig` and `decodeConfig` load plugin config from CPA lifecycle payloads. Config fields are `enabled`, `global_rules`, `claude_messages_rules`, `codex_responses_rules`, `openai_completions_rules`, and `state_file`.
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
