# 可审计的管理操作与 Keeper 认证名称同步 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境从 T00 起按序执行。状态以 plan/progress.yaml 为唯一来源，任务正文不使用复选框。
>
> **偏差处理**：路径笔误、明确实现细节就地修正并记录；改变已批准行为、公开接口、数据结构或测试边界时停止受影响任务并确认，不猜测修改。

**目标**：交付直接写每日文件的管理操作审计，以及纳入该审计的 Codex 认证名称立即同步功能。
**Spec**：[已批准设计](../spec/admin-audit-keeper-names-design.md)
**架构**：管理变更互斥门先落盘 start，再执行既有修改，再落盘 finish；GET 查询合并为操作。Keeper 只经现有 HTTP/Cookie 边界访问。前端分别消费名称服务和审计服务。
**技术栈**：仓库现有 Go/CPA SDK、React 19、Semi Design、Vitest；HTTP、JSONL、时间和散列均用 Go 标准库，不新增生产依赖。
**关联 skill**：
- spec-dev:executing-plans：实施编排；/Users/maverick/.codex/plugins/cache/spec-agent-skills/spec-dev/8.6.0/skills/executing-plans/SKILL.md。
- spec-dev:using-git-worktrees：T00 与 T08；同插件 skills/using-git-worktrees/SKILL.md。
- spec-dev:test-driven-development：T01—T06 公共行为红绿；同插件 skills/test-driven-development/SKILL.md。
- spec-dev:test-strategy：所有测试 Lane；同插件 skills/test-strategy/SKILL.md。
- spec-dev:acceptance-qa：T07；同插件 skills/acceptance-qa/SKILL.md。
- go：Go 编写和测试时；/Users/maverick/.codex/skills/go/SKILL.md。

**设计原则**：不留无需求的兼容垫片、最简实现、分层构建、不以未完成复杂性换可工作产品、模块化、优先成熟库与已有依赖、长期架构决策。现行 state/渠道/API Key 别名契约按 spec 的分面共存保护。判据见 /Users/maverick/.codex/plugins/cache/spec-agent-skills/spec-dev/8.6.0/skills/writing-plans/references/design-principles.md。

## 全局约束

- 只写本仓库；/Users/maverick/CLIProxyAPI 与 /Users/maverick/cpa-usage-keeper 均只读。禁止线上部署、真实名称改写、推送、发版。
- 日志直接追加 JSONL，不进入 state、不延迟导出、不自动重放操作；禁止修改 Scheduler/模型路由/认证优先级。
- 北京时间 Asia/Shanghai；目录 0700，日文件 0600；同一操作跨午夜仍归开始日文件。
- 名称最多 50 个 Unicode 字符，空字符串清空；同步立即生效，取消绑定不撤销。
- Keeper IO 在配置/state 锁外；总期限 5 秒，缓存 60 秒；PATCH 不因超时或 5xx 自动重试。
- 原 API Key “从 Keeper 同步”仍只填草稿；不和新增认证“同步到 Keeper”混淆。
- Begin 不能完整 Write+Sync 则 HTTP 503 且零业务副作用；Finish 失败保留真实业务结果及 audit 警告。
- 后端错误 envelope 的 status 与 HTTP status 分开判定；HTTP 200 不等于 Keeper 同步成功。
- 所有命令使用 rtk。示例 cwd 为实施工作区根；前端命令使用 --prefix web。
- 用户已明确选择并发执行；采用v1 parallel扩展，无集成组。主线程独占进度、集成和清理。为隔离首批写集合，T03的路由挂载移至T04，不改变最终公开协议。

## 文件职责

| 文件 | 职责 |
|---|---|
| audit.go、audit_query.go | Append/Sync、日界与 ticket、读取配对、校验分页 |
| audit_management.go、management.go、main.go | 管理变更门、脱敏差异、响应提示、重配协调 |
| keeper_client.go、keeper_auth_names.go、management_keeper_names.go | 认证复用、名称读缓存、即刻写回 |
| web/src/api.ts、keeperAuthNames.ts | 公开类型、API 调用、精确索引关联 |
| ChannelTargetEditor、KeeperAuthNameEditor、KeysPanel | 名称显示、搜索、独立编辑与竞态隔离 |
| App、AuditPanel、RulesPanel、KeysPanel | 审计页与各写入口审计异常提示 |
| audit*_test.go、management_audit_test.go、keeper_auth_names_test.go、management_keeper_names_test.go | 公共管理 API、真实文件及受控 Keeper 验证 |
| 新增/修改的前端测试、README、CLAUDE、web/dist/index.html | 交互验证、使用说明、交付 UI |

