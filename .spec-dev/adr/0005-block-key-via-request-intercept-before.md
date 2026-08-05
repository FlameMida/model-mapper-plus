# ADR-0005: 禁用 Key 通过 `request.intercept_before` 短路拒绝

- 日期：2026-08-05
- 状态：已接受

## 背景

`model.route` 只能路由不能拒绝；需要自定义 HTTP 403 与 OpenAI 风格「额度用尽」英文 body，且不进入上游。可选 self-executor 本地报错或 request interceptor Terminate。

## 决定

注册 `RequestInterceptor`，在 `request.intercept_before` 对 `blocked=true` 的 binding 执行 `Terminate=true`、`StatusCode=403`、固定 JSON body（`insufficient_quota` / `Your quota has been exhausted.`）。不在 `model.route` / executor 承担门禁。

## 理由

门禁与模型映射解耦；宿主对 interceptor Terminate 提供 DirectResponse，可完整自定义 status/body。self-executor 路径要求 blocked 时强制 `Handled=true`（即使模型未变），扭曲路由语义且流式/非流式都要补洞。被否方案：`frontend_auth` 拒绝（宿主只给 401 通用文案，无法额度用尽英文）；仅改 `enabled` 语义。
