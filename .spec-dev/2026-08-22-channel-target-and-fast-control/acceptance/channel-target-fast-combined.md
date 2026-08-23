# 渠道定向 + Fast 组合 Live 验收

- 结果：**PASS**
- 配置：唯一 auth ID `kimi-1784435564425.json`，`channel_target.enabled=true`，`fast_allowed=false`
- trace ID：`20260823180218-bf82fbbf5a5a90b1-d808d610`
- 响应：HTTP 403，为目标凭据本身的上游额度耗尽错误

脱敏 request-log 同时证明：

- Scheduler 选中 `provider=kimi, auth_id=kimi-1784435564425.json`，无池外 auth ID。
- 上游 body 中 `"speed":"fast"` 命中数为 0。
- 上游 header 中 `fast-mode-2026-02-01` 命中数为 0。
- `prompt-caching-2024` 命中数为 1，证明其他 beta token 被保留。

验收前 binding 不存在；结束后 DELETE 返回 200，复查数量为 0。
