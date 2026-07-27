# ADR-0001: state_file 作为插件状态真相源

- 日期：2026-07-25
- 状态：已接受

## 背景

web 管理界面需要可写存储；插件运行在 CPA 进程内，无法安全回写 CPA 主配置 config.yaml（并发写风险），而 key 绑定（key→规则集 map）也无法由现有单行标量配置解析器表达。

## 决定

引入 `state_file`（JSON）作为顶层规则四段与 key 绑定的真相源：存在即唯一真相，YAML 规则字段仅作首次 seed；`enabled` 开关永留 YAML。

**迁移不变量（2026-07-28 增补）**：reconfigure 把 state_file 换到一个不存在的新路径、且当前已有持久化数据时，先把当前数据迁移到新路径再以新路径为真相源。理由：用户在 CPA 配置页改 state_file 路径是合法且常见的运维操作（换盘、换目录），「换路径=数据消失回退空 seed」会让规则与 key 绑定看起来丢失，迫使用户重新填写——与「管理界面直改生效」的体验相悖。首次运行（无持久化数据）不迁移，仍回退 YAML seed、不创建文件，保持原语义。

## 理由

key-policy 插件已在生产验证同一不变量（"If state_file exists, it is the source of truth"），原子写与 seed 合并模式可直接搬运；"web UI 产出 YAML 片段由用户手动应用"的被否方案会破坏管理界面直改生效的体验。
