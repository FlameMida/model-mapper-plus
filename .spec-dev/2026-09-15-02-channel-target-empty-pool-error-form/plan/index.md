# 渠道定向池空错误形态（503→429 + 结构化错误体）实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。任务状态由 `plan/progress.yaml` 跟踪（唯一状态源；任务文件步骤用「**步骤 N:**」标题式、不含复选框）；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：定向池空错误从 HTTP 503 匿名 JSON 改为 HTTP 429 + 结构化错误体（type/code 不变、message 可重试语义、detail 聚合计数且脱敏），并同步旧 spec 部分取代回写与用户文档。

**Spec**：`.spec-dev/2026-09-15-02-channel-target-empty-pool-error-form/spec/channel-target-empty-pool-error-form-design.md`

**架构**：`handleSchedulerPick` 池空分支改返回 `pluginMethodError{HTTPStatus: 429, Message: channelTargetUnavailableBody(...)}`；错误体经宿主 ABI→`rpcError`→`WriteErrorResponse` 链原样透传为客户端响应体（链路已在设计期逐跳核实）。红线「绝不使用池外凭据」与 `type`/`code`=`auth_not_found` 断言不变。

**技术栈**：Go 单包 c-shared 插件（标准库 `encoding/json`、`net/http`）；无新依赖。

**关联 skill**：executing-plans（执行方式与收尾审查）、test-driven-development（T01 五步纪律；spec 测试落点声明见其任务接口块）、using-git-worktrees（T00 隔离）、acceptance-qa（T03 验收票）。

**设计原则**：本计划遵循 spec-dev 设计原则（不留向后兼容垫片 / 最简实现 / 分层构建 / 不以未完成复杂性换可工作产品 / 模块化 / 优先成熟库 / 优先已有依赖 / 长期架构决策，定义见 `spec-dev/writing-plans/references/design-principles.md`）；任务与代码不得违反，冲突时停下向计划作者确认。

## 全局约束

- 「绝不使用池外凭据」红线不变（spec：MODIFIED Requirement 1/2）。
- `error.type`/`error.code` 保持 `auth_not_found`（断言兼容）。
- `error.detail` 仅聚合计数（`candidates`、`binding.suppliers`、`binding.auth_ids`）；错误体全文不得出现凭据 ID、供应商名、客户端 key 任何形态。
- message 必须是合法 JSON（宿主原样透传前提）。
- 不提供 `Retry-After`；不修改宿主 CPA；无新依赖、无前端改动、无 state 结构变更。

## 相关测试范围

单包仓库（package main），无法按 import 划分；范围 = 本特性修改的 `scheduler_test.go` + 单包内直接调用被改入口 `handleMethod` 的既有分发测试（`main_test.go` 的 `TestHandleMethod*`）。

命令：`go test . -count=1 -run 'TestChannelTarget|TestHandleMethod'`

静态快检：`go vet ./...`（无独立 typecheck；`make vet` 同义）。

---

## 任务导航表

| 任务 | 依赖 | 消费接口 | 产出接口 |
|---|---|---|---|
| T00 | — | — | 隔离工作区 `.worktrees/plan/2026-09-15-02-channel-target-empty-pool-error-form`（实际路径记入 progress.yaml notes） |
| T01 | T00 | `handleMethod(pluginabi.MethodSchedulerPick, raw)`（既有）；`ChannelTarget{Suppliers, AuthIDs []string}`（既有，state.go；经 `*ChannelTarget` 使用） | `channelTargetUnavailableBody(candidateCount int, target *ChannelTarget) string`（main.go，包内）；池空错误 `HTTPStatus=http.StatusTooManyRequests` |
| T02 | T01 | T01 产出的新错误形态语义（用于旧 spec 文本同步措辞） | 旧 spec 部分取代回写完成（2 个 Requirement Superseded 标注 + 关联文本同步 + pending 行移除）；host-version-compat.md 历史注记 |
| T03 | T01, T02 | T01 交付的 429 错误形态（真机验证对象） | 验收报告落盘 `acceptance/` |
| T04 | T00-T03 | T00 记录的实际工作区/分支；T02 回写结果（步骤 3 核对） | 合并回来源分支；spec `sync_commit` 锚定；台账清理 |

**体量说明**：任务文件之和（约 450 行）大于预期代码改动（约 120 行）——T02 的旧 spec 回写清单必须逐处给出文本锚点（约 18 处，执行者无法从 spec 推断旧文件全部 503 位置），T00/T04 为固定模板全额保留。

计划生成时 spec 有一处随计划精确化的修改（取代回写形制二分，commit `5d91fdd`），已列入交接展示。
