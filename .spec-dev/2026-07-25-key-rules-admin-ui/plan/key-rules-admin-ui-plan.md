# Key 维度规则与 Web 管理界面 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。步骤用复选框（`- [ ]`）语法跟踪；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：为 model-mapper-plus 插件增加 key 绑定（指定客户端 key 追加规则集、串联跑在顶层规则之后）与 web 管理界面（规则/Key 绑定/试跑三板块，state_file 为真相源）。

**Spec**：`.spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md`

**架构**：`state.go` 承载 State/RuleSet/KeyBinding 与原子写；`main.go` 的 `routeModel` 重构为两层接力（顶层规则集 → key 规则集），key 从入站请求头提取；`management.go` + `web_embed.go` 实现 management.register/handle 与嵌入页；`web/` 为 React 19 + Vite 8 + Semi Design 单文件前端。

**技术栈**：Go 1.23（c-shared 插件，package main）、CLIProxyAPI SDK v7.2.48（pluginapi/pluginabi）、React 19、TypeScript 7、Vite 8、@douyinfe/semi-ui 2.x、vite-plugin-singlefile。

## 全局约束

- 仓库为根目录单 `package main`；新增 Go 文件不建子包、不全面重构目录
- `selectRules(cfg, format)` 签名不变（既有测试直接调用）；`routeModel` 签名变更为 `(cfg Config, src ruleSource, format, model, apiKey string)`，main_test.go 既有调用点随任务 2 机械迁移
- 响应模型恢复白名单与 `streamChunkRewriter` 零改动
- 思考强度仅经规则内模型名后缀表达，不新增任何强度专用代码路径（ADR-0002）
- state_file 权限 0600、原子写（tmp+fsync+rename）；`enabled` 只来自 YAML，不进 state
- 前端界面与布局全部使用 Semi 组件；发布物仍只有 .so（go:embed 单文件）
- 提交信息格式：`feat(TN): ...` / `test(TN): ...` / `docs(TN): ...`（N 为全局任务号）

---

### 任务 0：建立隔离工作区

- [ ] **步骤 1：检测已有隔离**

运行：`git rev-parse --git-dir` 与 `git rev-parse --git-common-dir`
两者不同、且 `git rev-parse --show-superproject-working-tree` 无输出（排除 submodule）
→ 已在隔离工作区，跳过本任务。

- [ ] **步骤 2：建立 worktree**

有原生 worktree 工具（如 EnterWorktree）或 using-git-worktrees skill 时优先使用；否则手工降级：
确认 `.worktrees/` 已被忽略（`git check-ignore -q .worktrees`，未忽略先加入 `.gitignore` 并提交），然后
`git worktree add .worktrees/plan-2026-07-25-key-rules-admin-ui -b plan/2026-07-25-key-rules-admin-ui` 并切换到该目录。

- [ ] **步骤 3：安装依赖并验证基线**

`go mod download && go test ./...`
基线测试失败 → 停下报告，先问再继续。

---

## 后端：状态层与引擎

### 任务 1：state.go — State 结构与原子持久化

**文件**：
- 创建：`state.go`
- 测试：`state_test.go`

**接口**：
- 消费：`Config`（main.go:274-280，含任务 2 才加入的 `StateFile` 字段——本任务先定义常量与独立函数，不引用该字段）
- 产出：`RuleSet`、`KeyBinding`、`State`、`stateVersion`、`defaultStateFile`、`ruleSetFromConfig(cfg Config) RuleSet`、`ruleSource`、`ruleSourceFromState(st State) ruleSource`、`ruleSourceFromConfig(cfg Config) ruleSource`、`seedStateFromConfig(cfg Config) State`、`readStateFile(path string) (State, error)`、`validateState(st State) error`、`atomicWriteState(path string, st State) error`——后续任务 2/3/4/5 全部依赖

- [ ] **步骤 1：写失败测试**

```go
// state_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedStateFromConfig(t *testing.T) {
	cfg := Config{GlobalRules: "g1", ClaudeMessagesRules: "c1"}
	st := seedStateFromConfig(cfg)
	if st.Version != stateVersion {
		t.Fatalf("version = %d, want %d", st.Version, stateVersion)
	}
	if st.Rules.Global != "g1" || st.Rules.Claude != "c1" || st.Rules.Codex != "" || st.Rules.OpenAI != "" {
		t.Fatalf("unexpected seed rules: %+v", st.Rules)
	}
	if len(st.KeyBindings) != 0 {
		t.Fatalf("seed key bindings = %v, want empty", st.KeyBindings)
	}
}

// Scenario: seed 仅一次（读取侧：state_file 存在时 YAML 不再生效）
func TestReadStateFileTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := State{Version: stateVersion, Rules: RuleSet{Global: "from-state"}, KeyBindings: []KeyBinding{{Key: "sk-a", Enabled: true}}}
	if err := atomicWriteState(path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readStateFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Rules.Global != "from-state" || len(got.KeyBindings) != 1 {
		t.Fatalf("unexpected state: %+v", got)
	}
}

// Scenario: 非法 JSON 回退（readStateFile 报错，调用方回退 seed）
func TestReadStateFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStateFile(path); err == nil {
		t.Fatal("want error for corrupt state file")
	}
}

func TestReadStateFileUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"rules":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStateFile(path); err == nil {
		t.Fatal("want error for unknown version")
	}
}

// Scenario: 任意时刻文件完整（原子写产物合法且权限 0600）
func TestAtomicWriteStatePermAndValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := State{Version: stateVersion, Rules: RuleSet{Global: "g"}}
	if err := atomicWriteState(path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	var decoded State
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("written file not valid json: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %d entries", len(entries))
	}
}

func TestValidateState(t *testing.T) {
	cases := []struct {
		name    string
		st      State
		wantErr string
	}{
		{"ok", State{Version: stateVersion, Rules: RuleSet{Global: "a=>b"}}, ""},
		{"bad top-level rules", State{Version: stateVersion, Rules: RuleSet{Global: "a => b"}}, "rules.global"},
		{"bad binding rules", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "sk-a", Rules: RuleSet{Claude: "x => y"}}}}, "claude"},
		{"empty binding key", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "  "}}}, "key"},
		{"duplicate binding key", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "sk-a"}, {Key: "sk-a"}}}, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateState(tc.st)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestSeedStateFromConfig|TestReadStateFile|TestAtomicWriteState|TestValidateState' -v`
预期：编译失败，`undefined: State` 等

- [ ] **步骤 3：写最小实现**

```go
// state.go
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	stateVersion    = 1
	defaultStateFile = "model-mapper-plus-state.json"
)

// RuleSet mirrors the four top-level rule fields; the same shape is reused
// for per-key bindings (ADR-0003 symmetric segment selection).
type RuleSet struct {
	Global string `json:"global"`
	Claude string `json:"claude"`
	Codex  string `json:"codex"`
	OpenAI string `json:"openai"`
}

type KeyBinding struct {
	Key     string  `json:"key"`
	Alias   string  `json:"alias"`
	Enabled bool    `json:"enabled"`
	Rules   RuleSet `json:"rules"`
}

type State struct {
	Version     int          `json:"version"`
	Rules       RuleSet      `json:"rules"`
	KeyBindings []KeyBinding `json:"key_bindings"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
}

// ruleSource is the runtime view consumed by routeModel/preview.
type ruleSource struct {
	Rules       RuleSet
	KeyBindings []KeyBinding
}

func ruleSetFromConfig(cfg Config) RuleSet {
	return RuleSet{Global: cfg.GlobalRules, Claude: cfg.ClaudeMessagesRules, Codex: cfg.CodexResponsesRules, OpenAI: cfg.OpenAICompletionsRules}
}

func seedStateFromConfig(cfg Config) State {
	return State{Version: stateVersion, Rules: ruleSetFromConfig(cfg)}
}

func ruleSourceFromState(st State) ruleSource {
	return ruleSource{Rules: st.Rules, KeyBindings: st.KeyBindings}
}

func ruleSourceFromConfig(cfg Config) ruleSource {
	return ruleSourceFromState(seedStateFromConfig(cfg))
}

func readStateFile(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse state file: %w", err)
	}
	if st.Version != stateVersion {
		return State{}, fmt.Errorf("unsupported state version %d", st.Version)
	}
	return st, nil
}

func validateState(st State) error {
	segments := []struct{ name, rules string }{
		{"rules.global", st.Rules.Global}, {"rules.claude", st.Rules.Claude},
		{"rules.codex", st.Rules.Codex}, {"rules.openai", st.Rules.OpenAI},
	}
	seen := map[string]bool{}
	for i, b := range st.KeyBindings {
		key := strings.TrimSpace(b.Key)
		if key == "" {
			return fmt.Errorf("key_bindings[%d]: key is required", i)
		}
		if seen[key] {
			return fmt.Errorf("key_bindings[%d]: duplicate key", i)
		}
		seen[key] = true
		prefix := fmt.Sprintf("key_bindings[%d].rules", i)
		segments = append(segments,
			struct{ name, rules string }{prefix + ".global", b.Rules.Global},
			struct{ name, rules string }{prefix + ".claude", b.Rules.Claude},
			struct{ name, rules string }{prefix + ".codex", b.Rules.Codex},
			struct{ name, rules string }{prefix + ".openai", b.Rules.OpenAI},
		)
	}
	for _, seg := range segments {
		if seg.rules == "" {
			continue
		}
		if _, err := parseRules(seg.rules); err != nil {
			return fmt.Errorf("%s: %w", seg.name, err)
		}
	}
	return nil
}

