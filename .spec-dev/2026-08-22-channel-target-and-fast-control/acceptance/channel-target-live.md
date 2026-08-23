# 渠道定向 Live 验收

- 时间：2026-08-24（Asia/Shanghai）
- 环境：Debian 12 x86_64 临时容器，宿主 CLIProxyAPI v7.2.139
- 插件：model-mapper-plus v0.0.0-acceptance.5f0e922
- 结果：**PASS**

临时 key 只定向到复制的 auth ID `kimi-1784435564425.json`，对 `POST /v1/messages` 发起 `kimi-k2.5` 非流式请求。下游返回 HTTP 401 `invalid_authentication_error`，属于该目标凭据自身的上游失效错误，满足“2xx 或目标凭据自身上游错误”。

可审计证据：

- trace ID：`89aaa7d6`
- 宿主日志：`Use OAuth provider=kimi auth_file=kimi-1784435564425.json for model kimi-k2.5`
- request-log：`v1-messages-2026-08-24T001643-89aaa7d6.log`，目标 auth ID 命中 1 次
- 临时实例仅加载这一份 auth，未出现池外 auth ID

验收前 binding 不存在；结束后 DELETE 并复查匹配 binding 数量为 0。真实 auth 文件未被修改，仅复制品进入临时目录。
