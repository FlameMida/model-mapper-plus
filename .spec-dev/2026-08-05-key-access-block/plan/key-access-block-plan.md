# Key 访问禁用（blocked）实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。步骤用复选框（`- [ ]`）语法跟踪；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：为 Key 绑定新增与 `enabled` 正交的 `blocked` 访问门禁；插件启用时在 `request.intercept_before` 对 blocked key 返回固定 HTTP 403 JSON，管理 API 与 Admin UI 均可设置和解除。

**Spec**：`.spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md`

**架构**：`KeyBinding.Blocked` 负责持久化，`findBlockedKeyBinding` 只按 key 与 blocked 状态查找；`request.intercept_before` 在模型路由与上游执行前终止命中请求，`request.intercept_after` 固定空放行。Management POST/PATCH/GET 传递 `blocked`，KeysPanel 的编辑窗和列表分别提供具有独立可访问名称的「禁止访问」Switch。

**技术栈**：Go 1.26、CLIProxyAPI SDK `v7.2.119`（native `ABIVersion=1`、RPC `SchemaVersion=2`）、React 19、Semi Design、Vitest、Testing Library、Vite。

## 全局约束

- `go.mod` 必须精确固定 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.119`；这是升级现有依赖，不新增第三方依赖。
- 部署的 CPA runtime 必须不低于 `v7.2.103`；低于该版本或无法从 management response 的 `X-CPA-VERSION` 确认版本时，不得把本特性标为验收通过或发布就绪。
- SDK 升级后 native `ABIVersion` 仍为 `1`，插件注册 RPC `SchemaVersion` 必须为 `2`。
- 固定响应状态为 `403`；响应 body 必须逐字节等于 `{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`。
- `enabled` 只控制 key 层追加规则；门禁查找不得调用 `findKeyBinding`，且不得要求 `Enabled=true`。
- `Config.Enabled == false`、无客户端 key、无 binding、`Blocked=false` 均不得 Terminate。
- Bearer key 优先于 `x-api-key`；只有 Bearer 不可用时才回退 `x-api-key`。
- 门禁只在 `request.intercept_before` 执行；`request.intercept_after` 必须返回空响应，`model.route` 与 executor 不承担拒绝逻辑。
- 前端仓库已经配置 Vitest 与 Testing Library；UI 任务必须先写交互失败测试，不得以构建或手工检查替代。
- 每个实施任务遵循 TDD：失败测试 → 确认红灯 → 最小实现 → 确认绿灯 → 提交。

## 文件结构（将创建/修改）

| 路径 | 动作 | 职责 |
|------|------|------|
| `go.mod` / `go.sum` | 修改 | CLIProxyAPI SDK 固定为 `v7.2.119` |
| `state.go` | 修改 | `KeyBinding.Blocked`；`findBlockedKeyBinding` |
| `state_test.go` | 修改 | 旧 state 缺字段仍为 false 且 enabled binding 仍参与路由；blocked 查找 |
| `keybinding_test.go` | 修改 | `enabled=false, blocked=false` 的既有路由语义回归 |
| `management.go` | 修改 | PATCH 支持可选 `blocked *bool`；POST/GET 继续使用完整 `KeyBinding` |
| `management_api_test.go` | 修改 | POST 后 GET 回读 blocked；PATCH 解禁且不覆盖 enabled |
| `management_test.go` | 验证，不修改 | Management 分发回归由全量 Go 测试覆盖 |
| `main.go` | 修改 | capability、before/after handler、固定 body、method dispatch |
| `main_test.go` | 修改 | RequestInterceptor capability 与 RPC schema 断言 |
| `intercept_block_test.go` | 创建 | 门禁、header 优先级、放行、解禁和 after 空放行测试 |
| `web/src/api.ts` / `web/src/api.test.ts` | 修改 | `blocked` 类型、PATCH 类型和 typed fixtures |
| `web/src/panels/KeysPanel.tsx` | 修改 | 新建/编辑默认值、编辑窗与列表 Switch、PATCH |
| `web/src/panels/KeysPanel.test.tsx` | 修改 | `planKeySave` 保留 blocked |
| `web/src/panels/KeysPanel.blocked.test.tsx` | 创建 | 编辑保存 POST 与列表 PATCH 交互测试 |
| `web/src/panels/KeysPanel.delete.test.tsx` | 修改 | typed fixture 补齐 blocked |
| `web/dist/index.html` | 修改 | Vite 单文件构建产物 |

---

### 任务 0：建立隔离工作区

- [x] **步骤 1：检测已有隔离**

运行：`git rev-parse --git-dir` 与 `git rev-parse --git-common-dir`  
两者不同、且 `git rev-parse --show-superproject-working-tree` 无输出（排除 submodule）→ 已在隔离工作区，跳过本任务。

- [x] **步骤 2：建立 worktree**

Codex 无原生 worktree 工具时使用手工路径。先确认 `.worktrees/` 已被忽略：

```bash
git check-ignore -q .worktrees
```

预期：exit 0。若不是 exit 0，先把 `.worktrees/` 加入 `.gitignore` 并单独提交，然后：

```bash
git worktree add .worktrees/plan/2026-08-05-key-access-block -b plan/2026-08-05-key-access-block
cd .worktrees/plan/2026-08-05-key-access-block
```

- [x] **步骤 3：安装依赖并验证基线**

```bash
go mod download
npm --prefix web ci
go test ./...
npm --prefix web run typecheck
npm --prefix web test
```

预期：安装命令 exit 0 且 `package-lock.json` 不变；Go、typecheck 与全量 Vitest 均 exit 0。

任一命令非零退出、出现 Vitest unhandled error，或测试进程无法正常收敛，都视为基线失败：立即停止并报告，修复基线后重新开始任务 0。不得忽略异常或只跑定向测试后继续。

---

## 数据层

### 任务 1：持久化 `blocked` 并保持 `enabled` 路由语义

**文件**：
- 修改：`state.go`
- 修改：`state_test.go`
- 修改：`keybinding_test.go`

**接口**：
- 消费：`routeModel(cfg Config, src ruleSource, format, model, apiKey string) (routeDecision, error)`
- 产出：`KeyBinding.Blocked bool`，JSON 字段名为 `blocked`
- 产出：`findBlockedKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool)`；空 key 不命中，只要求 key 常量时间相等且 `Blocked=true`

- [x] **步骤 1：写失败测试**

在 `state_test.go` 追加：

```go
// Scenario: 旧 state 加载后 blocked 为 false。
func TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding(t *testing.T) {
	raw := []byte(`{"version":1,"rules":{},"key_bindings":[{"key":"sk-a","enabled":true,"rules":{"global":"a=>b"}}]}`)
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal old state: %v", err)
	}
	if len(st.KeyBindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(st.KeyBindings))
	}
	if st.KeyBindings[0].Blocked {
		t.Fatal("missing blocked field must decode as false")
	}
	decision, err := routeModel(testConfig(), ruleSourceFromState(st), "openai", "a", "sk-a")
	if err != nil {
		t.Fatalf("routeModel: %v", err)
	}
	if !decision.Handled || decision.UpstreamModel != "b" {
		t.Fatalf("old enabled binding no longer routes: %+v", decision)
	}
}

