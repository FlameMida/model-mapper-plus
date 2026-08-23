# 渠道定向与 Fast 控制 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。步骤用复选框（`- [ ]`）语法跟踪；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：为每个客户端 key 增加“供应商整选 + 认证文件单选”的隔离候选池与独立 Fast 准入开关，并在管理 API、预览和 Admin UI 中完整读写、展示与验收这两个控制维度。

**Spec**：`.spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md`

**架构**：`KeyBinding` 增量保存 `ChannelTarget` 与 `FastAllowed`；定向请求在 `model.route` 阶段跳过映射、在 `scheduler.pick` 阶段过滤并轮转认证候选、在非流式 `response.intercept_after` 阶段还原客户端模型名。Fast 关闭时复用现有 `request.intercept_before`，在 blocked 短路之后仅对 Claude 请求删除 body/header 中的 fast 标记。管理 API 与 React 管理页共享同一数据结构，前端直接读取 CPA 的 `/v0/management/auth-files`。

**技术栈**：Go 1.26、CLIProxyAPI SDK `v7.2.119`（RPC `SchemaVersion=2`）、`tidwall/gjson`、`tidwall/sjson`、React 19、Semi Design `2.62.x`、TypeScript 7、Vitest 4、Testing Library、Vite 8。

## 全局约束

- `go.mod` 继续精确固定 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.119`；`github.com/tidwall/gjson v1.18.0` 与 `github.com/tidwall/sjson v1.2.5` 只从现有间接依赖升为 direct，不引入新模块。
- state `version` 保持 `1`；`channel_target` 与 `fast_allowed` 均为增量可选字段，旧 state 不迁移。
- `fast_allowed` 未设置时语义必须等同 `true`；只有显式 `false` 才改写请求。
- `enabled` 只控制 per-key 规则映射；`blocked`、`channel_target.enabled`、`fast_allowed` 三者彼此独立，不得借用 `findKeyBinding` 的 `Enabled=true` 条件。
- 渠道候选集合固定为 `Provider ∈ suppliers`（大小写不敏感）或 `ID ∈ auth_ids` 的并集；候选必须再排除 `disabled/error/expired/revoked/invalid/unavailable/cooldown/cooling_down/quota_exhausted/exhausted/blocked` 状态。
- 池内候选按 ID 升序形成确定性顺序；同 key 的候选 ID 集合改变时轮转游标归零，进程重启后游标归零。
- 定向池空必须返回 ABI 错误 `code=auth_not_found`、`HTTPStatus=503`；不得用 `DelegateBuiltin`，不得返回池外 `AuthID`。
- 池内凭据全冷却的 429 `model_cooldown` 与 `Retry-After` 由 CPA 在调用 `scheduler.pick` 之前生成；插件不得伪造 429。
- 定向开启时顶层与 per-key 映射均不执行；非流式响应只重写 `model`、`modelVersion`、`message.model`、`message.modelVersion`、`response.model`、`response.modelVersion`，流式响应不注册 chunk 改写。
- Fast 剥离只处理 `SourceFormat == "claude"`、body 顶层大小写不敏感值 `speed:"fast"`、以及 `anthropic-beta` 中精确 token `fast-mode-2026-02-01`；其余 body 字段与 beta token 原样保留。
- `PATCH /keys` 中 `channel_target` 缺席或为 `null` 都不改变旧值；关闭定向必须发送完整对象并只把 `enabled` 改为 `false`。
- Admin UI 必须只使用 Semi Design 组件；弹窗固定三页“基础 / 渠道定向 / 规则集”，渠道定向关闭后配置保留且两个选择区禁用。
- CPA `auth-files` 列表响应固定读取 `{files:[{id,provider,status,disabled,label}]}`；前端不代理该接口。
- Scheduler 是宿主全局单实例：与 `cpa-plugin-key-policy` 同时声明时只有宿主选择的首个插件生效；README 必须明确这一限制，不做运行时冲突探测。
- 每个实施任务遵循 TDD：失败测试 → 确认红灯 → 最小实现 → 确认绿灯 → 提交。

## 相关测试范围

项目没有测试影响分析工具；本特性覆盖单一 Go main package 与 KeysPanel 前端子树。任务 0 运行以下现有范围作为基线：

```bash
go mod download
npm --prefix web ci
go test .
npm --prefix web run typecheck
npm --prefix web test -- src/api.test.ts src/panels/KeysPanel.test.tsx src/panels/KeysPanel.blocked.test.tsx
```

预期：全部 exit 0，`go.mod`、`go.sum`、`web/package-lock.json` 无变化。实施中新增的 `scheduler_test.go`、`fast_strip_test.go`、`ChannelTargetEditor.test.tsx`、`KeysPanel.channel-target.test.tsx` 由对应任务的定向命令覆盖；最终任务仍运行全量安全网。

## 文件结构（将创建/修改）

| 路径 | 动作 | 职责 |
|------|------|------|
| `state.go` | 修改 | `ChannelTarget` / `FastAllowed` 数据结构、深拷贝、独立 key 查找与数组校验 |
| `state_test.go` | 修改 | 旧 state 零迁移与新字段校验回归 |
| `main.go` | 修改 | 能力注册、定向跳过映射、Scheduler、Fast 剥离、响应还原与 ABI 错误状态 |
| `main_test.go` | 修改 | 能力/dispatch、映射跳过、响应还原与流式透传 |
| `scheduler_test.go` | 创建 | 候选池并集、动态供应商、轮转、fail-open、池空 503 与宿主冷却契约 |
| `fast_strip_test.go` | 创建 | body/header Fast 标记剥离与默认放行 |
| `management.go` | 修改 | PATCH 新字段、preview 定向解析结果与 mapping_skipped |
| `management_test.go` | 修改 | PATCH、重复 ID 拒绝、preview 定向场景 |
| `go.mod` / `go.sum` | 修改/核对 | 把已存在的 gjson/sjson 固定为直接依赖 |
| `web/src/api.ts` | 修改 | 新类型、PATCH 字段、preview 类型与 auth-files 客户端 |
| `web/src/api.test.ts` | 修改 | auth-files 解包、授权头与错误响应 |
| `web/src/components/ChannelTargetEditor.tsx` | 创建 | 总开关、供应商整选、认证文件分组多选、组头全选、加载错误重试 |
| `web/src/components/ChannelTargetEditor.test.tsx` | 创建 | 混选回显、关闭置灰与组头全选组件测试 |
| `web/src/panels/KeysPanel.tsx` | 修改 | 三页 Tabs、Fast 开关、auth-files 生命周期、列表摘要列 |
| `web/src/panels/KeysPanel.test.tsx` | 修改 | 保存规划保留新字段、旧绑定 Fast 默认允许 |
| `web/src/panels/KeysPanel.channel-target.test.tsx` | 创建 | 弹窗集成、auth-files 失败重试与表格摘要测试 |
| `web/dist/index.html` | 修改 | `npm run build` 生成的单文件 Admin UI |
| `README.md` | 修改 | 使用说明、Fast 语义、定向池空行为与 Scheduler 单实例限制 |

---

### 任务 0：建立隔离工作区

- [x] **步骤 1：检测已有隔离**

运行：`git rev-parse --git-dir` 与 `git rev-parse --git-common-dir`
两者不同、且 `git rev-parse --show-superproject-working-tree` 无输出（排除 submodule）
→ 已在隔离工作区，跳过本任务。

- [x] **步骤 2：建立 worktree**

有原生 worktree 工具（如 EnterWorktree）或 using-git-worktrees skill 时优先使用（Codex 无原生 worktree 工具，直接走下面的手工路径）；否则手工降级：
确认 `.worktrees/` 已被忽略（`git check-ignore -q .worktrees`，未忽略先加入 `.gitignore` 并提交），然后：

```bash
git worktree add .worktrees/plan/2026-08-22-channel-target-and-fast-control -b plan/2026-08-22-channel-target-and-fast-control
cd .worktrees/plan/2026-08-22-channel-target-and-fast-control
```

- [x] **步骤 3：安装依赖并验证基线**

```bash
go mod download
npm --prefix web ci
go test .
npm --prefix web run typecheck
npm --prefix web test -- src/api.test.ts src/panels/KeysPanel.test.tsx src/panels/KeysPanel.blocked.test.tsx
git diff --exit-code -- go.mod go.sum web/package-lock.json
```

预期：全部 exit 0。基线测试失败 → 停下报告，先问再继续；声明命令不可用时回退 `go test ./... && npm --prefix web test` 并记录计划测试范围失效。

执行记录（2026-08-23）：环境未提供 Node/npm，使用已有 Bun 1.4.0 执行 `bun install --cwd web --no-save --frozen-lockfile`、`bun run --cwd web typecheck` 与对应 Vitest 脚本；Go 全包测试、typecheck、3 个基线测试文件（18 tests）均通过，项目既有锁文件无变化，Bun 临时生成的 `web/bun.lock` 已删除。

---

## 数据与路由契约

### 任务 1：持久化渠道定向与 Fast 默认值

**文件**：
- 修改：`state.go`
- 修改：`state_test.go`

**接口**：
- 消费：现有 `KeyBinding`、`validateState(st State) error`、`cloneKeyBindings([]KeyBinding) []KeyBinding`
- 产出：`ChannelTarget{Enabled bool, Suppliers []string, AuthIDs []string}`
- 产出：`KeyBinding.ChannelTarget *ChannelTarget`、`KeyBinding.FastAllowed *bool`
- 产出：`findKeyBindingByKey(bindings []KeyBinding, apiKey string) (KeyBinding, bool)`，忽略 `Enabled/Blocked` 等功能开关
- 产出：`findActiveChannelTarget(bindings []KeyBinding, apiKey string) (KeyBinding, bool)`，只要求绑定存在且 `ChannelTarget.Enabled=true`

- [x] **步骤 1：写失败测试**

在 `state_test.go` 追加：

```go
func TestChannelTargetStateContract(t *testing.T) {
	t.Run("存量文件零迁移加载", func(t *testing.T) {
		raw := []byte(`{"version":1,"rules":{},"key_bindings":[{"key":"sk-old","enabled":false,"blocked":false,"rules":{}}]}`)
		var st State
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatalf("unmarshal old state: %v", err)
		}
		if err := validateState(st); err != nil {
			t.Fatalf("validate old state: %v", err)
		}
		got := st.KeyBindings[0]
		if got.ChannelTarget != nil || got.FastAllowed != nil {
			t.Fatalf("old optional fields = target:%+v fast:%v, want nil/nil", got.ChannelTarget, got.FastAllowed)
		}
		if _, ok := findActiveChannelTarget(st.KeyBindings, "sk-old"); ok {
			t.Fatal("old binding must not activate channel target")
		}
	})

	t.Run("数组元素非空且去重", func(t *testing.T) {
		cases := []struct {
			name string
			b    KeyBinding
			want string
		}{
			{"supplier empty", KeyBinding{Key: "k1", ChannelTarget: &ChannelTarget{Suppliers: []string{" "}}}, "suppliers[0]"},
			{"supplier duplicate fold", KeyBinding{Key: "k2", ChannelTarget: &ChannelTarget{Suppliers: []string{"Gemini", "gemini"}}}, "duplicate"},
			{"auth id empty", KeyBinding{Key: "k3", ChannelTarget: &ChannelTarget{AuthIDs: []string{""}}}, "auth_ids[0]"},
			{"auth id duplicate", KeyBinding{Key: "k4", ChannelTarget: &ChannelTarget{AuthIDs: []string{"a", "a"}}}, "duplicate"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := validateState(State{Version: stateVersion, KeyBindings: []KeyBinding{tc.b}})
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("validate error = %v, want substring %q", err, tc.want)
				}
			})
		}
	})

	t.Run("功能开关彼此独立", func(t *testing.T) {
		fastOff := false
		bindings := []KeyBinding{{
			Key: "sk-k", Enabled: false, Blocked: false, FastAllowed: &fastOff,
			ChannelTarget: &ChannelTarget{Enabled: true, Suppliers: []string{"gemini"}},
		}}
		got, ok := findKeyBindingByKey(bindings, "sk-k")
		if !ok || got.FastAllowed == nil || *got.FastAllowed {
			t.Fatalf("findKeyBindingByKey = %+v, %v", got, ok)
		}
		if _, ok := findKeyBinding(bindings, "sk-k"); ok {
			t.Fatal("existing rule lookup must still honor Enabled=false")
		}
		if _, ok := findActiveChannelTarget(bindings, "sk-k"); !ok {
			t.Fatal("channel target must not depend on rule Enabled")
		}
	})

	t.Run("深拷贝不共享定向数组", func(t *testing.T) {
		in := []KeyBinding{{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, Suppliers: []string{"gemini"}, AuthIDs: []string{"f1"}}}}
		out := cloneKeyBindings(in)
		out[0].ChannelTarget.Suppliers[0] = "claude"
		out[0].ChannelTarget.AuthIDs[0] = "f2"
		if in[0].ChannelTarget.Suppliers[0] != "gemini" || in[0].ChannelTarget.AuthIDs[0] != "f1" {
			t.Fatalf("clone mutated source: %+v", in[0].ChannelTarget)
		}
	})
}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run TestChannelTargetStateContract -v
```

预期：FAIL，编译错误包含 `undefined: ChannelTarget`、`KeyBinding.FastAllowed undefined` 或 `undefined: findActiveChannelTarget`。

- [x] **步骤 3：写最小实现**

把 `state.go` 的 `KeyBinding` 定义改为：

```go
type ChannelTarget struct {
	Enabled   bool     `json:"enabled"`
	Suppliers []string `json:"suppliers,omitempty"`
	AuthIDs   []string `json:"auth_ids,omitempty"`
}

