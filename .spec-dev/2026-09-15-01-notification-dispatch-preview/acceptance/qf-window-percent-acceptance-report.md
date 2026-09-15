# Acceptance Report: 窗口百分比与重置卡到期行

> **Language / 语言**: 报告内容以对话语言填写；表头与结构标签保持英文。
> Time: 2026-09-15 | Triggered by: quick-fix 步骤 6（用户选择要验收） | Tier: standard
> Spec: `.spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md`（active）；相交仍有效 Requirement「渠道窗口与认证信息」来自 `.spec-dev/2026-09-13-01-key-usage-notifications/spec/key-usage-notifications-design.md`
> Evidence dir: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/`
> Spec status: active

## Overview

| Dimension | Execution | Pass | Fail | Warn | Unverified | Notes |
|-----------|-----------|------|------|------|------------|-------|
| unit | D (`go test`) | 3 | 0 | 0 | 0 | 渲染分行 + 缺测省略 + 既有窗口/张数 |
| integration | D (`go test` + httptest) | 4 | 0 | 0 | 0 | quota cache / reset-credits / CPA 降级 |
| e2e | — | — | — | — | 1 | 矩阵无独立 e2e 行；本次未改页面流程 |
| visual | — | — | — | — | 1 | 未触及预览弹窗布局；T08 报告不覆盖 |
| a11y | — | — | — | — | 1 | 已裁剪 |
| perf-web | — | — | — | — | 1 | 已裁剪 |
| perf-api | — | — | — | — | 1 | 已裁剪 |

命令：`go test . -count=1 -timeout 60s -v -run '^Test(RenderMessageWindowPercentsAndResetCardExpiry|RenderMessageOmitsUnknownWindowPercents|RenderMessageSectionLayout|ParseQuotaCacheProjectsWindowPercents|ParseResetCreditsKeepsAvailableExpiry|CollectWindowsFetchesResetCardExpiryWhenCacheHasCount|CollectWindowsFallsBackToCPAQuota|CollectWindowsParsesQuotaCache|CollectWindowsFetchesCodexResetCardsWhenCacheOmitsCount|ParseQuotaCacheAcceptsRealCodexRows)$'`
退出码：0；10 passed / 0 failed；0.432s。

## Requirement Coverage (when a spec exists)

| Matrix row (Scenario / check item) | Dimension | Status | Evidence |
|------------------------------------|-----------|--------|----------|
| 窗口百分比与重置卡到期分行（本轮新增 Scenario） | unit | pass | `qf-window-percent-tests.log` PASS `TestRenderMessageWindowPercentsAndResetCardExpiry` |
| 缺百分比不伪造 0% | unit | pass | `TestRenderMessageOmitsUnknownWindowPercents` |
| Keeper usedPercent / remainingFraction 投影 | integration | pass | `TestParseQuotaCacheProjectsWindowPercents` |
| 可用重置卡到期时间 | integration | pass | `TestParseResetCreditsKeepsAvailableExpiry` |
| cache 有张数仍拉 reset-credits | integration | pass | `TestCollectWindowsFetchesResetCardExpiryWhenCacheHasCount` |
| CPA remainingFraction 投影 | integration | pass | `TestCollectWindowsFallsBackToCPAQuota` |
| 5H/Weekly 缺失、重置倒计时、套餐和重置卡（09-13 矩阵相交行） | unit | pass | `TestCollectWindowsParsesQuotaCache` + `TestRenderMessageSectionLayout` |
| 每隔第二拍仍发送；轮询不滑动 | unit | unverified | 本次未触及调度，已裁剪 |
| 重启只补最近一次 | integration | unverified | 本次未触及 |
| 全局两渠道两条正文且不合并 | integration | unverified | 本次未触及 |
| 专属通知不取消全局渠道循环 | integration | unverified | 本次未触及 |
| 全局预览/试发按渠道堆叠或拆条 | integration | unverified | 本次未触及 fan-out |
| Key 级不含其他 Key；映射失败闭码 | integration | unverified | 本次未触及占比分母 |
| 定时使用实体 webhook | integration | unverified | 本次未触及 |
| 预览三栏、未启用不出现、不发送 | unit + 组件 | unverified | 本次未改预览 UI |
| 全局空用户 ID 可保存 | unit + 组件 | unverified | 本次未触及 |
| 飞书空名补齐或纯 ID | integration + 组件 | unverified | 本次未触及 |
| 列表人类友好时间 | 组件 | unverified | 本次未触及 |
| 桌面/窄屏预览弹窗 | visual | unverified | 变更面裁剪；既有 T08 见 `acceptance-report.md` |
| 真实三平台群消息 | manual | unverified | 未授权 webhook |

## Key Findings (by severity)

无阻塞项。相交的 unit/integration 行全部通过。

## Diagnosis Details

all passed, diagnosis skipped

## Evidence Index

- Contract JSON: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/qf-window-percent-check-items.json`（`validate-output.mjs acceptance-check-items` ok）
- Facts: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/qf-window-percent-facts.json`（sha256 `7e00d70f02932a7a061b21b63d4e6266321b90fc1ece9a81578f4362b6b63923`）
- Test log: `.spec-dev/2026-09-15-01-notification-dispatch-preview/acceptance/qf-window-percent-tests.log`
- Test files: `notification_template_test.go`, `notification_keeper_test.go`
- 既有 T08 visual：`acceptance-report.md` + `preview-desktop-3col.png` 等（本轮未复跑）

## coverage_note

按变更面只验通知正文的窗口百分比与重置卡到期行。调度、预览弹窗、飞书补名、人类友好时间、真实群投递未跑。e2e / visual / a11y / perf-* 已裁剪（无页面或性能改动；detect-env 无 Playwright/k6）。standard 档不对 pass 做证据审计。无本次创建的持久资源。