func TestFindBlockedKeyBindingIgnoresEnabled(t *testing.T) {
	bindings := []KeyBinding{
		{Key: "sk-blocked-rules-off", Enabled: false, Blocked: true},
		{Key: "sk-allowed", Enabled: true, Blocked: false},
	}
	got, ok := findBlockedKeyBinding(bindings, "sk-blocked-rules-off")
	if !ok || got.Key != "sk-blocked-rules-off" {
		t.Fatalf("blocked binding must match with enabled=false: got=%+v ok=%v", got, ok)
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-allowed"); ok {
		t.Fatal("blocked=false must not match")
	}
	if _, ok := findBlockedKeyBinding(bindings, ""); ok {
		t.Fatal("empty api key must not match")
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-missing"); ok {
		t.Fatal("unknown api key must not match")
	}
}
```

在 `keybinding_test.go` 的 `TestRouteModelBindingDisabled` fixture 中显式加入 `Blocked: false`，保留其既有 `Handled=true`、`UpstreamModel=="b"` 断言：

```go
KeyBindings: []KeyBinding{{
	Key: "sk-k", Enabled: false, Blocked: false,
	Rules: RuleSet{Global: `b=>c`},
}},
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding|TestFindBlockedKeyBindingIgnoresEnabled|TestRouteModelBindingDisabled' -v
```

预期：FAIL，编译错误包含 `KeyBinding.Blocked undefined` 或 `undefined: findBlockedKeyBinding`。

- [x] **步骤 3：写最小实现**

把 `state.go` 的 `KeyBinding` 改为：

```go
type KeyBinding struct {
	Key     string  `json:"key"`
	Alias   string  `json:"alias"`
	Enabled bool    `json:"enabled"`
	Blocked bool    `json:"blocked"`
	Rules   RuleSet `json:"rules"`
}
```

在 `findKeyBinding` 后新增：

```go
// findBlockedKeyBinding returns a blocked binding for apiKey using the same
// constant-time key comparison as findKeyBinding. Enabled is intentionally
// ignored because rule application and access denial are orthogonal.
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

- [x] **步骤 4：运行测试确认通过**

