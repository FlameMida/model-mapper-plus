# 渠道定向 Live 验收

- 时间：2026-08-23（Asia/Shanghai）
- 环境：独立临时容器 `cpa-channel-target-acceptance`，宿主 `CLIProxyAPI v7.2.119`，端口 `127.0.0.1:18327`
- 插件：`model-mapper-plus v0.0.0-acceptance.b8754c4`
- 结果：**PARTIAL**

将验收 key 定向到唯一 auth ID `kimi-1784435564425.json` 后，对 `/v1/messages` 发起 `kimi-k2.5` 请求。下游收到 HTTP 403，是目标凭据自身的上游额度耗尽错误，符合“2xx 或目标凭据自身上游错误”。

脱敏 request-log 证据：

- trace ID：`20260823180313-bf82fbbf5a5a90b1-1986d2f3`
- 实际选择：`provider=kimi, auth_id=kimi-1784435564425.json, type=oauth`
- 日志中仅有该 auth ID，未出现池外凭据。
- 验收前 binding 不存在；结束后 DELETE 返回 200，复查数量为 0。

限制：计划要求的 `make smoke-local` 基线未通过。原 `.env` 的 client key 已漂移，改用复制配置中的当前 client key 后，现有上游凭据又分别返回鉴权失败或额度耗尽。因此本项的“定向命中”已证实，但全套 smoke 基线门禁仍未满足。