type KeyBinding struct {
	Key           string         `json:"key"`
	Alias         string         `json:"alias"`
	Enabled       bool           `json:"enabled"`
	Blocked       bool           `json:"blocked"`
	Rules         RuleSet        `json:"rules"`
	ChannelTarget *ChannelTarget `json:"channel_target,omitempty"`
	FastAllowed   *bool          `json:"fast_allowed,omitempty"`
}
```

在 `validateState` 的 binding 循环中、追加 rules segments 之前加入：

```go
		if b.ChannelTarget != nil {
			if err := validateUniqueNonEmptyStrings(fmt.Sprintf("key_bindings[%d].channel_target.suppliers", i), b.ChannelTarget.Suppliers, true); err != nil {
				return err
			}
			if err := validateUniqueNonEmptyStrings(fmt.Sprintf("key_bindings[%d].channel_target.auth_ids", i), b.ChannelTarget.AuthIDs, false); err != nil {
				return err
			}
		}
```

并在 `validateState` 之前新增：

```go
func validateUniqueNonEmptyStrings(path string, values []string, foldCase bool) error {
	seen := make(map[string]struct{}, len(values))
	for i, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("%s[%d]: value is required", path, i)
		}
		key := value
		if foldCase {
			key = strings.ToLower(value)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s[%d]: duplicate value %q", path, i, value)
		}
		seen[key] = struct{}{}
	}
	return nil
}
```

把 `findKeyBinding` 与深拷贝段改为：

```go
func findKeyBindingByKey(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}

func findKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	b, ok := findKeyBindingByKey(bindings, apiKey)
	return b, ok && b.Enabled
}

func findActiveChannelTarget(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	b, ok := findKeyBindingByKey(bindings, apiKey)
	if !ok || b.ChannelTarget == nil || !b.ChannelTarget.Enabled {
		return KeyBinding{}, false
	}
	return b, true
}

func cloneChannelTarget(in *ChannelTarget) *ChannelTarget {
	if in == nil {
		return nil
	}
	out := *in
	out.Suppliers = append([]string(nil), in.Suppliers...)
	out.AuthIDs = append([]string(nil), in.AuthIDs...)
	return &out
}

func cloneKeyBindings(in []KeyBinding) []KeyBinding {
	if in == nil {
		return nil
	}
	out := make([]KeyBinding, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].ChannelTarget = cloneChannelTarget(in[i].ChannelTarget)
		if in[i].FastAllowed != nil {
			value := *in[i].FastAllowed
			out[i].FastAllowed = &value
		}
	}
	return out
}
```

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w state.go state_test.go
go test . -run 'TestChannelTargetStateContract|TestValidateState|TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding' -v
```

预期：PASS；现有 blocked 与 enabled 语义保持不变。

- [x] **步骤 5：提交**

```bash
git add state.go state_test.go
git commit -m "feat(T1): 持久化渠道定向与 Fast 配置"
```

---

### 任务 2：定向开启时跳过两层模型映射

**文件**：
- 修改：`main.go`
- 修改：`main_test.go`

**接口**：
- 消费：任务 1 的 `findActiveChannelTarget(bindings, apiKey)`
- 产出：`routeModel(...)` 在插件启用且 key 定向开启时返回零值 `routeDecision{}`，即 `Handled=false`

- [x] **步骤 1：写失败测试**

在 `main_test.go` 追加：

```go
func TestRouteModelChannelTarget(t *testing.T) {
	t.Run("定向时模型名不被改写", func(t *testing.T) {
		src := ruleSource{
			Rules: RuleSet{Global: "model-m=>model-n"},
			KeyBindings: []KeyBinding{{
				Key: "sk-k", Enabled: true,
				Rules: RuleSet{Global: "model-n=>model-p"},
				ChannelTarget: &ChannelTarget{Enabled: true, Suppliers: []string{"gemini"}},
			}},
		}
		decision, err := routeModel(Config{Enabled: true}, src, "openai", "model-m", "sk-k")
		if err != nil {
			t.Fatalf("routeModel: %v", err)
		}
		if decision.Handled || decision.UpstreamModel != "" {
			t.Fatalf("targeted decision = %+v, want unhandled original model", decision)
		}

		untargeted, err := routeModel(Config{Enabled: true}, src, "openai", "model-m", "sk-other")
		if err != nil || !untargeted.Handled || untargeted.UpstreamModel != "model-n" {
			t.Fatalf("untargeted mapping regression: decision=%+v err=%v", untargeted, err)
		}
	})
}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run TestRouteModelChannelTarget -v
```

预期：FAIL，定向 key 仍得到 `Handled=true`、`UpstreamModel=model-p`。

- [x] **步骤 3：写最小实现**

在 `main.go` 的 `routeModel` 中，`Config.Enabled` 检查之后、应用顶层规则之前插入：

```go
	if _, targeted := findActiveChannelTarget(src.KeyBindings, apiKey); targeted {
		return routeDecision{}, nil
	}
```

完整函数开头应为：

