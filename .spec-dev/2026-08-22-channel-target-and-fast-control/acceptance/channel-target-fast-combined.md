# 渠道定向 + Fast 组合 Live 验收

- 时间：2026-08-24（Asia/Shanghai）
- 宿主：CLIProxyAPI v7.2.139
- 插件：model-mapper-plus v0.0.0-acceptance.5f0e922
- 结果：**PASS**
- trace ID：`b5ec4e97`

binding 同时配置：

- `channel_target.enabled=true`
- `auth_ids=["kimi-1784435564425.json"]`
- `fast_allowed=false`

宿主日志明确选择 `provider=kimi auth_file=kimi-1784435564425.json`；request-log `v1-messages-2026-08-24T001743-b5ec4e97.log` 中目标 auth ID 命中 1 次。

同一 request-log 的 `API REQUEST 1` 证明上游 body 无 `speed`，上游 beta 只保留 `prompt-caching-2024`，未保留 `fast-mode-2026-02-01`。请求到达指定上游后因复制凭据失效返回 HTTP 401。

验收结束后临时 binding 已删除，复查数量为 0。
