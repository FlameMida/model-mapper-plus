# 渠道定向与 Fast 控制验收总结

## 结论

**PASS（含 1 项按计划 DEFERRED）**。代码、双宿主错误契约、真实定向、Fast 剥离、组合场景与当前 UI 均通过。仅“真实目标 cooldown + 池外 active”因环境只有一份凭据、且不允许改写真实冷却状态而标记 DEFERRED；已保留 race 单测替代证据和可复跑条件。

## 确定性安全网

| 检查 | 结果 |
|---|---|
| `go test ./...` | PASS |
| `go test -race .` | PASS |
| `go vet ./...` | PASS |
| `bun run --cwd web typecheck` | PASS |
| `bun run --cwd web test` | PASS，11 files / 47 tests |
| `VITE_HOSTED=1 bun run --cwd web build` | PASS；仅 lottie-web 依赖的既有 direct-eval warning |
| `git diff --exit-code -- web/package-lock.json` | PASS |
| `git diff --check` | PASS |
| `make test-scripts` | 既有 FAIL：`FAIL: windows output not versioned`；已在源分支复现并经用户裁决为本特性非阻塞 |

## 验收矩阵

| 场景 | 结果 | 证据 |
|---|---|---|
| 定向请求落在目标 auth | PASS | `channel-target-live.md` |
| 目标 cooldown、池外 active 时 503 且不越池 | DEFERRED / UNVERIFIED | `channel-target-cooldown.md` |
| 目标低优先级、池外高优先级时不越池 | PASS（integration fixture） | `channel-target-priority.md` |
| v7.2.119 / v7.2.139 三协议错误兼容 | PASS，6/6 | `host-version-compat.md` |
| Fast 关闭后上游无 Fast 标记 | PASS | `fast-strip-live.md` |
| 定向 + Fast 组合 | PASS | `channel-target-fast-combined.md` |
| 编辑表单浅色/深色与失败重试 | PASS | `ui-review.md`、`ui-run.json`、`ui-light.png`、`ui-dark.png` |

## 已处置审查发现

1. wire JSON 缺失 `suppliers` / `auth_ids`：摘要安全计数，编辑边界补齐数组。
2. cooldown / 优先级边界：契约已校准为宿主预过滤后插件返回 503；全局无候选才 MAY 由宿主返回 429。
3. v7.2.119 typed code 丢失：改为合法 JSON message，双版本六次真实 HTTP 请求均保留协议字段。
4. provider/auth ID 规范化：列表、分组、回显、缺失判定、组选与保存共用同一 helper。
5. blocked/Fast 热更新快照：单次请求只加载一次 rule source，race 用例通过。
6. 第二轮审查发现的“直接保存未规范化”已于 `5f0e922` 修复；独立复审结论 `CLOSED`，无新发现。
7. T15 独立证据审计：6 个 PASS 全部维持；UI 首轮证据降级后补齐 10 项 fail-fast CDP 断言，复核恢复为维持；cooldown 仍保持 DEFERRED。

## 环境与恢复

- 宿主源码固定为 `v7.2.119` archive 与本地 clean `v7.2.139`；构建输出全部在 `/tmp/cpa-channel-compat-model-mapper-plus`。
- Darwin 原生宿主加载 Go c-shared 插件时触发双 Go runtime 崩溃，因此真实双版本请求在 Debian 12 x86_64 容器中使用精确源码交叉构建的 CGO 宿主执行。
- 验收 client key 在两个临时实例中的原 binding 均不存在；结束后 DELETE 并复查为 0。
- 只复制真实 auth 文件到 `/tmp`；未改写 `/Users/flame/CLIProxyAPI` 的源码、配置、auth 文件或插件目录，验收后宿主仓库仍为 clean。
