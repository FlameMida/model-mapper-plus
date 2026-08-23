# 渠道定向与 Fast 控制验收总结

## 结论

**FAIL（阻塞合并）**。主体实现、确定性测试、真实定向与 Fast 剥离均有效，但独立审查和对抗复核确认了 5 项未处置发现；其中 4 项为中级、1 项为低级。另有 1 个既有脚本基线失败和 1 个无法安全执行的 cooldown live 场景。

## 确定性安全网

| 检查 | 结果 |
|---|---|
| `go test ./...` | PASS |
| `go test -race .` | PASS |
| `go vet ./...` | PASS |
| `bun run --cwd web typecheck` | PASS |
| `bun run --cwd web test` | PASS，11 files / 41 tests |
| `VITE_HOSTED=1 bun run --cwd web build` | PASS；仅 lottie-web 依赖的既有 direct-eval warning |
| `git diff --exit-code -- web/package-lock.json` | PASS |
| `git diff --check` | PASS |
| `make test-scripts` | FAIL；`FAIL: windows output not versioned`，已在主工作区源分支复现，属既有失败 |

## 验收矩阵

| 场景 | 结果 | 证据 |
|---|---|---|
| 定向请求落在目标 auth | PARTIAL | `channel-target-live.md` |
| 目标池全冷却返回原生 429 | DEFERRED / UNVERIFIED | `channel-target-cooldown.md` |
| Fast 关闭后上游无 Fast 标记 | PASS | `fast-strip-live.md` |
| 定向 + Fast 组合 | PASS | `channel-target-fast-combined.md` |
| 编辑表单浅色/深色交互 | PARTIAL PASS | `ui-review.md`、`ui-light.png`、`ui-dark.png` |

## 已确认发现

1. 中：Go `omitempty` 会让合法空数组从管理响应消失，`KeysPanel` 摘要直接读 `.length` 导致渲染崩溃。
2. 中：目标凭据 cooldown 但池外有 active 凭据时，宿主的全局预过滤使插件返回 503，无法兑现原生 429 + `Retry-After` 契约。
3. 中：CLIProxyAPI v7.2.119 RPC 适配层丢弃 ABI `auth_not_found` code；真实池空请求为 HTTP 503，但下游看不到承诺的 code。
4. 中：前端 provider/auth ID 去重与后端 trim + 大小写不敏感语义不一致，可导致重复分组、误报缺失或保存 400。
5. 低：`request.intercept_before` 同一请求读取两次状态快照，热更新窗口内可产生 blocked/Fast 混合时序；无 Go data race。

每项中/高原始发现均由独立对抗复核员尝试反驳，上述 5 项均被证实，并按复核结果对严重性去重/校准。

## 状态恢复

- 验收 client key 的原 binding 不存在；验收结束后已 DELETE，复查为 0。
- 视觉验收临时 binding 已 DELETE，独立 CPA 总 binding 数量为 0。
- 未修改 auth 文件、冷却状态或主工作区的 CPA 容器/插件。
