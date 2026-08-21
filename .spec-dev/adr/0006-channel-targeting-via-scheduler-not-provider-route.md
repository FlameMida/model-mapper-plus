# ADR-0006: 渠道定向走 Scheduler 能力而非 provider 路由

- 日期：2026-08-22
- 状态：已接受

## 背景

key 绑定需要「定向到指定 AI 供应商 / 认证文件集合」。SDK 提供两条挂接点：`ModelRouteResponse.TargetKind=provider`（路由层显式换渠道）与 `Scheduler` 能力（`scheduler.pick`，认证选择前过滤/钉住候选）。用户裁决要求支持多供应商与认证文件混选取并集、池内正常调度、池空报错不降级。

## 决定

只用 Scheduler 能力实现定向，不动 ModelRouter 的路由决策：定向开启时 `routeModel` 返回 Handled=false（顺带自然跳过规则映射），候选过滤完全在 `scheduler.pick` 完成（Candidates 自带 Provider 归属字段）。不采用 `TargetKind=provider`。

## 理由

- `ModelRouteResponse.Target` 是单值（一个 provider key），无法表达跨供应商并集；scheduler 的 Candidates 每条带 Provider 字段，天然支持。
- provider 路由路径需处理 TargetModel、forced-provider 校验链（`providersForExecution`），并会与其他使用 provider 路由的插件行为纠缠。
- 定向语义是「换执行路径」而非「改模型名」——Handled=false 让宿主按原始模型名解析，正是所需。
- 代价：Scheduler 能力全宿主单实例，与 cpa-plugin-key-policy 共存时后加载者失效（用户裁决接受）；已写入 spec 风险节。

## 被否方案

`TargetKind=provider + scheduler 钉文件`：路由层显式但表达力不足（单供应商限制）、边界链路复杂，放弃。