## 相关测试范围

当前没有测试影响分析工具；范围由自有新增/修改测试及一层直接消费者确定。Go 同包测试使用名称筛选，完整包源码仍参与编译。
- T00 基线 Go：rtk go test . -run 'Test(Management|Keeper|Reconfigure|State|AtomicWrite|ReadState|ResolveState)' -count=1。
- T00 基线前端：rtk npm --prefix web test -- src/api.test.ts src/App.test.tsx src/auditFixes.test.tsx src/channelCredentials.test.ts src/useKeyOptions.test.tsx src/keyOptions.test.ts src/components/RuleSetEditor.test.tsx src/components/ChannelTargetEditor.test.tsx src/panels/KeysPanel.test.tsx src/panels/KeysPanel.delete.test.tsx src/panels/KeysPanel.blocked.test.tsx src/panels/KeysPanel.alias-sync.test.tsx src/panels/KeysPanel.channel-target.test.tsx src/panels/RulesPanel.test.tsx。
- 特性新增：audit_test.go、audit_query_test.go、management_audit_test.go、keeper_auth_names_test.go、management_keeper_names_test.go；web/src/keeperAuthNames.test.ts、components/KeeperAuthNameEditor.test.tsx、panels/KeysPanel.auth-names.test.tsx、panels/AuditPanel.test.tsx。T01—T06 仅运行各票列出的目标与自有测试。
- 静态快检：rtk npm --prefix web run typecheck，来自 web/package.json；Go 每票测试自带编译检查。
- 最终一次：Go 全测/race/vet、前端全测/typecheck/build、打包脚本测试，见 T08。
- T00 新测试文件尚不存在时只跑上面现有测试，不能记新增行为 PASS。所有 red/green 日志保留 stdout/stderr 和 exit，编译失败/零测试/SKIP 不算行为红。
- 场景执行表：S1/S2→T03/T05；S3/S4→T04/T05；S5/S6→T02；S7→T01/T02；S8/S9→T02/T04；S10→T01/T06；S11→T02/T04。

## 任务导航

| 任务 | 依赖 | 消费接口 | 产出接口 |
|---|---|---|---|
| T00 | 无 | 已批准 spec、Git 来源 | 真实隔离绑定、基线 SHA 与日志 |
| T01 | T00 | dispatchManagement(req)、临时日文件 | beginAudit(statePath string, event auditEvent) (auditTicket, error); finishAudit(ticket auditTicket, event auditEvent) error; GET /audit |
| T02 | T01 | beginAudit/finishAudit、既有四个变更 handler | withAuditedManagement(req pluginapi.ManagementRequest, run func() (pluginapi.ManagementResponse, auditResult)) pluginapi.ManagementResponse; managementMutationMu |
| T03 | T00 | keeperClient、GET Keeper identities | keeperAuthNamesForConfig(cfg Config, force bool) keeperAuthNamesResponse; managementKeeperAuthNames(force bool) pluginapi.ManagementResponse（路由由T04挂载） |
| T04 | T02, T03 | withAuditedManagement、名称读取handler | managementPatchKeeperAuthName(req pluginapi.ManagementRequest) pluginapi.ManagementResponse; GET/POST/PATCH /keeper/auth-names[/refresh] |
| T05 | T03, T04 | 名称 GET/PATCH wire 协议 | api.getKeeperAuthNames/refreshKeeperAuthNames/patchKeeperAuthName; KeeperAuthNameEditor; 渠道名称显示 |
| T06 | T02, T05 | GET /audit、mutation audit 字段 | api.getAudit(date?: string, page?: number, pageSize?: number): Promise<AuditPage>; ManagementAPIError(message: string, audit?: AuditMeta); AuditPanel |
| T07 | T05, T06 | 完整 UI/管理 API | 本地受控浏览器验收记录 |
| T08 | T00-T07 | 已完成前序、真实来源/资源 | 全量验证、合并、清理、sync_commit、最终状态 |

## 公开协议与测试边界

