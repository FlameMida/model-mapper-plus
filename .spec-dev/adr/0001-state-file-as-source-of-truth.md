# ADR-0001: state_file 作为插件状态真相源

- 日期：2026-07-25
- 状态：已接受

## 背景

web 管理界面需要可写存储；插件运行在 CPA 进程内，无法安全回写 CPA 主配置 config.yaml（并发写风险），而 key 绑定（key→规则集 map）也无法由现有单行标量配置解析器表达。

## 决定

引入 `state_file`（JSON）作为顶层规则四段与 key 绑定的真相源：存在即唯一真相，YAML 规则字段仅作首次 seed；`enabled` 开关永留 YAML。

## 理由

key-policy 插件已在生产验证同一不变量（"If state_file exists, it is the source of truth"），原子写与 seed 合并模式可直接搬运；"web UI 产出 YAML 片段由用户手动应用"的被否方案会破坏管理界面直改生效的体验。
