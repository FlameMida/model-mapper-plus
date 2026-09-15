# Acceptance Report: 渠道定向池空错误形态

> Time: 2026-09-15 | Triggered by: executing-plans T04（用户指示合入 main） | Tier: standard
> Spec: `.spec-dev/2026-09-15-02-channel-target-empty-pool-error-form/spec/channel-target-empty-pool-error-form-design.md`
> Spec status: active

## Overview

| Dimension | Execution | Pass | Fail | Warn | Unverified | Notes |
|-----------|-----------|------|------|------|------------|-------|
| unit / integration | D（T01 `go test`） | 34 | 0 | 0 | 0 | T04 全量 584 passed |
| e2e / 宿主透传 | 冒烟（补验） | 6 | 0 | 0 | 0 | 2026-09-15 补验：v7.2.119 + v7.3.2 × 三协议 6/6，见下方补验节 |
| visual / a11y / perf | — | — | — | — | — | 矩阵未要求（无前端） |

## Requirement Coverage

| Matrix row | Dimension | Status | Evidence |
|------------|-----------|--------|----------|
| 宿主链路 429 透传（codex `/v1/responses`） | integration | pass（补验） | 2026-09-15 补验 6/6：两版本 × 三协议 HTTP 429，codex 断言 `error.code` + retry 语义 + detail 计数 + 脱敏；回执 `.test-cpa/smoke-7.2.119.json`、`.test-cpa/smoke-7.3.2.json`（本地，gitignored） |
| 宿主链路 429 透传（Claude `/v1/messages`） | integration | pass（补验） | 同上；Claude 断言 `error.type=auth_not_found`（重包装形态）+ 429 + 脱敏 |
| host-version-compat 重跑 | integration | pass（补验） | v7.2.119（go.mod 锚定）+ v7.3.2（本地更高版本追加）；429 新表已追加至旧特性 `acceptance/host-version-compat.md`（历史 503 表保留） |
| 池空 429 + 结构化错误体（单元） | unit | pass | T01 `TestChannelTargetScheduler` 等，commit `7899a41` |

## Requirement Reconciliation

| Requirement | Verdict | Evidence / Reason |
|-------------|---------|-------------------|
| 池空 HTTP 429 + 脱敏 detail | DELIVERED | T01 单测 |
| 宿主三协议透传 | DELIVERED（补验改判） | 原 DEFERRED（无 docker CPA）；2026-09-15 真机补验 6/6 通过，缺口闭合 |

## 2026-09-15 补验（T03 缺口闭合）

- **环境**：macOS docker desktop（linux/amd64 容器经 `--platform linux/amd64`）；CPA 二进制由源码 zig 交叉编译（`CGO_ENABLED=1`、`x86_64-linux-gnu`）——v7.2.119 取自 tag 临时 worktree，v7.3.2 取自本地仓库 HEAD；插件 `.so` 由 main HEAD `ed4e90f` 构建（`buildmode=c-shared`，含 429 变更）。基础镜像 `debian:bookworm-slim`（amd64）。
- **构造**：`gemini-api-key` 伪凭据（免 OAuth、加载即 active，使 Candidates 非空、宿主征询插件 scheduler）+ 绑定 client key 定向不存在 ID `nonexistent-target-f1.json` → 定向池空。
- **断言**（`python3 .test-cpa/smoke.py`）：HTTP=429；openai/codex 透传体 `error.code=auth_not_found`、message 含 retry 语义、`detail.candidates=1`、`detail.binding={suppliers:0,auth_ids:1}`；claude 重包装体 `error.type=auth_not_found`；三协议错误体全文不含凭据 key 值、供应商名、客户端 key。
- **结果**：v7.2.119 3/3 PASS；v7.3.2 3/3 PASS（exit=0）。
- **样本**（v7.3.2 openai 路径原始响应体）：`{"error":{"code":"auth_not_found","detail":{"binding":{"auth_ids":1,"suppliers":0},"candidates":1},"message":"all credentials in the channel target are currently unavailable (cooling down, refreshing, or quota-limited); retry later","type":"auth_not_found"}}`——与 spec「关键接口」逐字段一致，宿主 `BuildErrorResponseBodyWithError` 原样透传验证成立。

## Key Findings

无阻塞。收尾审查（AS/BC 两路）零高/中发现；1 条低严重性建议（`main.go` marshal 兜底常量与主路径形态漂移，不可达分支，无行为影响，留作后续优化）。真机 429 透传已于 2026-09-15 补验闭合。

## coverage_note

原时点（T04）：T03 真机两项因环境缺件未执行，记 unverified；unit/integration 引用 T01/T04 全量回执。2026-09-15 补验后全部 integration 行闭合。无 UI 故裁剪 visual/a11y/perf。trace ID 字段在本地回执中为空（容器版本未启用 trace 头），不影响断言。