```bash
go test . -run 'TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding|TestFindBlockedKeyBindingIgnoresEnabled|TestRouteModelBindingDisabled' -v
go test ./...
```

预期：全部 PASS；旧 state 中的 enabled binding 仍把 `a` 路由为 `b`，仅关闭规则仍保持既有顶层结果。

- [x] **步骤 5：提交**

```bash
git add state.go state_test.go keybinding_test.go
git commit -m "feat(T1): 持久化 blocked 并保持规则启用语义"
```

---

## 管理 API

### 任务 2：Management POST、GET、PATCH 读写 `blocked`

**文件**：
- 修改：`management.go`
- 修改：`management_api_test.go`
- 验证：`management_test.go`

**接口**：
- 消费：任务 1 的 `KeyBinding.Blocked`
- 产出：POST body 与 GET `stateResponse.KeyBindings` 通过完整 `KeyBinding` 传递 `blocked`
- 产出：PATCH body 支持 `Blocked *bool`，`false` 必须与“字段缺省”区分

- [x] **步骤 1：写失败测试**

在 `management_api_test.go` 追加：

```go
// Scenario: blocked 经管理 API 写入后可再读出。
func TestManagementPostKeyPersistsBlockedAndGetStateReadsIt(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"blocked":true,"rules":{"global":"x=>y"}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", post.StatusCode, post.Body)
	}
	get := managementGetState()
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.StatusCode, get.Body)
	}
	var body stateResponse
	decodeBody(t, get, &body)
	if len(body.KeyBindings) != 1 || body.KeyBindings[0].Key != "sk-a" {
		t.Fatalf("GET key_bindings=%+v", body.KeyBindings)
	}
	if !body.KeyBindings[0].Blocked {
		t.Fatalf("GET did not read back blocked=true: %+v", body.KeyBindings[0])
	}
}

func TestManagementPatchKeyUnblocksWithoutClobberingEnabled(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	seed := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"blocked":true,"rules":{"global":"x=>y"}}`),
	})
	if seed.StatusCode != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", seed.StatusCode, seed.Body)
	}
	patch := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", patch.StatusCode, patch.Body)
	}
	get := managementGetState()
	var body stateResponse
	decodeBody(t, get, &body)
	got := body.KeyBindings[0]
	if got.Blocked {
		t.Fatalf("blocked=%v, want false after PATCH", got.Blocked)
	}
	if !got.Enabled || got.Alias != "A" || got.Rules.Global != "x=>y" {
		t.Fatalf("PATCH blocked clobbered another field: %+v", got)
	}
}
```

现有 imports 已包含 `net/http`、`net/url` 与 `pluginapi`，无需新增测试依赖。

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestManagementPostKeyPersistsBlockedAndGetStateReadsIt|TestManagementPatchKeyUnblocksWithoutClobberingEnabled' -v
```

预期：POST/GET 用例因任务 1 的完整 `KeyBinding` 可能已经 PASS；PATCH 用例必须 FAIL，表现为 `blocked` 仍为 true。至少一个失败即为本任务红灯。

- [x] **步骤 3：写最小实现**

把 `management.go` 中 `managementPatchKey` 的 patch 结构改为：

```go
var patch struct {
	Alias   *string  `json:"alias"`
	Enabled *bool    `json:"enabled"`
	Blocked *bool    `json:"blocked"`
	Rules   *RuleSet `json:"rules"`
}
```

在 `Enabled` 更新后、`Rules` 更新前加入：

```go
if patch.Blocked != nil {
	st.KeyBindings[i].Blocked = *patch.Blocked
}
```

POST 与 GET 已直接使用 `KeyBinding`，不增加平行 DTO。

- [x] **步骤 4：运行测试确认通过**

```bash
go test . -run 'TestManagementPostKeyPersistsBlockedAndGetStateReadsIt|TestManagementPatchKeyUnblocksWithoutClobberingEnabled|TestManagementPatchKey|TestManagementPostKeyUpsert' -v
go test ./...
```

预期：全部 PASS；POST 后实际调用 `managementGetState()` 可回读 true，PATCH false 可解禁且不覆盖 enabled、alias、rules。

- [x] **步骤 5：提交**

```bash
git add management.go management_api_test.go
git commit -m "feat(T2): 管理 API 支持 blocked 读写与解禁"
```

---

## 运行时门禁

### 任务 3：`request.intercept_before` 短路拒绝

**文件**：
- 修改：`go.mod`（CLIProxyAPI 精确固定 `v7.2.119`）
- 修改：`go.sum`
- 修改：`main.go`（`registrationCapabilities`、`pluginRegistration`、`dispatchMethod`、新增 handler 与常量）
- 修改：`main_test.go`（能力断言扩展）
- 创建：`intercept_block_test.go`（门禁全部 Scenario 与 header 语义）

