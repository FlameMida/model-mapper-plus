# Keeper API Key 别名接入实施计划

> 执行：按 executing-plans 从 T00 顺序执行。用户要求先记录已批准计划再执行。本目录是会话中已批准计划的落盘版本；progress.yaml 是执行状态源。
> 偏差：路径/实现细节按既有契约就地修正；改变已批准产品行为须先说明。

**目标**：两个 Key 选择器展示 Keeper 别名，绑定编辑框支持主动同步后保存。
**Spec**：`.spec-dev/2026-09-05-01-keeper-key-aliases/spec/keeper-key-aliases-design.md`
**技术栈**：Go 1.26、CPA SDK v7.2.119、React 19、Semi UI、Vitest；HTTP/Cookie 使用 Go 标准库。
**架构**：Keeper → 插件后端认证/缓存 → CPA 管理接口 → 共享 Key 选项；同步只改草稿 alias，保存复用绑定链路。
**设计原则**：最简实现、模块化、复用现有依赖和已批准状态契约；不引入数据库依赖或推理路径网络 IO。

## 全局约束

- 新配置在 CPA 插件设置/YAML，不在 state；密码只通过环境变量。
- 成功 TTL 60 秒、总请求期限 5 秒、最多一次登录重试、并发合并。
- 旧输入与新 Key 的编辑安全优先于迟到同步响应。
- 单向读取 Keeper；本地 alias 只有保存绑定后持久化。
- 工作基线 `f1d3d35ac4389d4223ac20e7f6ee9699d329c936`。
- 实现与提交/推送分开；开发验收后用户已明确授权提交、推送和发版，详见 progress.yaml。

## 相关测试范围

基线：`go test ./...`、`npm --prefix web ci`、`npm --prefix web run typecheck`、`npm --prefix web test`。
任务：T01/T02 按 Go 测试前缀；T03/T04 按新增前端文件；最终全量测试、race、vet、web-build、packager 和真实 Linux CPA/Keeper。

| 任务 | 依赖 | 消费接口 | 产出接口 |
|---|---|---|---|
| T00 | 无 | Git 基线 | 隔离 worktree 与测试结果 |
| T01 | T00 | Keeper settings/login | newKeeperClient(baseURL, passwordEnv string) (*keeperClient, error); (*keeperClient).fetch(ctx context.Context) ([]keeperAlias, error) |
| T02 | T01 | keeperClient.fetch | keeperAliasesForConfig(cfg Config, force bool) keeperAliasesResponse; GET/POST keeper 管理接口 |
| T03 | T02 | KeeperAliasesResponse; StateResponse | buildKeyOptions(keys, bindings, aliases): KeyOption[]; useKeyOptions(bindings, enabled): KeyOptionsState; ApiKeySelect |
| T04 | T03 | KeyOptionsState.refreshAliases(): Promise<KeeperAliasesResponse> | 编辑框同步、保存及竞态保护 |
| T05 | T04 | 前端/Go 测试和构建 | CI、配置文档、嵌入 UI |
| T06 | T05 | 全部功能 | 审查与真实联调验收证据 |
| T07 | T06 | 审查/验收结论 | 交付记录、工作区集成及资源清理 |
