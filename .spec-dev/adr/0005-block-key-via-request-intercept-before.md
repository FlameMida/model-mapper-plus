# ADR-0005: 禁用 Key 通过 `request.intercept_before` 短路拒绝

- 日期：2026-08-05
- 状态：已接受

## 背景

`model.route` 只能路由不能拒绝；需要自定义 HTTP 403 与 OpenAI 风格「额度用尽」英文 body，且不进入上游。可选 self-executor 本地报错或 request interceptor Terminate。

## 决定

注册 `RequestInterceptor`，在 `request.intercept_before` 对 `blocked=true` 的 binding 执行 `Terminate=true`、`StatusCode=403`、固定 JSON body（`insufficient_quota` / `Your quota has been exhausted.`）。不在 `model.route` / executor 承担门禁。

插件构建依赖固定为 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.119`。部署本特性的 CPA runtime 最低版本为 `v7.2.103`；该版本是 request interceptor termination wire contract（`Terminate` / `StatusCode` / `ResponseHeaders` / `ResponseBody`）与注册 RPC `SchemaVersion=2` 首次进入发布 tag 的版本。native `ABIVersion` 仍为 1。

## 理由

门禁与模型映射解耦；宿主对 interceptor Terminate 提供 DirectResponse，可完整自定义 status/body。self-executor 路径要求 blocked 时强制 `Handled=true`（即使模型未变），扭曲路由语义且流式/非流式都要补洞。被否方案：`frontend_auth` 拒绝（宿主只给 401 通用文案，无法额度用尽英文）；仅改 `enabled` 语义。

## 兼容性后果

- 只升级插件编译使用的 SDK 不足以完成可部署交付：升级后的插件声明注册 RPC `SchemaVersion=2`，CPA `< v7.2.103` 最多支持 schema 1，会以 `plugin schema version 2 is not supported` 拒绝注册。即便绕过注册兼容检查，旧 wire contract 也不支持 termination 字段。
- 实施计划与发布验收必须同时检查 SDK pin 和 CPA runtime 版本；宿主版本低于 `v7.2.103` 或无法确认时，本特性不得标记为验收通过。
- 这是现有 CLIProxyAPI 依赖的升级，不引入新的第三方依赖；native `ABIVersion=1`，注册 RPC `SchemaVersion=2`。