**接口**：
- 消费：`findBlockedKeyBinding`、`apiKeyFromHeaders`、`loadedConfig`、`loadedRuleSource`（或 `loadedStateSnapshot` 的 KeyBindings）
- 产出：
  - 包级常量 `blockedQuotaExhaustedBody`（固定 JSON 字节）
  - `func handleRequestInterceptBefore(raw []byte) ([]byte, error)`
  - `func handleRequestInterceptAfter(raw []byte) ([]byte, error)` — 空放行
  - 注册 `RequestInterceptor bool`，JSON 字段名为 `request_interceptor`，值为 true
  - `dispatchMethod` 处理 `pluginabi.MethodRequestInterceptBefore` / `After`
  - SDK native `ABIVersion=1`、注册 RPC `SchemaVersion=2`

- [x] **步骤 1：写失败测试**

**A.** 扩展 `main_test.go` 的 `TestPluginRegistrationMetadataAndConfigFields`：在既有 capability 断言后增加：

```go
	if !reg.Capabilities.RequestInterceptor {
		t.Fatalf("capabilities=%#v, want request_interceptor=true", reg.Capabilities)
	}
	if pluginabi.ABIVersion != 1 || reg.SchemaVersion != 2 {
		t.Fatalf("ABI/schema = %d/%d, want 1/2", pluginabi.ABIVersion, reg.SchemaVersion)
	}
```

**B.** 新建 `intercept_block_test.go`：

```go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const wantBlockedResponseBody = `{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`

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
	if !bytes.Equal(resp.ResponseBody, []byte(wantBlockedResponseBody)) {
		t.Fatalf("body=%s\nwant=%s", resp.ResponseBody, wantBlockedResponseBody)
	}
	if got := resp.ResponseHeaders.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q, want application/json", got)
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

func TestInterceptBlocksViaXAPIKeyFallback(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-x", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"X-Api-Key": {"sk-x"}})
	if !resp.Terminate || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("x-api-key fallback did not block: %+v", resp)
	}
}

func TestInterceptBearerTakesPriorityOverXAPIKey(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-bearer", Enabled: true, Blocked: false},
		{Key: "sk-x", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{
		"Authorization": {"Bearer sk-bearer"},
		"X-Api-Key":     {"sk-x"},
	})
	if resp.Terminate {
		t.Fatal("unblocked Bearer must win over blocked x-api-key")
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
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch=%d", patch.StatusCode)
	}
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}}); resp.Terminate {
		t.Fatal("after unblock must pass")
	}
}

func TestManagementPostBlockedKeyImmediatelyRejects(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, nil)
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-new","enabled":true,"blocked":true,"rules":{}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", post.StatusCode, post.Body)
	}
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-new"}})
	if !resp.Terminate || resp.StatusCode != http.StatusForbidden ||
		!bytes.Equal(resp.ResponseBody, []byte(wantBlockedResponseBody)) {
		t.Fatalf("new blocked binding did not reject immediately: %+v", resp)
	}
}
```

固定 body 测试使用独立的 `wantBlockedResponseBody` 字面量，禁止引用生产常量，避免实现与断言同时改错。

- [x] **步骤 2：在旧 SDK 上运行，确认契约红灯**

```bash
go list -m -f '{{.Version}}' github.com/router-for-me/CLIProxyAPI/v7
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestInterceptBlocksBlockedKeyWithFixedBody' -v
```

预期：第一条输出 `v7.2.48`；测试编译 FAIL，至少包含旧 SDK 不认识 `RequestID`、`Terminate`、`StatusCode`、`ResponseHeaders` 或 `ResponseBody` 的错误。这一步证明只写实现而不升级 SDK 无法满足契约。

- [x] **步骤 3：升级并固定 SDK，再确认实现仍为红灯**

```bash
go get github.com/router-for-me/CLIProxyAPI/v7@v7.2.119
go mod tidy
test "$(go list -m -f '{{.Version}}' github.com/router-for-me/CLIProxyAPI/v7)" = "v7.2.119"
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestInterceptBlocksBlockedKeyWithFixedBody' -v
```

预期：版本断言通过；测试仍 FAIL，因为 `registrationCapabilities.RequestInterceptor` 尚不存在，或 method dispatch 返回 `unknown_method`。`go.mod` 中必须是直接依赖 `v7.2.119`。

- [x] **步骤 4：写最小实现**

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
const blockedQuotaExhaustedBody = `{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`

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
			Terminate:       true,
			StatusCode:      http.StatusForbidden,
			ResponseHeaders: http.Header{"Content-Type": {"application/json"}},
			ResponseBody:    []byte(blockedQuotaExhaustedBody),
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

- [x] **步骤 5：运行测试确认通过**

```bash
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestIntercept|TestManagementUnblock|TestManagementPostBlockedKeyImmediatelyRejects' -v
go test ./...
go mod verify
```

预期：全部 PASS；fixed body 与测试中的独立 JSON 字面量逐字节一致；`go mod verify` 输出 `all modules verified`。

- [x] **步骤 6：提交**

```bash
git add go.mod go.sum main.go main_test.go intercept_block_test.go
git commit -m "feat(T3): 在请求前拦截标记为 blocked 的 key"
```

---

## 前端

### 任务 4：Admin UI「禁止访问」Switch

**文件**：
- 修改：`web/src/api.ts`
- 修改：`web/src/api.test.ts`
- 修改：`web/src/panels/KeysPanel.tsx`
- 修改：`web/src/panels/KeysPanel.test.tsx`
- 创建：`web/src/panels/KeysPanel.blocked.test.tsx`
- 修改：`web/src/panels/KeysPanel.delete.test.tsx`
- 修改：`web/dist/index.html`

**接口**：
- 消费：management `blocked` 字段
- 产出：`KeyBinding.blocked: boolean`；`patchKey` 可传 `blocked`
- 产出：编辑窗 Switch accessible name `编辑绑定：禁止访问`；列表 Switch 使用 `禁止访问：alias`

- [x] **步骤 1：写失败交互测试并补 typed fixtures**

创建 `web/src/panels/KeysPanel.blocked.test.tsx`：

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      postKey: vi.fn(),
      patchKey: vi.fn(),
    },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
  }
})