func atomicWriteState(path string, st State) error {
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".model-mapper-plus-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// findKeyBinding returns the enabled binding for apiKey using constant-time
// comparison; empty apiKey never matches.
func findKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if b.Enabled && subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}
```

- [ ] **步骤 4：运行测试确认通过**

运行：`go test . -run 'TestSeedStateFromConfig|TestReadStateFile|TestAtomicWriteState|TestValidateState' -v`
预期：全部 PASS（`findKeyBinding` 的行为由任务 2 的接力测试经 `routeModel` 覆盖）

- [ ] **步骤 5：提交**

```bash
git add state.go state_test.go
git commit -m "feat(T1): add state model with atomic persistence"
```

---

### 任务 2：引擎两层接力（key 提取 + routeModel 重构）

**文件**：
- 修改：`main.go`（Config 加 `StateFile`；`decodeLifecycleConfig` switch 加 case；新增 `apiKeyFromHeaders`、`selectRulesFrom`、`applyRuleSet`；重构 `routeModel`；3 个调用点；`handlePluginReconfigure` 接 state；全局 state holder）
- 修改：`main_test.go`（既有 `routeModel(cfg, format, model)` 调用点迁移为新签名）
- 测试：`keybinding_test.go`（新）

**接口**：
- 消费：`ruleSource`、`findKeyBinding`、`defaultStateFile`（任务 1）
- 产出：`apiKeyFromHeaders(h http.Header) string`、`selectRulesFrom(rs RuleSet, format string) (string, bool)`、`applyRuleSet(rs RuleSet, format, model string) (string, bool, error)`、`routeModel(cfg Config, src ruleSource, format, model, apiKey string) (routeDecision, error)`、`loadedRuleSource() ruleSource`、`loadedStateSnapshot() (State, bool)`、`applyStateUpdate(mutate func(*State) error) error`——任务 3/4/5 依赖

- [ ] **步骤 1：写失败测试**

```go
// keybinding_test.go
package main

import (
	"net/http"
	"testing"
)

// Scenario: Bearer 优先于 x-api-key
func TestAPIKeyFromHeadersBearerPreferred(t *testing.T) {
	h := http.Header{"Authorization": {"Bearer sk-a"}, "X-Api-Key": {"sk-b"}}
	if got := apiKeyFromHeaders(h); got != "sk-a" {
		t.Fatalf("got %q, want sk-a", got)
	}
}

func TestAPIKeyFromHeadersFallbacks(t *testing.T) {
	if got := apiKeyFromHeaders(http.Header{"X-Api-Key": {"sk-b"}}); got != "sk-b" {
		t.Fatalf("x-api-key got %q", got)
	}
	// 非 Bearer 的 Authorization 不当作 key，回退 x-api-key
	h := http.Header{"Authorization": {"Basic abc"}, "X-Api-Key": {"sk-c"}}
	if got := apiKeyFromHeaders(h); got != "sk-c" {
		t.Fatalf("non-bearer got %q", got)
	}
}

