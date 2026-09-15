# Acceptance Report: 通知调度、预览三栏与认证渠道发送

> **Language / 语言**: 报告内容以对话语言填写；表头与结构标签保持英文。
> Time: 2026-09-15 | Triggered by: user request after merge (T08) | Tier: standard
> Spec: `.spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md` | Evidence dir: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/`
> Spec status: active

## Overview

| Dimension | Execution | Pass | Fail | Warn | Unverified | Notes |
|-----------|-----------|------|------|------|------------|-------|
| unit | — | — | — | — | — | 任务内 TDD，本轮不复跑 |
| integration | — | — | — | — | — | 任务内 TDD，本轮不复跑 |
| e2e | — | — | — | — | — | 矩阵无独立 e2e 行 |
| visual | A (Playwright + Chrome) | 3 | 0 | 1 | 1 | 夹具管理页；390 默认停在最右栏 |
| a11y | — | — | — | — | 1 | 矩阵未要求，已裁剪 |
| perf-web | — | — | — | — | 1 | 矩阵未要求，已裁剪 |
| perf-api | — | — | — | — | 1 | 矩阵未要求，已裁剪 |

## Requirement Coverage (when a spec exists)

| Matrix row (Scenario / check item) | Dimension | Status | Evidence |
|------------------------------------|-----------|--------|----------|
| 桌面/窄屏预览弹窗 | visual | warn | `preview-desktop-3col.png` 三栏+分行通过；`preview-390-3col.png` 默认可横滚但首屏停在钉钉；`preview-390-scroll-left.png` 证明可滚到企微 |
| 未启用平台不出现 | visual | pass | `preview-desktop-2col.png` |
| 列表人类友好时间 | visual | pass | `list-next-fire.png`：`2026-09-15 12:14:32` |
| 真实三平台群消息 | manual | unverified | 未授权；夹具投递失败码见 `fail-notifications.png` |
| 每隔第二拍仍发送；轮询不滑动 | unit | — | T01/T02 TDD，本轮引用不复跑 |
| 全局预览/试发按渠道堆叠或拆条 | integration | — | T05 TDD；本轮预览 JSON 仍见 ai_1/ai_2 堆叠 |

## Requirement Reconciliation (delivery delta; filled by executing-plans wrap-up, remove when triggered standalone)

T09 已先于 T08 合入 `main`（`b38e9e2`）。本轮只补验收。

| Requirement | Verdict | Evidence / Reason |
|-------------|---------|-------------------|
| 预览弹窗三栏皮肤 | DELIVERED | 桌面三栏+指标分行通过；窄屏可横滚，首屏默认位置记 P3 |
| 真实三平台群对照 | DEFERRED | 无生产 webhook 授权；计划允许 manual-pending |

## Key Findings (by severity)

1. **[P3] 390 宽预览默认停在最右栏** (visual, 复核维持) — Semi `.semi-modal-wrap` 对 1080 宽弹窗 `overflow-x:auto`，首屏 `scrollLeft` 到最大值，只露出钉钉；向左滑可见企微/飞书。不挡「可横向滚动」，但首屏不是三栏同时可见。
2. **[P3] 嵌入 UI 在 T07 后未烘焙** (visual) — 验收启动时 `web/dist/index.html` 不含「结构还原 / 下次发送 / 企业微信 · markdown」。已 `make web-build` 并纳入本轮提交，否则插件仍是旧 Banner 预览。
3. **[P3] 全局预览每渠道块仍带全部渠道指标** (visual) — 列内 `ai_1`/`ai_2` 标题分开，但块内同时出现 Codex 与 Claude。布局验收不失败；与「单渠道窗口对齐 identity」的发送路径可能仍不一致。

## Diagnosis Details

all passed against T08 硬阈值 except the 390 default-scroll warn (reproduced: wrap.scrollWidth=1080, clientWidth=390, initial scrollLeft=690; setting scrollLeft=0 shows 企业微信). No product change in this T08 pass.

## Evidence Index

- Contract JSON: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/check-items.json`
- Screenshots: `preview-desktop-3col.png`, `preview-desktop-2col.png`, `preview-390-3col.png`, `preview-390-scroll-left.png`, `list-next-fire.png`, `fail-notifications.png`
- Facts: `t08-facts.json`（本地，不进 git）
- Driver: `t08_preview.py`（本地，不进 git）
- Fixture: `MAPPER_ACCEPTANCE_SERVE=1 go test . -run '^TestAdminAcceptanceServe$'` on `127.0.0.1:31819`

## coverage_note

T08 只跑计划验收任务的 visual/manual。unit/integration 引用 T01–T07，不复跑。无 playwright MCP，用 Python Playwright + 本机 Chrome。目标是回环夹具不是 docker CPA / agent.colinwoo.com。a11y 与性能未要求已裁剪。standard 档不对 pass 做证据审计。真实三平台未授权 → unverified / manual-pending。
