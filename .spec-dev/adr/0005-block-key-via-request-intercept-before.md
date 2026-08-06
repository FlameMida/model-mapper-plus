# ADR-0005: 禁用 Key 通过 `request.intercept_before` 短路拒绝

- 日期：2026-08-05
- 状态：已接受
- 修订：2026-08-06（确认 CLIProxyAPI `v7.2.119` 的生命周期顺序与插件范围）

## 背景

`model.route` 只能路由不能拒绝；需要为标准 HTTP/SSE 请求自定义 HTTP 403 与 OpenAI 风格「额度用尽」英文 body，且不进入上游。可选 self-executor 本地报错或 request interceptor Terminate。

固定 SDK `v7.2.119` 的非流式和流式链路都会先调用 `model.route`，再调用 `request.intercept_before`。因此“blocked 请求完全不进入模型路由”不是插件在该宿主版本中能实现的契约；要求更早的顺序只能由 CPA 宿主新增 hook 来满足。

## 决定

注册 `RequestInterceptor`，在 `request.intercept_before` 对 `blocked=true` 的 binding 执行 `Terminate=true`、`StatusCode=403`、固定 JSON body（`insufficient_quota` / `Your quota has been exhausted.`）。不在 `model.route` / executor 承担门禁。

接受 `v7.2.119` 的既定顺序：在 Home 关闭、且实际进入标准 `BaseAPIHandler` request-interceptor 链的 OpenAI/Claude/Responses **HTTP/SSE（非 WebSocket upgrade）**请求中，blocked 请求可先经过纯 `model.route` 回调，但随后必须收到相同的固定 403，且不得进入插件 executor、认证选择/执行或上游。`model.route` 对本特性只负责规则/状态计算，不得以拒绝或执行替代门禁；`request.intercept_before` 是唯一的拒绝点。

本 ADR 明确采用插件范围方案：不修改 CPA 宿主。Home 已开启且 `model.route` 命中 self/executor target 时，SDK 会在 before-interceptor 前返回 503；`/v1/alpha/search` 与 `/backend-api/codex/alpha/search`（Codex direct alias）不通过该 interceptor。它们不承诺固定 403 或本门禁效果；若要覆盖，须单独修改 CPA 宿主并另行设计。

带 `Upgrade: websocket` 的 `GET /v1/responses` 与 `GET /backend-api/codex/responses` 是另一种承载形式：`ResponsesWebsocket` 先完成 WebSocket upgrade。只有完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的 `response.create` / `response.append` 消息，才会在 AuthManager/executor 前进入 before-interceptor；命中 `Terminate` 时，宿主将错误写为 WebSocket `type:"error"` 文本事件，而不是 HTTP DirectResponse。首个 `response.create` 且 `generate:false` 的本地 synthetic prewarm、消息规范化/校验错误、native-interaction 校验、provider 解析及其他 hook 前早退分支不进入该 hook，且不属于本特性门禁。固定 HTTP 403/status/body 因而不是任何这两条 GET upgrade 分支的契约。若要约定 WebSocket 帧的精确错误格式，必须修改 CPA 宿主并新增专门验收。

插件构建依赖固定为 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.119`。部署本特性的 CPA runtime 最低版本为 `v7.2.103`；该版本是 request interceptor termination wire contract（`Terminate` / `StatusCode` / `ResponseHeaders` / `ResponseBody`）与注册 RPC `SchemaVersion=2` 首次进入发布 tag 的版本。native `ABIVersion` 仍为 1。

## 理由

门禁与模型映射解耦；宿主对 HTTP/SSE interceptor Terminate 提供 DirectResponse，可完整自定义 status/body。在适用范围内，即使路由回调先运行，Terminate 仍发生在 executor 与认证选择/执行之前，因此不会发起上游调用。WebSocket 已在 handler 开始处升级，不能再向客户端返回 HTTP 403；仅对完成 WebSocket 本地处理、通过路由与执行前置检查并实际到达 `applyRequestInterceptorsBeforeAuth` 的消息，宿主才会把 Terminate 写为 `type:"error"` 事件，不能把它伪称为固定 HTTP 响应。synthetic prewarm、校验、provider 解析及其他 hook 前早退的 WebSocket 消息不经过本门禁。self-executor 路径要求 blocked 时强制 `Handled=true`（即使模型未变），扭曲路由语义且流式/非流式都要补洞。被否方案：要求 blocked 请求完全跳过 `model.route`（需要改 CPA 宿主）；`frontend_auth` 拒绝（宿主只给 401 通用文案，无法额度用尽英文）；仅改 `enabled` 语义。

## 兼容性后果

- 只升级插件编译使用的 SDK 不足以完成可部署交付：升级后的插件声明注册 RPC `SchemaVersion=2`，CPA `< v7.2.103` 最多支持 schema 1，会以 `plugin schema version 2 is not supported` 拒绝注册。即便绕过注册兼容检查，旧 wire contract 也不支持 termination 字段。
- 实施计划与发布验收必须同时检查 SDK pin 和 CPA runtime 版本；宿主版本低于 `v7.2.103` 或无法确认时，本特性不得标记为验收通过。
- 这是现有 CLIProxyAPI 依赖的升级，不引入新的插件运行时或业务第三方依赖；host lifecycle test 所需的是该 SDK 已有的传递模块记录。native `ABIVersion=1`，注册 RPC `SchemaVersion=2`。
- 本地 Home 关闭的 `BaseAPIHandler` HTTP/SSE 生命周期回归须断言 `route → intercept` 的真实顺序、固定 403、真实客户端头传递，以及 self-executor / provider 分支都在 executor 或认证执行前被短路；它不把“路由回调是否运行”误报为安全缺陷。WebSocket GET upgrade 需要 CPA 宿主级帧契约和独立测试，未包含在本插件的固定 HTTP 验收中。
