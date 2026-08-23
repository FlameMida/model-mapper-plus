# 渠道定向优先级边界验收

- 结果：**PASS（integration fixture）**
- 证据性质：固定 CLIProxyAPI v7.2.119 SDK 选择器 + 插件 Scheduler 回调，非 live 凭据改写

当前环境只有一份 auth，不能安全构造目标低优先级、池外高优先级的真实双凭据场景。实施计划允许以 host integration fixture 代替。

```text
go test -race . -run TestChannelTargetScheduler -count=1 -v
--- PASS: TestChannelTargetScheduler/目标低优先级、池外高优先级时不越池
```

fixture 模拟宿主只把全局最高优先级层的 `outside` 候选交给 Scheduler；目标 `f1` 已在回调前被排除。插件返回 HTTP 503 对应的 `auth_not_found` JSON，不选择 `outside`。