```go
func routeModel(cfg Config, src ruleSource, format, model, apiKey string) (routeDecision, error) {
	if !cfg.Enabled {
		return routeDecision{}, nil
	}
	if _, targeted := findActiveChannelTarget(src.KeyBindings, apiKey); targeted {
		return routeDecision{}, nil
	}
	current := model
	mapped, matched, err := applyRuleSet(src.Rules, format, current)
	if err != nil {
		return routeDecision{}, err
	}
	if matched {
		current = mapped
	}
	if binding, ok := findKeyBinding(src.KeyBindings, apiKey); ok {
		mapped, matched, err := applyRuleSet(binding.Rules, format, current)
		if err != nil {
			return routeDecision{}, err
		}
		if matched {
			current = mapped
		}
	}
	if current == model {
		return routeDecision{}, nil
	}
	return routeDecision{Handled: true, OriginalModel: model, UpstreamModel: current}, nil
}
```

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w main.go main_test.go
go test . -run 'TestRouteModelChannelTarget|TestRouteModel|TestHandleModelRoute' -v
```

预期：PASS；定向 key 不被映射，非定向既有测试继续通过。

- [x] **步骤 5：提交**

```bash
git add main.go main_test.go
git commit -m "feat(T2): 定向请求跳过规则映射"
```

---

## 运行时钩子

### 任务 3：Scheduler 候选池过滤、轮转与 503 契约

**文件**：
- 修改：`main.go`
- 修改：`main_test.go`
- 创建：`scheduler_test.go`

**接口**：
- 消费：任务 1 的 `findActiveChannelTarget`
- 产出：`handleSchedulerPick(raw []byte) ([]byte, error)`
- 产出：`channelRoundRobin.pick(key string, candidates []pluginapi.SchedulerAuthCandidate) string`
- 产出：注册 JSON `capabilities.scheduler=true`，dispatch 支持 `pluginabi.MethodSchedulerPick`
- 产出：`pluginMethodError{Code, Message, HTTPStatus}` 使 ABI 错误携带 503

- [x] **步骤 1：写失败测试**

创建 `scheduler_test.go`：

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func schedulerEnvelope(t *testing.T, req pluginapi.SchedulerPickRequest) pluginabi.Envelope {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envRaw, err := handleMethod(pluginabi.MethodSchedulerPick, raw)
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil {
		t.Fatalf("decode envelope: %v raw=%s", err, envRaw)
	}
	return env
}

func schedulerResponse(t *testing.T, req pluginapi.SchedulerPickRequest) pluginapi.SchedulerPickResponse {
	t.Helper()
	env := schedulerEnvelope(t, req)
	if !env.OK {
		t.Fatalf("scheduler envelope error: %+v", env.Error)
	}
	var resp pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func seedSchedulerBinding(t *testing.T, binding KeyBinding) {
	t.Helper()
	setupManagementTest(t, Config{Enabled: true})
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: raw})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed binding status=%d body=%s", resp.StatusCode, resp.Body)
	}
	channelTargetRoundRobin.reset()
	t.Cleanup(channelTargetRoundRobin.reset)
}

func schedulerRequest(apiKey string, candidates ...pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickRequest {
	return pluginapi.SchedulerPickRequest{
		Provider: "mixed",
		Providers: []string{"gemini", "claude"},
		Model: "model-m",
		Options: pluginapi.SchedulerOptions{Headers: http.Header{"Authorization": {"Bearer " + apiKey}}},
		Candidates: candidates,
	}
}

func TestChannelTargetScheduler(t *testing.T) {
	t.Run("单选认证文件命中", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		resp := schedulerResponse(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
			pluginapi.SchedulerAuthCandidate{ID: "f1", Provider: "claude", Status: "active"},
		))
		if !resp.Handled || resp.AuthID != "f1" || resp.DelegateBuiltin != "" {
			t.Fatalf("response = %+v", resp)
		}
	})

	t.Run("供应商整选动态入池", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, Suppliers: []string{"Gemini"}}})
		first := schedulerResponse(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "g1", Provider: "gemini", Status: "active"},
		))
		if first.AuthID != "g1" {
			t.Fatalf("first = %+v", first)
		}
		seen := map[string]bool{}
		for i := 0; i < 2; i++ {
			resp := schedulerResponse(t, schedulerRequest("sk-k",
				pluginapi.SchedulerAuthCandidate{ID: "g1", Provider: "gemini", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "g2", Provider: "GEMINI", Status: "active"},
			))
			seen[resp.AuthID] = true
		}
		if !seen["g1"] || !seen["g2"] {
			t.Fatalf("dynamic provider candidates not both selected: %v", seen)
		}
	})

	t.Run("池内多凭据轮转分摊", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"c", "a", "b"}}})
		want := []string{"a", "b", "c", "a", "b", "c"}
		for i, id := range want {
			resp := schedulerResponse(t, schedulerRequest("sk-k",
				pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "gemini", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "c", Provider: "claude", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "claude", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "claude", Status: "active"},
			))
			if resp.AuthID != id {
				t.Fatalf("pick %d = %q, want %q", i, resp.AuthID, id)
			}
		}
	})

	t.Run("无法识别上下文时 fail-open", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		for _, req := range []pluginapi.SchedulerPickRequest{
			{Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "f1", Status: "active"}}},
			schedulerRequest("sk-other", pluginapi.SchedulerAuthCandidate{ID: "f1", Status: "active"}),
		} {
			resp := schedulerResponse(t, req)
			if resp.Handled {
				t.Fatalf("fail-open response = %+v", resp)
			}
		}
	})

	t.Run("池内候选全部不可用", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		env := schedulerEnvelope(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "f1", Provider: "claude", Status: "cooldown"},
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
		))
		if env.OK || env.Error == nil || env.Error.Code != "auth_not_found" || env.Error.HTTPStatus != http.StatusServiceUnavailable {
			t.Fatalf("envelope = %+v", env)
		}
	})

	t.Run("全部冷却走宿主原生应答", func(t *testing.T) {
		model := "model-m"
		next := time.Now().Add(time.Minute)
		auths := []*cliproxyauth.Auth{{
			ID: "f1", Provider: "claude",
			ModelStates: map[string]*cliproxyauth.ModelState{model: {
				Status: cliproxyauth.StatusActive, Unavailable: true, NextRetryAfter: next,
				Quota: cliproxyauth.QuotaState{Exceeded: true, NextRecoverAt: next},
			}},
		}}
		_, err := (&cliproxyauth.FillFirstSelector{}).Pick(context.Background(), "claude", model, cliproxyexecutor.Options{}, auths)
		if err == nil {
			t.Fatal("host selector must reject all-cooldown before scheduler.pick")
		}
		var statusErr interface{ StatusCode() int }
		var headerErr interface{ Headers() http.Header }
		if !errors.As(err, &statusErr) || statusErr.StatusCode() != http.StatusTooManyRequests {
			t.Fatalf("cooldown status error = %T %v", err, err)
		}
		if !errors.As(err, &headerErr) || headerErr.Headers().Get("Retry-After") == "" {
			t.Fatalf("cooldown headers missing: %T %v", err, err)
		}
	})
}
```

在 `main_test.go` 的注册测试中追加：

```go
	if !reg.Capabilities.Scheduler {
		t.Fatalf("capabilities=%#v, want scheduler=true", reg.Capabilities)
	}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestChannelTargetScheduler|TestPluginRegistration' -v
```

预期：FAIL，编译错误包含 `registrationCapabilities.Scheduler undefined`、`undefined: channelTargetRoundRobin`，或 dispatch 返回 `unknown_method`。

- [x] **步骤 3：写最小实现**

在 `main.go` imports 增加 `errors`、`sort`；在 `registrationCapabilities` 与 `pluginRegistration` 增加：

```go
type registrationCapabilities struct {
	// 既有字段保持原顺序。
	Scheduler           bool `json:"scheduler"`
	ResponseInterceptor bool `json:"response_interceptor"`
}

// pluginRegistration().Capabilities 中：
Scheduler: true,
```

在包级状态区新增轮转器：

```go
type channelRoundRobinState struct {
	signature string
	next      uint64
}

type channelRoundRobin struct {
	mu     sync.Mutex
	states map[string]channelRoundRobinState
}

var channelTargetRoundRobin = channelRoundRobin{states: make(map[string]channelRoundRobinState)}

func (r *channelRoundRobin) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = make(map[string]channelRoundRobinState)
}

func (r *channelRoundRobin) pick(key string, candidates []pluginapi.SchedulerAuthCandidate) string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	signature := strings.Join(ids, "\x00")

	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.states[key]
	if state.signature != signature {
		state = channelRoundRobinState{signature: signature}
	}
	id := ids[state.next%uint64(len(ids))]
	state.next++
	r.states[key] = state
	return id
}
```

新增候选过滤与 handler：

```go
var unavailableSchedulerStatuses = map[string]struct{}{
	"disabled": {}, "error": {}, "expired": {}, "revoked": {}, "invalid": {},
	"unavailable": {}, "cooldown": {}, "cooling_down": {},
	"quota_exhausted": {}, "exhausted": {}, "blocked": {},
}

func schedulerCandidateUsable(candidate pluginapi.SchedulerAuthCandidate) bool {
	_, unavailable := unavailableSchedulerStatuses[strings.ToLower(strings.TrimSpace(candidate.Status))]
	return strings.TrimSpace(candidate.ID) != "" && !unavailable
}

func schedulerCandidateTargeted(candidate pluginapi.SchedulerAuthCandidate, target *ChannelTarget) bool {
	for _, id := range target.AuthIDs {
		if strings.TrimSpace(id) == strings.TrimSpace(candidate.ID) {
			return true
		}
	}
	for _, supplier := range target.Suppliers {
		if strings.EqualFold(strings.TrimSpace(supplier), strings.TrimSpace(candidate.Provider)) {
			return true
		}
	}
	return false
}

type pluginMethodError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *pluginMethodError) Error() string { return e.Message }

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled {
		return json.Marshal(pluginapi.SchedulerPickResponse{Handled: false})
	}
	apiKey := apiKeyFromHeaders(http.Header(req.Options.Headers))
	binding, targeted := findActiveChannelTarget(loadedRuleSource().KeyBindings, apiKey)
	if !targeted {
		return json.Marshal(pluginapi.SchedulerPickResponse{Handled: false})
	}
	pool := make([]pluginapi.SchedulerAuthCandidate, 0, len(req.Candidates))
	for _, candidate := range req.Candidates {
		if schedulerCandidateUsable(candidate) && schedulerCandidateTargeted(candidate, binding.ChannelTarget) {
			pool = append(pool, candidate)
		}
	}
	if len(pool) == 0 {
		return nil, &pluginMethodError{
			Code: "auth_not_found", Message: "no usable auth candidate in channel target",
			HTTPStatus: http.StatusServiceUnavailable,
		}
	}
	return json.Marshal(pluginapi.SchedulerPickResponse{
		Handled: true,
		AuthID: channelTargetRoundRobin.pick(binding.Key, pool),
	})
}
```

把错误信封改为保留 typed status：

```go
func wrapEnvelope(payload []byte, err error) ([]byte, error) {
	if err != nil {
		var methodErr *pluginMethodError
		if errors.As(err, &methodErr) {
			return errorEnvelopeWithStatus(methodErr.Code, methodErr.Message, methodErr.HTTPStatus), nil
		}
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	return okEnvelope(json.RawMessage(payload))
}

func errorEnvelopeWithStatus(code, message string, status int) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{Code: code, Message: message, HTTPStatus: status},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}
```

在 `dispatchMethod` 中加入：

