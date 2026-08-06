# Acceptance Report: key-access-block

> Time: 2026-08-06 (Asia/Shanghai) | Triggered by: executing-plans wrap-up | Tier: standard
> Spec: `.spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md` | Evidence dir: `.spec-dev/2026-08-05-key-access-block/acceptance/`
>
> 用户已确认插件范围：CLIProxyAPI `v7.2.119` 可先调用纯 `model.route`；固定 HTTP 403 仅承诺给 Home 关闭、且实际进入标准 `BaseAPIHandler` request-interceptor 链的 OpenAI/Claude/Responses HTTP/SSE（非 WebSocket upgrade）请求。Home 的 self-executor route、`/v1/alpha/search` 与 `/backend-api/codex/alpha/search`（Codex direct alias）不在本特性门禁范围。带 `Upgrade: websocket` 的 `GET /v1/responses` 和 `GET /backend-api/codex/responses` 只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息可在执行前被拦截，宿主会将该结果写为 WebSocket `type:"error"` 事件；synthetic prewarm、校验、provider 解析及其他 hook 前早退分支不经过 hook，全部 WebSocket 分支均不属于固定 HTTP 403/body 契约。本地验收已通过；live CPA 环境未配置，因此本报告不是部署或发布就绪结论。

## Overview

| Dimension | Execution | Pass | Fail | Warn | Unverified | Notes |
|-----------|-----------|------|------|------|------------|-------|
| unit | D | 9 | 0 | 0 | 0 | state、management、intercept、持久化重载、删除解禁与 React UI 场景均由确定性测试覆盖 |
| integration | D | 2 | 0 | 0 | 0 | 真实 SDK `BaseAPIHandler` 的非流式/流式生命周期，以及全量 Go/UI/build 套件均通过 |
| compatibility | D | 1 | 0 | 0 | 1 | SDK v7.2.119/ABI/schema 已确认；live CPA 版本尚未读取 |
| e2e | D | 0 | 0 | 0 | 1 | 无 live CPA 凭据或地址，未执行 HTTP/SSE 403 与 cleanup（不以 WebSocket upgrade 替代） |

## Requirement Coverage

| Matrix row (Scenario / check item) | Dimension | Status | Evidence |
|------------------------------------|-----------|--------|----------|
| 旧 state 加载 blocked=false；保留既有 enabled 路由 | unit | pass | `TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding`，`make test` exit 0 |
| blocked 管理 API POST/GET/PATCH 与解禁 | unit | pass | `TestManagementPostKeyPersistsBlockedAndGetStateReadsIt`、`TestManagementPatchKeyUnblocksWithoutClobberingEnabled`、`TestManagementUnblockAllowsInterceptPass` |
| blocked key HTTP/SSE 固定 403 body；规则关闭/放行分支/header 优先级 | unit | pass | `intercept_block_test.go`，`make test` exit 0 |
| POST blocked binding 后立即拒绝，并在 reconfigure 后持续拒绝 | unit | pass | `TestManagementPostBlockedKeyImmediatelyRejects`、`TestManagementPostBlockedKeyStillRejectsAfterReconfigure` |
| 删除 blocked binding 后放行 | unit | pass | `TestManagementDeleteBlockedBindingAllowsInterceptPass` |
| enabled 与 blocked 正交 | unit | pass | `TestRouteModelBindingDisabled`、`TestFindBlockedKeyBindingIgnoresEnabled` |
| 编辑窗与列表的禁止访问 Switch | unit | pass | `KeysPanel.blocked.test.tsx` 覆盖新建默认 false、编辑 false→true、true→false、列表 PATCH |
| Home 关闭的 HTTP/SSE 宿主允许 route、随后固定拒绝且不执行 | integration | pass | self-executor 与 provider 分支的 4 个 `TestBlockedKeyHost*Lifecycle*`；真实 Gin 客户端头传播 |
| SDK pin 与 ABI/schema | compatibility | pass | `go list -m` = `v7.2.119`；`go mod verify`；`TestPluginRegistrationMetadataAndConfigFields` |
| CPA runtime >= v7.2.103 | compatibility | unverified | 未设置 live CPA 环境变量，未读取 `X-CPA-VERSION` |
| Home 关闭的 live HTTP/SSE `POST /v1/responses` 固定 403 / body / cleanup | e2e | unverified | 未设置 live CPA 环境变量，未创建测试 binding 或发出请求；不含 `GET` WebSocket upgrade |
| 全量 Go/前端/构建 | integration | pass | `go mod tidy -diff`、`make test`、`make vet`、typecheck、Vitest、`make web-build`、bundle diff 全部 exit 0 |

## Requirement Reconciliation

