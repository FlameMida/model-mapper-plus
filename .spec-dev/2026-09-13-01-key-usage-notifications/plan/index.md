# Key 渠道用量通知 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。任务状态由 `plan/progress.yaml` 跟踪（唯一状态源；任务文件步骤用「**步骤 N:**」标题式、不含复选框）；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：为 mapper 增加 Key 渠道用量通知——每 Key 多条独立通知 + 全局默认通知实体，由内置通知服务按计划从 Keeper 采集统计、按分节风格渲染消息并投递到企业微信/飞书/钉钉群机器人。

**Spec**：`.spec-dev/2026-09-13-01-key-usage-notifications/spec/key-usage-notifications-design.md`（status: active）

**架构**：通知配置存入现有 state（`State.Notifications` 全局实体 + `KeyBinding.Notifications[]`）；运行数据（快照/覆盖/任务/投递）入独立 bbolt 文件。`notificationService` 在 register/reconfigure 时停旧启新、shutdown 时取消并等待；调度器按计划纯函数触发，发送前持久化任务、按 (key 指纹, 平台, 周期) 合并待发任务。管理 API 挂现有 `dispatchManagement`；前端在 Key 弹窗加「通知」页签（Table + SideSheet 编辑抽屉）并新增「通知」顶级面板。

**技术栈**：Go 1.26（c-shared 插件）+ go.etcd.io/bbolt（版本执行时 `go get go.etcd.io/bbolt` 锁定，bolt.Open/DB.Update/Bucket API v1.3+ 稳定）；复用 `keeperClient`（closed error codes、5s 期限、flight 合并）。前端 React 19.3 + @douyinfe/semi-ui 2.103 + @dnd-kit/core 6.3.1 + @dnd-kit/sortable 10.0.0 + Vite 8.3 + TS 7.0.2 + vitest 5 + jsdom 30。

**关联 skill**：executing-plans（整体验排）；test-driven-development（T02–T15 行为票的五步纪律）；test-strategy（Lane 处方：fast=任务内 TDD、final=T16、manual=T16 记录 manual-pending）；using-git-worktrees（T00）；acceptance-qa（T16 触发）；semi-ui-skills（T11–T15 交互前用 semi-mcp 查组件源码；UI 组件测试沿用 canvas shim + Select motion 禁用惯例）。

**设计原则**：本计划遵循 spec-dev 设计原则（不留向后兼容垫片 / 最简实现 / 分层构建 / 不以未完成复杂性换可工作产品 / 模块化 / 优先成熟库 / 优先已有依赖 / 长期架构决策）；任务与代码不得违反，冲突时停下向计划作者确认。模块判据要点：平台适配器是真实 adapter（三家真实差异：企微无加签/飞书 open_id/钉钉加签+内部群）；模板渲染与调度计算是纯函数以便表驱动测试；不为此新增更多抽象层。

## 全局约束

- 运行数据 bbolt 文件与 state_file 同目录（默认名 `model-mapper-plus-notifications.db`，`<state 去掉 .json>-notifications.db`）；配置 state 与运行库不互为恢复来源。
- 敏感配置文件权限 0600（bbolt 由其文件创建模式保证，state 沿用 `atomicWriteState`）。
- 完整 API Key 不进任何管理响应；Webhook/签名密钥在管理响应中明文回显，但不得进入通知正文、投递日志、审计投影（spec 2026-09-13 确认）。
- Keeper IO 不持有 state 锁、推理锁或 bbolt 事务；单次 Keeper 请求链路 deadline 5 秒（沿用 `keeperAuthNamesForConfig` 模式）。
- 错误码封闭（沿用 `keeperError` 风格）：URL/响应体/凭据不逃逸到管理响应或日志。
- 统计周期边界与展示时区统一用固定 `Asia/Shanghai`（设计补充：与 `audit.go` 运营口径一致；docker 内 UTC 时错位是已知坑）。
- Token 用 K/M/B 缩写展示，原始整数用于计算与存档；占比未知显示「未知」、不显示 0%。
- 审计日期/覆盖标注：历史缺失明确标注，缺失不计为零。
- 平台消息单条字节上限按平台官方限制裁剪（企微 markdown 4096 字节、飞书 text/post 30KB、钉钉 markdown 20000 字节），超限拆分；适配细节在 T07 锁定。
- 每次 state 通知配置变更 bump bbolt `config_revision`；版本不符的迟到结果丢弃（spec 热重配 Scenario）。
- 前端依赖升级至最新稳定版（react/react-dom 19.3.0、semi-ui/semi-icons 2.103.0、vite 8.3.0、typescript 7.0.2、@vitejs/plugin-react 6.1.1、vitest 5.0.0、jsdom 30.0.1，执行时以 `pnpm view` 复核为准）；新增 @dnd-kit/core+sortable。升级以既有前端测试、typecheck、构建全绿为验收基线。
- 提交信息格式沿用 `feat(TN)/test(TN)/chore(TN):` 前缀。