```go
	case pluginabi.MethodSchedulerPick:
		return wrapEnvelope(handleSchedulerPick(request))
```

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w main.go main_test.go scheduler_test.go
go test . -run 'TestChannelTargetScheduler|TestPluginRegistration|TestHandleMethodDispatchesRegisterReconfigureAndUnknown' -v
go test -race . -run TestChannelTargetScheduler -v
```

预期：全部 PASS；race 检查不报告轮转器并发读写。

- [x] **步骤 5：提交**

```bash
git add main.go main_test.go scheduler_test.go
git commit -m "feat(T3): 定向过滤并轮转 Scheduler 候选池"
```

---

### 任务 4：Fast 关闭时剥离 Claude 标记

**文件**：
- 修改：`main.go`
- 创建：`fast_strip_test.go`
- 修改：`go.mod`
- 核对：`go.sum`

**接口**：
- 消费：任务 1 的 `findKeyBindingByKey` 与 `KeyBinding.FastAllowed`
- 产出：`stripFastBeta(headers http.Header) (http.Header, []string)`
- 产出：`handleRequestInterceptBefore` 在 blocked 放行之后返回 body/header 的最小差量

- [x] **步骤 1：写失败测试**

创建 `fast_strip_test.go`：

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"

	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func fastIntercept(t *testing.T, binding KeyBinding, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	t.Helper()
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{binding})
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleRequestInterceptBefore(raw)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func boolPointer(value bool) *bool { return &value }

func TestFastAllowedRequestRewrite(t *testing.T) {
	binding := KeyBinding{Key: "sk-k", FastAllowed: boolPointer(false)}

	t.Run("仅 body 带 speed", func(t *testing.T) {
		resp := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{"Authorization": {"Bearer sk-k"}},
			Body: []byte(`{"model":"m","speed":"FaSt","messages":[]}`),
		})
		if resp.Terminate || string(resp.Body) != `{"model":"m","messages":[]}` {
			t.Fatalf("response = %+v body=%s", resp, resp.Body)
		}
	})

	t.Run("仅 beta 头标记", func(t *testing.T) {
		resp := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{
				"Authorization": {"Bearer sk-k"},
				"Anthropic-Beta": {"fast-mode-2026-02-01,prompt-caching-2024"},
			},
			Body: []byte(`{"model":"m"}`),
		})
		if got := resp.Headers.Values("Anthropic-Beta"); len(got) != 1 || got[0] != "prompt-caching-2024" {
			t.Fatalf("beta headers = %v clear=%v", got, resp.ClearHeaders)
		}
	})

	t.Run("默认放行", func(t *testing.T) {
		oldBinding := KeyBinding{Key: "sk-k"}
		resp := fastIntercept(t, oldBinding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{
				"Authorization": {"Bearer sk-k"},
				"Anthropic-Beta": {"fast-mode-2026-02-01"},
			},
			Body: []byte(`{"model":"m","speed":"fast"}`),
		})
		if len(resp.Body) != 0 || len(resp.Headers) != 0 || len(resp.ClearHeaders) != 0 {
			t.Fatalf("old binding must pass through: %+v", resp)
		}
	})

	t.Run("非 Claude 与 blocked 优先级", func(t *testing.T) {
		resp := fastIntercept(t, KeyBinding{Key: "sk-k", Blocked: true, FastAllowed: boolPointer(false)}, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{"Authorization": {"Bearer sk-k"}},
			Body: []byte(`{"speed":"fast"}`),
		})
		if !resp.Terminate || resp.StatusCode != http.StatusForbidden || len(resp.Body) != 0 {
			t.Fatalf("blocked must terminate before fast rewrite: %+v", resp)
		}

		pass := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "openai",
			Headers: http.Header{"Authorization": {"Bearer sk-k"}},
			Body: []byte(`{"speed":"fast"}`),
		})
		if len(pass.Body) != 0 || len(pass.Headers) != 0 {
			t.Fatalf("non-Claude request changed: %+v", pass)
		}
	})
}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run TestFastAllowedRequestRewrite -v
```

预期：FAIL；body/header 未被剥离，或 `FastAllowed` 尚未被 handler 使用。

- [x] **步骤 3：写最小实现**

在 `main.go` imports 加入：

```go
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
```

新增 helper：

```go
const fastModeBeta = "fast-mode-2026-02-01"

func stripFastBeta(headers http.Header) (http.Header, []string) {
	values := headers.Values("Anthropic-Beta")
	if len(values) == 0 {
		return nil, nil
	}
	kept := make([]string, 0)
	found := false
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if token == fastModeBeta {
				found = true
				continue
			}
			kept = append(kept, token)
		}
	}
	if !found {
		return nil, nil
	}
	if len(kept) == 0 {
		return nil, []string{"Anthropic-Beta"}
	}
	return http.Header{"Anthropic-Beta": {strings.Join(kept, ",")}}, nil
}

func stripFastBody(body []byte) ([]byte, bool, error) {
	value := gjson.GetBytes(body, "speed")
	if !value.Exists() || !strings.EqualFold(value.String(), "fast") {
		return nil, false, nil
	}
	out, err := sjson.DeleteBytes(body, "speed")
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
```

把 `handleRequestInterceptBefore` 的 blocked 分支之后、最终空响应之前改为：

```go
	binding, exists := findKeyBindingByKey(loadedRuleSource().KeyBindings, apiKey)
	if !exists || binding.FastAllowed == nil || *binding.FastAllowed || !strings.EqualFold(req.SourceFormat, "claude") {
		return json.Marshal(pluginapi.RequestInterceptResponse{})
	}
	body, bodyChanged, err := stripFastBody(req.Body)
	if err != nil {
		return nil, err
	}
	headers, clearHeaders := stripFastBeta(req.Headers)
	resp := pluginapi.RequestInterceptResponse{Headers: headers, ClearHeaders: clearHeaders}
	if bodyChanged {
		resp.Body = body
	}
	return json.Marshal(resp)
```

把 `go.mod` direct require block改为：

```go
require (
	github.com/gin-gonic/gin v1.10.1
	github.com/tidwall/gjson v1.18.0
	github.com/tidwall/sjson v1.2.5
	gopkg.in/yaml.v3 v3.0.1
)
```

再运行 `go mod tidy`，预期只调整 direct/indirect 分组，版本与 `go.sum` 校验值不变。

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w main.go fast_strip_test.go
go mod tidy
go test . -run 'TestFastAllowedRequestRewrite|TestInterceptBlocksBlockedKeyWithFixedBody|TestInterceptAfterIsPassThrough' -v
git diff --exit-code -- go.sum
```

预期：测试 PASS；`go.sum` 无内容变化。

- [x] **步骤 5：提交**

```bash
git add main.go fast_strip_test.go go.mod go.sum
git commit -m "feat(T4): 按 key 剥离 Claude Fast 标记"
```

---

### 任务 5：定向响应的非流式模型名还原

**文件**：
- 修改：`main.go`
- 修改：`main_test.go`

**接口**：
- 消费：任务 1 的 `findActiveChannelTarget` 与既有 `rewriteResponseModelFields(body, model)`
- 产出：`handleResponseInterceptAfter(raw []byte) ([]byte, error)`
- 产出：注册 JSON `capabilities.response_interceptor=true`，dispatch 支持 `pluginabi.MethodResponseInterceptAfter`

- [x] **步骤 1：写失败测试**

在 `main_test.go` 追加：

```go
func responseInterceptResult(t *testing.T, req pluginapi.ResponseInterceptRequest) pluginapi.ResponseInterceptResponse {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envRaw, err := handleMethod(pluginabi.MethodResponseInterceptAfter, raw)
	if err != nil {
		t.Fatalf("handle response intercept: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil || !env.OK {
		t.Fatalf("envelope=%+v err=%v raw=%s", env, err, envRaw)
	}
	var resp pluginapi.ResponseInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestChannelTargetResponseIntercept(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body: []byte(`{"key":"sk-k","channel_target":{"enabled":true,"suppliers":["gemini"]}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("seed binding: %d %s", post.StatusCode, post.Body)
	}

	t.Run("非流式还原", func(t *testing.T) {
		resp := responseInterceptResult(t, pluginapi.ResponseInterceptRequest{
			RequestedModel: "client-model", Stream: false,
			RequestHeaders: http.Header{"Authorization": {"Bearer sk-k"}},
			Body: []byte(`{"model":"upstream-real-name","modelVersion":"v2","message":{"model":"nested"},"response":{"modelVersion":"nested-v"}}`),
		})
		var body map[string]any
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatalf("decode rewritten body: %v body=%s", err, resp.Body)
		}
		message := body["message"].(map[string]any)
		response := body["response"].(map[string]any)
		if body["model"] != "client-model" || body["modelVersion"] != "client-model" ||
			message["model"] != "client-model" || response["modelVersion"] != "client-model" {
			t.Fatalf("rewritten body = %#v", body)
		}
	})

	t.Run("流式透传", func(t *testing.T) {
		resp := responseInterceptResult(t, pluginapi.ResponseInterceptRequest{
			RequestedModel: "client-model", Stream: true,
			RequestHeaders: http.Header{"Authorization": {"Bearer sk-k"}},
			Body: []byte(`{"model":"upstream-real-name"}`),
		})
		if len(resp.Body) != 0 || len(resp.Headers) != 0 {
			t.Fatalf("stream response must be untouched: %+v", resp)
		}
	})

	t.Run("非定向与缺 key 原样透传", func(t *testing.T) {
		for _, headers := range []http.Header{
			{"Authorization": {"Bearer sk-other"}},
			nil,
		} {
			resp := responseInterceptResult(t, pluginapi.ResponseInterceptRequest{
				RequestedModel: "client-model", RequestHeaders: headers,
				Body: []byte(`{"model":"upstream-real-name"}`),
			})
			if len(resp.Body) != 0 {
				t.Fatalf("untargeted response changed: %+v", resp)
			}
		}
	})
}
```

在注册测试追加：

```go
	if !reg.Capabilities.ResponseInterceptor {
		t.Fatalf("capabilities=%#v, want response_interceptor=true", reg.Capabilities)
	}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run 'TestChannelTargetResponseIntercept|TestPluginRegistration' -v
```

预期：FAIL；注册字段仍为 false，或 dispatch 返回 `unknown_method`。

- [x] **步骤 3：写最小实现**

在 `pluginRegistration().Capabilities` 中加入：

```go
	ResponseInterceptor: true,
```

新增 handler：

```go
func handleResponseInterceptAfter(raw []byte) ([]byte, error) {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled || req.Stream || strings.TrimSpace(req.RequestedModel) == "" {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	apiKey := apiKeyFromHeaders(req.RequestHeaders)
	if _, targeted := findActiveChannelTarget(loadedRuleSource().KeyBindings, apiKey); !targeted {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	body, changed, err := rewriteResponseModelFields(req.Body, req.RequestedModel)
	if err != nil {
		return nil, err
	}
	if !changed {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	return json.Marshal(pluginapi.ResponseInterceptResponse{Body: body})
}
```

在 `dispatchMethod` 中加入：

```go
	case pluginabi.MethodResponseInterceptAfter:
		return wrapEnvelope(handleResponseInterceptAfter(request))
```

不要注册 `pluginabi.MethodResponseInterceptStreamChunk`，也不要声明 `response_stream_interceptor`。

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w main.go main_test.go
go test . -run 'TestChannelTargetResponseIntercept|TestPluginRegistration|TestRewriteResponseModelFields' -v
```

预期：PASS；非流式六个允许字段按已有 helper 还原，流式与非定向返回空差量。

- [x] **步骤 5：提交**

```bash
git add main.go main_test.go
git commit -m "feat(T5): 还原定向非流式响应模型名"
```

---

## 管理面

### 任务 6：PATCH 与 preview 感知新控制维度

**文件**：
- 修改：`management.go`
- 修改：`management_test.go`

**接口**：
- 消费：任务 1 的 `ChannelTarget`、`findActiveChannelTarget`
- 产出：PATCH 可选字段 `ChannelTarget *ChannelTarget`、`FastAllowed *bool`
- 产出：`previewResponse.ChannelTarget *channelTargetPreview`、`MappingSkipped bool`

- [x] **步骤 1：写失败测试**

在 `management_test.go` imports 增加 `os`、`net/url`，并追加：

```go
func TestManagementChannelTargetAndFast(t *testing.T) {
	t.Run("校验拒绝重复 ID", func(t *testing.T) {
		statePath := setupManagementTest(t, Config{Enabled: true})
		resp := managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body: []byte(`{"key":"sk-k","channel_target":{"enabled":true,"auth_ids":["a","a"]}}`),
		})
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(resp.Body), "duplicate") {
			t.Fatalf("response = %d %s", resp.StatusCode, resp.Body)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("rejected save changed state file: %v", err)
		}
	})

	t.Run("PATCH 局部更新 fast_allowed", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true})
		managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body: []byte(`{"key":"sk-k","alias":"A","enabled":true,"rules":{"global":"a=>b"}}`),
		})
		resp := managementPatchKey(pluginapi.ManagementRequest{
			Method: http.MethodPatch,
			Query: url.Values{"key": {"sk-k"}},
			Body: []byte(`{"fast_allowed":false}`),
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH = %d %s", resp.StatusCode, resp.Body)
		}
		st, _ := loadedStateSnapshot()
		got := st.KeyBindings[0]
		if got.FastAllowed == nil || *got.FastAllowed || got.Alias != "A" || got.Rules.Global != "a=>b" || !got.Enabled {
			t.Fatalf("PATCH clobbered binding: %+v", got)
		}
	})

	t.Run("PATCH 更新渠道定向", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true})
		managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-k","enabled":true}`)})
		patch := func(body string) KeyBinding {
			resp := managementPatchKey(pluginapi.ManagementRequest{
				Method: http.MethodPatch,
				Query: url.Values{"key": {"sk-k"}},
				Body: []byte(body),
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH %s = %d %s", body, resp.StatusCode, resp.Body)
			}
			st, _ := loadedStateSnapshot()
			return st.KeyBindings[0]
		}
		got := patch(`{"channel_target":{"enabled":true,"suppliers":["gemini"],"auth_ids":["f1"]}}`)
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled {
			t.Fatalf("enabled target = %+v", got.ChannelTarget)
		}
		got = patch(`{"channel_target":null}`)
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled {
			t.Fatalf("null must preserve target: %+v", got.ChannelTarget)
		}
		got = patch(`{"channel_target":{"enabled":false,"suppliers":["gemini"],"auth_ids":["f1"]}}`)
		if got.ChannelTarget == nil || got.ChannelTarget.Enabled ||
			len(got.ChannelTarget.Suppliers) != 1 || got.ChannelTarget.Suppliers[0] != "gemini" ||
			len(got.ChannelTarget.AuthIDs) != 1 || got.ChannelTarget.AuthIDs[0] != "f1" {
			t.Fatalf("disabled target lost configuration: %+v", got.ChannelTarget)
		}
	})

	t.Run("preview 显示定向", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true, GlobalRules: "model-m=>model-n"})
		managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body: []byte(`{"key":"sk-k","enabled":true,"rules":{"global":"model-n=>model-p"},"channel_target":{"enabled":true,"suppliers":["gemini"],"auth_ids":["f1"]}}`),
		})
		resp := managementPreview(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body: []byte(`{"key":"sk-k","format":"openai","model":"model-m"}`),
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview = %d %s", resp.StatusCode, resp.Body)
		}
		var got previewResponse
		decodeBody(t, resp, &got)
		if !got.MappingSkipped || got.M1 != "model-m" || got.M2 != "model-m" || got.Final != "model-m" || got.Routed {
			t.Fatalf("preview routing = %+v", got)
		}
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled ||
			len(got.ChannelTarget.Resolved.Suppliers) != 1 || got.ChannelTarget.Resolved.Suppliers[0] != "gemini" ||
			len(got.ChannelTarget.Resolved.AuthIDs) != 1 || got.ChannelTarget.Resolved.AuthIDs[0] != "f1" {
			t.Fatalf("preview target = %+v", got.ChannelTarget)
		}
	})
}
```

- [x] **步骤 2：运行测试确认失败**

```bash
go test . -run TestManagementChannelTargetAndFast -v
```

预期：FAIL；PATCH 忽略新字段，preview 仍返回映射后的模型，或 `previewResponse.MappingSkipped` 尚不存在。

- [x] **步骤 3：写最小实现**

把 `managementPatchKey` 的 patch 结构与赋值扩展为：

```go
	var patch struct {
		Alias         *string        `json:"alias"`
		Enabled       *bool          `json:"enabled"`
		Blocked       *bool          `json:"blocked"`
		Rules         *RuleSet       `json:"rules"`
		ChannelTarget *ChannelTarget `json:"channel_target"`
		FastAllowed   *bool          `json:"fast_allowed"`
	}