| Requirement | Verdict | Evidence / Reason |
|-------------|---------|-------------------|
| `blocked` 持久化、管理读写、HTTP/SSE 运行时固定 403、`enabled` 正交与 UI 开关 | DELIVERED | 本地 unit/integration 套件全绿；追加的 reconfigure、删除解禁与 UI 双向用例已补齐。 |
| Home 关闭的标准 `BaseAPIHandler` HTTP/SSE 生命周期：允许纯 `model.route`，禁止 executor / credential execution / upstream | DELIVERED | 真实 SDK 的非流式与流式、self-executor 与 provider 分支均验证 `route → intercept`、固定 DirectResponse 403、真实客户端头传递；provider 分支用真实空 `AuthManager`，故若未在前置 hook 短路会返回认证错误而不是 DirectResponse。 |
| Home self-executor route、`/v1/alpha/search` 与 `/backend-api/codex/alpha/search`（Codex direct alias） | EXCLUDED | 用户选择插件范围方案；前者会在 hook 前由宿主返回 503，后二者不进入 request-interceptor 链。 |
| `GET /v1/responses` 与 `GET /backend-api/codex/responses` 的 WebSocket upgrade | EXCLUDED（仅固定 HTTP 契约） | 连接先升级为 WebSocket；只有完成本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息可在执行前被 blocked 拒绝，并由宿主写为 `type:"error"` 事件。synthetic prewarm、校验、provider 解析及其他 hook 前早退分支不经过 hook；全部分支均非 HTTP 403/body。 |
| CPA runtime 版本与 Home 关闭的 live HTTP/SSE direct-response / cleanup | DEFERRED | `CPA_BASE_URL`、`CPA_MANAGEMENT_KEY` 均 unset；无法安全地确认版本/Home 状态、发送请求或验证清理。 |

## Key Findings

1. **[Deferred] live CPA compatibility/e2e** — 本地 SDK 契约与 HTTP/SSE payload 已覆盖，但缺少运行中 CPA 的版本 header、真实 `POST /v1/responses` 返回和专用 key cleanup 证据；不能据此宣称部署或发布就绪。
2. **[Resolved] 生命周期范围** — 用户选择不改 CPA 宿主：spec、ADR、计划和验收项已将固定 HTTP 403 限定为 Home 关闭的标准 `BaseAPIHandler` HTTP/SSE 路径，并显式排除 Home self-executor route、`/v1/alpha/search`、`/backend-api/codex/alpha/search`（Codex direct alias），以及 `GET /v1/responses` / `GET /backend-api/codex/responses` 的 WebSocket fixed-HTTP contract；最后一类只有完成本地处理、通过路由与执行前置检查并到达 before-interceptor 的消息可在执行前拦截、结果为 `type:"error"`，其他 hook 前早退分支不经过门禁。
3. **[Resolved] 生命周期覆盖缺口** — 回归现在使用真实 Gin 请求上下文，断言同一 Authorization 头到达 route 与 before-interceptor；另以真实空 `AuthManager` 覆盖 provider 分支，并完整断言 after hook 返回零值响应。

## Diagnosis Details

固定 CLIProxyAPI `v7.2.119` 的 Home 关闭 `BaseAPIHandler` HTTP/SSE 非流式与流式宿主链均会先运行 `applyModelRouter`。本次 `host_lifecycle_test.go` 使用真实 Gin 请求上下文、SDK `BaseAPIHandler`、本插件的真实 `handleModelRoute` 与 `handleRequestInterceptBefore`，确认同一 Authorization 头到达 route 和 before-interceptor。self-executor bridge 验证 `route → intercept` 后 executor 调用数为 0；provider bridge 使用真实空 `AuthManager`，仍得到逐字节匹配的 DirectResponse 403，因此认证选择/执行未发生（否则会产生认证错误）。

SDK `v7.2.119` 将带 `Upgrade: websocket` 的 `GET /v1/responses` 与 `GET /backend-api/codex/responses` 绑定到 `ResponsesWebsocket`。该 handler 先升级连接；只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息才会进入执行前拦截，命中后由 `writeResponsesWebsocketError` 写成 `type:"error"` 帧，而不是 HTTP DirectResponse。首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、消息规范化/校验错误、native-interaction 校验、provider 解析及其他 hook 前早退分支不进入该 hook。因此本次 fixed-403 验收只覆盖 HTTP/SSE，WebSocket 行为仅作为已记录的宿主承载差异，不据此宣称 HTTP body 一致。

此前 completeness 审查指出的三项覆盖缺口均已补齐：POST 后重载、DELETE 后放行、UI 默认值与解禁方向。每个新增断言还通过临时变异验证其会在关键行为被破坏时失败，之后恢复生产代码。

## Evidence Index

- Contract JSON: `acceptance/check-items.json`
- 审查报告：`acceptance/reviews/quality.json`、`acceptance/reviews/functionality.json`、`acceptance/reviews/conformance.json`、`acceptance/reviews/completeness.json`
- Go 生命周期测试：`host_lifecycle_test.go`
- Go 门禁与持久化测试：`intercept_block_test.go`
- UI 测试：`web/src/panels/KeysPanel.blocked.test.tsx`
- 生成物：`web/dist/index.html`（`make web-build` 后无 diff）

## coverage_note

按 spec 矩阵，unit、integration 与本地 compatibility 已按确定性命令复跑。live compatibility/e2e 因环境变量缺失明确 DEFERRED，不是失败也不能由本地测试替代；即使环境可用，也必须先确认目标 **POST** `/v1/responses` 为 Home 关闭的标准 HTTP/SSE 执行路径。Home self-executor route、`/v1/alpha/search`、`/backend-api/codex/alpha/search`、以及两个 Responses `GET` 的 WebSocket upgrade（后者只有完成本地处理、通过路由与执行前置检查并到达 `applyRequestInterceptorsBeforeAuth` 的消息会以 `type:"error"` 承载拒绝；synthetic prewarm、校验、provider 解析及其他 hook 前早退分支不经过 hook；全部均非 fixed HTTP contract）、visual、a11y、perf-web、perf-api 不在本特性的 HTTP/SSE 验收范围中；环境检测也未找到可用浏览器、视觉基线或性能工具，故未伪造通过结论。