const EMPTY_RULES = { global: '', claude: '', codex: '', openai: '' }
const BINDING: KeyBinding = {
  key: 'sk-block-test',
  alias: 'Blocked Test',
  enabled: true,
  blocked: false,
  rules: { ...EMPTY_RULES },
}
const STATE: StateResponse = {
  version: 1,
  rules: { ...EMPTY_RULES },
  key_bindings: [BINDING],
  persisted: true,
  state_file: '/tmp/key-access-block-test.json',
}

afterEach(() => {
  vi.clearAllMocks()
})

describe('KeysPanel：禁止访问', () => {
  it('编辑窗打开禁止访问并保存时 POST blocked=true', async () => {
    const user = userEvent.setup()
    vi.mocked(api.postKey).mockResolvedValue({
      ...STATE,
      key_bindings: [{ ...BINDING, blocked: true }],
    })
    render(<KeysPanel state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(await screen.findByRole('switch', { name: '编辑绑定：禁止访问' }))

    const dialog = screen.getByRole('dialog')
    const okButton = dialog.querySelector('.semi-button-primary') as HTMLButtonElement
    await user.click(okButton)

    await waitFor(() => {
      expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({
        key: 'sk-block-test',
        blocked: true,
      }))
    })
  })

  it('列表禁止访问 Switch 直接 PATCH blocked=true', async () => {
    const user = userEvent.setup()
    vi.mocked(api.patchKey).mockResolvedValue({
      ...STATE,
      key_bindings: [{ ...BINDING, blocked: true }],
    })
    render(<KeysPanel state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('switch', { name: '禁止访问：Blocked Test' }))

    await waitFor(() => {
      expect(api.patchKey).toHaveBeenCalledWith('sk-block-test', { blocked: true })
    })
  })
})
```

同步修改 typed fixtures：

1. `web/src/panels/KeysPanel.test.tsx` 的 `binding` 增加 `blocked: false`；最后一个用例传入 `blocked: true` 并增加 `expect(plan?.binding.blocked).toBe(true)`。
2. `web/src/panels/KeysPanel.delete.test.tsx` 的 `BINDING` 增加 `blocked: false`。
3. `web/src/api.test.ts` 把 import 改为 `import { api, StateResponse } from './api'`，把 `BASE_STATE` 声明为 `const BASE_STATE: StateResponse = { ... }`，并为两个 `key_bindings` fixture 增加 `blocked: false`。

- [x] **步骤 2：运行测试确认失败**

```bash
npm --prefix web test -- src/panels/KeysPanel.blocked.test.tsx src/panels/KeysPanel.test.tsx src/panels/KeysPanel.delete.test.tsx src/api.test.ts
npm --prefix web run typecheck
```

预期：FAIL；TypeScript 报 `blocked` 不属于现有 `KeyBinding`，或交互测试找不到带独立 accessible name 的 Switch。任务 0 已保证无 `Range.getBoundingClientRect` 基线异常；该异常若再次出现，停止并按基线问题处理。

- [x] **步骤 3：写最小实现**

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

2. `openEdit` 将旧响应缺省值归一为 false：

```ts
setEditing({ ...b, blocked: !!b.blocked, rules: { ...b.rules } })
```

3. `toggleBlocked`（与 `toggleEnabled` 并列）：

```ts
  const toggleBlocked = (b: KeyBinding, blocked: boolean) => {
    api.patchKey(b.key, { blocked }).then(onSaved).catch((e: Error) => Toast.error(e.message))
  }
