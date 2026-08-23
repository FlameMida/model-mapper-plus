# CLIProxyAPI 双版本三协议错误兼容验收

- 时间：2026-08-24（Asia/Shanghai）
- 结果：**PASS，6/6**
- 插件：`model-mapper-plus v0.0.0-acceptance.5f0e922`
- 方法：两个独立 Debian 12 x86_64 容器，绑定不存在的 auth ID，依次请求三个协议

| 宿主 | 协议/端点 | HTTP | 必要错误字段 | trace ID | 结果 |
|---|---|---:|---|---|---|
| v7.2.119 | OpenAI `/v1/chat/completions` | 503 | `error.code=auth_not_found` | `e71c9f64` | PASS |
| v7.2.119 | Codex `/v1/responses` | 503 | `error.code=auth_not_found` | `c9404fa0` | PASS |
| v7.2.119 | Claude `/v1/messages` | 503 | `error.type=auth_not_found` | `e31fc34c` | PASS |
| v7.2.139 | OpenAI `/v1/chat/completions` | 503 | `error.code=auth_not_found` | `c77c6d5f` | PASS |
| v7.2.139 | Codex `/v1/responses` | 503 | `error.code=auth_not_found` | `57d4bd15` | PASS |
| v7.2.139 | Claude `/v1/messages` | 503 | `error.type=auth_not_found` | `dad83cd8` | PASS |

六个错误体的 message 均为 `no usable auth candidate in channel target`。OpenAI/Responses 还同时保留 `error.type=auth_not_found`；Claude 按其协议外形只要求 `error.type`。

## 构建边界

- v7.2.119：从 `/Users/flame/CLIProxyAPI` 的 `v7.2.119` tag 用 `git archive` 解压到 `/tmp` 后构建。
- v7.2.139：从本地 clean checkout 以 `-mod=readonly` 构建；验收前后 `git status --porcelain=v1` 均为空。
- 宿主和插件均使用 `zig cc -target x86_64-linux-gnu` 的 CGO 构建，所有输出位于 `/tmp/cpa-channel-compat-model-mapper-plus`。

SHA-256：

```text
365a459b718333b821cd72cf28fc091eeb56fb1eb6406e857f76e8b45805444e  cpa-v7.2.119-linux-amd64
fce6c14766483755260479fda62b7a53384cecda8a59181f8bc2608bcc72ae82  cpa-v7.2.139-linux-amd64
d509dce4db672720be91e68c9fbb89a5b849b26132fc70dc53dc6c8b044e72c6  model-mapper-plus.so
```

## 环境降级说明

计划中的 Darwin 原生二进制已成功构建，但加载 Go c-shared 插件时在 `cliproxy_plugin_init` 前后触发双 Go runtime fatal error，尚未监听端口。为执行真实 HTTP 兼容测试，改用精确同源码的 Linux CGO 宿主与插件；该路径实际运行了两个宿主的 RPC/协议错误转换链。