```

在既有字段更新之后加入：

```go
			if patch.ChannelTarget != nil {
				st.KeyBindings[i].ChannelTarget = cloneChannelTarget(patch.ChannelTarget)
			}
			if patch.FastAllowed != nil {
				value := *patch.FastAllowed
				st.KeyBindings[i].FastAllowed = &value
			}
```

把 preview 类型改为：

```go
type resolvedChannelTarget struct {
	Suppliers []string `json:"suppliers"`
	AuthIDs   []string `json:"auth_ids"`
}

type channelTargetPreview struct {
	Enabled  bool                  `json:"enabled"`
	Resolved resolvedChannelTarget `json:"resolved"`
}

type previewResponse struct {
	M1             string                `json:"m1"`
	M2             string                `json:"m2"`
	Routed         bool                  `json:"routed"`
	Final          string                `json:"final"`
	ChannelTarget  *channelTargetPreview `json:"channel_target,omitempty"`
	MappingSkipped bool                  `json:"mapping_skipped,omitempty"`
}
```

在 `previewRoute` 的 plugin enabled 检查之后、应用顶层规则之前加入：

```go
	if binding, targeted := findActiveChannelTarget(src.KeyBindings, apiKey); targeted {
		target := binding.ChannelTarget
		return previewResponse{
			M1: model, M2: model, Routed: false, Final: model, MappingSkipped: true,
			ChannelTarget: &channelTargetPreview{
				Enabled: true,
				Resolved: resolvedChannelTarget{
					Suppliers: append([]string(nil), target.Suppliers...),
					AuthIDs: append([]string(nil), target.AuthIDs...),
				},
			},
		}, nil
	}
```

未定向时继续返回旧四字段，两个新字段因 `omitempty` 不出现。

- [x] **步骤 4：运行测试确认通过**

```bash
gofmt -w management.go management_test.go
go test . -run 'TestManagementChannelTargetAndFast|TestManagementPatchKey|TestManagementPreview' -v
```

预期：PASS；`channel_target:null` 保留旧配置，未定向 preview 的 JSON 结构不增加新字段。

- [x] **步骤 5：提交**

```bash
git add management.go management_test.go
git commit -m "feat(T6): 管理面读写并预览渠道定向"
```

---

## 前端数据与组件

### 任务 7：前端类型、PATCH 与 auth-files 客户端

**文件**：
- 修改：`web/src/api.ts`
- 修改：`web/src/api.test.ts`

**接口**：
- 消费：CPA `GET /v0/management/auth-files` 的 `{files:[...]}` 响应
- 产出：`ChannelTarget`、`CpaAuthFile`、扩展后的 `KeyBinding` / `PreviewResponse`
- 产出：`listCpaAuthFiles(): Promise<CpaAuthFile[]>`

- [x] **步骤 1：写失败测试**

把 `web/src/api.test.ts` import 改为 `import { api, listCpaAuthFiles, StateResponse } from './api'`，并追加：

```ts
describe('api：渠道定向与 Fast', () => {
  it('auth-files 解包 files 并携带管理密钥', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => ({
        files: [{ id: 'f1', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' }],
      }),
    }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(listCpaAuthFiles()).resolves.toEqual([
      { id: 'f1', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
    ])
    expect(fetchMock).toHaveBeenCalledWith('/v0/management/auth-files', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: expect.stringMatching(/^Bearer /) }),
    }))
  })

  it('auth-files 非 2xx 返回可显示错误', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 503, json: async () => ({}) })))
    await expect(listCpaAuthFiles()).rejects.toThrow('读取 CPA auth-files 失败：HTTP 503')
  })

  it('PATCH 可发送 false 与完整渠道定向对象', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(BASE_STATE),
    }))
    vi.stubGlobal('fetch', fetchMock)
    await api.patchKey('sk-k', {
      fast_allowed: false,
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1'] },
    })
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(JSON.parse(String(init.body))).toEqual({
      fast_allowed: false,
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1'] },
    })
  })
})
```

- [x] **步骤 2：运行测试确认失败**

```bash
npm --prefix web test -- src/api.test.ts
npm --prefix web run typecheck
```

预期：FAIL，TypeScript 报 `listCpaAuthFiles` 未导出、`fast_allowed` 与 `channel_target` 不属于 PATCH 类型。

- [x] **步骤 3：写最小实现**

在 `web/src/api.ts` 增加并扩展类型：

```ts
export interface ChannelTarget {
  enabled: boolean
  suppliers: string[]
  auth_ids: string[]
}

export interface CpaAuthFile {
  id: string
  provider: string
  status: string
  disabled: boolean
  label: string
}

export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  blocked: boolean
  rules: RuleSet
  channel_target?: ChannelTarget
  fast_allowed?: boolean
}

export interface PreviewResponse {
  m1: string
  m2: string
  routed: boolean
  final: string
  mapping_skipped?: boolean
  channel_target?: {
    enabled: boolean
    resolved: Pick<ChannelTarget, 'suppliers' | 'auth_ids'>
  }
}
```

把 `api.patchKey` 改为：

```ts
  patchKey: (key: string, patch: Partial<Pick<KeyBinding,
    'alias' | 'enabled' | 'blocked' | 'rules' | 'channel_target' | 'fast_allowed'>>) =>
    call<StateResponse>('PATCH', `/keys?key=${encodeURIComponent(key)}`, patch),
```

在 `listCpaApiKeys` 后新增：

```ts
export async function listCpaAuthFiles(): Promise<CpaAuthFile[]> {
  const resp = await fetch('/v0/management/auth-files', {
    headers: { Authorization: `Bearer ${getKey()}` },
  })
  if (resp.status === 401 || resp.status === 403) {
    clearKey()
    throw new Error('认证失败，请重新登录')
  }
  if (!resp.ok) throw new Error(`读取 CPA auth-files 失败：HTTP ${resp.status}`)
  const body = (await resp.json()) as { files?: CpaAuthFile[] }
  return body.files ?? []
}
```

- [x] **步骤 4：运行测试确认通过**

```bash
npm --prefix web test -- src/api.test.ts
npm --prefix web run typecheck
```

预期：PASS；现有 HTML entity 还原测试继续通过。

- [x] **步骤 5：提交**

```bash
git add web/src/api.ts web/src/api.test.ts
git commit -m "feat(T7): 前端接入定向与 auth-files 类型"
```

---

### 任务 8：实现双区块渠道定向编辑器

**文件**：
- 创建：`web/src/components/ChannelTargetEditor.tsx`
- 创建：`web/src/components/ChannelTargetEditor.test.tsx`

**接口**：
- 消费：任务 7 的 `ChannelTarget`、`CpaAuthFile`
- 产出：`ChannelTargetEditor({value, authFiles, loading, error, onChange, onRetry})`
- 产出：供应商 CheckboxGroup、按 provider 分组的认证文件 Collapse、组头全选/半选、总开关禁用态

- [x] **步骤 1：写失败测试**

创建 `web/src/components/ChannelTargetEditor.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ChannelTargetEditor from './ChannelTargetEditor'
import { ChannelTarget, CpaAuthFile } from '../api'