```

4. 把列表的「启用」列改名为「启用规则」并给两个 Switch 不同的可访问名称：

```tsx
{
  title: '启用规则',
  dataIndex: 'enabled',
  render: (on: boolean, b: KeyBinding) => (
    <Switch
      aria-label={`启用规则：${b.alias || maskKey(b.key)}`}
      checked={on}
      onChange={(v) => toggleEnabled(b, v)}
    />
  ),
},
{
  title: '禁止访问',
  dataIndex: 'blocked',
  render: (blocked: boolean, b: KeyBinding) => (
    <Switch
      aria-label={`禁止访问：${b.alias || maskKey(b.key)}`}
      checked={!!blocked}
      onChange={(v) => toggleBlocked(b, v)}
    />
  ),
},
```

5. 用下面内容替换 Modal 中旧的单独「启用」容器：

```tsx
<div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
  <span>
    <Switch
      aria-label="编辑绑定：启用规则"
      checked={editing.enabled}
      onChange={(v) => setEditing({ ...editing, enabled: v })}
    />{' '}
    启用规则
  </span>
  <span>
    <Switch
      aria-label="编辑绑定：禁止访问"
      checked={!!editing.blocked}
      onChange={(v) => setEditing({ ...editing, blocked: v })}
    />{' '}
    禁止访问
  </span>
  {isKeyCollision && (
    <Tag color="orange">同 key 已存在，保存将覆盖</Tag>
  )}
</div>
```

`save` 与 `planKeySave` 已提交完整 `KeyBinding`，不增加第二套 payload 组装逻辑。

- [x] **步骤 4：运行确认通过**

```bash
npm --prefix web test -- src/panels/KeysPanel.blocked.test.tsx src/panels/KeysPanel.test.tsx src/panels/KeysPanel.delete.test.tsx src/api.test.ts
npm --prefix web run typecheck
npm --prefix web test
make web-build
go test ./...
```

预期：全部 exit 0；新文件 2 个交互用例 PASS；`web/dist/index.html` 更新并包含「禁止访问」文案。

- [x] **步骤 5：提交**

```bash
git add web/src/api.ts web/src/api.test.ts \
  web/src/panels/KeysPanel.tsx \
  web/src/panels/KeysPanel.test.tsx \
  web/src/panels/KeysPanel.blocked.test.tsx \
  web/src/panels/KeysPanel.delete.test.tsx \
  web/dist/index.html
git commit -m "feat(T4): 管理页支持设置和解除 key 访问禁用"
```

---

## 验收与收尾

### 任务 5：验收（acceptance-qa）

> 本任务由 executing-plans 收尾审查阶段触发 acceptance-qa 按下表执行，
> 不参与逐任务连续执行；报告与证据落盘特性目录 `acceptance/` 子目录。

| Scenario / 检查项 | 维度 | 执行方式 | 目标 | 阈值/预期 | 验收证据 |
|-------------------|------|---------|------|----------|---------|
| SDK pin 与 ABI/schema | compatibility | 验收任务 | 本 checkout | 模块版本精确为 `v7.2.119`；`ABIVersion=1`、`SchemaVersion=2` 测试通过 | 命令输出 |
| CPA runtime 版本 | compatibility | 验收任务 | live CPA management state endpoint | `X-CPA-VERSION` 可解析且 `>=v7.2.103` | response headers + 版本比较输出 |
| live CPA blocked key 固定拒绝 | e2e | 验收任务 | `POST /v1/responses` | 专用 key 返回 403；body 字节精确一致 | status/body + 清理记录 |
| 全量 Go/前端/构建 | integration | 验收任务 | 本 checkout | `make test`、typecheck、Vitest、`make web-build` 全部 exit 0 | 命令输出 |

- [ ] **步骤 1：验证 SDK pin 与完整本地套件**

```bash
test "$(go list -m -f '{{.Version}}' github.com/router-for-me/CLIProxyAPI/v7)" = "v7.2.119"
go mod verify
make test
npm --prefix web run typecheck
npm --prefix web test
make web-build
git diff --exit-code -- web/dist/index.html
```

预期：全部 exit 0；最后一条证明 tracked bundle 与源码一致。

- [ ] **步骤 2：从 live CPA response header 验证宿主版本**

`CPA_BLOCKED_TEST_KEY` 是专用且不加入 CPA 主配置的测试 key：

```bash
set -euo pipefail
: "${CPA_BASE_URL:?set CPA_BASE_URL to the live CPA origin}"
: "${CPA_MANAGEMENT_KEY:?set CPA_MANAGEMENT_KEY to a valid management key}"
CPA_BLOCKED_TEST_KEY="sk-model-mapper-blocked-e2e-20260806"
CPA_E2E_TMP="$(mktemp -d)"

