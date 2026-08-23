# Fast 剥离 Live 验收

- 结果：**PASS**
- 请求：`POST /v1/messages`，body 含 `speed:"fast"`，`anthropic-beta` 同时含 `fast-mode-2026-02-01` 与 `prompt-caching-2024`
- key 配置：`fast_allowed=false`，`channel_target.enabled=false`
- trace ID：`20260823180400-bf82fbbf5a5a90b1-b9149f13`

脱敏上游请求截面统计：

- `"speed":"fast"` 命中数：0
- `fast-mode-2026-02-01` 命中数：0
- `prompt-caching-2024` 命中数：1

请求确实到达上游，并因该凭据的计费周期额度耗尽返回 HTTP 403；错误与 Fast 标记无关。验收 binding 结束后已删除，复查数量为 0。