// Scenario: 无 key 头跳过 key 层
func TestAPIKeyFromHeadersEmpty(t *testing.T) {
	if got := apiKeyFromHeaders(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func testConfig() Config {
	return Config{Enabled: true}
}

// Scenario: 接力降档
func TestRouteModelKeyChainDowngrade(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "claude-opus-4-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "claude-opus-4-5(medium)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 未绑定 key 不受 key 层影响
func TestRouteModelUnboundKeyUntouched(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "claude-opus-4-5(max)", "sk-other")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "claude-opus-4-5(high)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: key 端点段留空回退其全局段
func TestRouteModelKeyGlobalFallback(t *testing.T) {
	src := ruleSource{
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `*(max)=>$1(high)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "gpt-5(high)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 绑定停用时透传顶层结果
func TestRouteModelBindingDisabled(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `a=>b`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: false,
			Rules: RuleSet{Global: `b=>c`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "a", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "b" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 净效果为零不路由（key 层改回原值）
func TestRouteModelNetZeroNotHandled(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{OpenAI: `gpt-4o=>deepseek-V3`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `deepseek-V3=>gpt-4o`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-4o", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if d.Handled {
		t.Fatalf("want unhandled, got %+v", d)
	}
}

// Scenario: 无 key 头跳过 key 层（路由维度）
func TestRouteModelNoKeySkipsKeyLayer(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{OpenAI: `gpt-4o=>deepseek-V3`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `deepseek-V3=>qwen-max`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "deepseek-V3" {
		t.Fatalf("got %+v", d)
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestAPIKeyFromHeaders|TestRouteModelKey|TestRouteModelUnbound|TestRouteModelNetZero|TestRouteModelNoKey|TestRouteModelBindingDisabled' -v`
预期：编译失败，`undefined: apiKeyFromHeaders` 等

- [ ] **步骤 3：写最小实现**

main.go 的改动（按位置）：

① `Config` 增加字段（main.go:274-280 处）：

```go
type Config struct {
	Enabled                bool   `json:"enabled"`
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
	StateFile              string `json:"state_file"`
}
```

② `defaultConfig`（main.go:889-891 处）改为：

```go
func defaultConfig() Config {
	return Config{Enabled: true, StateFile: defaultStateFile}
}
```

③ `decodeLifecycleConfig` 的 switch（main.go:747-758 处）增加：

```go
			case "state_file":
				cfg.StateFile = value
```

④ `pluginRegistration().Metadata.ConfigFields` 追加（main.go:304-310 处）：

```go
				{Name: "state_file", Type: pluginapi.ConfigFieldTypeString, Description: "Path to the JSON state file that stores rules and key bindings once managed via the admin UI."},
```

⑤ 全局 state holder（放在 `loadedConfig` 全局块旁，main.go:341-361 处）：

```go
var (
	loadedStateMu sync.RWMutex
	loadedHolder  = stateHolder{src: ruleSourceFromConfig(defaultConfig())}
)

type stateHolder struct {
	src       ruleSource
	persisted bool
	state     State
}

func loadedRuleSource() ruleSource {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	return loadedHolder.src
}

func loadedStateSnapshot() (State, bool) {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	return loadedHolder.state, loadedHolder.persisted
}

// resolveState loads state_file when present and valid; otherwise falls back
// to a YAML-seed state without creating the file (ADR-0001).
func resolveState(cfg Config) stateHolder {
	path := strings.TrimSpace(cfg.StateFile)
	if path == "" {
		path = defaultStateFile
	}
	if st, err := readStateFile(path); err == nil {
		return stateHolder{src: ruleSourceFromState(st), persisted: true, state: st}
	}
	seed := seedStateFromConfig(cfg)
	return stateHolder{src: ruleSourceFromState(seed), state: seed}
}

func stateFilePath() string {
	path := strings.TrimSpace(loadedConfig().StateFile)
	if path == "" {
		return defaultStateFile
	}
	return path
}

// applyStateUpdate clones the current state, applies mutate, validates,
// persists atomically, then swaps the runtime view. Any failure leaves the
// previous state untouched.
func applyStateUpdate(mutate func(*State) error) error {
	loadedStateMu.Lock()
	defer loadedStateMu.Unlock()
	st := loadedHolder.state
	if err := mutate(&st); err != nil {
		return err
	}
	if err := validateState(st); err != nil {
		return err
	}
	if err := atomicWriteState(stateFilePath(), st); err != nil {
		return err
	}
	loadedHolder = stateHolder{src: ruleSourceFromState(st), persisted: true, state: st}
	return nil
}
```

⑥ `setLoadedConfigForTest` 增强（main.go:357-361 处）——必须同步 seed 规则源，否则既有 56 个测试注入 cfg 后 `loadedRuleSource()` 仍读到空规则：

```go
func setLoadedConfigForTest(cfg Config) {
	loadedConfigMu.Lock()
	loadedCfg = cfg
	loadedConfigMu.Unlock()
	loadedStateMu.Lock()
	loadedHolder = stateHolder{src: ruleSourceFromConfig(cfg), state: seedStateFromConfig(cfg)}
	loadedStateMu.Unlock()
}
```

⑦ `handlePluginReconfigure`（main.go:367-378 处）在 `setLoadedConfigForTest(cfg)` 后覆盖为真实解析（state_file 存在则以 state 为准）：

```go
	setLoadedConfigForTest(cfg)
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
```

⑧ key 提取与规则集执行（放在 `selectRules` 之后，main.go:411 处）：

```go
// apiKeyFromHeaders extracts the client API key from inbound headers:
// "Authorization: Bearer <key>" wins, then "x-api-key" (ADR key extraction).
func apiKeyFromHeaders(h http.Header) string {
	if auth := strings.TrimSpace(h.Get("Authorization")); auth != "" {
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			if token = strings.TrimSpace(token); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(h.Get("x-api-key"))
}

func selectRulesFrom(rs RuleSet, format string) (string, bool) {
	switch format {
	case "claude":
		if rs.Claude != "" {
			return rs.Claude, true
		}
	case "openai-response":
		if rs.Codex != "" {
			return rs.Codex, true
		}
	case "openai":
		if rs.OpenAI != "" {
			return rs.OpenAI, true
		}
	}
	if rs.Global != "" {
		return rs.Global, true
	}
	return "", false
}

// applyRuleSet selects the segment for format and applies it once.
// It returns (output, matched, error); unmatched leaves model unchanged.
func applyRuleSet(rs RuleSet, format, model string) (string, bool, error) {
	raw, ok := selectRulesFrom(rs, format)
	if !ok {
		return model, false, nil
	}
	rules, err := parseRules(raw)
	if err != nil {
		return "", false, err
	}
	mapped, matched, err := applyRules(model, rules)
	if err != nil {
		return "", false, err
	}
	if !matched {
		return model, false, nil
	}
	return mapped, true, nil
}
```

⑨ `selectRules` 改为包装（main.go:392-411 处，签名与行为不变）：

```go
func selectRules(cfg Config, format string) (string, bool) {
	return selectRulesFrom(ruleSetFromConfig(cfg), format)
}
```

⑩ `routeModel` 重构（main.go:428-448 处）：

```go
func routeModel(cfg Config, src ruleSource, format, model, apiKey string) (routeDecision, error) {
	if !cfg.Enabled {
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

⑪ 三个调用点：

- `handleModelRoute`（main.go:418）：`decision, err := routeModel(loadedConfig(), loadedRuleSource(), req.SourceFormat, req.RequestedModel, apiKeyFromHeaders(req.Headers))`
- `runStreamForward`（main.go:498）：`decision, err := routeModel(loadedConfig(), loadedRuleSource(), req.SourceFormat, req.Model, apiKeyFromHeaders(req.Headers))`
- `handleExecutorExecute`（main.go:630）：`decision, err := routeModel(loadedConfig(), loadedRuleSource(), req.SourceFormat, req.Model, apiKeyFromHeaders(req.Headers))`

- [ ] **步骤 4：迁移既有测试签名并运行全部测试**

`main_test.go` 中所有 `routeModel(cfg, format, model)` / `routeModel(cfg, format, model)` 形态调用改为 `routeModel(cfg, ruleSourceFromConfig(cfg), format, model, "")`（全局搜索 `routeModel(` 逐一核对，含 handleModelRoute 间接调用无需改）。

运行：`go test ./...`
预期：全部 PASS（含新测试）

- [ ] **步骤 5：提交**

```bash
git add main.go main_test.go keybinding_test.go
git commit -m "feat(T2): chain key-bound rule sets after top-level rules"
```

---

## 管理 API 层

### 任务 3：management 骨架、资源服务与注册接入

**文件**：
- 创建：`management.go`、`web_embed.go`、`web/dist/index.html`（占位页）
- 修改：`main.go`（`registrationCapabilities` 加 `ManagementAPI`、`handleMethod` 加两 case）
- 测试：`management_test.go`

**接口**：
- 消费：pluginapi `ManagementRegistrationResponse`/`ManagementRoute`/`ResourceRoute`/`ManagementRequest`/`ManagementResponse`；pluginabi `MethodManagementRegister`/`MethodManagementHandle`
- 产出：`managementBase`、`resourcePrefix`、`handleManagementRegister() ([]byte, error)`、`handleManagement(raw []byte) ([]byte, error)`、`dispatchManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse`、`serveIndexHTML()`、`managementJSON(status int, v any)`、`managementError(status int, msg string)`——任务 4/5 的路由 handler 挂进 `dispatchManagement`

- [ ] **步骤 1：写失败测试**

```go
// management_test.go
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Scenario: 注册包含 management 声明
func TestManagementRegisterRoutes(t *testing.T) {
	raw, err := handleManagementRegister()
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, r := range resp.Routes {
		routes[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /plugins/model-mapper-plus/state",
		"PUT /plugins/model-mapper-plus/rules",
		"POST /plugins/model-mapper-plus/keys",
		"PATCH /plugins/model-mapper-plus/keys",
		"DELETE /plugins/model-mapper-plus/keys",
		"POST /plugins/model-mapper-plus/preview",
	} {
		if !routes[want] {
			t.Fatalf("missing route %s in %v", want, routes)
		}
	}
	if len(resp.Resources) != 1 || resp.Resources[0].Path != "/index.html" {
		t.Fatalf("unexpected resources: %+v", resp.Resources)
	}
}

// Scenario: 管理页可访问
func TestDispatchManagementServesIndex(t *testing.T) {
	resp := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/model-mapper-plus/index.html",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
		t.Fatalf("content-type = %q", resp.Headers.Get("Content-Type"))
	}
	lower := strings.ToLower(string(resp.Body))
	if !strings.Contains(lower, "<html") || !strings.Contains(lower, "model mapper") {
		t.Fatal("body missing html or plugin marker")
	}
}

// Scenario: 注册能力含 management_api 且 ConfigFields 含 state_file
func TestPluginRegistrationDeclaresManagement(t *testing.T) {
	reg := pluginRegistration()
	if !reg.Capabilities.ManagementAPI {
		t.Fatal("management_api capability missing")
	}
	found := false
	for _, f := range reg.Metadata.ConfigFields {
		if f.Name == "state_file" {
			found = true
		}
	}
	if !found {
		t.Fatal("state_file config field missing")
	}
}

func TestDispatchManagementUnknown(t *testing.T) {
	resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/plugins/model-mapper-plus/nope"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Scenario: 注册能力含 management_api（经 handleMethod 全流程）
func TestHandleMethodManagementRegister(t *testing.T) {
	raw, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("unexpected envelope: %s", raw)
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestManagementRegisterRoutes|TestDispatchManagement|TestHandleMethodManagement|TestPluginRegistration' -v`
预期：编译失败，`undefined: handleManagementRegister` 等

- [ ] **步骤 3：写最小实现**

```go
// management.go
package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	managementBase = "/plugins/model-mapper-plus"
	resourcePrefix = "/v0/resource/plugins/model-mapper-plus"
)

func handleManagementRegister() ([]byte, error) {
	return json.Marshal(pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: managementBase + "/state", Description: "Read full model-mapper-plus state."},
			{Method: http.MethodPut, Path: managementBase + "/rules", Description: "Replace top-level rule sets."},
			{Method: http.MethodPost, Path: managementBase + "/keys", Description: "Create or replace a key binding."},
			{Method: http.MethodPatch, Path: managementBase + "/keys", Description: "Update a key binding by key."},
			{Method: http.MethodDelete, Path: managementBase + "/keys", Description: "Delete a key binding by key."},
			{Method: http.MethodPost, Path: managementBase + "/preview", Description: "Dry-run rule resolution."},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/index.html", Menu: "Model Mapper Plus", Description: "Model Mapper Plus admin UI."},
		},
	})
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return json.Marshal(dispatchManagement(req))
}

func dispatchManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	path := strings.TrimRight(req.Path, "/")
	// Browser resource GETs arrive through the same method without management auth.
	if req.Method == http.MethodGet && strings.HasPrefix(path, resourcePrefix) {
		return serveIndexHTML()
	}
	switch {
	case req.Method == http.MethodGet && path == managementBase+"/state":
		return managementGetState()
	case req.Method == http.MethodPut && path == managementBase+"/rules":
		return managementPutRules(req)
	case req.Method == http.MethodPost && path == managementBase+"/keys":
		return managementPostKey(req)
	case req.Method == http.MethodPatch && path == managementBase+"/keys":
		return managementPatchKey(req)
	case req.Method == http.MethodDelete && path == managementBase+"/keys":
		return managementDeleteKey(req)
	case req.Method == http.MethodPost && path == managementBase+"/preview":
		return managementPreview(req)
	}
	return managementError(http.StatusNotFound, "unknown management route")
}

func managementJSON(status int, v any) pluginapi.ManagementResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		return managementError(http.StatusInternalServerError, err.Error())
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	}
}

func managementError(status int, msg string) pluginapi.ManagementResponse {
	return managementJSON(status, map[string]string{"error": msg})
}
```

```go
// web_embed.go
package main

import (
	_ "embed"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

//go:embed web/dist/index.html
var indexHTML []byte

func serveIndexHTML() pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       indexHTML,
	}
}
```

```html
<!-- web/dist/index.html — 占位页，任务 6 由 vite 构建产物替换 -->
<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>Model Mapper Plus</title></head>
<body>Model Mapper Plus admin UI placeholder — run <code>make web-build</code> to embed the real UI.</body>
</html>
```

main.go 改动：

① `registrationCapabilities`（main.go:288-294 处）加字段：

```go
	ManagementAPI           bool     `json:"management_api"`
```

② `pluginRegistration().Capabilities` 里加 `ManagementAPI: true,`

③ `handleMethod`（main.go:700-719 处）`default` 前加：

```go
	case pluginabi.MethodManagementRegister:
		return wrapEnvelope(handleManagementRegister())
	case pluginabi.MethodManagementHandle:
		return wrapEnvelope(handleManagement(request))
```

- [ ] **步骤 4：运行测试确认通过**

运行：`go test . -run 'TestManagementRegisterRoutes|TestDispatchManagement|TestHandleMethodManagement|TestPluginRegistration' -v && go vet ./...`
预期：PASS（`managementGetState` 等 6 个 handler 此刻 undefined——见下）

**注意**：步骤 3 的 `dispatchManagement` 引用了任务 4/5 的 4 个 handler。为了让本任务可独立编译提交，先在 `management.go` 尾部放 4 个临时桩，任务 4/5 用真实现替换（桩在替换前若被调用返回 501）：

```go
// 临时桩：任务 4/5 替换为真实现
func managementGetState() pluginapi.ManagementResponse     { return managementError(http.StatusNotImplemented, "state api pending") }
func managementPutRules(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "rules api pending")
}
func managementPostKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementPatchKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementDeleteKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementPreview(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "preview api pending")
}
```

- [ ] **步骤 5：提交**

```bash
git add management.go web_embed.go web/dist/index.html main.go management_test.go
git commit -m "feat(T3): register management API and serve embedded admin page"
```

---

### 任务 4：数据 API（/state、/rules、keys CRUD）

**文件**：
- 修改：`management.go`（桩替换为真实现 + 辅助函数）
- 测试：`management_api_test.go`（新）

**接口**：
- 消费：`loadedStateSnapshot`、`applyStateUpdate`、`loadedRuleSource`（任务 2）；`managementJSON`（任务 3）
- 产出：`managementGetState()`、`managementPutRules(req)`、`managementPostKey(req)`、`managementPatchKey(req)`、`managementDeleteKey(req)`、`keyFromRequest(req pluginapi.ManagementRequest) string`；state 响应含 `persisted bool` 字段（前端显示"已接管/仍 YAML"）

- [ ] **步骤 1：写失败测试**

每个测试前用 `setLoadedConfigForTest(...)` 与临时 state 路径初始化；新增测试辅助（放 `management_api_test.go` 顶部）：

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func setupManagementTest(t *testing.T, cfg Config) string {
	t.Helper()
	statePath := t.TempDir() + "/state.json"
	cfg.StateFile = statePath
	setLoadedConfigForTest(cfg)
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
	return statePath
}

func decodeBody(t *testing.T, resp pluginapi.ManagementResponse, v any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body, v); err != nil {
		t.Fatalf("decode body: %v (%s)", err, resp.Body)
	}
}

func TestManagementGetStateSeedsFromYAML(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true, GlobalRules: "g1", StateFile: ""})
	resp := managementGetState()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Rules     RuleSet `json:"rules"`
		Persisted bool    `json:"persisted"`
	}
	decodeBody(t, resp, &body)
	if body.Persisted {
		t.Fatal("want persisted=false before first save")
	}
	if body.Rules.Global != "g1" {
		t.Fatalf("rules = %+v", body.Rules)
	}
}

// Scenario: seed 仅一次
func TestManagementFirstSaveCreatesStateFile(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "g1"})
	resp := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, resp.Body)
	}
	st, err := readStateFile(statePath)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if st.Rules.Global != "g1" {
		t.Fatalf("seed rules lost: %+v", st.Rules)
	}
	if len(st.KeyBindings) != 1 || st.KeyBindings[0].Key != "sk-a" {
		t.Fatalf("bindings = %+v", st.KeyBindings)
	}
	// 此后改 YAML 不再生效
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "g2"})
	loadedStateMu.Lock()
	loadedHolder = resolveState(loadedConfig())
	loadedStateMu.Unlock()
	if got := loadedRuleSource().Rules.Global; got != "g1" {
		t.Fatalf("yaml override leaked: %q", got)
	}
}

