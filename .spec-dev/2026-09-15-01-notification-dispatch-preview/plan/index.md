# 通知调度、预览与渠道粒度 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。任务状态由 `plan/progress.yaml` 跟踪（唯一状态源；任务文件步骤用「**步骤 N:**」标题式、不含复选框）；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：修好用量通知到期发送、人类友好下次时间、认证渠道粒度、三栏预览、全局选填用户 ID 与飞书成员姓名。

**Spec**：`.spec-dev/2026-09-15-01-notification-dispatch-preview/spec/notification-dispatch-preview-design.md`（status: active）

**架构**：删除进程 `lastFire`，bbolt clock 记上次成功发送。每次 tick 读 `loadedStateSnapshot()`。全局循环独立于 Key 列表（认证渠道 × 全局通知）；Key 循环仅专属通知。采集改读 `auth_files_composition` ∪ `ai_provider_composition`。预览 Semi Modal 三栏；job 合并键含渠道 identity。

**技术栈**：Go 1.26 c-shared 插件、既有 bbolt、keeperClient、React 19 + Semi Design。不新增依赖。

**关联 skill**：executing-plans（整体编排）；test-driven-development（T01–T07 五步）；test-strategy（fast=任务内 TDD，final=T08 视觉，manual=真实三平台）；using-git-worktrees（T00）；acceptance-qa（T08）；semi-design-guide（T07 Modal）；go（Go 测试惯例）。

**设计原则**：本计划遵循 spec-dev 设计原则（不留向后兼容垫片 / 最简实现 / 分层构建 / 不以未完成复杂性换可工作产品 / 模块化 / 优先成熟库 / 优先已有依赖 / 长期架构决策）；任务与代码不得违反，冲突时停下向计划作者确认。不新建第二调度器。

## 全局约束

- 运营时区固定 `NotificationLocation`（东八区）；展示 `YYYY-MM-DD HH:mm:ss`。
- 完整 API Key 只用于进程内与 `/usage/api-keys/settings` 比对，不进通知正文、投递记录、审计。
- Keeper 失败闭码，不发零值、不更新 clock。
- 占比未知显示「未知」，不用 0%；Key 级占比分母为渠道全站 tokens。
- 全局用户 ID 选填；仅 at_all 才 @所有人。Key 级仍必填 ID 并剥离 at_all。
- 提交信息 `feat(TN)/test(TN)/fix(TN)/chore(TN):`。

## 相关测试范围

- 修改/新增：`notification_schedule_test.go`、`notification_store_test.go`、`notification_service_test.go`、`notification_keeper_test.go`、`notification_template_test.go`、`notification_types_test.go`、`notification_management_test.go`、`notification_members_client_test.go`、`web/src/components/NotificationEditor.test.tsx`、`web/src/panels/NotificationsPanel.test.tsx`、`web/src/panels/KeyNotificationsTab.test.tsx`、`web/src/components/PlatformIdentityEditor.test.tsx`。
- 一层关联：`management_test.go`、`management_audit_test.go`。
- 命令：`go test . -count=1 -run '^Test(Notification|ManagementNotification|Keeper)'`；`npm --prefix web run typecheck && npm --prefix web test -- --run src/components/NotificationEditor.test.tsx src/panels/NotificationsPanel.test.tsx src/panels/KeyNotificationsTab.test.tsx src/components/PlatformIdentityEditor.test.tsx`。
- 本节约束 T00 基线与各任务步骤 4；最终任务全量验证不受此限。

## 任务导航

| 任务 | 依赖 | 消费接口 | 产出接口 |
|---|---|---|---|
| T00 | — | — | 隔离 worktree |
| T01 | T00 | `nextTrigger`、`jobKey`、`UpsertJob` | `dueAt(sched, lastSuccess, now, loc) (due bool, next time.Time)`；`formatNextFire(t time.Time) string`；`notificationJob.Channel string`；`jobKey` 含 Channel；`store.Get/SetClock(notificationID, scope, channel string, t time.Time)` |
| T02 | T01 | T01 产出；`loadedStateSnapshot`；`effectiveNotifications` | `scheduleTick` 全局渠道循环 + Key 专属循环；`identityFor(st, nid, fingerprint, kind) (PlatformIdentity, bool)`；无 `lastFire` |
| T03 | T02 | `keeperStatsSource` | `collectAuthChannels(ctx, apiKey string) ([]channelStats, error)`；Key 级 `api_key_id` 映射；占比用全站分母 |
| T04 | T03 | `renderMessage` | 分行渲染；全局单渠道窗口按 identity |
| T05 | T04 | `validateNotificationEntity`；`dispatchManagement` | 全局空 UserIDs 合法；preview `{platforms:[{kind,text,warnings}]}`；test-send 渠道 fan-out；`next_fire` 人类友好 |
| T06 | T00 | `fetchFeishuMembers` | 空名走 `users/batch`；选项无「 · {id}」空名 |
| T07 | T05,T06 | preview API；`next_fire` | Semi Modal 三栏；列表下次发送；空列表文案；成员姓名 |
| T08 | T07 | — | 验收任务（acceptance-qa） |
| T09 | T08 | — | 合并、取代回写、清理、sync_commit |

**体量说明**：任务文件之和可能略超实现 diff，因 T00/T08/T09 为固定编排票。
