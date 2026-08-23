# T15 独立证据审计

- 时间：2026-08-24（Asia/Shanghai）
- 方式：独立只读、逐项对抗式复核验收文档、原始 request-log、响应体、二进制 build info / SHA-256、确定性测试与 UI 截图
- 最终结论：6 个 PASS 全部**维持**；cooldown live 的 `UNVERIFIED / DEFERRED` 维持，未被替代证据错误升级

## 独立审计结论

| Check item | Verdict | 关键复核依据 |
|------------|---------|-------------|
| 定向请求实际落在目标认证文件 | 维持 | trace `89aaa7d6` 的上游请求记录目标 provider/auth；目标凭据返回自身 401；binding 恢复为不存在 |
| 目标 cooldown、池外 active | 确认未验证 | 单 auth 环境不足以安全构造；race 只证明插件候选边界，不能冒充 live |
| 目标低优先级、池外高优先级 | 维持 | 固定 v7.2.119 SDK fixture 验证宿主预过滤后的插件责任边界；不扩张为宿主 selector live 证明 |
| 双宿主三协议错误兼容 | 维持 | 六份请求均为 HTTP 503；OpenAI/Responses 保留 type/code，Claude 保留 type；二进制哈希匹配 |
| Fast 关闭后上游收到普通请求 | 维持 | trace `2f1e1dd7` 的原始/上游段对照证明 body 去除 speed、beta 只保留 prompt-caching |
| 定向与 Fast 组合叠加 | 维持 | trace `b5ec4e97` 同时证明目标 auth 命中与 Fast body/header 剥离 |
| 编辑表单全流程视觉与交互 | 维持（补证后） | 见下节；10 项 fail-fast 断言全真，浅/深色截图目视通过 |

## UI 补证复核

首轮独立审计确认两张截图的当前视觉状态通过，但因 CDP 收集值未持久化、脚本未 fail-fast，将“全流程交互”降级。随后在相同 v7.2.139 临时实例补跑：`ui-run.json` 持久化 10 项全真断言，脚本在任一条件为 false 时抛错；独立复核据此恢复为“维持”。

## Residual Risks

- 定向与组合 live 的临时实例只有一份 auth；原始日志能证明目标实际使用及无池外 ID，但有竞争候选时的池隔离主要由 Scheduler 确定性测试补足。
- 优先级行是宿主预过滤后插件边界的 integration fixture，不是宿主 priority selector 的完整 live 复验。
- v7.2.119 来自 git archive，Go build info 显示 `(devel)`；版本来源还依赖保留的 archive 源树与固定构建命令。
- UI 没有 Playwright 像素基线或 Axe/WCAG 扫描；本轮只声明 Tier A 当前视觉与交互通过。