// Scenario: 非法规则拒绝且状态不变
func TestManagementPutRulesRejectsInvalid(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "g1"})
	resp := managementPutRules(pluginapi.ManagementRequest{
		Method: http.MethodPut,
		Body:   []byte(`{"global":"gpt-* => deepseek"}`),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if _, err := readStateFile(statePath); err == nil {
		t.Fatal("state file must not be created on rejected save")
	}
	var body map[string]string
	decodeBody(t, resp, &body)
	if body["error"] == "" {
		t.Fatal("missing error description")
	}
}

func TestManagementPostKeyUpsert(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true,"rules":{"global":"x=>y"}}`)})
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","alias":"B","enabled":true,"rules":{"global":"p=>q"}}`)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 1 || st.KeyBindings[0].Alias != "B" || st.KeyBindings[0].Rules.Global != "p=>q" {
		t.Fatalf("upsert failed: %+v", st.KeyBindings)
	}
}

func TestManagementPatchKey(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`)})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"enabled":false}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	st, _ := loadedStateSnapshot()
	b := st.KeyBindings[0]
	if b.Enabled || b.Alias != "A" || b.Rules.Global != "x=>y" {
		t.Fatalf("patch clobbered fields: %+v", b)
	}
}

func TestManagementDeleteKey(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true}`)})
	resp := managementDeleteKey(pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Query:  url.Values{"key": {"sk-a"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 0 {
		t.Fatalf("delete failed: %+v", st.KeyBindings)
	}
}

func TestManagementPatchKeyMissing(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-absent"}},
		Body:   []byte(`{"enabled":false}`),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Scenario: 非法 JSON 回退（resolveState 损坏时以 seed 运行，保存可修复）
func TestManagementRecoversFromCorruptState(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "g1"}
	setLoadedConfigForTest(cfg)
	statePath := t.TempDir() + "/state.json"
	cfg.StateFile = statePath
	setLoadedConfigForTest(cfg)
	if err := os.WriteFile(statePath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
	if got := loadedRuleSource().Rules.Global; got != "g1" {
		t.Fatalf("seed fallback failed: %q", got)
	}
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true}`)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repair save failed: %d", resp.StatusCode)
	}
	if _, err := readStateFile(statePath); err != nil {
		t.Fatalf("state not repaired: %v", err)
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestManagement' -v`
预期：编译失败或桩返回 501 导致的断言失败

- [ ] **步骤 3：写最小实现**

删除任务 3 的 6 个桩函数，在 `management.go` 尾部加入：

```go
type stateResponse struct {
	Version     int          `json:"version"`
	Rules       RuleSet      `json:"rules"`
	KeyBindings []KeyBinding `json:"key_bindings"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
	Persisted   bool         `json:"persisted"`
}

func managementGetState() pluginapi.ManagementResponse {
	st, persisted := loadedStateSnapshot()
	if st.KeyBindings == nil {
		st.KeyBindings = []KeyBinding{}
	}
	return managementJSON(http.StatusOK, stateResponse{
		Version: stateVersion, Rules: st.Rules,
		KeyBindings: st.KeyBindings, UpdatedAt: st.UpdatedAt, Persisted: persisted,
	})
}

func managementPutRules(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body RuleSet
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "invalid rules payload: "+err.Error())
	}
	err := applyStateUpdate(func(st *State) error {
		st.Rules = body
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementGetState()
}

func managementPostKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var binding KeyBinding
	if err := json.Unmarshal(req.Body, &binding); err != nil {
		return managementError(http.StatusBadRequest, "invalid key binding payload: "+err.Error())
	}
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key == binding.Key {
				st.KeyBindings[i] = binding // upsert
				return nil
			}
		}
		st.KeyBindings = append(st.KeyBindings, binding)
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementGetState()
}

// keyFromRequest locates the target binding key from query (?key=) or body {"key":...}.
func keyFromRequest(req pluginapi.ManagementRequest) string {
	if key := strings.TrimSpace(req.Query.Get("key")); key != "" {
		return key
	}
	var body struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(req.Body, &body)
	return strings.TrimSpace(body.Key)
}

func managementPatchKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	target := keyFromRequest(req)
	if target == "" {
		return managementError(http.StatusBadRequest, "key is required")
	}
	var patch struct {
		Alias   *string  `json:"alias"`
		Enabled *bool    `json:"enabled"`
		Rules   *RuleSet `json:"rules"`
	}
	if err := json.Unmarshal(req.Body, &patch); err != nil {
		return managementError(http.StatusBadRequest, "invalid patch payload: "+err.Error())
	}
	found := false
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key != target {
				continue
			}
			found = true
			if patch.Alias != nil {
				st.KeyBindings[i].Alias = *patch.Alias
			}
			if patch.Enabled != nil {
				st.KeyBindings[i].Enabled = *patch.Enabled
			}
			if patch.Rules != nil {
				st.KeyBindings[i].Rules = *patch.Rules
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	if !found {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	return managementGetState()
}

func managementDeleteKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	target := keyFromRequest(req)
	if target == "" {
		return managementError(http.StatusBadRequest, "key is required")
	}
	found := false
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key == target {
				st.KeyBindings = append(st.KeyBindings[:i], st.KeyBindings[i+1:]...)
				found = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	if !found {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	return managementGetState()
}
```

注意：`applyStateUpdate` 的 `validateState` 会把"不存在的 target"场景视为成功（未改任何东西），所以 404 判定放在 `found` 标志上；但此时 state 已被原子写了一次（无变化）——可接受的冗余写，或更精细地在 mutate 返回哨兵错误。保持简单：接受冗余写。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./... && go vet ./...`
预期：全部 PASS

- [ ] **步骤 5：提交**

```bash
git add management.go management_api_test.go
git commit -m "feat(T4): implement state, rules and key binding APIs"
```

---

### 任务 5：POST /preview 规则试跑

**文件**：
- 修改：`management.go`（preview 桩替换为真实现）
- 测试：`preview_test.go`（新）

**接口**：
- 消费：`applyRuleSet`、`findKeyBinding`、`loadedRuleSource`（任务 2）；`managementJSON`（任务 3）
- 产出：`previewRoute(src ruleSource, format, model, apiKey string) (previewResponse, error)`、`managementPreview(req)`

设计说明：preview 是纯规则演算，**忽略** `cfg.Enabled`（试跑目的是调试规则，插件开关不影响演算）。

- [ ] **步骤 1：写失败测试**

```go
// preview_test.go
package main

import (
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Scenario: 分步结果可见
func TestPreviewRouteChained(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	got, err := previewRoute(src, "claude", "claude-opus-4-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if got.M1 != "claude-opus-4-5(high)" || got.M2 != "claude-opus-4-5(medium)" {
		t.Fatalf("got %+v", got)
	}
	if !got.Routed || got.Final != "claude-opus-4-5(medium)" {
		t.Fatalf("routed/final wrong: %+v", got)
	}
}

func TestPreviewRouteNoMatch(t *testing.T) {
	got, err := previewRoute(ruleSource{Rules: RuleSet{Global: "a=>b"}}, "openai", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Routed || got.M1 != "gpt-4o" || got.M2 != "gpt-4o" || got.Final != "gpt-4o" {
		t.Fatalf("got %+v", got)
	}
}

func TestPreviewRouteInvalidRules(t *testing.T) {
	src := ruleSource{Rules: RuleSet{Global: "a => b"}}
	if _, err := previewRoute(src, "openai", "x", ""); err == nil {
		t.Fatal("want parse error surfaced")
	}
}

func TestManagementPreviewEndpoint(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true, ClaudeMessagesRules: `claude-opus-4-5(max)=>claude-opus-4-5(high)`})
	resp := managementPreview(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"format":"claude","model":"claude-opus-4-5(max)"}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, resp.Body)
	}
	var body previewResponse
	decodeBody(t, resp, &body)
	if body.M1 != "claude-opus-4-5(high)" || !body.Routed {
		t.Fatalf("got %+v", body)
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestPreview|TestManagementPreview' -v`
预期：编译失败，`undefined: previewRoute`（或桩 501）

- [ ] **步骤 3：写最小实现**

删除 `managementPreview` 桩，在 `management.go` 尾部加入：

```go
type previewRequest struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Model  string `json:"model"`
}

type previewResponse struct {
	M1     string `json:"m1"`
	M2     string `json:"m2"`
	Routed bool   `json:"routed"`
	Final  string `json:"final"`
}

// previewRoute evaluates the two-layer chain without the enabled gate:
// dry-run is a rule debugging tool (see plan task 5 note).
func previewRoute(src ruleSource, format, model, apiKey string) (previewResponse, error) {
	m1 := model
	mapped, matched, err := applyRuleSet(src.Rules, format, model)
	if err != nil {
		return previewResponse{}, err
	}
	if matched {
		m1 = mapped
	}
	m2 := m1
	if binding, ok := findKeyBinding(src.KeyBindings, apiKey); ok {
		mapped, matched, err = applyRuleSet(binding.Rules, format, m1)
		if err != nil {
			return previewResponse{}, err
		}
		if matched {
			m2 = mapped
		}
	}
	return previewResponse{M1: m1, M2: m2, Routed: m2 != model, Final: m2}, nil
}

func managementPreview(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body previewRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "invalid preview payload: "+err.Error())
	}
	if strings.TrimSpace(body.Model) == "" || strings.TrimSpace(body.Format) == "" {
		return managementError(http.StatusBadRequest, "format and model are required")
	}
	resp, err := previewRoute(loadedRuleSource(), body.Format, body.Model, strings.TrimSpace(body.Key))
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementJSON(http.StatusOK, resp)
}
```

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./... && go vet ./...`
预期：全部 PASS

- [ ] **步骤 5：提交**

```bash
git add management.go preview_test.go
git commit -m "feat(T5): add dry-run preview API"
```

---

## 前端

### 任务 6：前端脚手架、构建管线与 API/会话层

**文件**：
- 创建：`web/package.json`、`web/vite.config.ts`、`web/tsconfig.json`、`web/index.html`、`web/src/main.tsx`、`web/src/App.tsx`、`web/src/api.ts`、`web/src/session.ts`、`web/src/panelAuth.ts`
- 修改：`Makefile`（web-build 目标）、`.gitignore`（web/node_modules）

**接口**：
- 产出（供任务 7/8/9）：`api.getState()/putRules(rules)/postKey(b)/patchKey(key,patch)/deleteKey(key)/preview(req)/listCpaApiKeys()`、`RuleSet/KeyBinding/StateResponse/PreviewResponse` 类型、`session.getKey/setKey/clearKey/hasKey`、`panelAuth.readPanelAuth()`、`App` 骨架（Layout.Header + Tabs 三板块占位 + 登录门）

- [ ] **步骤 1：创建脚手架文件**

`web/package.json`：

```json
{
  "name": "model-mapper-plus-web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": {
    "@douyinfe/semi-icons": "^2.62.0",
    "@douyinfe/semi-ui": "^2.62.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  },
  "devDependencies": {
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.3.4",
    "typescript": "^7.0.0",
    "vite": "^8.0.0",
    "vite-plugin-singlefile": "^2.2.0"
  }
}
```

`web/vite.config.ts`：

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { viteSingleFile } from 'vite-plugin-singlefile'

// VITE_HOSTED=1: base 指向 CPA 资源挂载路径（与 key-policy 同一模式）。
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  base: process.env.VITE_HOSTED === '1' ? '/v0/resource/plugins/model-mapper-plus/' : '/',
  build: {
    assetsInlineLimit: 100000000,
    cssCodeSplit: false,
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
```

`web/tsconfig.json`：

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "skipLibCheck": true,
    "noEmit": true,
    "types": ["vite/client"]
  },
  "include": ["src"]
}
```

`web/index.html`：

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Model Mapper Plus</title></head>
<body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body>
</html>
```

`web/src/main.tsx`：

```tsx
import { createRoot } from 'react-dom/client'
import App from './App'

createRoot(document.getElementById('root')!).render(<App />)
```

`web/src/session.ts`：

```ts
// Management key lives in memory only; a page refresh means logging in again
// (same rule as key-policy).
let managementKey = ''

export function getKey(): string {
  return managementKey
}
export function setKey(key: string): void {
  managementKey = key
}
export function clearKey(): void {
  managementKey = ''
}
export function hasKey(): boolean {
  return managementKey !== ''
}
```

`web/src/panelAuth.ts`：从 `/Users/flame/cpa-plugin-key-policy/web/src/store/panelAuth.ts` **逐行复制**（CPA 生态通用方案，含 `ENC_PREFIX`/`SECRET_SALT`/zustand envelope 解析；spec 已确认平移）。

`web/src/api.ts`：

```ts
import { getKey, clearKey } from './session'

export interface RuleSet {
  global: string
  claude: string
  codex: string
  openai: string
}

export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  rules: RuleSet
}

export interface StateResponse {
  version: number
  rules: RuleSet
  key_bindings: KeyBinding[]
  updated_at?: string
  persisted: boolean
}

export interface PreviewRequest {
  key?: string
  format: string
  model: string
}

export interface PreviewResponse {
  m1: string
  m2: string
  routed: boolean
  final: string
}

const PLUGIN_BASE = '/v0/management/plugins/model-mapper-plus'

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(PLUGIN_BASE + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${getKey()}`,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (resp.status === 401 || resp.status === 403) {
    clearKey()
    throw new Error('认证失败，请重新登录')
  }
  const text = await resp.text()
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`
    try {
      const parsed = JSON.parse(text)
      if (parsed && typeof parsed.error === 'string') msg = parsed.error
    } catch { /* keep default */ }
    throw new Error(msg)
  }
  return JSON.parse(text) as T
}

export const api = {
  getState: () => call<StateResponse>('GET', '/state'),
  putRules: (rules: RuleSet) => call<StateResponse>('PUT', '/rules', rules),
  postKey: (binding: KeyBinding) => call<StateResponse>('POST', '/keys', binding),
  patchKey: (key: string, patch: Partial<Pick<KeyBinding, 'alias' | 'enabled' | 'rules'>>) =>
    call<StateResponse>('PATCH', `/keys?key=${encodeURIComponent(key)}`, patch),
  deleteKey: (key: string) => call<StateResponse>('DELETE', `/keys?key=${encodeURIComponent(key)}`),
  preview: (req: PreviewRequest) => call<PreviewResponse>('POST', '/preview', req),
}

// CPA 主程序的 api-keys 列表（GET /v0/management/api-keys 返回原文）。
export async function listCpaApiKeys(): Promise<string[]> {
  const resp = await fetch('/v0/management/api-keys', {
    headers: { Authorization: `Bearer ${getKey()}` },
  })
  if (!resp.ok) throw new Error(`读取 CPA api-keys 失败：HTTP ${resp.status}`)
  const body = (await resp.json()) as { 'api-keys'?: string[] }
  return body['api-keys'] ?? []
}
```

`web/src/App.tsx`（三板块占位组件在任务 7/8/9 逐一替换 `Placeholder`）：

```tsx
import { useEffect, useState } from 'react'
import { Layout, Nav, Button, Tag, Typography, Card, Input, Toast } from '@douyinfe/semi-ui'
import { api, StateResponse } from './api'
import { hasKey, setKey } from './session'
import { readPanelAuth } from './panelAuth'

const { Header, Content } = Layout

function Placeholder({ name }: { name: string }) {
  return <Card style={{ margin: 16 }}>{name}（任务 7/8/9 实现）</Card>
}

export default function App() {
  const [authed, setAuthed] = useState(hasKey())
  const [state, setState] = useState<StateResponse | null>(null)
  const [inputKey, setInputKey] = useState('')

  useEffect(() => {
    if (!authed) {
      const panel = readPanelAuth()
      if (panel) {
        setKey(panel.managementKey)
        setAuthed(true)
      }
    }
  }, [authed])

  useEffect(() => {
    if (authed) {
      api.getState().then(setState).catch((e: Error) => Toast.error(e.message))
    }
  }, [authed])

  if (!authed) {
    return (
      <Layout style={{ minHeight: '100vh' }}>
        <Content style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
          <Card title="Model Mapper Plus 登录" style={{ width: 380 }}>
            <Input.Password
              placeholder="CPA management key"
              value={inputKey}
              onChange={setInputKey}
              onEnterPress={() => { setKey(inputKey); setAuthed(true) }}
            />
            <Button theme="solid" style={{ marginTop: 12 }} block
              onClick={() => { setKey(inputKey); setAuthed(true) }}>登录</Button>
          </Card>
        </Content>
      </Layout>
    )
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header>
        <Nav mode="horizontal" header={{ text: 'Model Mapper Plus' }}
          footer={
            <>
              <Tag color={state ? 'green' : 'grey'}>{state ? '已连接' : '加载中'}</Tag>
              {state && (
                <Typography.Text size="small" style={{ marginLeft: 8 }}>
                  {state.persisted ? `state 已保存 ${state.updated_at ?? ''}` : '当前为 YAML 配置（首次保存后接管）'}
                </Typography.Text>
              )}
            </>
          }
        />
      </Header>
      <Content>
        <Nav mode="horizontal" selectedKey="rules" style={{ marginBottom: 8 }}
          items={[
            { itemKey: 'rules', text: '规则管理' },
            { itemKey: 'keys', text: 'Key 绑定' },
            { itemKey: 'preview', text: '规则试跑' },
          ]} />
        <Placeholder name="规则管理" />
      </Content>
    </Layout>
  )
}
```

`.gitignore` 追加：

```
# web build
web/node_modules/
```

`Makefile` 追加目标（放在 `test` 目标前）：

```make
web-build:
	cd web && npm install && VITE_HOSTED=1 npm run build
```

并在 `.PHONY` 行加 `web-build`。

- [ ] **步骤 2：安装依赖并构建**

运行：`cd web && npm install && npm run typecheck && VITE_HOSTED=1 npm run build && cd ..`
预期：typecheck 无错；`web/dist/index.html` 被真实构建产物替换（占位页被覆盖）

- [ ] **步骤 3：验证嵌入与 Go 测试**

运行：`go test ./... && go vet ./...`
预期：全部 PASS（`web/dist/index.html` 已是构建产物（title "Model Mapper Plus"，与任务 3 断言的 ToLower "model mapper" 匹配），`go:embed` 正常）

- [ ] **步骤 4：生成并提交 lockfile**

运行：`git check-ignore -q web/node_modules || echo "web/node_modules/" >> .gitignore`

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tsconfig.json web/index.html web/src/ web/dist/index.html Makefile .gitignore
git commit -m "feat(T6): scaffold Semi Design web UI with build pipeline"
```

---

### 任务 7：规则管理板块（RuleSetEditor + RulesPanel）

**文件**：
- 创建：`web/src/dsl.ts`、`web/src/components/RuleSetEditor.tsx`、`web/src/panels/RulesPanel.tsx`
- 修改：`web/src/App.tsx`（接入 RulesPanel 与板块切换）

**接口**：
- 消费：`api.putRules`、`StateResponse.rules`（任务 6）
- 产出：`splitEntries(dsl: string): Entry[]`、`joinEntries(entries: Entry[]): string`、`Entry` 类型、`<RuleSetEditor value onChange />`——任务 8 的 key 弹窗复用同一组件

DSL 条目模型：映射条目 `find=>replace`（原文保留转义符）与大小写条目 `\a`/`\A`。切分只识别**未转义**的 `;` 与首个未转义 `=>`，字段内容原样保留（不二次转义/反转义）。

- [ ] **步骤 1：实现 DSL 切分/拼接与 RuleSetEditor**

`web/src/dsl.ts`：

```ts
export type Entry =
  | { kind: 'map'; find: string; replace: string }
  | { kind: 'case'; op: 'lower' | 'upper' }

function isEscaped(s: string, i: number): boolean {
  let backslashes = 0
  for (let j = i - 1; j >= 0 && s[j] === '\\'; j--) backslashes++
  return backslashes % 2 === 1
}

// splitUnescaped splits on sep characters that are not backslash-escaped.
export function splitUnescaped(s: string, sep: string): string[] {
  const out: string[] = []
  let start = 0
  for (let i = 0; i < s.length; i++) {
    if (s[i] === sep && !isEscaped(s, i)) {
      out.push(s.slice(start, i))
      start = i + 1
    }
  }
  out.push(s.slice(start))
  return out
}

export function splitEntries(dsl: string): Entry[] {
  const entries: Entry[] = []
  for (const raw of splitUnescaped(dsl, ';')) {
    if (raw === '') continue
    if (raw === '\\a') { entries.push({ kind: 'case', op: 'lower' }); continue }
    if (raw === '\\A') { entries.push({ kind: 'case', op: 'upper' }); continue }
    // first unescaped "=>"
    for (let i = 0; i + 1 < raw.length; i++) {
      if (raw[i] === '=' && raw[i + 1] === '>' && !isEscaped(raw, i)) {
        entries.push({ kind: 'map', find: raw.slice(0, i), replace: raw.slice(i + 2) })
        break
      }
    }
  }
  return entries
}

export function joinEntries(entries: Entry[]): string {
  return entries
    .map((e) => (e.kind === 'case' ? (e.op === 'lower' ? '\\a' : '\\A') : `${e.find}=>${e.replace}`))
    .join(';')
}
```

`web/src/components/RuleSetEditor.tsx`：

```tsx
import { useState } from 'react'
import { Tabs, TabPane, Input, Button, Select, Typography } from '@douyinfe/semi-ui'
import { IconArrowUp, IconArrowDown, IconDelete, IconPlus } from '@douyinfe/semi-icons'
import { RuleSet } from '../api'
import { Entry, splitEntries, joinEntries } from '../dsl'

const SEGMENTS = [
  { key: 'global', label: '全局' },
  { key: 'claude', label: 'Claude Messages' },
  { key: 'codex', label: 'Codex Responses' },
  { key: 'openai', label: 'OpenAI Completions' },
] as const

type SegmentKey = (typeof SEGMENTS)[number]['key']

interface Props {
  value: RuleSet
  onChange: (next: RuleSet) => void
}

function EntryRow({ entry, onUpdate, onMove, onDelete }: {
  entry: Entry
  onUpdate: (e: Entry) => void
  onMove: (delta: -1 | 1) => void
  onDelete: () => void
}) {
  if (entry.kind === 'case') {
    return (
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '6px 0' }}>
        <Select value={entry.op} style={{ width: 200 }}
          onChange={(v) => onUpdate({ kind: 'case', op: v as 'lower' | 'upper' })}>
          <Select.Option value="lower">\a（转小写）</Select.Option>
          <Select.Option value="upper">\A（转大写）</Select.Option>
        </Select>
        <Button icon={<IconArrowUp />} size="small" onClick={() => onMove(-1)} />
        <Button icon={<IconArrowDown />} size="small" onClick={() => onMove(1)} />
        <Button icon={<IconDelete />} size="small" type="danger" onClick={onDelete} />
      </div>
    )
  }
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '6px 0' }}>
      <Input value={entry.find} placeholder="find（* 捕获，(max) 等后缀可参与）" style={{ flex: 2 }}
        onChange={(v) => onUpdate({ ...entry, find: v })} />
      <span>⇒</span>
      <Input value={entry.replace} placeholder="replace（$1 引用捕获）" style={{ flex: 2 }}
        onChange={(v) => onUpdate({ ...entry, replace: v })} />
      <Button icon={<IconArrowUp />} size="small" onClick={() => onMove(-1)} />
      <Button icon={<IconArrowDown />} size="small" onClick={() => onMove(1)} />
      <Button icon={<IconDelete />} size="small" type="danger" onClick={onDelete} />
    </div>
  )
}

