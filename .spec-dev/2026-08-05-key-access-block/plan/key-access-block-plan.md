# Key 访问禁用（blocked）实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。步骤用复选框（`- [ ]`）语法跟踪；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：为 Key 绑定新增 `blocked` 访问门禁，在 `request.intercept_before` 对禁用 key 返回固定 403「额度用尽」英文 JSON；Admin UI 可开关；既有 `enabled` 规则语义不变。

**Spec**：`.spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md`

**架构**：`KeyBinding.Blocked` 持久化；独立 `findBlockedKeyBinding`（不复用 `findKeyBinding`）；注册 `request_interceptor`，before 短路 Terminate、after 空放行；management POST/PATCH 读写 `blocked`；KeysPanel 并列 Switch。

**技术栈**：Go c-shared 插件（CLIProxyAPI pluginabi/pluginapi）、React 19 + Semi Design 管理 UI、`go test` / `make web-build`。

## 全局约束

- 固定拒绝 body（一字不差）：`{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`
- HTTP status：`403`
- JSON 字段名：`blocked`（bool）；能力字段：`request_interceptor: true`
- `enabled` 语义不得改为门禁；门禁查找**禁止**调用 `findKeyBinding`
- 插件总开关 `Config.Enabled == false` 时不拦截
- 无新依赖；不改 CPA / keeper
- 每个任务 TDD：先失败测试 → 最小实现 → 通过 → 提交；commit 前缀 `feat(TN):` / `test(TN):` / `chore(TN):`

## 文件结构（将创建/修改）

| 路径 | 职责 |
|------|------|
| `state.go` | `KeyBinding.Blocked`；`findBlockedKeyBinding` |
| `state_test.go` | 旧 state 缺字段；blocked 查找不依赖 enabled |
| `management.go` | PATCH 支持 `blocked *bool` |
| `management_api_test.go` / `management_test.go` | POST/PATCH/解禁读写 |
| `main.go` | 能力注册；`blockedQuotaExhaustedBody`；`handleRequestInterceptBefore/After`；`dispatchMethod` 分支 |
| `main_test.go` 或新建 `intercept_block_test.go` | 门禁全部 Scenario |
| `web/src/api.ts` | 类型与 `patchKey` 含 `blocked` |
| `web/src/panels/KeysPanel.tsx` | 表单 + 列表「禁止访问」Switch |

---

### 任务 0：建立隔离工作区

- [ ] **步骤 1：检测已有隔离**

运行：`git rev-parse --git-dir` 与 `git rev-parse --git-common-dir`  
两者不同、且 `git rev-parse --show-superproject-working-tree` 无输出（排除 submodule）→ 已在隔离工作区，跳过本任务。

- [ ] **步骤 2：建立 worktree**

确认 `.worktrees/` 已被忽略（`git check-ignore -q .worktrees`，未忽略先加入 `.gitignore` 并提交），然后：

```bash
git worktree add .worktrees/plan/2026-08-05-key-access-block -b plan/2026-08-05-key-access-block
cd .worktrees/plan/2026-08-05-key-access-block
```

- [ ] **步骤 3：安装依赖并验证基线**

```bash
go test ./...
```

预期：全绿。失败 → 停下报告，先问再继续。  
（前端依赖在任务 4 的 `make web-build` 时安装即可；本任务不强制 `npm install`。）

---

## 数据层

### 任务 1：State 增加 `blocked` 与独立查找

**文件**：
- 修改：`state.go`（`KeyBinding`、新增 `findBlockedKeyBinding`）
- 测试：`state_test.go`（追加用例）

**接口**：
- 消费：无
- 产出：
  - `KeyBinding.Blocked bool \`json:"blocked"\``
  - `func findBlockedKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool)` — 仅当 key 常量时间匹配且 `Blocked==true` 时命中；**不**检查 `Enabled`；空 apiKey 永不命中

- [ ] **步骤 1：写失败测试**

在 `state_test.go` 追加：

