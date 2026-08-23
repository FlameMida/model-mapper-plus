# Fast 剥离 Live 验收

- 时间：2026-08-24（Asia/Shanghai）
- 宿主：CLIProxyAPI v7.2.139
- 插件：model-mapper-plus v0.0.0-acceptance.5f0e922
- 结果：**PASS**
- trace ID：`2f1e1dd7`

请求 `POST /v1/messages` 同时包含：

- body：`"speed":"fast"`
- header：`fast-mode-2026-02-01,prompt-caching-2024`
- binding：`fast_allowed=false`、`channel_target.enabled=false`

request-log `v1-messages-2026-08-24T001713-2f1e1dd7.log` 的原始请求保留上述输入作为对照；真正的 `API REQUEST 1` 中：

- 上游 body 为 `{"model":"k2.5",...}`，无 `speed`
- 上游 `Anthropic-Beta` 仅为 `prompt-caching-2024`
- `fast-mode-2026-02-01` 只在原始请求段出现一次
- `prompt-caching-2024` 在原始与上游段各出现一次

请求已到达上游并因复制凭据失效返回 HTTP 401；错误与 Fast 剥离无关。