export default function RuleSetEditor({ value, onChange }: Props) {
  const [active, setActive] = useState<SegmentKey>('global')
  const entries = splitEntries(value[active])

  const update = (next: Entry[]) => onChange({ ...value, [active]: joinEntries(next) })

  return (
    <div>
      <Tabs activeKey={active} onChange={(k) => setActive(k as SegmentKey)}>
        {SEGMENTS.map((s) => <TabPane tab={s.label} itemKey={s.key} key={s.key} />)}
      </Tabs>
      {entries.length === 0 && (
        <Typography.Text type="tertiary">
          本段为空{active !== 'global' ? '，请求将回退「全局」段' : ''}
        </Typography.Text>
      )}
      {entries.map((e, i) => (
        <EntryRow key={i} entry={e}
          onUpdate={(ne) => update(entries.map((x, j) => (j === i ? ne : x)))}
          onMove={(d) => {
            const j = i + d
            if (j < 0 || j >= entries.length) return
            const next = [...entries]
            ;[next[i], next[j]] = [next[j], next[i]]
            update(next)
          }}
          onDelete={() => update(entries.filter((_, j) => j !== i))} />
      ))}
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <Button icon={<IconPlus />} onClick={() => update([...entries, { kind: 'map', find: '', replace: '' }])}>
          添加映射
        </Button>
        <Button icon={<IconPlus />} onClick={() => update([...entries, { kind: 'case', op: 'lower' }])}>
          添加大小写操作
        </Button>
      </div>
      <Typography.Paragraph size="small" type="tertiary" style={{ marginTop: 8 }}>
        DSL：{value[active] || '（空）'} · 后缀 (none/auto/minimal/low/medium/high/xhigh/max) 或 (8192) 预算值可直接用于规则，CPA 执行时后缀覆盖请求体强度字段
      </Typography.Paragraph>
    </div>
  )
}
```

- [ ] **步骤 2：实现 RulesPanel 并接入 App**

`web/src/panels/RulesPanel.tsx`：

```tsx
import { useState } from 'react'
import { Button, Card, Toast } from '@douyinfe/semi-ui'
import { api, RuleSet, StateResponse } from '../api'
import RuleSetEditor from '../components/RuleSetEditor'