```go
func TestKeyBindingBlockedDefaultsFalseOnUnmarshal(t *testing.T) {
	raw := []byte(`{"version":1,"rules":{},"key_bindings":[{"key":"sk-a","enabled":true,"rules":{}}]}`)
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(st.KeyBindings) != 1 {
		t.Fatalf("bindings=%d", len(st.KeyBindings))
	}
	if st.KeyBindings[0].Blocked {
		t.Fatalf("missing blocked field must default to false")
	}
}

func TestFindBlockedKeyBindingIgnoresEnabled(t *testing.T) {
	bindings := []KeyBinding{
		{Key: "sk-a", Enabled: false, Blocked: true},
		{Key: "sk-b", Enabled: true, Blocked: false},
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-a"); !ok {
		t.Fatal("blocked=true must match even when enabled=false")
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-b"); ok {
		t.Fatal("blocked=false must not match")
	}
	if _, ok := findBlockedKeyBinding(bindings, ""); ok {
		t.Fatal("empty key must never match")
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-missing"); ok {
		t.Fatal("unknown key must not match")
	}
}

func TestFindKeyBindingStillRequiresEnabled(t *testing.T) {
	bindings := []KeyBinding{{Key: "sk-a", Enabled: false, Blocked: true, Rules: RuleSet{Global: "a=>b"}}}
	if _, ok := findKeyBinding(bindings, "sk-a"); ok {
		t.Fatal("findKeyBinding must still require enabled=true")
	}
}
```

（若 `state_test.go` 未 import `encoding/json`，补上。）

- [ ] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestKeyBindingBlockedDefaultsFalseOnUnmarshal|TestFindBlockedKeyBindingIgnoresEnabled|TestFindKeyBindingStillRequiresEnabled' -v
```

预期：FAIL（`Blocked` 未定义或 `findBlockedKeyBinding` 未定义）。

- [ ] **步骤 3：写最小实现**

`state.go` 中 `KeyBinding`：

```go
type KeyBinding struct {
	Key     string  `json:"key"`
	Alias   string  `json:"alias"`
	Enabled bool    `json:"enabled"`
	Blocked bool    `json:"blocked"`
	Rules   RuleSet `json:"rules"`
}
```

在 `findKeyBinding` 旁新增（须 `crypto/subtle` 已 import，与现有一致）：

```go
// findBlockedKeyBinding returns a binding that has Blocked=true for apiKey.
// Unlike findKeyBinding, Enabled is ignored so access denial still applies when
// key-layer rules are turned off.
func findBlockedKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if b.Blocked && subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}
```

- [ ] **步骤 4：运行测试确认通过**

```bash
go test . -run 'TestKeyBindingBlockedDefaultsFalseOnUnmarshal|TestFindBlockedKeyBindingIgnoresEnabled|TestFindKeyBindingStillRequiresEnabled' -v
```

预期：PASS。再跑：

```bash
go test .
```

预期：全绿（含既有 keybinding 用例）。

- [ ] **步骤 5：提交**

```bash
git add state.go state_test.go
git commit -m "feat(T1): add KeyBinding.blocked and findBlockedKeyBinding"
```

---

## 管理 API

### 任务 2：Management 读写 `blocked`

**文件**：
- 修改：`management.go`（`managementPatchKey` 的 patch 结构）
- 测试：`management_api_test.go`（追加）

**接口**：
- 消费：`KeyBinding.Blocked`（任务 1）
- 产出：POST 全量 body 已通过 `json.Unmarshal` 进 `KeyBinding`（含 blocked，无需改 post 结构体）；PATCH 支持 `"blocked": true|false`

- [ ] **步骤 1：写失败测试**

在 `management_api_test.go` 追加（沿用 `setupManagementTest` / `url` / `http` 既有 import）：

```go
func TestManagementPostKeyPersistsBlocked(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","enabled":true,"blocked":true,"rules":{"global":"x=>y"}}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 1 || !st.KeyBindings[0].Blocked {
		t.Fatalf("want blocked=true, got %+v", st.KeyBindings)
	}
	// GET state body must include blocked
	if !bytes.Contains(resp.Body, []byte(`"blocked":true`)) && !bytes.Contains(resp.Body, []byte(`"blocked": true`)) {
		// re-marshal snapshot for stable check
		raw, _ := json.Marshal(st.KeyBindings[0])
		if !bytes.Contains(raw, []byte(`"blocked":true`)) {
			t.Fatalf("serialized binding missing blocked: %s", raw)
		}
	}
}

