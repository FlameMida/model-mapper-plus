# 插件版本号机制 实施计划

> **执行方式**：使用 spec-dev 的 executing-plans skill 逐任务执行本计划；无该 skill 的环境直接从任务 0 起按序执行至最终任务。步骤用复选框（`- [ ]`）语法跟踪；脱离项目携带时连同特性目录（含 spec）整体带走。
>
> **偏差处理**：执行中发现计划与现实不符——小偏差（路径笔误、明显遗漏但意图清楚）就地修正并在提交信息中注明；接口、数据结构等契约级偏差停下向计划作者确认，不猜着改。

**目标**：让每个 .so/.dll/.dylib 内嵌可辨识的 pluginVersion（dev 用 git SHA、发版用 tag），插件管理页可见版本，发版产物文件名遵循 CPA `-v<version>` 规范。

**Spec**：`.spec-dev/2026-07-27-plugin-version/spec/plugin-version-design.md`

**架构**：Makefile 单一计算 `PLUGIN_VERSION`（VERSION > HEAD tag > dev SHA，均去 `v` 前缀）经 `-ldflags -X` 注入 `main.pluginVersion`；`/state` 响应在 stateResponse 层（不持久化）暴露 `plugin_version`；前端 footer 显示；发版构建产物文件名 `<name>-v<version><ext>`，dev-so/install 部署固定名覆写；CI 全平台（含 cgo-actions）同步版本与文件名。

**技术栈**：Go c-shared、GNU Make、GitHub Actions（go-cross/cgo-actions）、React 19 + Semi Design + vitest

## 全局约束

- CPA 文件名规范：产物文件名必须 `<id>-v<version><ext>`，分隔符字面量 `-v`，Version 不能以 `v` 开头（CPA platform.go:43/84）
- `pluginVersion` 始终非空（CPA validPlugin 强制，空则拒绝加载）
- 单变量 `pluginVersion`（不注入 commit/buildTime）
- 前端版本从 `/state` 的 `plugin_version` 取，**不**注入前端 bundle
- dev-so 部署固定名 `model-mapper-plus.so`；发版产物（build/package）带 `-v<version>`
- 所有构建路径产出非空版本（含 `0.0.0-dev.unknown` / `0.0.0-dev.unbuilt`）

---

### 任务 0：建立隔离工作区

- [ ] **步骤 1：检测已有隔离**

运行：`git rev-parse --git-dir` 与 `git rev-parse --git-common-dir`
两者不同、且 `git rev-parse --show-superproject-working-tree` 无输出（排除 submodule）
→ 已在隔离工作区，跳过本任务。

- [ ] **步骤 2：建立 worktree**

有原生 worktree 工具（如 EnterWorktree）或 using-git-worktrees skill 时优先使用；否则手工降级：
确认 `.worktrees/` 已被忽略（`git check-ignore -q .worktrees`，未忽略先加入 `.gitignore` 并提交），然后
`git worktree add .worktrees/plan-2026-07-27-plugin-version -b plan/2026-07-27-plugin-version` 并切换到该目录。

- [ ] **步骤 3：安装依赖并验证基线**

`go mod download` 与 `cd web && npm install`；
运行 `make test` 与 `npm --prefix web test` 确认基线全绿。基线测试失败 → 停下报告，先问再继续。

---

## 后端

### 任务 1：pluginVersion 默认值 + /state 暴露 plugin_version

**文件**：
- 修改：`main.go:21`（pluginVersion 默认值）
- 修改：`management.go:82-89`（stateResponse 加字段）、`management.go:91-101`（managementGetState 填充）
- 测试：`management_api_test.go`

**接口**：
- 消费：无（本任务为版本源的根）
- 产出：`managementGetState()` 返回的 `stateResponse` JSON 含 `"plugin_version": <string>`；`main.pluginVersion` 默认值 `0.0.0-dev.unbuilt`。后续前端任务依赖此字段。

- [ ] **步骤 1：写失败测试**

在 `management_api_test.go` 末尾追加：