interface Props {
  state: StateResponse
  onSaved: (s: StateResponse) => void
}

export default function RulesPanel({ state, onSaved }: Props) {
  const [rules, setRules] = useState<RuleSet>(state.rules)
  const [saving, setSaving] = useState(false)

  const save = () => {
    setSaving(true)
    api.putRules(rules)
      .then((s) => { onSaved(s); Toast.success('规则已保存') })
      .catch((e: Error) => Toast.error(e.message))
      .finally(() => setSaving(false))
  }

  return (
    <Card title="映射规则（按端点分段，有序执行）" style={{ margin: 16 }}
      headerExtraContent={<Button theme="solid" loading={saving} onClick={save}>保存</Button>}>
      <RuleSetEditor value={rules} onChange={setRules} />
    </Card>
  )
}
```

`App.tsx` 改动：`Nav` 加 `onSelect`，按 `selectedKey` 渲染对应板块；引入 `RulesPanel`，`Placeholder` 暂留 keys/preview：

```tsx
const [tab, setTab] = useState('rules')
// Nav items onSelect={(data) => setTab(String(data.itemKey))}，selectedKey={tab}
// Content 内：
{tab === 'rules' && state && <RulesPanel state={state} onSaved={setState} />}
{tab === 'keys' && <Placeholder name="Key 绑定" />}
{tab === 'preview' && <Placeholder name="规则试跑" />}
```

- [ ] **步骤 3：构建验证**

运行：`cd web && npm install && npm run typecheck && VITE_HOSTED=1 npm run build && cd .. && go test ./...`
预期：typecheck 无错、构建成功、Go 测试全绿

- [ ] **步骤 4：提交**

```bash
git add web/
git commit -m "feat(T7): add rules panel with ordered entry editor"
```

---

### 任务 8：Key 绑定板块

**文件**：
- 创建：`web/src/panels/KeysPanel.tsx`
- 修改：`web/src/App.tsx`（接入）

**接口**：
- 消费：`api.postKey/patchKey/deleteKey/listCpaApiKeys`、`RuleSetEditor`（任务 7）、`KeyBinding` 类型（任务 6）

- [ ] **步骤 1：实现 KeysPanel**

`web/src/panels/KeysPanel.tsx`：

```tsx
import { useEffect, useState } from 'react'
import { Button, Card, Table, Modal, Input, Select, Switch, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { api, KeyBinding, RuleSet, StateResponse, listCpaApiKeys } from '../api'
import RuleSetEditor from '../components/RuleSetEditor'

const EMPTY_RULES: RuleSet = { global: '', claude: '', codex: '', openai: '' }

function maskKey(key: string): string {
  if (key.length <= 10) return key
  return `${key.slice(0, 6)}…${key.slice(-4)}`
}

function ruleSummary(b: KeyBinding): string {
  const parts: string[] = []
  const count = (dsl: string) => (dsl ? dsl.split(';').filter(Boolean).length : 0)
  if (b.rules.global) parts.push(`全局 ${count(b.rules.global)} 条`)
  if (b.rules.claude) parts.push(`Claude ${count(b.rules.claude)} 条`)
  if (b.rules.codex) parts.push(`Codex ${count(b.rules.codex)} 条`)
  if (b.rules.openai) parts.push(`OpenAI ${count(b.rules.openai)} 条`)
  return parts.join(' · ') || '—'
}

interface Props {
  state: StateResponse
  onSaved: (s: StateResponse) => void
}

export default function KeysPanel({ state, onSaved }: Props) {
  const [cpaKeys, setCpaKeys] = useState<string[]>([])
  const [editing, setEditing] = useState<KeyBinding | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    listCpaApiKeys().then(setCpaKeys).catch(() => setCpaKeys([]))
  }, [])

  const save = () => {
    if (!editing) return
    setSaving(true)
    api.postKey(editing)
      .then((s) => { onSaved(s); setEditing(null); Toast.success('绑定已保存') })
      .catch((e: Error) => Toast.error(e.message))
      .finally(() => setSaving(false))
  }

  const toggleEnabled = (b: KeyBinding, enabled: boolean) => {
    api.patchKey(b.key, { enabled }).then(onSaved).catch((e: Error) => Toast.error(e.message))
  }

  const remove = (b: KeyBinding) => {
    Modal.confirm({
      title: '删除绑定',
      content: `确认删除 ${b.alias || maskKey(b.key)} 的绑定？`,
      onOk: () => api.deleteKey(b.key).then(onSaved).catch((e: Error) => Toast.error(e.message)),
    })
  }

  return (
    <Card title="指定 Key 追加规则集（串联跑在顶层规则之后）" style={{ margin: 16 }}
      headerExtraContent={
        <Button theme="solid" onClick={() => setEditing({ key: '', alias: '', enabled: true, rules: EMPTY_RULES })}>
          + 新增绑定
        </Button>
      }>
      <Table
        dataSource={state.key_bindings}
        pagination={false}
        columns={[
          { title: 'API Key', dataIndex: 'key', render: (k: string) => <Typography.Text code>{maskKey(k)}</Typography.Text> },
          { title: '别名', dataIndex: 'alias' },
          { title: '追加规则', dataIndex: 'rules', render: (_: unknown, b: KeyBinding) => ruleSummary(b) },
          { title: '启用', dataIndex: 'enabled', render: (on: boolean, b: KeyBinding) => <Switch checked={on} onChange={(v) => toggleEnabled(b, v)} /> },
          { title: '', dataIndex: 'ops', render: (_: unknown, b: KeyBinding) => (
            <>
              <Button size="small" onClick={() => setEditing(b)}>编辑</Button>{' '}
              <Button size="small" type="danger" onClick={() => remove(b)}>删除</Button>
            </>
          ) },
        ]}
      />
      <Typography.Paragraph size="small" type="tertiary" style={{ marginTop: 8 }}>
        Key 来源：GET /v0/management/api-keys（下拉选择，也可手动输入未列出的 key）
      </Typography.Paragraph>

      <Modal
        title={editing?.key ? `编辑绑定：${editing.alias || maskKey(editing.key)}` : '新增绑定'}
        visible={editing !== null}
        onCancel={() => setEditing(null)}
        onOk={save}
        confirmLoading={saving}
        width={860}
      >
        {editing && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Select
              style={{ width: '100%' }}
              filter
              allowCreate
              placeholder="选择或输入 API key"
              value={editing.key || undefined}
              onChange={(v) => setEditing({ ...editing, key: String(v) })}
          >
              {cpaKeys.map((k) => (
                <Select.Option key={k} value={k}>{maskKey(k)}</Select.Option>
              ))}
            </Select>
            <Input placeholder="别名（可选）" value={editing.alias}
              onChange={(v) => setEditing({ ...editing, alias: v })} />
            <div>
              <Typography.Text size="small" type="tertiary">
                追加规则集（与规则管理同构；本 key 的请求在顶层规则跑完后接力执行）
              </Typography.Text>
              <RuleSetEditor value={editing.rules} onChange={(r) => setEditing({ ...editing, rules: r })} />
            </div>
            <div>
              <Switch checked={editing.enabled} onChange={(v) => setEditing({ ...editing, enabled: v })} /> 启用
              {state.key_bindings.some((b) => b.key === editing.key && b !== editing) && (
                <Tag color="orange" style={{ marginLeft: 8 }}>同 key 已存在，保存将覆盖</Tag>
              )}
            </div>
          </div>
        )}
      </Modal>
    </Card>
  )
}
```

- [ ] **步骤 2：接入 App 并构建验证**

`App.tsx`：`tab === 'keys'` 分支替换为 `<KeysPanel state={state} onSaved={setState} />`。

运行：`cd web && npm run typecheck && VITE_HOSTED=1 npm run build && cd .. && go test ./...`
预期：全部通过

- [ ] **步骤 3：提交**

```bash
git add web/
git commit -m "feat(T8): add key bindings panel with CPA key picker"
```

---

### 任务 9：规则试跑板块

**文件**：
- 创建：`web/src/panels/PreviewPanel.tsx`
- 修改：`web/src/App.tsx`（接入，删除 `Placeholder`）

**接口**：
- 消费：`api.preview`、`listCpaApiKeys`（任务 6）

- [ ] **步骤 1：实现 PreviewPanel**

`web/src/panels/PreviewPanel.tsx`：

```tsx
import { useEffect, useState } from 'react'
import { Button, Card, Input, Select, Tag, Toast, Typography, Descriptions } from '@douyinfe/semi-ui'
import { api, PreviewResponse, listCpaApiKeys } from '../api'