curl -sS \
  -D "$CPA_E2E_TMP/state.headers" \
  -o "$CPA_E2E_TMP/state.json" \
  -H "Authorization: Bearer $CPA_MANAGEMENT_KEY" \
  "$CPA_BASE_URL/v0/management/plugins/model-mapper-plus/state"

CPA_VERSION="$(awk 'BEGIN{IGNORECASE=1} /^X-CPA-VERSION:/ {print $2}' "$CPA_E2E_TMP/state.headers" | tr -d '\r' | tail -1)"
node -e '
const raw = process.argv[1].trim().replace(/^v/, "")
const match = raw.match(/^(\d+)\.(\d+)\.(\d+)(?:-|$)/)
if (!match) throw new Error(`unparseable X-CPA-VERSION: ${process.argv[1]}`)
const got = match.slice(1).map(Number)
const min = [7, 2, 103]
const comparison = (got[0] - min[0]) || (got[1] - min[1]) || (got[2] - min[2])
if (comparison < 0) throw new Error(`CPA ${raw} is below v7.2.103`)
console.log(`CPA runtime accepted: v${raw}`)
' "$CPA_VERSION"
```

预期：版本可解析且比较通过。header 缺失、`dev` 等不可解析值、或版本低于 `v7.2.103` 均为验收失败，不得猜测版本。

- [ ] **步骤 3：用专用 key 做 live `/v1/responses` 拒绝与清理**

先确认插件 state 不含同名 binding，防止覆盖真实数据：

```bash
node -e '
const fs = require("fs")
const state = JSON.parse(fs.readFileSync(process.argv[1], "utf8"))
if ((state.key_bindings || []).some((b) => b.key === process.argv[2])) {
  throw new Error(`refusing to overwrite existing binding: ${process.argv[2]}`)
}
' "$CPA_E2E_TMP/state.json" "$CPA_BLOCKED_TEST_KEY"
```

创建专用 blocked binding，并立即安装清理 trap：

```bash
cleanup_key_access_block_e2e() {
  curl --fail-with-body -sS -X DELETE \
    -H "Authorization: Bearer $CPA_MANAGEMENT_KEY" \
    "$CPA_BASE_URL/v0/management/plugins/model-mapper-plus/keys?key=$CPA_BLOCKED_TEST_KEY" \
    > "$CPA_E2E_TMP/cleanup.json"
}
trap 'cleanup_key_access_block_e2e || true' EXIT

curl --fail-with-body -sS -X POST \
  -H "Authorization: Bearer $CPA_MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  --data "{
    \"key\":\"$CPA_BLOCKED_TEST_KEY\",
    \"alias\":\"key-access-block-e2e\",
    \"enabled\":false,
    \"blocked\":true,
    \"rules\":{\"global\":\"\",\"claude\":\"\",\"codex\":\"\",\"openai\":\"\"}
  }" \
  "$CPA_BASE_URL/v0/management/plugins/model-mapper-plus/keys" \
  > "$CPA_E2E_TMP/create.json"

CPA_BLOCK_STATUS="$(curl -sS \
  -o "$CPA_E2E_TMP/blocked-response.json" \
  -w '%{http_code}' \
  -H "Authorization: Bearer $CPA_BLOCKED_TEST_KEY" \
  -H "Content-Type: application/json" \
  --data '{"model":"gpt-5.6","input":"key-access-block-e2e"}' \
  "$CPA_BASE_URL/v1/responses")"

test "$CPA_BLOCK_STATUS" = "403"
test "$(cat "$CPA_E2E_TMP/blocked-response.json")" = \
  '{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}'

cleanup_key_access_block_e2e
trap - EXIT
curl --fail-with-body -sS \
  -H "Authorization: Bearer $CPA_MANAGEMENT_KEY" \
  "$CPA_BASE_URL/v0/management/plugins/model-mapper-plus/state" \
  > "$CPA_E2E_TMP/state-after-cleanup.json"