```yaml spec-dev-parallel
parallel:
  tasks:
    T01:
      writes:
        - "audit.go"
        - "audit_query.go"
        - "audit_test.go"
        - "audit_query_test.go"
        - "management.go"
        - "management_test.go"
      resources: []
    T02:
      writes:
        - "audit_management.go"
        - "management_audit_test.go"
        - "management.go"
        - "main.go"
        - "management_api_test.go"
      resources: []
    T03:
      writes:
        - "keeper_auth_names.go"
        - "management_keeper_names.go"
        - "keeper_auth_names_test.go"
        - "management_keeper_names_test.go"
        - "keeper_client.go"
        - "keeper_client_test.go"
        - "keeper_aliases.go"
      resources: []
    T04:
      writes:
        - "keeper_auth_names.go"
        - "management_keeper_names.go"
        - "management.go"
        - "keeper_auth_names_test.go"
        - "management_keeper_names_test.go"
        - "management_audit_test.go"
      resources: []
    T05:
      writes:
        - "web/src/keeperAuthNames.ts"
        - "web/src/keeperAuthNames.test.ts"
        - "web/src/components/KeeperAuthNameEditor.tsx"
        - "web/src/components/KeeperAuthNameEditor.test.tsx"
        - "web/src/panels/KeysPanel.auth-names.test.tsx"
        - "web/src/api.ts"
        - "web/src/api.test.ts"
        - "web/src/components/ChannelTargetEditor.tsx"
        - "web/src/components/ChannelTargetEditor.css"
        - "web/src/panels/KeysPanel.tsx"
      resources: []
    T06:
      writes:
        - "web/src/panels/AuditPanel.tsx"
        - "web/src/panels/AuditPanel.css"
        - "web/src/panels/AuditPanel.test.tsx"
        - "web/src/App.tsx"
        - "web/src/App.test.tsx"
        - "web/src/api.ts"
        - "web/src/api.test.ts"
        - "web/src/panels/RulesPanel.tsx"
        - "web/src/panels/RulesPanel.test.tsx"
        - "web/src/panels/KeysPanel.tsx"
        - "web/src/panels/KeysPanel.auth-names.test.tsx"
      resources: []
```

精确 JSON 协议直接继承 spec“接口与实现边界”。API 主测试落点为 dispatchManagement/handleManagement，React 使用可见交互；允许替換 Keeper HTTP、fetch、时钟及 Write/Sync 故障，不 mock 审计分类/匹配逻辑。表内新函数是跨任务实现接口，不要求逐一私有函数直测。

内部公共类型由 T01 定义：auditEvent 包含 version/operation_id/phase/occurred_at、actor/action/object_type/object_ref、outcome、changed（*bool）、changes（map[string]auditChange）、error_code；auditChange 的 Before/After 为 json.RawMessage。auditTicket 固定 ID、Path、StartedAt。T02 定义 auditResult{Outcome string; Changed *bool; Changes map[string]auditChange; ErrorCode string}；不得把整个 State 或请求对象塞进 event。

状态提交：各票先提交实现与证据，取得真实 SHA，再用 apply_patch 更新完整 progress.yaml，保留既有 notes/resources，并单独提交进度；不写引用自身未来提交的 SHA。原子状态保存沿 executing-plans 的既有文件写入方式，不在本计划创建通用进度工具。

并发执行时，上述进度和证据归档仅由主线程实施；implementer只提交writes内文件，日志/result写入其预登记claim专属临时目录，禁止编辑.spec-dev。T03测试在公开名称服务及管理响应handler边界先取得红绿，T04以dispatchManagement验证最终路由挂载；该接线迁移仅为消除management.go写冲突，原S1/S2管理API验收仍保留。

## 体量说明

每票控制在 200 行以内，只给关键改动和完整最小测试示例，不复制现行文件。计划未执行；所有测试结果均是预期值。

## 计划自检记录

- Spec覆盖：R1→T03/T05；R2→T04/T05；R3→T02/T04；R4→T01/T02/T04；R5→T01/T02/T06。S1—S11均有任务和断言，浏览器矩阵进入T07。
- 接口一致性：名称PATCH handler内部仅套一次审计；T02闭包返回明确auditResult，不能从HTTP200猜结果；T05/T06同时保留业务结果和audit提示。
- 粒度/依赖：T03名称读取独立于审计写入；T04同时消费两条接口；T05使用真实读写协议；T06共享响应类型；T07/T08为必要的验证与交付安全顺序。没有环、遗漏ID或无理由的集成组。
- 文件/路径：spec相对链接解析到同特性spec目录；所有进度Git命令使用完整仓库根相对路径；所有实施/清理操作绑定T00记录，不能删除复用资源。
- 结构检查：plan-index返回ok=true，exit0，9个任务与文件一致，所有任务低于200行（合计651行）。
- 进度检查：尝试plan-state返回“plan-state requires integration v2”，对本串行v1不适用，不计通过；随后使用Ruby YAML实际解析检查format_version、9项pending、current=null与资源台账，exit0。
- 占位符及Git空白检查无问题；新增测试示例补全了环境和数据定义。本轮未执行生成的任务、没有功能测试红绿或浏览器验收结果。