const FORMATS = [
  { value: 'claude', label: 'claude（/v1/messages）' },
  { value: 'openai', label: 'openai（chat completions）' },
  { value: 'openai-response', label: 'openai-response（responses/codex）' },
]

export default function PreviewPanel() {
  const [cpaKeys, setCpaKeys] = useState<string[]>([])
  const [key, setKey] = useState('')
  const [format, setFormat] = useState('claude')
  const [model, setModel] = useState('')
  const [result, setResult] = useState<PreviewResponse | null>(null)
  const [running, setRunning] = useState(false)

  useEffect(() => {
    listCpaApiKeys().then(setCpaKeys).catch(() => setCpaKeys([]))
  }, [])

  const run = () => {
    setRunning(true)
    api.preview({ key: key || undefined, format, model })
      .then(setResult)
      .catch((e: Error) => { setResult(null); Toast.error(e.message) })
      .finally(() => setRunning(false))
  }

  return (
    <Card title="规则试跑（不落盘、不发上游）" style={{ margin: 16 }}>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'center' }}>
        <Select style={{ width: 260 }} filter allowCreate placeholder="Key（可选，模拟 key 维度）"
          value={key || undefined} onChange={(v) => setKey(String(v))} showClear>
          {cpaKeys.map((k) => <Select.Option key={k} value={k}>{k.slice(0, 6)}…{k.slice(-4)}</Select.Option>)}
        </Select>
        <Select style={{ width: 280 }} value={format} onChange={(v) => setFormat(String(v))}>
          {FORMATS.map((f) => <Select.Option key={f.value} value={f.value}>{f.label}</Select.Option>)}
        </Select>
        <Input style={{ width: 320 }} placeholder="模型，如 claude-opus-4-5(max)"
          value={model} onChange={setModel} onEnterPress={run} />
        <Button theme="solid" loading={running} onClick={run} disabled={!model}>试跑</Button>
      </div>

      {result && (
        <Descriptions style={{ marginTop: 16 }} align="left"
          data={[
            { key: '顶层规则输出 M₁', value: result.m1 },
            { key: 'key 层输出 M₂', value: result.m2 },
            { key: '是否路由', value: result.routed ? <Tag color="green">路由回插件执行</Tag> : <Tag>不接管，走默认路径</Tag> },
            { key: '最终出站模型', value: <Typography.Text strong>{result.final}</Typography.Text> },
            { key: 'CPA 处理', value: '剥后缀选模型并应用强度；响应模型字段恢复为客户端请求模型' },
          ]}
        />
      )}
    </Card>
  )
}
```

- [ ] **步骤 2：接入 App 并构建验证**

`App.tsx`：`tab === 'preview'` 分支替换为 `<PreviewPanel />`，删除 `Placeholder` 组件。

运行：`cd web && npm run typecheck && VITE_HOSTED=1 npm run build && cd .. && go test ./... && go vet ./...`
预期：全部通过

- [ ] **步骤 3：提交**

```bash
git add web/
git commit -m "feat(T9): add dry-run preview panel"
```

---

## CI 与文档

### 任务 10：CI Node 集成与文档更新

**文件**：
- 修改：`.github/workflows/build.yml`（test job 加 Node setup + web-build）
- 修改：`README.md`（功能/配置/web UI/state_file 文档）
- 修改：`CLAUDE.md`（架构节）

- [ ] **步骤 1：CI 集成**

`.github/workflows/build.yml` 的 test job，在 `Setup Go` 步之后、`Test` 步之前插入：

```yaml
      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: Build web UI
        run: make web-build
```

- [ ] **步骤 2：README 更新**

在配置示例段后新增两节（中文，沿用现有 README 语体）：

```markdown
### Web 管理界面

插件注册管理页面于 `http://<cpa-host>:<api-port>/v0/resource/plugins/model-mapper-plus/index.html`，
使用 CPA management key 登录。界面提供三个板块：

- **规则管理**：编辑全局与三个端点的有序规则条目（含 `\a`/`\A` 大小写操作）
- **Key 绑定**：为指定客户端 API key 追加规则集，在该 key 的请求上串联跑在顶层规则之后
- **规则试跑**：输入 key（可选）、端点、模型，预览 M→M₁→M₂ 分步改写结果

首次保存后，规则与 key 绑定写入 `state_file`（默认 `model-mapper-plus-state.json`，权限 0600）；
此后 `state_file` 为唯一真相源，YAML 中的规则字段不再生效（`enabled` 仍只来自 YAML）。
删除 `state_file` 即可回到 YAML 配置。

### 指定 Key 的映射与思考强度控制

key 绑定使指定客户端 key 的请求在顶层规则输出之上再执行一段同构规则集
（端点段非空用端点段，否则回退该 key 的全局段）。思考强度通过模型名后缀直接表达，
例如 `claude-opus-4-5(max)=>claude-opus-4-5(high)`——CPA 执行时后缀覆盖请求体中的强度字段。
注意 `*` 捕获会连同后缀一起捕获（`claude-*` 捕获 `opus-4-5(max)`）。
```

- [ ] **步骤 3：CLAUDE.md 更新**

`Architecture overview` 节的 `main.go` 列表追加：

```markdown
- `state.go` implements the state_file persistence layer (ADR-0001: once `state_file` exists it is the source of truth for rules and key bindings; YAML rule fields are seed-only). Writes are atomic (tmp+fsync+rename, mode 0600).
- `management.go` and `web_embed.go` implement `management.register`/`management.handle`: a resource route serves the embedded admin UI, and data routes manage rules, key bindings, and dry-run previews.
- `routeModel` chains two layers: the top-level rule set runs first, then the bound key's rule set runs on its output (ADR-0003). The client key comes from inbound `Authorization: Bearer`/`x-api-key` headers. Thinking effort is expressed via model-name suffixes inside ordinary rules (ADR-0002); no dedicated effort code path exists.
- `web/` is the React 19 + Vite 8 + Semi Design admin UI, built to a single inlined `web/dist/index.html` via `make web-build` and embedded with `go:embed`.
```

同时把 `selectRules` 条目更新为"顶层与 key 规则集共用（`selectRulesFrom`）"。

- [ ] **步骤 4：验证并提交**

运行：`make web-build && go test ./... && go vet ./...`
预期：全部通过

```bash
git add .github/workflows/build.yml README.md CLAUDE.md
git commit -m "docs(T10): wire web build into CI and document key bindings"
```

---

## 验收与合并

### 任务 11：验收（acceptance-qa）

> 本任务由 executing-plans 收尾审查阶段触发 acceptance-qa 按下表执行，
> 不参与逐任务连续执行；报告与证据落盘特性目录 `acceptance/` 子目录。

| Scenario / 检查项 | 维度 | 执行方式 | 目标 | 阈值/预期 | 验收证据 |
|-------------------|------|---------|------|----------|---------|
| key 下拉来自 CPA | integration | 验收任务 | 本地 CPA（`make smoke-local` 环境） | 新增绑定弹窗的 key 下拉列出 `config.yaml` 的 api-keys | 截图/录屏 |
| 端到端：绑定 key 请求经接力改写且响应模型字段恢复 | e2e | 验收任务 | smoke 环境（`CPA_SMOKE_API_KEY`/`CPA_SMOKE_CPA_BIN`） | 绑定 key 的请求出站模型为 key 层结果；响应 `model` 字段恢复为客户端请求模型 | smoke 输出 |
| 管理页可访问（真实浏览器） | e2e | 验收任务 | `http://<cpa-host>:<port>/v0/resource/plugins/model-mapper-plus/index.html` | 页面加载、登录、三板块可切换 | 截图 |

### 任务 12：合并与清理

- [ ] **步骤 1：全量验证**

在 worktree 内运行 `make web-build && go test ./... && go vet ./...`，确认全绿。失败 → 修复后才进入合并。

- [ ] **步骤 2：合并回来源分支**

```bash
cd "$(dirname "$(git rev-parse --git-common-dir)")"   # 回到主工作区
git merge plan/2026-07-25-key-rules-admin-ui
```

合并冲突、或主工作区有未提交改动 → 停下向计划作者确认，不强行合并。

- [ ] **步骤 3：清理**

```bash
git worktree remove .worktrees/plan-2026-07-25-key-rules-admin-ui
git branch -d plan/2026-07-25-key-rules-admin-ui
```

- [ ] **步骤 4：sync_commit 锚定**

```bash
SYNC=$(git rev-parse HEAD)   # 合并完成后的主工作区 HEAD
# 把 .spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md
# frontmatter 的 sync_commit: null 更新为 $SYNC
git add .spec-dev/2026-07-25-key-rules-admin-ui/spec/key-rules-admin-ui-design.md
git commit -m "chore(spec): sync_commit 锚定 ${SYNC:0:7}"
```