```go
// Scenario: /state 返回插件版本且不持久化
func TestManagementGetStateIncludesPluginVersion(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementGetState()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		PluginVersion string `json:"plugin_version"`
	}
	decodeBody(t, resp, &body)
	if body.PluginVersion != pluginVersion {
		t.Fatalf("plugin_version = %q, want %q", body.PluginVersion, pluginVersion)
	}
	if body.PluginVersion == "" {
		t.Fatal("plugin_version must be non-empty (CPA rejects empty Version)")
	}
}

// Scenario: plugin_version 不写 state_file（仅响应层）
func TestStateFileExcludesPluginVersion(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if strings.Contains(string(raw), "plugin_version") {
		t.Fatalf("state_file must not contain plugin_version: %s", raw)
	}
}
```

确认 `management_api_test.go` 顶部 import 含 `"strings"`（已有 `os`）；若无则补 `"strings"`。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test . -run 'TestManagementGetStateIncludesPluginVersion|TestStateFileExcludesPluginVersion' -v`
预期：FAIL（编译错误：`stateResponse` 无 `PluginVersion` 字段，`body.PluginVersion` 解析不到值）。

- [ ] **步骤 3：写最小实现**

`main.go:21`：
```go
var pluginVersion = "0.0.0-dev.unbuilt"
```

`management.go` 的 `stateResponse` 结构（约 82-89 行）末尾加字段：
```go
type stateResponse struct {
	Version     int          `json:"version"`
	Rules       RuleSet      `json:"rules"`
	KeyBindings []KeyBinding `json:"key_bindings"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
	Persisted   bool         `json:"persisted"`
	StateFile   string       `json:"state_file"`
	PluginVersion string     `json:"plugin_version"` // 新增：仅响应层，不持久化
}
```

`management.go` 的 `managementGetState()`（约 96-100 行）组装处补 `PluginVersion: pluginVersion`：
```go
return managementJSON(http.StatusOK, stateResponse{
	Version: stateVersion, Rules: st.Rules,
	KeyBindings: st.KeyBindings, UpdatedAt: st.UpdatedAt, Persisted: persisted,
	StateFile:     stateFilePath(),
	PluginVersion: pluginVersion,
})
```

- [ ] **步骤 4：运行测试确认通过**

运行：`go test . -run 'TestManagementGetStateIncludesPluginVersion|TestStateFileExcludesPluginVersion' -v`
预期：PASS。再跑 `go test ./...` 确认未破坏既有测试。

- [ ] **步骤 5：提交**

```bash
git add main.go management.go management_api_test.go
git commit -m "feat(T1): /state 暴露 plugin_version（响应层不持久化）"
```

---

## 构建系统

### 任务 2：Makefile 版本计算单一来源

**文件**：
- 修改：`Makefile:7-9`（替换 VERSION_LDFLAGS 为 PLUGIN_VERSION 计算块）
- 修改：`Makefile:24`（.PHONY 加 print-version）
- 创建：`scripts/test-version.sh`

**接口**：
- 消费：无
- 产出：Makefile 变量 `PLUGIN_VERSION`（始终非空）与 `VERSION_LDFLAGS`（始终注入 `-X main.pluginVersion=$(PLUGIN_VERSION)`）；目标 `print-version` 输出当前 PLUGIN_VERSION。后续任务 3/4 依赖 `$(PLUGIN_VERSION)`。

- [ ] **步骤 1：写失败测试（验证脚本）**

创建 `scripts/test-version.sh`：
```bash
#!/usr/bin/env bash
# 验证 Makefile PLUGIN_VERSION 计算逻辑（覆盖 spec 的核心 Scenario）。
set -eu
cd "$(dirname "$0")/.."
fail() { echo "FAIL: $*" >&2; exit 1; }

# Scenario: VERSION 环境变量优先（覆盖 tag 与 SHA）
[ "$(make print-version VERSION=9.9.9)" = "9.9.9" ] || fail "VERSION override"

# Scenario: VERSION 带 v 前缀被剥离
[ "$(make print-version VERSION=v1.2.4)" = "1.2.4" ] || fail "VERSION v-prefix strip"

# Scenario: dev 构建用 SHA（HEAD 无 tag）
V=$(make print-version)
case "$V" in 0.0.0-dev.*) ;; *) fail "dev SHA got '$V'";; esac

# Scenario: untracked 新文件标记 dirty
F="./.version-test-dirty-$$"
echo x > "$F"
V=$(make print-version)
case "$V" in *.dirty) ok=1;; *) ok=0;; esac
rm -f "$F"
[ "$ok" -eq 1 ] || fail "dirty suffix (untracked) got '$V'"

# Scenario: HEAD tag 去 v 前缀
TAG="v8.8.8-test$$"
git tag "$TAG" HEAD >/dev/null 2>&1 || fail "cannot create temp tag"
V=$(make print-version)
git tag -d "$TAG" >/dev/null 2>&1
case "$V" in 8.8.8-test*) ;; *) fail "HEAD tag v-strip got '$V'";; esac

# Scenario: VERSION 环境变量优先于 tag
git tag "$TAG" HEAD >/dev/null 2>&1
V=$(make print-version VERSION=6.6.6)
git tag -d "$TAG" >/dev/null 2>&1
[ "$V" = "6.6.6" ] || fail "VERSION>tag got '$V'"

echo "all version checks passed"
```
`chmod +x scripts/test-version.sh`。

- [ ] **步骤 2：运行测试确认失败**

运行：`bash scripts/test-version.sh`
预期：FAIL（`make: *** No rule to make target 'print-version'`）。

- [ ] **步骤 3：写最小实现**

替换 `Makefile:9`（原 `VERSION_LDFLAGS := $(if $(VERSION),-X main.pluginVersion=$(VERSION),)`）为版本计算块：
```makefile
# 版本号单一来源：VERSION 强制覆盖 > HEAD 精确 tag > dev SHA；VERSION 与 tag 均去 v 前缀
# （CPA 拒绝以 v 开头的 Version）。始终非空（CPA validPlugin 强制）。
HEAD_TAG  := $(shell git describe --tags --exact-match HEAD 2>/dev/null | head -1)
GIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(shell [ -z "$$(git status --porcelain 2>/dev/null)" ] || echo ".dirty")
ifneq ($(VERSION),)
  PLUGIN_VERSION := $(patsubst v%,%,$(VERSION))
else ifneq ($(HEAD_TAG),)
  PLUGIN_VERSION := $(patsubst v%,%,$(HEAD_TAG))
else ifneq ($(GIT_SHORT),)
  PLUGIN_VERSION := 0.0.0-dev.$(GIT_SHORT)$(GIT_DIRTY)
else
  PLUGIN_VERSION := 0.0.0-dev.unknown
endif
VERSION_LDFLAGS := -X main.pluginVersion=$(PLUGIN_VERSION)

print-version:
	@echo "$(PLUGIN_VERSION)"
```

在 `Makefile:24` 的 `.PHONY:` 行末尾追加 ` print-version`。

- [ ] **步骤 4：运行测试确认通过**

运行：`bash scripts/test-version.sh` → 预期 `all version checks passed`。

手动补测 spec 另两条 Scenario（HEAD tag 与无 git）：
- `git tag v9.9.9-testtmp HEAD && make print-version` → 输出 `9.9.9-testtmp`；`git tag -d v9.9.9-testtmp`
- 无 git 分支：阅读确认 `else ... 0.0.0-dev.unknown` 存在（移走 .git 在 worktree 不便，留 acceptance 覆盖）。

- [ ] **步骤 5：提交**

```bash
git add Makefile scripts/test-version.sh
git commit -m "feat(T2): Makefile 版本号单一计算（VERSION>tag>SHA，去 v 前缀）"
```

---

### 任务 3：发版构建产物文件名版本化

**文件**：
- 修改：`Makefile:10-11`（WINDOWS_AMD64_OUT/LINUX_AMD64_OUT）、`Makefile:42`（build-platform-go out）、`Makefile:76`（package-platform library）
- 修改：`.github/scripts/package-release.go:63`（binaryPath 调用）、`.github/scripts/package-release.go:95-97`（binaryPath 签名）
- 测试：`.github/scripts/package-release_test.go`

**接口**：
- 消费：任务 2 产出的 `$(PLUGIN_VERSION)`
- 产出：`build-platform-go` 输出 `$(DIST_DIR)/<os>_<arch>/model-mapper-plus-v<PLUGIN_VERSION>.<ext>`；`package-release.go` 的 `(artifactSpec).binaryPath(distDir, version)` 返回同构路径。后续任务 4（dev-so cp 源路径）、任务 6（CI -library 路径）依赖此文件名约定。

- [ ] **步骤 1：写失败测试**

在 `.github/scripts/package-release_test.go` 加（若无此文件，参考既有 package-release_test.go 的 package 声明 `package main`）：
```go
func TestBinaryPathVersioned(t *testing.T) {
	a := artifactSpec{osName: "linux", arch: "amd64"}
	got := a.binaryPath("dist", "1.2.3")
	want := filepath.Join("dist", "linux_amd64", "model-mapper-plus-v1.2.3.so")
	if got != want {
		t.Fatalf("binaryPath = %s, want %s", got, want)
	}

	w := artifactSpec{osName: "windows", arch: "arm64"}
	if got := w.binaryPath("dist", "0.0.0-dev.abc1"); got != filepath.Join("dist", "windows_arm64", "model-mapper-plus-v0.0.0-dev.abc1.dll") {
		t.Fatalf("windows binaryPath = %s", got)
	}
}
```
确认测试文件 import 含 `"path/filepath"`。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run TestBinaryPathVersioned -v`
预期：FAIL（编译错：`binaryPath` 接收 1 个参数而非 2，或 `not enough arguments`）。

- [ ] **步骤 3：写最小实现**

`.github/scripts/package-release.go:95-97` 改 `binaryPath` 接收 version：
```go
func (a artifactSpec) binaryPath(distDir, version string) string {
	return filepath.Join(distDir, a.osName+"_"+a.arch,
		fmt.Sprintf("%s-v%s%s", pluginName, version, libraryExtension(a.osName)))
}
```

`.github/scripts/package-release.go:63` 调用点传入 version（`packageExistingArtifacts` 已有 version 参数）：
```go
binaryPath := artifact.binaryPath(distDir, version)
```

`Makefile:10-11`：
```makefile
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).so
```

`Makefile:42`（build-platform-go 的 out）：
```makefile
	out="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)-v$(PLUGIN_VERSION)$$ext"; \