func TestManagementPatchKeyBlockedUnblock(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","enabled":true,"blocked":true}`),
	})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	st, _ := loadedStateSnapshot()
	if st.KeyBindings[0].Blocked {
		t.Fatalf("want blocked=false after patch, got %+v", st.KeyBindings[0])
	}
	// enabled must not be clobbered
	if !st.KeyBindings[0].Enabled {
		t.Fatalf("patch blocked must not clear enabled: %+v", st.KeyBindings[0])
	}
}
```

若缺 `bytes` / `encoding/json` import，补上。

- [ ] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestManagementPostKeyPersistsBlocked|TestManagementPatchKeyBlockedUnblock' -v
```

预期：POST 可能已因 `KeyBinding` 含字段而通过；PATCH 应 FAIL（patch 结构无 `Blocked`，解禁不生效）。若 POST 也意外通过而 PATCH 失败，仍进入步骤 3。

- [ ] **步骤 3：写最小实现**

`management.go` 中 `managementPatchKey` 的 patch 结构改为：

```go
	var patch struct {
		Alias   *string  `json:"alias"`
		Enabled *bool    `json:"enabled"`
		Blocked *bool    `json:"blocked"`
		Rules   *RuleSet `json:"rules"`
	}
```

在 `if patch.Enabled != nil { ... }` 之后增加：

```go
			if patch.Blocked != nil {
				st.KeyBindings[i].Blocked = *patch.Blocked
			}
```

POST 无需改代码（已 unmarshal 到完整 `KeyBinding`）。

- [ ] **步骤 4：运行测试确认通过**

```bash
go test . -run 'TestManagementPostKeyPersistsBlocked|TestManagementPatchKeyBlockedUnblock|TestManagementPatchKey' -v
go test .
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add management.go management_api_test.go
git commit -m "feat(T2): management API read/write key binding blocked"
```

---

## 运行时门禁

### 任务 3：`request.intercept_before` 短路拒绝

**文件**：
- 修改：`main.go`（`registrationCapabilities`、`pluginRegistration`、`dispatchMethod`、新增 handler 与常量）
- 修改：`main_test.go`（能力断言扩展）
- 创建或修改：`intercept_block_test.go`（门禁 Scenario；可用新建文件保持聚焦）

**接口**：
- 消费：`findBlockedKeyBinding`、`apiKeyFromHeaders`、`loadedConfig`、`loadedRuleSource`（或 `loadedStateSnapshot` 的 KeyBindings）
- 产出：
  - 包级常量 `blockedQuotaExhaustedBody`（固定 JSON 字节）
  - `func handleRequestInterceptBefore(raw []byte) ([]byte, error)`
  - `func handleRequestInterceptAfter(raw []byte) ([]byte, error)` — 空放行
  - 注册 `RequestInterceptor bool \`json:"request_interceptor"\`` = true
  - `dispatchMethod` 处理 `pluginabi.MethodRequestInterceptBefore` / `After`

- [ ] **步骤 1：写失败测试**

**A.** 扩展 `main_test.go` 的 `TestPluginRegistrationMetadataAndConfigFields`：在既有 capability 断言后增加：

```go
	if !reg.Capabilities.RequestInterceptor {
		t.Fatalf("capabilities=%#v, want request_interceptor=true", reg.Capabilities)
	}
```

**B.** 新建 `intercept_block_test.go`：

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func setupBlockedInterceptTest(t *testing.T, cfg Config, bindings []KeyBinding) {
	t.Helper()
	setupManagementTest(t, cfg)
	for _, b := range bindings {
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: raw})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seed binding status=%d body=%s", resp.StatusCode, resp.Body)
		}
	}
}