const FILES: CpaAuthFile[] = [
  { id: 'claude-main', provider: 'claude', status: 'active', disabled: false, label: 'Claude Main' },
  { id: 'gemini-main', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
  { id: 'gemini-backup', provider: 'gemini', status: 'disabled', disabled: true, label: 'Gemini Backup' },
]

const VALUE: ChannelTarget = {
  enabled: true,
  suppliers: ['claude'],
  auth_ids: ['gemini-main'],
}

describe('ChannelTargetEditor', () => {
  it('双区块混选回显', () => {
    render(<ChannelTargetEditor
      value={VALUE}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={vi.fn()}
      onRetry={vi.fn()}
    />)

    expect(screen.getByRole('checkbox', { name: '供应商 claude' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: '供应商 gemini' })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: '认证文件 gemini-main' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: '认证文件 claude-main' })).not.toBeChecked()
  })

  it('总开关关闭置灰', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { rerender } = render(<ChannelTargetEditor
      value={VALUE}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)

    await user.click(screen.getByRole('switch', { name: '渠道定向总开关' }))
    expect(onChange).toHaveBeenCalledWith({ ...VALUE, enabled: false })

    rerender(<ChannelTargetEditor
      value={{ ...VALUE, enabled: false }}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)
    expect(screen.getByRole('checkbox', { name: '供应商 claude' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: '认证文件 gemini-main' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: '供应商 claude' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: '认证文件 gemini-main' })).toBeChecked()
  })

  it('认证文件组头全选只更新该组', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ChannelTargetEditor
      value={{ enabled: true, suppliers: [], auth_ids: [] }}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)

    await user.click(screen.getByRole('checkbox', { name: '全选 gemini' }))
    expect(onChange).toHaveBeenCalledWith({
      enabled: true,
      suppliers: [],
      auth_ids: ['gemini-backup', 'gemini-main'],
    })
  })
})
```

- [x] **步骤 2：运行测试确认失败**

```bash
npm --prefix web test -- src/components/ChannelTargetEditor.test.tsx
```

预期：FAIL，模块 `./ChannelTargetEditor` 不存在。

- [x] **步骤 3：写最小实现**

创建 `web/src/components/ChannelTargetEditor.tsx`：

```tsx
import { Banner, Button, Checkbox, CheckboxGroup, Collapse, Spin, Switch, Tag, Typography } from '@douyinfe/semi-ui'
import { ChannelTarget, CpaAuthFile } from '../api'

interface Props {
  value: ChannelTarget
  authFiles: CpaAuthFile[]
  loading: boolean
  error: string
  onChange: (value: ChannelTarget) => void
  onRetry: () => void
}

function sortedUnique(values: string[]): string[] {
  return Array.from(new Set(values.filter((value) => value.trim() !== ''))).sort((a, b) => a.localeCompare(b))
}

function authStatus(file: CpaAuthFile) {
  if (file.disabled) return <Tag color="red">已禁用</Tag>
  if (file.status && file.status !== 'active') return <Tag color="orange">{file.status}</Tag>
  return <Tag color="green">可用</Tag>
}

export default function ChannelTargetEditor({ value, authFiles, loading, error, onChange, onRetry }: Props) {
  const providers = sortedUnique([...value.suppliers, ...authFiles.map((file) => file.provider)])
  const knownAuthIDs = new Set(authFiles.map((file) => file.id))
  const missingAuthIDs = value.auth_ids.filter((id) => !knownAuthIDs.has(id))
  const grouped = providers.map((provider) => ({
    provider,
    files: authFiles
      .filter((file) => file.provider.toLowerCase() === provider.toLowerCase())
      .sort((a, b) => a.id.localeCompare(b.id)),
  }))

  const setSuppliers = (suppliers: string[]) => onChange({ ...value, suppliers: sortedUnique(suppliers) })
  const setAuthIDs = (authIDs: string[]) => onChange({ ...value, auth_ids: sortedUnique(authIDs) })

  const toggleAuthGroup = (ids: string[], checked: boolean) => {
    const next = new Set(value.auth_ids)
    for (const id of ids) {
      if (checked) next.add(id)
      else next.delete(id)
    }
    setAuthIDs(Array.from(next))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <span>
        <Switch
          aria-label="渠道定向总开关"
          checked={value.enabled}
          onChange={(enabled) => onChange({ ...value, enabled })}
        />{' '}
        渠道定向
      </span>

      <div aria-disabled={!value.enabled} style={{ opacity: value.enabled ? 1 : 0.55 }}>
        <Typography.Title heading={6}>AI 供应商整选</Typography.Title>
        <Typography.Paragraph size="small" type="tertiary">
          勾选供应商后，该供应商未来新增的认证文件也会自动进入候选池。
        </Typography.Paragraph>
        <CheckboxGroup
          aria-label="AI 供应商"
          direction="horizontal"
          disabled={!value.enabled}
          value={value.suppliers}
          onChange={(items) => setSuppliers(items.map(String))}
        >
          {providers.map((provider) => (
            <Checkbox key={provider} value={provider} aria-label={`供应商 ${provider}`}>
              {provider}
            </Checkbox>
          ))}
        </CheckboxGroup>
      </div>

      <div aria-disabled={!value.enabled} style={{ opacity: value.enabled ? 1 : 0.55 }}>
        <Typography.Title heading={6}>认证文件单选</Typography.Title>
        <Typography.Paragraph size="small" type="tertiary">
          可跨供应商混选；最终候选池与上方整选供应商取并集。
        </Typography.Paragraph>

        {missingAuthIDs.length > 0 && (
          <div style={{ marginBottom: 8 }}>
            <Typography.Text type="tertiary">已保存但 CPA 当前未返回：</Typography.Text>{' '}
            {missingAuthIDs.map((id) => <Tag key={id} color="grey">{id}</Tag>)}
          </div>
        )}

        {loading && <Spin tip="正在读取 CPA auth-files" />}
        {!loading && error && (
          <Banner
            type="danger"
            title="认证文件加载失败"
            description={<><span>{error}</span>{' '}<Button onClick={onRetry}>重试</Button></>}
          />
        )}
        {!loading && !error && grouped.length === 0 && (
          <Typography.Text type="tertiary">CPA 当前没有可展示的认证文件。</Typography.Text>
        )}
        {!loading && !error && grouped.length > 0 && (
          <Collapse defaultActiveKey={providers} keepDOM>
            {grouped.map(({ provider, files }) => {
              const ids = files.map((file) => file.id)
              const selectedCount = ids.filter((id) => value.auth_ids.includes(id)).length
              const allChecked = ids.length > 0 && selectedCount === ids.length
              return (
                <Collapse.Panel
                  key={provider}
                  itemKey={provider}
                  header={`${provider}（${selectedCount}/${ids.length}）`}
                  extra={(
                    <span onClick={(event) => event.stopPropagation()}>
                      <Checkbox
                        aria-label={`全选 ${provider}`}
                        disabled={!value.enabled || ids.length === 0}
                        checked={allChecked}
                        indeterminate={selectedCount > 0 && !allChecked}
                        onChange={(event) => toggleAuthGroup(ids, event.target.checked)}
                      >全选</Checkbox>
                    </span>
                  )}
                >
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {files.map((file) => (
                      <Checkbox
                        key={file.id}
                        aria-label={`认证文件 ${file.id}`}
                        disabled={!value.enabled}
                        checked={value.auth_ids.includes(file.id)}
                        onChange={(event) => {
                          const next = new Set(value.auth_ids)
                          if (event.target.checked) next.add(file.id)
                          else next.delete(file.id)
                          setAuthIDs(Array.from(next))
                        }}
                      >
                        {file.label || file.id} <Typography.Text type="tertiary">({file.id})</Typography.Text>{' '}
                        {authStatus(file)}
                      </Checkbox>
                    ))}
                  </div>
                </Collapse.Panel>
              )
            })}
          </Collapse>
        )}
      </div>
    </div>
  )
}
```

- [x] **步骤 4：运行测试确认通过**

```bash
npm --prefix web test -- src/components/ChannelTargetEditor.test.tsx
npm --prefix web run typecheck
```

预期：PASS；总开关关闭后两个区块中的 checkbox 均 disabled，原选择仍 checked。

- [x] **步骤 5：提交**

```bash
git add web/src/components/ChannelTargetEditor.tsx web/src/components/ChannelTargetEditor.test.tsx
git commit -m "feat(T8): 新增双区块渠道定向编辑器"
```

---

### 任务 9：KeysPanel 三页表单、列表摘要与使用说明

**文件**：
- 修改：`web/src/panels/KeysPanel.tsx`
- 修改：`web/src/panels/KeysPanel.test.tsx`
- 创建：`web/src/panels/KeysPanel.channel-target.test.tsx`
- 修改：`README.md`
- 修改：`web/dist/index.html`（构建生成）

**接口**：
- 消费：任务 7 的 `listCpaAuthFiles` 与任务 8 的 `ChannelTargetEditor`
- 产出：旧绑定标准化为 `fast_allowed=true`、空的 disabled `channel_target`
- 产出：表格“渠道定向”摘要列与“Fast”状态列；弹窗“基础 / 渠道定向 / 规则集”三页

- [x] **步骤 1：写失败测试**

在 `web/src/panels/KeysPanel.test.tsx` 的 `binding` fixture 增加：

```ts
  channel_target: { enabled: true, suppliers: ['claude'], auth_ids: ['gemini-main'] },
  fast_allowed: false,
```

并在“保留除 key 外的其余字段”用例末尾追加：

```ts
    expect(plan?.binding.channel_target).toEqual({
      enabled: true,
      suppliers: ['claude'],
      auth_ids: ['gemini-main'],
    })
    expect(plan?.binding.fast_allowed).toBe(false)