```

`Makefile:76`（package-platform 的 library）：
```makefile
	library="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)-v$(VERSION)$$ext"; \
```
（package-platform 强制要求 VERSION，故用 `$(VERSION)`——它与 PLUGIN_VERSION 同源，VERSION 传 v 前缀时 PLUGIN_VERSION 已剥离；这里用 `$(VERSION)` 原值需保证 package-platform 调用前 VERSION 已是纯版本号。为一致，改用 `$(PLUGIN_VERSION)`：）
```makefile
	library="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)-v$(PLUGIN_VERSION)$$ext"; \
```

- [ ] **步骤 4：运行测试确认通过**

运行：`go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run TestBinaryPathVersioned -v` → PASS。

构建验证（需 VERSION 显式传，避免 dev SHA 含 `/` 等异常）：`make build-platform-go GOOS=linux GOARCH=amd64 BUILD_CC="zig cc -target x86_64-linux-gnu" VERSION=1.2.3` 后
`ls dist/linux_amd64/model-mapper-plus-v1.2.3.so` 存在。

- [ ] **步骤 5：提交**

```bash
git add Makefile .github/scripts/package-release.go .github/scripts/package-release_test.go
git commit -m "feat(T3): 发版产物文件名带 -v<version>（CPA 规范）"
```

---

### 任务 4：dev-so 注入版本 + 固定名部署 + 清理残留

**文件**：
- 修改：`Makefile:109-113`（dev-so 目标）

**接口**：
- 消费：任务 2 的 `$(VERSION_LDFLAGS)` / `$(PLUGIN_VERSION)`、任务 3 的版本化产物路径
- 产出：`make dev-so` 部署固定名 `model-mapper-plus.so` 到 `$(CPA_PLUGINS_DIR)/linux/amd64/`，部署前清理该 ID 的 release 残留（`model-mapper-plus-v*.so`）。

- [ ] **步骤 1：写失败测试（验证脚本）**

创建 `scripts/test-dev-so.sh`（需 zig；无 zig 时标记 skip，留 acceptance 实测）：
```bash
#!/usr/bin/env bash
set -eu
cd "$(dirname "$0")/.."
command -v zig >/dev/null 2>&1 || { echo "SKIP: zig not installed"; exit 0; }
fail() { echo "FAIL: $*" >&2; exit 1; }