## 相关测试范围

- 本特性新增 Go 测试：`notification_types_test.go`、`notification_schedule_test.go`、`notification_store_test.go`、`notification_keeper_test.go`、`notification_template_test.go`、`notification_platform_test.go`、`notification_service_test.go`、`notification_management_test.go`。
- 被改源文件直接关联的既有测试（一层）：`state.go`→`keybinding_test.go`、`management_test.go`；`management.go`→`management_test.go`、`management_audit_test.go`；`abi_cgo.go`→`host_lifecycle_test.go`。
- 前端：新增/修改组件的 `*.test.tsx` + `web/src/api.ts` 既有消费方测试。
- 执行命令——Go：`go vet . && go test . -count=1 -run '^Test(Notification|KeyBinding|Management|Audit|Host|Plugin)'`；前端：`npm --prefix web run typecheck && npm --prefix web test`（本仓库 npm shim 实际转发 pnpm，命令不变）。
- 本节约束 T00 基线与各任务步骤 4；最终任务全量验证不受此限（含 `go test -race ./...`、`npm --prefix web run build`）。

---

```yaml spec-dev-parallel
parallel:
  tasks:
    T02:
      writes:
        - "notification_types.go"
        - "notification_types_test.go"
        - "state.go"
      resources: []
    T03:
      writes:
        - "notification_schedule.go"
        - "notification_schedule_test.go"
      resources: []
    T04:
      writes:
        - "notification_store.go"
        - "notification_store_test.go"
        - "go.mod"
        - "go.sum"
      resources: []
    T05:
      writes:
        - "notification_keeper.go"
        - "notification_keeper_test.go"
      resources: []
    T06:
      writes:
        - "notification_template.go"
        - "notification_template_test.go"
      resources: []
    T07:
      writes:
        - "notification_platform.go"
        - "notification_platform_wecom.go"
        - "notification_platform_feishu.go"
        - "notification_platform_dingtalk.go"
        - "notification_platform_test.go"
      resources: []
    T08:
      writes:
        - "notification_service.go"
        - "notification_service_test.go"
        - "main.go"
        - "abi_cgo.go"
      resources: []
    T09:
      writes:
        - "notification_management.go"
        - "notification_management_test.go"
        - "management.go"
      resources: []
    T10:
      writes:
        - "web/src/notifications.ts"
        - "web/src/notifications.test.ts"
        - "web/src/api.ts"
      resources: []
    T11:
      writes:
        - "web/src/components/NotificationModulesEditor.tsx"
        - "web/src/components/NotificationModulesEditor.test.tsx"
        - "web/src/components/NotificationScheduleEditor.tsx"
        - "web/src/components/NotificationScheduleEditor.test.tsx"
      resources: []
    T12:
      writes:
        - "web/src/components/PlatformIdentityEditor.tsx"
        - "web/src/components/PlatformIdentityEditor.test.tsx"
      resources: []
    T13:
      writes:
        - "web/src/components/NotificationEditor.tsx"
        - "web/src/components/NotificationEditor.test.tsx"
      resources: []
    T14:
      writes:
        - "web/src/panels/KeyNotificationsTab.tsx"
        - "web/src/panels/KeyNotificationsTab.test.tsx"
        - "web/src/panels/KeysPanel.tsx"
      resources: []
    T15:
      writes:
        - "web/src/panels/NotificationsPanel.tsx"
        - "web/src/panels/NotificationsPanel.test.tsx"
        - "web/src/App.tsx"
        - "web/dist/index.html"
      resources: []
```