```

创建 `web/src/panels/KeysPanel.channel-target.test.tsx`：

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, listCpaApiKeys, listCpaAuthFiles, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    api: { ...actual.api, postKey: vi.fn(), patchKey: vi.fn(), deleteKey: vi.fn() },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
    listCpaAuthFiles: vi.fn(),
  }
})

const EMPTY_RULES = { global: '', claude: '', codex: '', openai: '' }
const BINDING: KeyBinding = {
  key: 'sk-k',
  alias: 'Target Key',
  enabled: true,
  blocked: false,
  rules: { ...EMPTY_RULES },
  channel_target: { enabled: true, suppliers: ['claude'], auth_ids: ['gemini-main'] },
  fast_allowed: false,
}
const STATE: StateResponse = {
  version: 1,
  rules: { ...EMPTY_RULES },
  key_bindings: [BINDING],
  persisted: true,
}

afterEach(() => {
  vi.clearAllMocks()
  vi.mocked(listCpaApiKeys).mockResolvedValue([])
})

describe('KeysPanel：渠道定向与 Fast', () => {
  it('弹窗采用三页并回显 Fast', async () => {
    vi.mocked(listCpaAuthFiles).mockResolvedValue([
      { id: 'gemini-main', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
    ])
    const user = userEvent.setup()
    render(<KeysPanel state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByRole('tab', { name: '基础' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '渠道定向' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '规则集' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: '编辑绑定：Fast 允许' })).not.toBeChecked()
  })

  it('auth-files 加载失败', async () => {
    vi.mocked(listCpaAuthFiles).mockRejectedValue(new Error('读取 CPA auth-files 失败：HTTP 503'))
    const user = userEvent.setup()
    render(<KeysPanel state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('tab', { name: '渠道定向' }))
    expect(await screen.findByText('认证文件加载失败')).toBeInTheDocument()
    expect(screen.getByText('读取 CPA auth-files 失败：HTTP 503')).toBeInTheDocument()
    expect(screen.getByText('gemini-main')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(listCpaAuthFiles).toHaveBeenCalledTimes(2))
  })

  it('旧绑定默认 Fast 允许且表格显示定向摘要', async () => {
    vi.mocked(listCpaAuthFiles).mockResolvedValue([])
    const user = userEvent.setup()
    const oldBinding: KeyBinding = {
      key: 'sk-old', alias: 'Old', enabled: true, blocked: false, rules: { ...EMPTY_RULES },
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1', 'f2'] },
    }
    render(<KeysPanel state={{ ...STATE, key_bindings: [oldBinding] }} onSaved={vi.fn()} />)

    expect(screen.getByText('1 个供应商 · 2 个认证文件')).toBeInTheDocument()
    expect(screen.getByText('允许')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByRole('switch', { name: '编辑绑定：Fast 允许' })).toBeChecked()
  })
})
```

- [x] **步骤 2：运行测试确认失败**

```bash
npm --prefix web test -- src/panels/KeysPanel.test.tsx src/panels/KeysPanel.channel-target.test.tsx
```

预期：FAIL；弹窗没有 Tabs/Fast 开关，`listCpaAuthFiles` 未被调用，表格没有新摘要。

- [x] **步骤 3：写最小实现**

把 `KeysPanel.tsx` imports 扩展为：

```tsx
import { Button, Card, Table, Modal, Input, Select, Switch, Tag, Tabs, TabPane, Toast, Typography } from '@douyinfe/semi-ui'
import { api, ChannelTarget, CpaAuthFile, KeyBinding, RuleSet, StateResponse, listCpaApiKeys, listCpaAuthFiles } from '../api'
import ChannelTargetEditor from '../components/ChannelTargetEditor'
import RuleSetEditor from '../components/RuleSetEditor'
```

在 `EMPTY_RULES` 后新增：

```tsx
const EMPTY_CHANNEL_TARGET: ChannelTarget = { enabled: false, suppliers: [], auth_ids: [] }

function normalizeBinding(binding: KeyBinding): KeyBinding {
  return {
    ...binding,
    blocked: !!binding.blocked,
    fast_allowed: binding.fast_allowed ?? true,
    channel_target: {
      ...EMPTY_CHANNEL_TARGET,
      ...binding.channel_target,
      suppliers: [...(binding.channel_target?.suppliers ?? [])],
      auth_ids: [...(binding.channel_target?.auth_ids ?? [])],
    },
    rules: { ...binding.rules },
  }
}

function channelTargetSummary(binding: KeyBinding): string {
  const target = binding.channel_target
  if (!target?.enabled) return '关闭'
  return `${target.suppliers.length} 个供应商 · ${target.auth_ids.length} 个认证文件`
}
```

在组件 state 中增加并加载 auth-files：

```tsx
  const [authFiles, setAuthFiles] = useState<CpaAuthFile[]>([])
  const [authLoading, setAuthLoading] = useState(false)
  const [authError, setAuthError] = useState('')

  const loadAuthFiles = () => {
    setAuthLoading(true)
    setAuthError('')
    listCpaAuthFiles()
      .then(setAuthFiles)
      .catch((error: Error) => {
        setAuthFiles([])
        setAuthError(error.message)
      })
      .finally(() => setAuthLoading(false))
  }

  useEffect(() => {
    if (editing !== null) loadAuthFiles()
  }, [editing !== null])
```

把新建与编辑初始化改为：

```tsx
  const openCreate = () => {
    setOriginalKey('')
    setEditing(normalizeBinding({
      key: '', alias: '', enabled: true, blocked: false,
      fast_allowed: true,
      channel_target: { ...EMPTY_CHANNEL_TARGET },
      rules: { ...EMPTY_RULES },
    }))
  }

  const openEdit = (binding: KeyBinding) => {
    setOriginalKey(binding.key)
    setEditing(normalizeBinding(binding))
  }
```

在表格 columns 的“追加规则”之后加入：

```tsx
          {
            title: '渠道定向',
            dataIndex: 'channel_target',
            render: (_: unknown, binding: KeyBinding) => channelTargetSummary(binding),
          },
          {
            title: 'Fast',
            dataIndex: 'fast_allowed',
            render: (_: unknown, binding: KeyBinding) => binding.fast_allowed === false
              ? <Tag color="grey">关闭</Tag>
              : <Tag color="green">允许</Tag>,
          },
```

把 Modal 内原表单整体替换为：

```tsx
        {editing && (
          <Tabs type="line" keepDOM>
            <TabPane tab="基础" itemKey="basic">
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <Select
                  style={{ width: '100%' }}
                  filter
                  allowCreate
                  placeholder="选择或输入 API key"
                  value={editing.key || undefined}
                  onChange={(value) => setEditing({ ...editing, key: String(value) })}
                  optionList={cpaKeys.map((key) => ({ value: key, label: maskKey(key) }))}
                />
                <Input
                  placeholder="别名（可选）"
                  value={editing.alias}
                  onChange={(alias) => setEditing({ ...editing, alias })}
                />
                <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
                  <span>
                    <Switch
                      aria-label="编辑绑定：启用规则"
                      checked={editing.enabled}
                      onChange={(enabled) => setEditing({ ...editing, enabled })}
                    />{' '}启用规则
                  </span>
                  <span>
                    <Switch
                      aria-label="编辑绑定：禁止访问"
                      checked={!!editing.blocked}
                      onChange={(blocked) => setEditing({ ...editing, blocked })}
                    />{' '}禁止访问
                  </span>
                  <span>
                    <Switch
                      aria-label="编辑绑定：Fast 允许"
                      checked={editing.fast_allowed ?? true}
                      onChange={(fast_allowed) => setEditing({ ...editing, fast_allowed })}
                    />{' '}Fast 允许
                  </span>
                  {isKeyCollision && <Tag color="orange">同 key 已存在，保存将覆盖</Tag>}
                </div>
              </div>
            </TabPane>
            <TabPane tab="渠道定向" itemKey="channel-target">
              <ChannelTargetEditor
                value={editing.channel_target ?? { ...EMPTY_CHANNEL_TARGET }}
                authFiles={authFiles}
                loading={authLoading}
                error={authError}
                onRetry={loadAuthFiles}
                onChange={(channel_target) => setEditing({ ...editing, channel_target })}
              />
            </TabPane>
            <TabPane tab="规则集" itemKey="rules">
              <Typography.Paragraph size="small" type="tertiary">
                追加规则集在顶层规则之后执行；渠道定向开启时本页规则跳过。
              </Typography.Paragraph>
              <RuleSetEditor value={editing.rules} onChange={(rules) => setEditing({ ...editing, rules })} />
            </TabPane>
          </Tabs>
        )}
```

把 Card title 改为 `Key 绑定策略`。

在 `README.md` 的 Admin UI/绑定说明处加入：

```markdown
### 渠道定向与 Fast 控制

每条 key 绑定可独立开启渠道定向：供应商整选与认证文件单选取并集，池内按认证文件 ID 确定性轮转。定向开启后该 key 跳过模型映射；候选池为空会返回 503 `auth_not_found`，不会降级到池外凭据。池内凭据全部冷却时由 CPA 返回原生 429 `model_cooldown` 与 `Retry-After`。

“Fast 允许”默认开启。关闭后，Claude 请求的 `speed:"fast"` 与 `fast-mode-2026-02-01` beta token 会在上游执行前被删除；其他协议不受影响。

CPA 的 Scheduler 能力为宿主全局单实例。若同时启用另一个声明 Scheduler 的插件（例如 `cpa-plugin-key-policy`），只有宿主选择的首个 Scheduler 生效；本插件不提供冲突探测，请在部署配置中只保留一个 Scheduler 插件。
```

- [x] **步骤 4：运行测试确认通过并构建嵌入页面**

```bash
npm --prefix web test -- src/panels/KeysPanel.test.tsx src/panels/KeysPanel.blocked.test.tsx src/panels/KeysPanel.delete.test.tsx src/panels/KeysPanel.channel-target.test.tsx src/components/ChannelTargetEditor.test.tsx
npm --prefix web run typecheck
VITE_HOSTED=1 npm --prefix web run build
git diff --exit-code -- web/package-lock.json
```

预期：测试与 typecheck PASS；`web/dist/index.html` 被重新生成；lockfile 不变。

- [x] **步骤 5：提交**

```bash
git add web/src/panels/KeysPanel.tsx web/src/panels/KeysPanel.test.tsx web/src/panels/KeysPanel.channel-target.test.tsx README.md web/dist/index.html
git commit -m "feat(T9): 重构 Key 表单并展示定向与 Fast"
```

---

## 验收与收尾

### 任务 10：验收（acceptance-qa）

> 本任务由 executing-plans 收尾审查阶段触发 acceptance-qa 按下表执行，
> 不参与逐任务连续执行；报告与证据落盘特性目录 `acceptance/` 子目录。
>
> **2026-08-23 执行结果**：已执行，总结论 FAIL（阻塞合并）。详见 `acceptance/acceptance-summary.md`。

