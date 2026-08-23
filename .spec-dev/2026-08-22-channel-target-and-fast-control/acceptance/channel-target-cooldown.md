# 定向凭据冷却 Live 验收

- 结果：**DEFERRED / UNVERIFIED**
- 非阻塞依据：实施计划明确允许在没有安全 fixture 时 DEFERRED

当前环境只有一份真实 auth；无法同时构造“目标 cooldown + 池外 active”，也不能为了验收改写真实凭据的 cooldown/优先级状态。因此未伪造 live 结果。

替代证据：

```text
go test -race . -run TestChannelTargetScheduler -count=1 -v
--- PASS: TestChannelTargetScheduler/目标_cooldown、池外_active_时不越池
--- PASS: TestChannelTargetScheduler/宿主全局无候选_MAY_在_Scheduler_前返回_429
```

测试固定了两个边界：宿主把池外 active 候选交给插件时，插件返回 JSON 503 且不越池；宿主全局无候选时，固定 SDK selector MAY 在插件前返回 429 与 `Retry-After`。

可复跑 live 条件：至少两份可安全恢复的测试 auth，能够只把目标 auth 置为 cooldown 并保留池外 auth active；请求后断言 HTTP 503 且 request-log 不含池外 ID，再恢复原状态。
