# 验收报告：Responses body 思考强度路由

- 日期：2026-08-23（Asia/Shanghai）
- 关联 Spec：`.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`
- 验收对象：本次 `gpt-5.6-sol(xhigh)=>gpt-5.6-sol(medium)` 修复
- 结论：PASS（伴随 1 项非目标环境 WARN）

## 验收矩阵

| 维度 | 场景 | 结果 | 确定性证据 |
| --- | --- | --- | --- |
| 单元 | Responses body 中 `reasoning.effort=xhigh` 可命中虚拟模型后缀规则 | PASS | `TestHandleModelRouteMatchesCodexReasoningEffortFromBody` |
| 单元 | 显式模型后缀优先于 body；裸模型回退；未知 effort 不伪造后缀；仅 `openai-response` 读取 body | PASS | `TestRouteModelForRequestCodexEffortPrecedenceAndFallback` 的 4 个子用例 |
| 集成 | `model.route`、非流式 executor、流式 executor 使用同一候选模型逻辑 | PASS | 3 条定向 handler/executor 测试通过 |
| E2E | `/v1/responses` 非流式：客户端 `xhigh` 经插件规则改写为上游 `medium` | PASS | CPA v7.2.119 + 实际 Linux `.so` + 隔离假上游抓包 |
| E2E | `/v1/responses` 流式：同样改写为上游 `medium`，客户端收到完整 `response.completed` | PASS | 上游抓包 `stream=true, reasoning_effort=medium`；客户端事件完整 |
| E2E 边界 | 请求 `high` 不应误命中只声明 `xhigh` 的规则 | PASS | 上游抓包保持 `reasoning_effort=high` |
| 回归 | 全量测试、race、vet、diff whitespace | PASS | 所有命令退出码均为 0 |
| 视觉 / 可访问性 / 性能 | 本修复没有页面、交互或性能契约变化 | CROPPED | 与本次后端请求路由变更无关 |

## 真实宿主链路

验收链路：

`Responses 请求（xhigh） -> CPA /v1/responses -> model-mapper-plus.so -> OpenAI-compatible 转译 -> 隔离假上游（medium）`

- CPA：`v7.2.119`，commit `6e92e3e`，Docker 镜像 `eceasy/cli-proxy-api:latest` 的本地固定快照
- 插件：Linux amd64 c-shared `.so`，版本 `0.0.0-acceptance`
- 插件 SHA-256：`770fca413896de9df34c5309c1712a6ed4e725b11dfd4e2ddb87666af2efad70`
- 插件管理接口确认：已加载、已注册、已启用，运行中规则为 `gpt-5.6-sol(xhigh)=>gpt-5.6-sol(medium)`
- 非流式上游抓包核心字段：`model=gpt-5.6-sol`、`stream=false`、`reasoning_effort=medium`
- 流式上游抓包核心字段：`model=gpt-5.6-sol`、`stream=true`、`reasoning_effort=medium`
- 边界请求上游抓包核心字段：`reasoning_effort=high`
- 客户端模型名始终恢复为 `gpt-5.6-sol`；流式完成事件恢复客户端原始 `reasoning.effort=xhigh`
- 假上游只在本机临时端口运行，没有调用或计费真实模型服务

## 回归命令

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
git diff --check
```

结果：全部 PASS。

另执行了定向用例：

```text
go test ./... -run 'Test(HandleModelRouteMatchesCodexReasoningEffortFromBody|RouteModelForRequestCodexEffortPrecedenceAndFallback|HandleExecutorExecuteMapsCodexReasoningEffortFromBody|HandleExecutorExecuteStreamMapsCodexReasoningEffortFromBody)$' -count=1 -v
```

结果：4 个顶层用例及候选模型优先级的 4 个子用例全部 PASS。

## 环境告警

- WARN：在 macOS 上把本地构建的 CPA 可执行文件与 Go `c-shared` dylib 直接组合时，插件初始化阶段触发 Go runtime `runtime.cgocallback` fatal，尚未进入本次路由逻辑。
- 处置：改用插件真实支持的 Linux amd64 部署形态，在 CPA v7.2.119 中完成同一二进制边界的端到端验收；目标运行环境结果为 PASS。

## 资源与清理

- 测试只使用临时配置、虚拟鉴权值、临时容器和本机假上游。
- 验收后已停止并移除临时 CPA 容器、停止假上游并删除临时目录；端口 `18317`、`18318` 均已释放。
- Docker Desktop 为本次验收启动；不自动退出桌面应用，以免影响用户的其他容器工作。