TMPDIR_CPA=$(mktemp -d)
trap 'rm -rf "$TMPDIR_CPA"' EXIT

# 预置一个旧 release 残留
mkdir -p "$TMPDIR_CPA/linux/amd64"
touch "$TMPDIR_CPA/linux/amd64/model-mapper-plus-v1.2.3.so"

make dev-so CPA_PLUGINS_DIR="$TMPDIR_CPA"

# 部署名固定
[ -f "$TMPDIR_CPA/linux/amd64/model-mapper-plus.so" ] || fail "fixed deploy name missing"
# release 残留已清理
[ ! -e "$TMPDIR_CPA/linux/amd64/model-mapper-plus-v1.2.3.so" ] || fail "stale release not cleaned"
# .so 内嵌 dev SHA（非 0.0.0-dev）
SO=$(ls dist/linux_amd64/model-mapper-plus-v*.so | head -1)
strings "$SO" | grep -q '0\.0\.0-dev\.' || fail "no dev SHA embedded"

echo "dev-so checks passed"
```
`chmod +x scripts/test-dev-so.sh`。

- [ ] **步骤 2：运行测试确认失败**

运行：`bash scripts/test-dev-so.sh`
预期：FAIL（当前 dev-so 清空 VERSION_LDFLAGS，.so 内嵌 `0.0.0-dev`，grep `0.0.0-dev.` 不命中）。

- [ ] **步骤 3：写最小实现**

替换 `Makefile:109-113` 的 dev-so 目标：
```makefile
# dev-so: cross-compile linux/amd64 .so via zig (with fresh UI + dev SHA version)
# and copy it as the FIXED name model-mapper-plus.so into your docker CPA's plugins
# dir (cp 覆盖，热重载友好）。部署前清理该 ID 的 release 残留（避免 CPA cleanup 冲突）。
dev-so: web-build
	@$(MAKE) --no-print-directory build-platform-go GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(ZIG) cc -target x86_64-linux-gnu" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"
	@mkdir -p "$(CPA_PLUGINS_DIR)/linux/amd64"
	@rm -f "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME)-v"*.so
	cp $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).so "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME).so"
	@echo ">> deployed $(PLUGIN_NAME).so (v$(PLUGIN_VERSION)) -> $(CPA_PLUGINS_DIR)/linux/amd64/ (restart/reload your CPA to pick it up)"
