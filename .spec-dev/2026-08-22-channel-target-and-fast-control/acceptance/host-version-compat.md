# CLIProxyAPI 双版本三协议错误兼容验收

> 2026-09-15 注：池空错误形态已变更为 HTTP 429 + 结构化错误体（现行契约见 .spec-dev/2026-09-15-02-channel-target-empty-pool-error-form/spec/channel-target-empty-pool-error-form-design.md）。下表为变更前（503 形态）的历史验收记录；429 形态的宿主兼容验收由本特性验收任务（T03）重跑后追加。

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

---

# 429 结构化错误形态重跑（2026-09-15 补验）

- 时间：2026-09-15（Asia/Shanghai）
- 结果：**PASS，6/6**
- 插件：`model-mapper-plus v0.0.0-dev.ed4e90f`（main HEAD，含 429 变更，`buildmode=c-shared` linux/amd64）
- 方法：macOS docker desktop `debian:bookworm-slim`（`--platform linux/amd64`）容器，CPA 二进制由源码 `zig cc -target x86_64-linux-gnu` CGO 交叉编译；`gemini-api-key` 伪凭据（加载即 active 使 Candidates 非空）+ 绑定 client key 定向不存在 ID 构造池空；断言脚本 `python3 .test-cpa/smoke.py`（429 + 协议对应 auth_not_found 标记 + retry 语义 + detail 计数 + 全文脱敏）

| 宿主 | 协议/端点 | HTTP | 必要错误字段 | detail 断言 | 结果 |
|---|---|---:|---|---|---|
| v7.2.119 | OpenAI `/v1/chat/completions` | 429 | `error.code=auth_not_found` | `candidates=1, binding={suppliers:0, auth_ids:1}`，message 含 retry | PASS |
| v7.2.119 | Codex `/v1/responses` | 429 | `error.code=auth_not_found` | 同上 | PASS |
| v7.2.119 | Claude `/v1/messages` | 429 | `error.type=auth_not_found` | 重包装外形（仅 type 断言） | PASS |
| v7.3.2 | OpenAI `/v1/chat/completions` | 429 | `error.code=auth_not_found` | 同 v7.2.119 | PASS |
| v7.3.2 | Codex `/v1/responses` | 429 | `error.code=auth_not_found` | 同上 | PASS |
| v7.3.2 | Claude `/v1/messages` | 429 | `error.type=auth_not_found` | 重包装外形（仅 type 断言） | PASS |

- v7.3.2（本地仓库 HEAD）替代旧表的 v7.2.139 作为「更高版本」追加项；v7.2.119 为插件 go.mod 锚定版本。
- OpenAI/Codex 错误体为插件结构化 JSON 原样透传（样本：`{"error":{"code":"auth_not_found","detail":{"binding":{"auth_ids":1,"suppliers":0},"candidates":1},"message":"all credentials in the channel target are currently unavailable (cooling down, refreshing, or quota-limited); retry later","type":"auth_not_found"}}`）；错误体全文不含凭据 key 值、供应商名与客户端 key（脱敏断言通过）。
- 完整报告见 `.spec-dev/2026-09-15-02-channel-target-empty-pool-error-form/acceptance/acceptance-report.md` 补验节。