func interceptBeforeRaw(t *testing.T, headers http.Header) pluginapi.RequestInterceptResponse {
	t.Helper()
	req := pluginapi.RequestInterceptRequest{
		RequestID: "req-1",
		Headers:   headers,
		Model:     "gpt-test",
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envRaw, err := handleMethod(pluginabi.MethodRequestInterceptBefore, raw)
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil {
		t.Fatalf("envelope: %v raw=%s", err, envRaw)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %+v", env.Error)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("result: %v", err)
	}
	return resp
}

func TestInterceptBlocksBlockedKeyWithFixedBody(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if !resp.Terminate {
		t.Fatal("want Terminate=true")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
	want := string(blockedQuotaExhaustedBody)
	if string(resp.ResponseBody) != want {
		t.Fatalf("body=%s\nwant=%s", resp.ResponseBody, want)
	}
}

func TestInterceptBlocksWhenRulesDisabled(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: false, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if !resp.Terminate || resp.StatusCode != 403 {
		t.Fatalf("want terminate 403, got terminate=%v status=%d", resp.Terminate, resp.StatusCode)
	}
}

func TestInterceptPassWhenNotBlocked(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: false, Rules: RuleSet{Global: "a=>b"}},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if resp.Terminate {
		t.Fatal("must not terminate unblocked key")
	}
}

func TestInterceptPassWhenNoBinding(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, nil)
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-orphan"}})
	if resp.Terminate {
		t.Fatal("must not terminate unknown key")
	}
}

func TestInterceptPassWhenPluginDisabled(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: false}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if resp.Terminate {
		t.Fatal("plugin disabled must not terminate")
	}
}

func TestInterceptPassWhenNoClientKey(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{})
	if resp.Terminate {
		t.Fatal("missing client key must not terminate")
	}
}

func TestInterceptAfterIsPassThrough(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	req := pluginapi.RequestInterceptRequest{
		RequestID: "req-2",
		Headers:   http.Header{"Authorization": {"Bearer sk-a"}},
		Body:      []byte(`{"model":"x"}`),
	}
	raw, _ := json.Marshal(req)
	envRaw, err := handleMethod(pluginabi.MethodRequestInterceptAfter, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil || !env.OK {
		t.Fatalf("env=%+v err=%v raw=%s", env, err, envRaw)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Terminate {
		t.Fatal("after must never terminate for this feature")
	}
}

func TestManagementUnblockAllowsInterceptPass(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}}); !resp.Terminate {
		t.Fatal("precondition: should block")
	}
	patch := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  map[string][]string{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch=%d", patch.StatusCode)
	}
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}}); resp.Terminate {
		t.Fatal("after unblock must pass")
	}
}
```

注意：`managementPatchKey` 的 Query 类型是 `url.Values`；若编译报错，改为 `url.Values{"key": {"sk-a"}}` 并 import `net/url`。

- [ ] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestIntercept' -v
```

预期：FAIL（`RequestInterceptor` 字段不存在 / method unknown / `blockedQuotaExhaustedBody` 未定义）。

- [ ] **步骤 3：写最小实现**

**1）** `registrationCapabilities` 增加字段：

```go
type registrationCapabilities struct {
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	ManagementAPI         bool     `json:"management_api"`
	RequestInterceptor    bool     `json:"request_interceptor"`
}
```

`pluginRegistration()` 的 Capabilities 中设 `RequestInterceptor: true`。

**2）** 在 `main.go` 合适位置（如 `apiKeyFromHeaders` 附近）增加：