```

- [ ] **步骤 4：运行测试确认通过**

运行：`bash scripts/test-dev-so.sh` → 预期 `dev-so checks passed`（无 zig 时 SKIP，留 acceptance 实测）。

- [ ] **步骤 5：提交**

```bash
git add Makefile scripts/test-dev-so.sh
git commit -m "feat(T4): dev-so 注入 dev SHA 版本 + 固定名部署 + 清理 release 残留"
```

---

## 前端

### 任务 5：管理页顶部状态栏展示版本号

**文件**：
- 修改：`web/src/api.ts:17-24`（StateResponse 加字段）
- 修改：`web/src/App.tsx:71-78`（footer 拼接版本）
- 修改：`web/src/test/setup.ts`（补 ResizeObserver polyfill，Semi 组件 jsdom 需要）
- 创建：`web/src/App.test.tsx`

**接口**：
- 消费：任务 1 的 `/state` 响应字段 `plugin_version`
- 产出：管理页顶部 footer 在 state_file 后显示 `· v<plugin_version>`；旧 .so 无该字段时不显示、不报错。

- [ ] **步骤 1：写失败测试**

创建 `web/src/App.test.tsx`：
```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import App from './App'

vi.mock('./api', () => ({
  api: { getState: vi.fn() },
  hasKey: () => true,
  onAuthChange: () => () => {},
  readPanelAuth: () => null,
}))
import { api } from './api'

