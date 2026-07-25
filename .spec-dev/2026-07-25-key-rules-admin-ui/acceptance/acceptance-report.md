# Acceptance Report — key-rules-admin-ui

- Date: 2026-07-25
- Spec: `.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`
- Plan branch: `plan/2026-07-25-key-rules-admin-ui`
- Closing review fix commit: `d991d59`

## Requirement Reconciliation

| Requirement | Status | Note |
|-------------|--------|------|
| 客户端 key 提取 | DELIVERED | unit: Bearer / x-api-key / empty |
| key 层接力执行 | DELIVERED | unit: chain, unbound, fallback, disabled |
| 净效果为零不路由 | DELIVERED | unit: net-zero |
| state_file 真相源与 YAML seed | DELIVERED | unit: first save + YAML override |
| state_file 损坏回退 | DELIVERED | unit: corrupt + invalid DSL fallback (M1) |
| state_file 原子写 | DELIVERED | unit: perm 0600 + valid JSON |
| management 能力注册与路由 | DELIVERED | unit + real host path (H1) |
| 保存前 DSL 预校验 | DELIVERED | unit: 400 + memory not corrupted (H2) |
| 规则试跑 | DELIVERED | unit: preview chain |
| web 管理界面三板块 | DELIVERED | build + embed; live browser e2e DEFERRED |
| 路由判定链路（两层） | DELIVERED | unit |
| 插件注册能力（ManagementAPI + state_file） | DELIVERED | unit |

**ADDED-IN-FLIGHT**: none  
**DEFERRED**:
- key 下拉来自 CPA（live integration）— 环境缺 `CPA_SMOKE_*`
- 端到端 smoke — 同上
- 真实浏览器管理页 — 同上  
**DROPPED**: none

## Closing review disposition

Fixed in `d991d59` (user approved full recommended set H1–H4 + M1–M5):

- H1 management handle path prefix  
- H2 applyStateUpdate deep clone  
- H3 loadedRuleSource copy  
- H4 lock-order (path before state lock)  
- M1 validateState on resolve  
- M2 missing key no state_file create  
- M3 persist error → 500  
- M4 collision warning  
- M5 auth logout on 401/403  

Deferred (low / non-blocking): M6 routeModel/previewRoute DRY, selectRules dead wrapper, ruleSource dual cache, index React keys, panelAuth write half, frontend DSL unit tests.

## Test evidence

- `go test ./...` PASS (post-fix)  
- `go vet ./...` PASS  
- `make web-build` PASS (embedded index.html rebuilt)