| Scenario / 检查项 | 维度 | 执行方式 | 目标 | 阈值/预期 | 验收证据 |
|-------------------|------|---------|------|----------|---------|
| 定向请求实际落在目标认证文件 | e2e | 验收任务 (D) | 已加载本分支插件的 CPA：`POST /v1/messages`；绑定 `CPA_SMOKE_CLIENT_KEY` 到从 `GET /v0/management/auth-files` 选定的唯一 auth ID | `make smoke-local` 基线通过；目标请求 2xx 或目标凭据自身上游错误，CPA request-log 中 auth ID 等于绑定 ID，日志中不出现池外 auth ID | `acceptance/channel-target-live.md` + 脱敏 request-log 摘要 |
| 池内凭据全冷却时收到 429 且不落池外 | e2e | 验收任务 (D) | 同一 CPA，将定向池唯一凭据置于可恢复冷却状态后请求相同模型 | HTTP 429；JSON `error.code=model_cooldown`；`Retry-After` 非空；request-log 无池外 auth ID | `acceptance/channel-target-cooldown.md` |
| fast 关闭后上游收到普通请求 | e2e | 验收任务 (D) | `POST /v1/messages` 同时发送 `speed:"fast"` 与 `anthropic-beta: fast-mode-2026-02-01,prompt-caching-2024`，key 的 `fast_allowed=false` | 上游/request-log 捕获 body 无 `speed`；beta 仅剩 `prompt-caching-2024`；请求结果不因 Fast 标记被拒 | `acceptance/fast-strip-live.md` + 脱敏上游请求捕获 |
| 定向 + fast 组合叠加 | e2e | 验收任务 (D) | 同一 key 同时 `channel_target.enabled=true`、`fast_allowed=false` 发 Claude 请求 | request-log auth ID 在定向池内，且上游 body/header 均无 Fast 标记；两机制均生效 | `acceptance/channel-target-fast-combined.md` |
| 编辑表单全流程人工审查 | visual | 验收任务 (D) | `http://127.0.0.1:8317/v0/resource/plugins/model-mapper-plus/index.html` 的 Key 绑定新增/编辑弹窗 | 三个 Tabs 可切换；混选回显正确；总开关关闭后禁用但保留选择；组头全选/半选正确；失败提示可重试；表格摘要与 Fast 列正确；浅色/深色均无溢出 | `acceptance/ui-light.png`、`acceptance/ui-dark.png`、`acceptance/ui-review.md` |

验收开始前把目标 key 的原始 binding JSON 写入 `acceptance/pre-state.json`；结束后原样 POST 恢复，原先不存在则 DELETE，并在每份 live 报告记录恢复结果。冷却状态只使用可恢复的测试凭据，验收结束确认恢复 active；无法安全制造冷却时该行标记 DEFERRED，不得用插件自造 429 冒充通过。

---

### 任务 11：合并与清理

**资源台账**（清理依据；写计划时预登记已知资源，执行中创建即追加；行格式 `- [ ] <类型>: <标识> —— <清理命令>`）：

- [ ] worktree: `.worktrees/plan/2026-08-22-channel-target-and-fast-control` —— `git worktree remove .worktrees/plan/2026-08-22-channel-target-and-fast-control && git branch -d plan/2026-08-22-channel-target-and-fast-control`
- [ ] 审查临时目录: `/tmp/cpa-channel-review.966s5j` —— `rm -rf /tmp/cpa-channel-review.966s5j`
- [x] 验收临时目录: `/tmp/cpa-channel-target-acceptance.20260823` —— 已删除（含凭据副本与原始日志）
- [x] 验收容器: `cpa-channel-target-acceptance` —— 已执行 `docker rm -f cpa-channel-target-acceptance`
- [x] 视觉验收 Chrome: `--user-data-dir=/tmp/cpa-channel-target-acceptance.20260823/chrome-profile` —— 进程已终止，复查无残留

台账总则：**清理只遍历本台账、台账外一律不动**（可疑残留只报告不删）；共享缓存（`~/.cargo`、pnpm store、npm cache 等）默认保留，仅用户显式要求清理时才登记入账；台账限定持久资源（容器、测试库/表、临时目录、后台服务），worktree 内构建产物随 worktree 删除自然回收、不入账。

- [ ] **步骤 1：全量验证（安全网）与归属裁决**

在 worktree 内运行：

```bash
go test ./...
go test -race .
make test-scripts
npm --prefix web run typecheck
npm --prefix web test
VITE_HOSTED=1 npm --prefix web run build
git diff --exit-code -- web/package-lock.json
git diff --check
```

- 全绿 → 进入步骤 2。
- 失败测试在相关测试范围内 → 修复并复跑全绿后进入步骤 2。
- 失败测试在范围之外 → 归属裁决：在主工作区的源分支检出上复跑该测试（主工作区有未提交改动 → 先询问用户）。源分支同样失败 → 报告“既有失败”，请用户裁决是否阻塞合并；源分支通过 → 判定为本次引入的回归，修复并复跑全绿。

执行记录：除 `make test-scripts` 的 `FAIL: windows output not versioned` 外全部通过；该失败已在主工作区源分支复现，属既有失败。另有 5 项已确认审查发现，故步骤 1 保持未勾选，等待用户裁决。

- [x] **步骤 2：测试退役检查**

扫描 `state_test.go`、`main_test.go`、`scheduler_test.go`、`fast_strip_test.go`、`management_test.go`、`web/src/api.test.ts`、`web/src/components/ChannelTargetEditor.test.tsx`、`web/src/panels/KeysPanel*.test.tsx`：仅当测试名对不上任何 active spec 的现行 Scenario，且对应 Requirement 已 REMOVED、带 `Superseded` 标注或所属 spec 已 superseded 时才列为候选。候选非空先征询用户；用户未确认不删除。无候选则记录“无孤儿测试”。

执行记录：无孤儿测试。

- [x] **步骤 3：取代回写**

本 spec 的 `supersedes: []`，声明“无取代回写”后跳过。

执行记录：无取代回写。

- [ ] **步骤 4：合并回来源分支**

```bash
cd "$(dirname "$(git rev-parse --git-common-dir)")"
git merge plan/2026-08-22-channel-target-and-fast-control
```

合并冲突、或主工作区有未提交改动 → 停下向计划作者确认，不强行合并。

- [ ] **步骤 5：清理（按资源台账逐条执行）**

逐条执行资源台账各行的清理命令并勾选。命令执行失败 → 该行保留未勾选并报告用户；资源已不存在 → 勾选并注明“已不存在”。台账外的文件、容器、数据一律不动。

- [ ] **步骤 6：sync_commit 锚定**

```bash
SYNC=$(git rev-parse HEAD)
# 把 spec frontmatter 的 sync_commit: null 更新为 $SYNC
git add .spec-dev/2026-08-22-channel-target-and-fast-control/spec/channel-target-and-fast-control-design.md
git commit -m "chore(spec): sync_commit 锚定 ${SYNC:0:7}"
```

此后 `git diff <sync_commit>..HEAD -- <covers glob>` 即“spec 上次确认同步以来的代码变化”。

任务 0 未由本计划建立 worktree 时，只执行步骤 1、2、3 与步骤 6；步骤 4–5 交回原有隔离机制收尾并注明。

---

## Self-Review（计划作者已完成）

### Spec 覆盖

| Requirement / Scenario | 对应任务与失败测试 |
|------------------------|-------------------|
| 渠道定向候选池过滤：单选认证文件命中 | 任务 3 `单选认证文件命中` |
| 渠道定向候选池过滤：供应商整选动态入池 | 任务 3 `供应商整选动态入池` |
| 渠道定向候选池过滤：池内多凭据轮转分摊 | 任务 3 `池内多凭据轮转分摊` |
| 渠道定向候选池过滤：无法识别上下文时 fail-open | 任务 3 `无法识别上下文时 fail-open` |
| 定向池空：池内候选全部不可用 | 任务 3 `池内候选全部不可用` + 任务 10 live |
| 定向池空：全部冷却走宿主原生应答 | 任务 3 `全部冷却走宿主原生应答` + 任务 10 live |
| 定向开启跳过规则映射：定向时模型名不被改写 | 任务 2 `定向时模型名不被改写` |
| 定向响应：非流式还原 | 任务 5 `非流式还原` |
| 定向响应：流式透传 | 任务 5 `流式透传` |
| Fast 关闭：仅 body 带 speed | 任务 4 `仅 body 带 speed` |
| Fast 关闭：仅 beta 头标记 | 任务 4 `仅 beta 头标记` |
| Fast 关闭：默认放行 | 任务 4 `默认放行` |
| KeyBinding：存量文件零迁移加载 | 任务 1 `存量文件零迁移加载` |
| KeyBinding：校验拒绝重复 ID | 任务 6 `校验拒绝重复 ID` |
| 管理面：PATCH 局部更新 fast_allowed | 任务 6 `PATCH 局部更新 fast_allowed` |
| 管理面：PATCH 更新渠道定向 | 任务 6 `PATCH 更新渠道定向` |
| 管理面：preview 显示定向 | 任务 6 `preview 显示定向` |
| Admin UI：双区块混选回显 | 任务 8 `双区块混选回显` |
| Admin UI：总开关关闭置灰 | 任务 8 `总开关关闭置灰` |
| Admin UI：auth-files 加载失败 | 任务 9 `auth-files 加载失败` |
| 验收矩阵五条“验收任务” | 任务 10 逐行承载，目标、阈值与证据路径已补齐 |

Spec 没有 ADDED/MODIFIED/REMOVED 差量三节；无需另建删除或迁移任务。风险节中的 Scheduler 单实例限制由任务 9 README 与任务 10 live 验收覆盖；Headers 通路由任务 3/5 单元契约和任务 10 live 共同覆盖。

### 占位符扫描

计划中每个实施任务均给出精确路径、失败测试代码、红灯命令、最小实现代码、绿灯命令与提交命令；未发现空实现指令。

### 类型一致性

- Go 全链路统一使用 `ChannelTarget.Suppliers` / `AuthIDs`、JSON `suppliers` / `auth_ids`；管理 preview 的 `resolvedChannelTarget` 只做响应形状转换。
- 前端统一使用 `ChannelTarget.enabled/suppliers/auth_ids` 与 `CpaAuthFile.id/provider/status/disabled/label`；`KeysPanel` 与 `ChannelTargetEditor` props 完全一致。
- Scheduler 全链路统一使用 `pluginapi.SchedulerPickRequest` / `SchedulerAuthCandidate` / `SchedulerPickResponse`；池空只走 `pluginMethodError`，不与正常 `Handled=false` 混用。
- Response hook 统一使用 `pluginabi.MethodResponseInterceptAfter` 与 `pluginapi.ResponseInterceptRequest/Response`；没有引入 stream-chunk capability。
- 任务 0 分支名、任务 11 merge/清理分支名与 worktree 路径一致。