describe('App 版本展示', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('/state 返回 plugin_version 时 footer 显示 v<version>', async () => {
    ;(api.getState as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
      plugin_version: '0.0.0-dev.abc1234',
    })
    render(<App />)
    await waitFor(() => {
      expect(screen.getByText(/v0\.0\.0-dev\.abc1234/)).toBeInTheDocument()
    })
  })

  it('旧 .so 无 plugin_version 时不显示版本段且不报错', async () => {
    ;(api.getState as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
    })
    render(<App />)
    await waitFor(() => {
      expect(screen.queryByText(/v0\.0\.0/)).not.toBeInTheDocument()
    })
  })
})
```

- [ ] **步骤 2：运行测试确认失败**

运行：`npm --prefix web test -- --run src/App.test.tsx`
预期：FAIL（`StateResponse` 无 `plugin_version`，`v0.0.0-dev.abc1234` 找不到；或 Semi 组件报 `ResizeObserver is not defined`）。

- [ ] **步骤 3：写最小实现**

`web/src/api.ts` 的 `StateResponse`（约 17-24 行）加字段：
```ts
export interface StateResponse {
  version: number
  rules: RuleSet
  key_bindings: KeyBinding[]
  updated_at?: string
  persisted: boolean
  state_file?: string
  plugin_version?: string  // 新增
}
```

`web/src/App.tsx` 的 footer `Typography.Text`（约 71-78 行）在 `state_file` 段后追加版本拼接。原段：
```tsx
{state.state_file ? ` · ${state.state_file}` : ''}
```
后接：
```tsx
{state.state_file ? ` · ${state.state_file}` : ''}
{state.plugin_version ? ` · v${state.plugin_version}` : ''}
```

`web/src/test/setup.ts` 顶部补（Semi Layout/Nav 在 jsdom 触发 ResizeObserver）：
```ts
// jsdom 不实现 ResizeObserver；Semi UI 组件依赖它，缺失会在 mount 时抛 ReferenceError。
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver
```

- [ ] **步骤 4：运行测试确认通过**

运行：`npm --prefix web test -- --run src/App.test.tsx` → PASS。再跑 `npm --prefix web test` 确认全绿。

- [ ] **步骤 5：提交**

```bash
git add web/src/api.ts web/src/App.tsx web/src/test/setup.ts web/src/App.test.tsx
git commit -m "feat(T5): 管理页 footer 展示 plugin_version"
```

---

## CI

### 任务 6：CI 全平台版本与文件名同步

**文件**：
- 修改：`.github/workflows/build.yml`（3 处 release_metadata 的非 tag 分支 + 2 处 cgo-actions output + 2 处 -library 路径）

**接口**：
- 消费：任务 2 的版本算法（`git rev-parse --short HEAD`，非 tag 同源）、任务 3 的文件名约定 `<name>-v<version><ext>`
- 产出：CI 非 tag 推送时各平台产物 pluginVersion = `0.0.0-dev.<shortSHA>`；windows-arm64/freebsd 产物文件名版本化。

- [ ] **步骤 1：写失败测试（yaml 结构断言）**

创建 `scripts/test-ci-yaml.sh`：
```bash
#!/usr/bin/env bash
# 断言 build.yml 已注入 dev SHA 计算与版本化 output（CI 难本地全跑，用结构断言）。
set -eu
cd "$(dirname "$0")/.."
f=.github/workflows/build.yml
fail() { echo "FAIL: $*" >&2; exit 1; }

grep -q 'VERSION="0\.0\.0-dev\.$(git rev-parse --short HEAD)"' "$f" || fail "non-tag SHA missing"
grep -q 'output: \${{ env\.PLUGIN_NAME }}-v\${{ env\.VERSION }}\.dll' "$f" || fail "windows output not versioned"
grep -q 'output: \${{ env\.PLUGIN_NAME }}-v\${{ env\.VERSION }}\.so' "$f" || fail "freebsd output not versioned"
grep -q '\-library "dist/windows_arm64/\${PLUGIN_NAME}-v\${VERSION}\.dll"' "$f" || fail "windows -library path not versioned"
grep -q '\-library "dist/freebsd_amd64/\${PLUGIN_NAME}-v\${VERSION}\.so"' "$f" || fail "freebsd -library path not versioned"

echo "ci yaml checks passed"
```
`chmod +x scripts/test-ci-yaml.sh`。

- [ ] **步骤 2：运行测试确认失败**

运行：`bash scripts/test-ci-yaml.sh`
预期：FAIL（当前非 tag 是 `0.0.0-dev`、output 无 `-v`）。

- [ ] **步骤 3：写最小实现**

`.github/workflows/build.yml` 三处 release_metadata（约 L110-114、L163-167、L236-240）的 `else` 分支：
```yaml
          else
            VERSION="0.0.0-dev.$(git rev-parse --short HEAD)"
```
（原 `VERSION="0.0.0-dev"`）

build-windows-arm64 的 cgo-actions（约 L182）：
```yaml
          output: ${{ env.PLUGIN_NAME }}-v${{ env.VERSION }}.dll
```
其 Package 步骤的 `-library`（约 L191）：
```yaml
            -library "dist/windows_arm64/${PLUGIN_NAME}-v${VERSION}.dll" \