node -e '
const fs = require("fs")
const body = JSON.parse(fs.readFileSync(process.argv[1], "utf8"))
if ((body.key_bindings || []).some((b) => b.key === process.argv[2])) {
  throw new Error(`cleanup did not remove binding: ${process.argv[2]}`)
}
' "$CPA_E2E_TMP/state-after-cleanup.json" "$CPA_BLOCKED_TEST_KEY"
```

步骤 2 与步骤 3 必须在同一个 shell session 中依次运行，使环境变量和 cleanup trap 持续有效。预期：`/v1/responses` 精确返回 403 与固定 body；清理后的 GET state 证明专用 binding 已删除。因为拒绝发生在 `request.intercept_before`，该请求不进入模型路由、插件 executor 或上游。

- [ ] **步骤 4：记录验收结论**

将命令、exit code、headers、status/body 对比与 cleanup 结果写入 `.spec-dev/2026-08-05-key-access-block/acceptance/`。没有可用 live CPA 时，步骤 2-3 标记为 `DEFERRED`；整体最多为 `DEFERRED/PARTIAL`，不得标为 `PASS`、发布就绪或部署就绪。本地 unit/integration 通过不能替代宿主版本与 live direct-response 验证。

---

### 任务 6：合并与清理

- [ ] **步骤 1：全量验证**

在 worktree 内：

```bash
test "$(go list -m -f '{{.Version}}' github.com/router-for-me/CLIProxyAPI/v7)" = "v7.2.119"
make test
npm --prefix web run typecheck
npm --prefix web test
make web-build
git diff --exit-code -- web/dist/index.html
```

预期：全部 exit 0。任一失败先修复并重新跑完整命令组；验收任务 5 的 live 行为若为 DEFERRED，合并可由计划作者决定，但不得据此宣称发布或部署就绪。

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
# 将 frontmatter sync_commit 更新为 SYNC 的完整值
git add .spec-dev/2026-08-05-key-access-block/spec/key-access-block-design.md
git commit -m "chore(spec): sync_commit 锚定 ${SYNC:0:7}"
```

任务 0 复用既有隔离机制时，只执行步骤 1 与步骤 4；步骤 2-3 交回原隔离机制并记录。非 git 环境跳过 `sync_commit`。

---

## Self-Review（计划作者已完成）

### Scenario 映射

| Spec Scenario / 检查项 | 失败测试或验收步骤 |
|------------------------|--------------------|
| 旧 state 加载后 blocked 为 false，enabled binding 仍路由 | T1 `TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding` |
| blocked 经管理 API 写入后可再读出 | T2 `TestManagementPostKeyPersistsBlockedAndGetStateReadsIt` |
| blocked key 403 且固定 body | T3 `TestInterceptBlocksBlockedKeyWithFixedBody` |
| enabled=false 仍拒绝 | T3 `TestInterceptBlocksWhenRulesDisabled` |
| 未 blocked 的 binding 不拦截 | T3 `TestInterceptPassWhenNotBlocked` |
| 无 binding 的 key 不拦截 | T3 `TestInterceptPassWhenNoBinding` |
| 插件总开关关闭不拦截 | T3 `TestInterceptPassWhenPluginDisabled` |
| 无客户端 key 头不拦截 | T3 `TestInterceptPassWhenNoClientKey` |
| PATCH 解禁后放行 | T3 `TestManagementUnblockAllowsInterceptPass` |
| 新建时可直接 blocked | T3 `TestManagementPostBlockedKeyImmediatelyRejects` |
| 编辑窗切换并保存 blocked | T4 `编辑窗打开禁止访问并保存时 POST blocked=true` |
| 列表直接切换 blocked | T4 `列表禁止访问 Switch 直接 PATCH blocked=true` |
| 仅关闭规则不拒绝且 key 层规则不应用 | T1 既有 `TestRouteModelBindingDisabled` |
| SDK `v7.2.119` 与 CPA `>=v7.2.103` | T3 pin + T5 compatibility |
| live CPA 固定 403/body 与 binding 清理 | T5 E2E |
| 全量 Go、Vitest、typecheck、bundle | T0、T4、T5、T6 |

### 一致性与闭环

- 占位符与条件式分支扫描完成；创建/修改路径和测试文件均已锁定。
- 类型一致性：`KeyBinding.Blocked` → Management `blocked` → TypeScript `blocked`；PATCH 使用 `*bool`/`boolean` 保留 false。
- 方法一致性：`findBlockedKeyBinding` 只由 before handler 使用；before/after method 均在 `dispatchMethod` 注册。
- 契约一致性：SDK pin `v7.2.119`、native ABI 1、RPC schema 2、宿主下限 `v7.2.103` 在 spec、ADR、任务 3 与验收任务一致。
- 固定响应测试使用独立 JSON 字面量，不引用生产 `blockedQuotaExhaustedBody`。
- 生命周期闭环：任务 0 建立隔离并阻断红色基线；任务 5 保留 live 证据并清理专用 binding；任务 6 全量验证、合并、删除 worktree、锚定 `sync_commit`。