```go
// blockedQuotaExhaustedBody is the exact client-facing body when a key binding is blocked.
// Keep byte-identical to the spec constant (OpenAI-shaped insufficient_quota).
var blockedQuotaExhaustedBody = []byte(
	`{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`,
)

func handleRequestInterceptBefore(raw []byte) ([]byte, error) {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled {
		return json.Marshal(pluginapi.RequestInterceptResponse{})
	}
	apiKey := apiKeyFromHeaders(req.Headers)
	if _, blocked := findBlockedKeyBinding(loadedRuleSource().KeyBindings, apiKey); blocked {
		return json.Marshal(pluginapi.RequestInterceptResponse{
			Terminate:    true,
			StatusCode:   http.StatusForbidden,
			ResponseBody: append([]byte(nil), blockedQuotaExhaustedBody...),
		})
	}
	return json.Marshal(pluginapi.RequestInterceptResponse{})
}

func handleRequestInterceptAfter(raw []byte) ([]byte, error) {
	// Required by RequestInterceptor capability; access gate runs only before auth.
	_ = raw
	return json.Marshal(pluginapi.RequestInterceptResponse{})
}
```

**3）** `dispatchMethod` 增加 case（在 Management 分支旁即可）：

```go
	case pluginabi.MethodRequestInterceptBefore:
		return wrapEnvelope(handleRequestInterceptBefore(request))
	case pluginabi.MethodRequestInterceptAfter:
		return wrapEnvelope(handleRequestInterceptAfter(request))
```

确认 `net/http` 已在 `main.go` import（executor 路径已用 status，通常已有）。

- [ ] **步骤 4：运行测试确认通过**

```bash
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestIntercept|TestManagementUnblock' -v
go test .
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add main.go main_test.go intercept_block_test.go
git commit -m "feat(T3): block disabled keys via request.intercept_before"
```

---

## 前端

### 任务 4：Admin UI「禁止访问」Switch

**文件**：
- 修改：`web/src/api.ts`
- 修改：`web/src/panels/KeysPanel.tsx`

**接口**：
- 消费：management `blocked` 字段
- 产出：`KeyBinding.blocked: boolean`；`patchKey` 可传 `blocked`；列表与编辑窗 Switch

- [ ] **步骤 1：写失败测试（类型/构建级）**

本仓库前端以 TypeScript 编译与 `make web-build` 为闸门。先改类型使旧构造对象缺 `blocked` 在严格检查下暴露，并在步骤 3 补齐所有构造点。

若存在 `web` 单测入口可追加；否则以步骤 4 的 `make web-build` 为确认。

可选：若项目有 vitest 且 `planKeySave` 可测，在现有 web 测试文件中加：

```ts
// 仅当仓库已有 vitest 配置时添加；否则跳过本代码块，直接步骤 3
import { planKeySave } from '../panels/KeysPanel'
// expect planKeySave('', { key: 'sk', alias: '', enabled: true, blocked: true, rules: ... }).binding.blocked === true
```

无 vitest 则**跳过本步的自动化失败测试**，在步骤 3 直接改实现，步骤 4 用 `make web-build` 验证（在提交信息注明「UI 无单测框架，以 web-build 验收」）。

- [ ] **步骤 2：运行确认类型缺口**

```bash
cd web && npx tsc --noEmit
```

在改完 `KeyBinding` 接口但未改 `KeysPanel` 的 openCreate 时，若 `tsc` 严格，可能报缺属性——也可直接进入步骤 3 一次改齐。

- [ ] **步骤 3：写最小实现**

**`web/src/api.ts`**

```ts
export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  blocked: boolean
  rules: RuleSet
}
```

```ts
  patchKey: (key: string, patch: Partial<Pick<KeyBinding, 'alias' | 'enabled' | 'blocked' | 'rules'>>) =>
    call<StateResponse>('PATCH', `/keys?key=${encodeURIComponent(key)}`, patch),
```

**`web/src/panels/KeysPanel.tsx`**

1. `openCreate`：

```ts
    setEditing({ key: '', alias: '', enabled: true, blocked: false, rules: EMPTY_RULES })
```

2. `toggleBlocked`（与 `toggleEnabled` 并列）：

```ts
  const toggleBlocked = (b: KeyBinding, blocked: boolean) => {
    api.patchKey(b.key, { blocked }).then(onSaved).catch((e: Error) => Toast.error(e.message))
  }
```

3. 表格列：在「启用」列后插入：

```ts
          {
            title: '禁止访问',
            dataIndex: 'blocked',
            render: (on: boolean, b: KeyBinding) => (
              <Switch checked={!!on} onChange={(v) => toggleBlocked(b, v)} />
            ),
          },
```

4. 编辑 Modal 中「启用」旁增加：

```tsx
            <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
              <span>
                <Switch checked={editing.enabled} onChange={(v) => setEditing({ ...editing, enabled: v })} /> 启用规则
              </span>
              <span>
                <Switch checked={!!editing.blocked} onChange={(v) => setEditing({ ...editing, blocked: v })} /> 禁止访问
              </span>
              {isKeyCollision && (
                <Tag color="orange">同 key 已存在，保存将覆盖</Tag>
              )}
            </div>
```

（删除旧的单独「启用」那一行，避免重复控件。）

5. `openEdit` 已 spread `b`，若后端返回缺 `blocked`，用 `blocked: !!b.blocked` 保证布尔：

```ts
    setEditing({ ...b, blocked: !!b.blocked, rules: { ...b.rules } })
```

- [ ] **步骤 4：运行确认通过**

```bash
make web-build
go test .
```

预期：`web/dist/index.html` 更新成功；Go 测试全绿。

- [ ] **步骤 5：提交**

```bash
git add web/src/api.ts web/src/panels/KeysPanel.tsx web/dist/index.html
git commit -m "feat(T4): admin UI switch for key access block"
```

（若 `web/dist` 被 force-track，必须纳入提交；若 build 未改 dist 哈希则只提交源码。）

---

## 验收与收尾

### 任务 5：验收（acceptance-qa）

> 本任务由 executing-plans 收尾审查阶段触发 acceptance-qa 按下表执行，
> 不参与逐任务连续执行；报告与证据落盘特性目录 `acceptance/` 子目录。

| Scenario / 检查项 | 维度 | 执行方式 | 目标 | 阈值/预期 | 验收证据 |
|-------------------|------|---------|------|----------|---------|
| `make test` / `go test ./...` | integration | 验收任务 | 全绿 | exit 0 | 命令输出 |
| `make web-build` | integration | 验收任务 | 嵌入 UI 构建成功 | exit 0；`web/dist/index.html` 存在 | 命令输出 |
| 手工（可选）：管理页禁止访问 Switch 保存后请求 403 | e2e | 验收任务 | 与固定 JSON 一致 | 有 live CPA 时 | 记录于 acceptance-report |

本地无 live CPA 时 e2e 行标为 DEFERRED，以 unit/integration 为准。

---

### 任务 6：合并与清理

- [ ] **步骤 1：全量验证**

在 worktree 内：

```bash
go test ./...
make web-build
```

确认全绿。失败 → 修复后才进入合并。

- [ ] **步骤 2：合并回来源分支**

```bash
cd "$(dirname "$(git rev-parse --git-common-dir)")"   # 回到主工作区
git merge plan/2026-08-05-key-access-block
```

合并冲突、或主工作区有未提交改动 → 停下向计划作者确认，不强行合并。

- [ ] **步骤 3：清理**

```bash
git worktree remove .worktrees/plan/2026-08-05-key-access-block
git branch -d plan/2026-08-05-key-access-block
```

- [ ] **步骤 4：sync_commit 锚定**

```bash
SYNC=$(git rev-parse HEAD)
# 编辑 .spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md
# 将 frontmatter sync_commit: null 改为 sync_commit: "<$SYNC 完整 SHA>"
git add .spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md
git commit -m "chore(spec): sync_commit 锚定 ${SYNC:0:7}"
```

---

## Self-Review（计划作者已完成）

| Spec Requirement / Scenario | 对应任务 |
|-----------------------------|----------|
| 持久化 blocked；旧 state false | T1 |
| findBlocked 不依赖 enabled | T1 + T3 |
| 403 + 固定 JSON | T3 |
| 未 blocked / 无 binding / 插件关 / 无 key | T3 |
| 管理 POST/PATCH/解禁 | T2 + T3 解禁用例 |
| enabled 语义不变 | T1 回归 + 既有 keybinding 测试 |
| Admin UI Switch | T4 |
| 验收 make test + web-build | T5 / T6 |
| 无 TBD/占位符 | 已扫 |
| 接口名一致 `findBlockedKeyBinding` / `blockedQuotaExhaustedBody` | T1→T3 |