```

build-freebsd 的 cgo-actions（约 L255）：
```yaml
          output: ${{ env.PLUGIN_NAME }}-v${{ env.VERSION }}.so
```
其 `-library`（约 L264）：
```yaml
            -library "dist/freebsd_amd64/${PLUGIN_NAME}-v${VERSION}.so" \
```

注意：release_metadata 步骤已 `echo "VERSION=${VERSION}" >> "${GITHUB_ENV}"`（L115/168/241），故 cgo-actions 用 `${{ env.VERSION }}`、shell 步骤用 `${VERSION}`。matrix build（L119-120）走 `make package VERSION=...`，由任务 2/3 的 Makefile 改动覆盖，本任务不改。

- [ ] **步骤 4：运行测试确认通过**

运行：`bash scripts/test-ci-yaml.sh` → `ci yaml checks passed`。
如有 `yq` 可补：`yq e '.jobs."build-windows-arm64".steps[] | select(.uses != null) | .with.output' "$f"` 人工核对。

- [ ] **步骤 5：提交**

```bash
git add .github/workflows/build.yml scripts/test-ci-yaml.sh
git commit -m "feat(T6): CI 全平台版本（dev SHA）与文件名（-v<version>）同步"
```

---

## 验收

### 任务 7：验收（acceptance-qa）

> 本任务由 executing-plans 收尾审查阶段触发 acceptance-qa 按下表执行，不参与逐任务连续执行；报告与证据落盘特性目录 `acceptance/` 子目录。

| Scenario / 检查项 | 维度 | 执行方式 | 目标 | 阈值/预期 | 验收证据 |
|-------------------|------|---------|------|----------|---------|
| CPA 正确解析发版产物身份 | e2e（跨仓库） | 部署 `model-mapper-plus-v1.2.3.so` 到 CPA plugins 目录、重启、看加载 | ID/Version 正确、插件加载 | 插件出现在 `/v0/management/plugins` 且 version=1.2.3 | 截图/响应 JSON |
| 无 git 降级 0.0.0-dev.unknown | unit | 在无 git 环境跑 `make print-version` | 降级不报错 | 输出 `0.0.0-dev.unknown` | 终端输出 |
| HEAD 多 tag 取第一个 | unit | `git tag v1a v1b HEAD; make print-version` | 取一个不串行 | 单个版本字符串（无空格） | 终端输出 |
| tag 无 v 前缀保持原样 | unit | `git tag 7.7.7 HEAD; make print-version; git tag -d 7.7.7` | patsubst 不误处理 | `7.7.7` | 终端输出 |
| 已跟踪文件改动标记 dirty | unit | `echo x >> README.md; make print-version; git checkout README.md` | git status --porcelain 捕获 | `0.0.0-dev.<sha>.dirty` | 终端输出 |
| dev-so 部署后管理页显示版本 | 集成 | `make dev-so` + 重载 CPA + 打开管理页 | footer 显示 `v0.0.0-dev.<sha>` | 版本段可见 | 截图 |
| 发版产物 zip 含版本化 .so | build | `make package VERSION=1.2.3` + 解压 zip | zip 内 .so 名带版本 | `model-mapper-plus-v1.2.3.so` 在 zip 根 | zip listing |

---

### 任务 8：合并与清理

- [ ] **步骤 1：全量验证**

在 worktree 内运行：`make test`、`go test ./...`、`npm --prefix web test`、`bash scripts/test-version.sh`、`bash scripts/test-ci-yaml.sh`，确认全绿。失败 → 修复后才进入合并。

- [ ] **步骤 2：合并回来源分支**

```bash
cd "$(dirname "$(git rev-parse --git-common-dir)")"   # 回到主工作区
git merge plan/2026-07-27-plugin-version
```

合并冲突、或主工作区有未提交改动 → 停下向计划作者确认，不强行合并。

- [ ] **步骤 3：清理**

```bash
git worktree remove .worktrees/plan-2026-07-27-plugin-version
git branch -d plan/2026-07-27-plugin-version
```

- [ ] **步骤 4：sync_commit 锚定**

```bash
SYNC=$(git rev-parse HEAD)   # 合并完成后的主工作区 HEAD
# 把 spec frontmatter 的 sync_commit 更新为 $SYNC
git add .spec-dev/2026-07-27-plugin-version/spec/plugin-version-design.md
git commit -m "chore(spec): sync_commit 锚定 ${SYNC:0:7}"
```