## 任务导航表

| 任务 | 依赖 | 消费接口 | 产出接口 |
|---|---|---|---|
| T00 | — | — | worktree 就绪 + 基线 SHA |
| T01 | — | — | web/package.json 依赖升级完成、既有测试全绿基线 |
| T02 | — | — | `NotificationSettings`/`Notification`/`validateNotifications(st)`/`notificationsOf(st, key)`（notification_types.go；state.State 扩展字段） |
| T03 | T02 | T02 的 `NotificationSchedule`/`ModuleKind`/`PeriodKind` | `nextTrigger(sched NotificationSchedule, after time.Time, loc *time.Location) (time.Time, bool)`、`periodRange(m ModulePeriodStat, now time.Time, loc) (start, end time.Time, label string)` |
| T04 | T02 | T02 类型 | `openNotificationStore(path) (*notificationStore, error)`、`(*notificationStore).AppendJob/TransitionJob/MergeJobs/AppendDelivery/SnapshotList/Coverage`（结构见任务文件接口块） |
| T05 | T02,T03 | T02 类型；T03 `periodRange`/`NotificationLocation`；`keeperClient.authenticatedRequest` | `type keeperStatsSource struct` + `(*keeperStatsSource).collect(ctx, key, period) (periodStats, error)`、`identifyAuthIndex`（notification_keeper.go） |
| T06 | T02,T03,T05 | T02 `Notification`；T03 `periodRange`/`NotificationLocation`；T05 `periodStats`/`channelStats`/`windowStat`/`periodKeyOf` | `renderMessage(n Notification, data statsData, loc) (string, renderWarnings)`、`abbreviateTokens(n int64) string`、`channelShare(shares) (float64, bool)` |
| T07 | T02,T04 | T02 `PlatformIdentity`；T04 `deliveryAccepted/deliveryFailed/deliveryUnknown` 常量 | `platformAdapter` 接口、`buildAdapter(p PlatformKind, target PlatformIdentity) platformAdapter`、`(*adapter).send(ctx, msg outboundMessage) deliveryResult` |
| T08 | T02,T03,T04,T05,T06,T07 | 上述全部 | `startNotificationService(cfg, store, deps) (*notificationService, error)`、`(*notificationService).Stop(ctx)`、`serviceDeps`（时钟/HTTP 替身边界） |
| T09 | T02,T04,T06,T08 | T02 校验、T04 投递查询、T06 渲染、T08 服务状态 | management 路由：`GET/PUT /notifications/settings`、`GET /notifications/status`、`POST /notifications/preview`、`POST /notifications/test-send`、`GET /notifications/deliveries`、`POST /notifications/deliveries/retry`；`handleNotificationManagement(req)` |
| T10 | T09 | T09 契约 | `web/src/notifications.ts` 类型 + `api.ts` 的 `api.notifications.*` 方法组 |
| T11 | T01,T10 | T10 类型 | `NotificationModulesEditor`（dnd-kit 拖拽 + 勾选 + 周期 Select）、`NotificationScheduleEditor`（每隔/每月/每年三态）props 契约 |
| T12 | T01,T10 | T10 | `PlatformIdentityEditor`（企微/飞书/钉钉 card Tabs、明文回显、企微无签名说明、必填校验） |
| T13 | T11,T12 | T11/T12 | `NotificationEditor` SideSheet 抽屉（两 Tab、继承开关、名称唯一、预览过期提示、footer） |
| T14 | T13 | T13、T10 | KeysPanel「通知」页签：Table 列表/行内开关/测试发送/空态/行展开投递 |
| T15 | T10,T11,T12 | T10/T11/T12 | `NotificationsPanel`（全局默认通知实体 + 服务状态 + 投递总查询）+ App 导航项 |
| T16 | T08,T09,T14,T15 | 全部 | 验收报告落盘 `acceptance/`；矩阵 final 行证据 + manual 行 manual-pending |
| T17 | T00-T16 | 全部 | 合并回来源分支、sync_commit 锚定、资源清理 |

**体量说明**：任务文件总行数 ≈ 预期代码改动行数（后端 8 文件 + 前端 6 文件 + 测试），T07/T08/T13 体量接近上限属预期，因三平台适配与抽屉表单片段密度高。
