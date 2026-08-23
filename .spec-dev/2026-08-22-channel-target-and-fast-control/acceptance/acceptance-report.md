# Acceptance Report: 渠道定向与 Fast 控制

> **Language / 语言**: 中文；table headers and structural labels keep English.
> Time: 2026-08-24 | Triggered by: executing-plans wrap-up | Tier: deep
> Spec: `.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md` | Evidence dir: `.spec-dev/2026-08-22-channel-target-and-fast-control/acceptance/`
> Spec status: active

## Overview

| Dimension | Execution | Pass | Fail | Warn | Unverified | Notes |
|-----------|-----------|------|------|------|------------|-------|
| unit | D | 47 + Go suites | 0 | 0 | 0 | Vitest 11 files / 47 tests；Go 全包与 race 测试通过 |
| integration | D | 2 | 0 | 0 | 0 | 优先级边界 fixture；双宿主三协议 6/6 |
| e2e | D | 3 | 0 | 0 | 1 | 定向、Fast、组合通过；cooldown live 按计划 DEFERRED |
| visual | A | 1 | 0 | 0 | 0 | Chrome 1440×1100 浅色/深色与失败重试 |
| a11y | — | — | — | — | — | 不在本次验收矩阵 |
| perf-web | — | — | — | — | — | 不在本次验收矩阵 |
| perf-api | — | — | — | — | — | 不在本次验收矩阵 |

## Requirement Coverage

| Matrix row (Scenario / check item) | Dimension | Status | Evidence |
|------------------------------------|-----------|--------|----------|
| 定向请求实际落在目标认证文件 | e2e | pass | `channel-target-live.md`；trace `89aaa7d6` |
| 目标 cooldown、池外 active 时不越池 | e2e | unverified / DEFERRED | `channel-target-cooldown.md`；安全 fixture 不足，race 单测替代 |
| 目标低优先级、池外高优先级时不越池 | integration | pass | `channel-target-priority.md`；Scheduler race fixture |
| v7.2.119 与 v7.2.139 三协议错误兼容 | integration | pass | `host-version-compat.md`；6 requests / 6 pass |
| Fast 关闭后上游收到普通请求 | e2e | pass | `fast-strip-live.md`；trace `2f1e1dd7` |
| 定向与 Fast 组合叠加 | e2e | pass | `channel-target-fast-combined.md`；trace `b5ec4e97` |
| 编辑表单全流程视觉与交互 | visual | pass | `ui-review.md`；`ui-run.json`（10/10 fail-fast assertions）；`ui-light.png`；`ui-dark.png` |

## Requirement Reconciliation

active spec 的 24 个 Scenario 均已 DELIVERED；第二轮完整性审查唯一 PARTIAL（直接保存未规范化）已由 `5f0e922` 修复，并经独立复审确认 CLOSED。cooldown 的 live 行是验收环境 DEFERRED，不代表实现或契约缺失。

| Requirement | Verdict | Evidence / Reason |
|-------------|---------|-------------------|
| 目标 cooldown、池外 active 的真实双凭据复验 | DEFERRED (acceptance only) | 只有一份 auth，且禁止改写真实冷却状态；`channel-target-cooldown.md` 保留 race 替代证据与可复跑条件 |

## Key Findings

无阻塞发现。六项已执行矩阵行全部通过；一项 cooldown live 依计划诚实标记为 DEFERRED / UNVERIFIED。

## Diagnosis Details

所有已执行检查均通过，无失败需要根因诊断。Darwin 原生宿主加载 Go c-shared 插件时因双 Go runtime 在监听前崩溃，因此双版本真实 HTTP 验收改用 Debian 12 x86_64 容器中的精确 CGO 构建；源码版本、二进制 SHA-256 与回退边界记录在 `host-version-compat.md`。cooldown live 未执行的原因和恢复前提记录在 `channel-target-cooldown.md`。

## Evidence Index

- Contract JSON: `acceptance/check-items.json`（已通过 `validate-output.mjs acceptance-check-items`）
- Deterministic tests: `state_test.go`、`main_test.go`、`scheduler_test.go`、`fast_strip_test.go`、`management_test.go`、`web/src/api.test.ts`、`web/src/components/ChannelTargetEditor.test.tsx`、`web/src/panels/KeysPanel.test.tsx`、`web/src/panels/KeysPanel.channel-target.test.tsx`
- Live evidence: `channel-target-live.md`、`channel-target-cooldown.md`、`channel-target-priority.md`、`host-version-compat.md`、`fast-strip-live.md`、`channel-target-fast-combined.md`
- Visual evidence: `ui-review.md`、`ui-run.json`、`ui-light.png`、`ui-dark.png`
- Independent audit: `evidence-audit.md`（6 个 PASS 维持；cooldown 维持 UNVERIFIED）
- Environment and restoration: `pre-state.json`、`acceptance-summary.md`

## coverage_note

完整执行 T15 的 7 行验收矩阵：6 pass，1 unverified / DEFERRED。cooldown live 因缺少第二份可安全恢复的 auth、且不得操纵真实凭据状态而降级为 race 单测替代证据；具备安全 fixture 后可按文档复跑。visual 为 Tier A 当前状态判读，无 Playwright 像素基线，不声称视觉回归。a11y 与性能未进入本特性矩阵，未额外发明门槛。
