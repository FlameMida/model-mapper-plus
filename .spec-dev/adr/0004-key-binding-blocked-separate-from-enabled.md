# ADR-0004: Key 访问禁用用独立 `blocked` 字段，不重定义 `enabled`

- 日期：2026-08-05
- 状态：已接受

## 背景

需要禁止指定客户端 API key 使用本 CPA 上的模型能力。`KeyBinding.Enabled` 已存在，语义是「是否应用该 key 的追加规则集」，`false` 时请求仍放行并只跑顶层规则。

## 决定

新增 `KeyBinding.Blocked`（JSON `blocked`）。`enabled` 语义不变；`blocked=true` 时在请求拦截层拒绝访问，与规则是否启用正交。

## 理由

重定义 `enabled=false` 为拒绝访问会破坏既有单测与产品文案（「追加规则集」开关），并失去「只关 key 规则、仍走顶层」的能力。独立字段成本低、可逆迁移清晰。被否方案：把 `enabled` 改成门禁；三态枚举（active/rules-off/blocked）。
