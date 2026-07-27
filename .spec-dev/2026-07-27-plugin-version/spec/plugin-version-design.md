---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: plugin-version
  status: active
  sync_commit: c830aa458fe206464d1ba6d3643f70ab9c0ffeb4
  covers:
    - "Makefile"
    - "main.go"
    - "management.go"
    - "management_test.go"
    - "management_api_test.go"
    - ".github/scripts/package-release.go"
    - ".github/workflows/build.yml"
    - "web/src/api.ts"
    - "web/src/App.tsx"
---

# 插件版本号机制 设计

## 背景与目标

model-mapper-plus 的编译产物（.so/.dll/.dylib）当前默认版本号是 `0.0.0-dev`：开发热重载构建（`make dev-so`）刻意清空 `VERSION_LDFLAGS`、CI 非 tag 推送显式赋 `0.0.0-dev`、本地未传 `VERSION` 也是默认值——三者混作一团，无法辨别一个已加载的 .so 是何时、从哪个提交构建的。这曾导致一次事故：CPA 加载了一个过期（含已知 bug）的 .so，用户在管理页操作"规则映射保存后不显示"，却无从得知是旧产物所致（`state.json` 全空、updated_at 停留在旧时间）。

本特性建立完整的版本号机制，让任何编译产物的版本号都**可辨识**（dev 用 git SHA、发版用 tag）且**可查**（插件自己的管理页显示版本），并让产物文件名遵循 CPA 规范携带版本号。

**成功标准**：每个 .so/.dll/.dylib 内嵌的 `pluginVersion` 能区分发版（tag）与开发构建（SHA）；CPA 管理页可见当前加载的版本号；发版产物文件名遵循 CPA 规范、可辨识。**非功能性约束**：所有构建路径（dev-so / build / install / CI）行为一致。

## 非目标

- 前后端版本不一致的主动校验/告警（YAGNI；运行时不比对）
- 多变量追溯（commit 完整 SHA / buildTime）——单变量 `pluginVersion`（含 shortSHA）已能定位提交，后续按需增量加
- **前端 bundle 内嵌版本号**——go:embed 下 index.html 与 .so 同体分发，前端版本从 `/state` 的 `plugin_version` 获取（无独立消费方），不重复注入 bundle（去掉 vite define / VITE_PLUGIN_VERSION）
- 版本号命中统计、过期自动提醒等运营功能
- 改变 CPA 主程序的 `Metadata.Version` 消费链路（CPA 已强制 Version 非空并在 `GET /v0/management/plugins` 暴露）

## 术语表

- **pluginVersion**：插件版本号字符串，由 Go 包级变量 `main.pluginVersion`（main.go）持有，构建期经 `-ldflags -X` 注入，注册时上报 `pluginapi.Metadata.Version`
- **发版构建**：HEAD 精确指向一个 git tag 的构建（发版产物）
- **dev 构建**：HEAD 无精确 tag 的构建（开发/CI 主干）
- **CPA 文件名规范**：宿主通过 `<id>-v<version><ext>` 解析插件文件名的 ID 与 Version（CPA platform.go:84，分隔符为字面量 `-v`；且 Version 不能以 `v` 开头，否则被 CPA platform.go:43 判非法）
- **注意区分**：`/state` 响应里 `version`（int）= state schema 版本（stateVersion=1）；`plugin_version`（string）= 插件版本号。两者无关

## 影响面

`Makefile`（版本计算单一来源 + dev-so 不再清空 + 产物文件名 + dev-so 部署清理 release 残留）；`main.go`（pluginVersion 默认值改为不可辨识标记）；`management.go`（/state 响应增 `plugin_version`）；`.github/scripts/package-release.go`（`binaryPath` 接收 version 构造版本化文件名）；`.github/workflows/build.yml`（release_metadata 非 tag 计算 SHA + cgo-actions output 文件名版本化）；前端 `web/src/api.ts`、`web/src/App.tsx`（版本展示）。

## 已确认的关键决策

- **版本来源单一化**：`PLUGIN_VERSION` 由 Makefile 一次性计算，优先级 `VERSION` 环境变量强制覆盖 > HEAD 精确 tag > dev SHA；VERSION 与 tag **都去 `v` 前缀**（CPA 拒绝以 v 开头的 Version）。同一来源供 `go build -ldflags`，保证 .so 内嵌版本唯一
- **dev 版本格式 `0.0.0-dev.<shortSHA>[.dirty]`**：简洁、人眼可辨识、SemVer 排序低于正式版。舍弃 git describe 原样（不合 SemVer BNF）与严格 git-semver（转换复杂）。dirty 判定用 `git status --porcelain`（含 untracked 文件，覆盖开发态新增文件场景）
- **发版产物文件名 `model-mapper-plus-v<version>.so/.dll`**：CPA 强制 `-v` 分隔，否则文件名被整体当作插件 ID（与配置 key `model-mapper-plus` 错配，插件被默认禁用不加载）。此规则应用于 **dist 中间产物**（`build`/`build-platform`/`package*`）
- **dev-so 与 install 部署用固定文件名**：dev-so 部署为 `model-mapper-plus.so`（cp 覆盖，热重载友好）。关键理由：CPA `pluginFilePreferred`（platform.go:204-216）使有 Version 的文件胜过 Version="" 的文件，且 `cleanupUnselectedPluginFiles`（host.go:367-371）会**删除**未被选中的同 ID 文件——若 dev-so（Version=""）与 release（有 Version）共存于同一 plugins 子目录，dev-so 会被 CPA 自动删除。因此 dev-so 部署前 SHALL 先清理该 ID 的 release 残留（`rm -f <name>-v*.so`），反之亦然
- **plugin_version 放 /state 响应层（stateResponse），不进 state_file 持久化**：插件版本是 .so 二进制的构建期属性，持久化会导致旧 state_file 在被不同版本 .so 加载时呈现错误版本
- **单变量注入**：仅 `pluginVersion`；前端从 `/state` 的 `plugin_version` 取得版本（非 bundle 内嵌）
- **main.go 默认值**：从 `0.0.0-dev` 改为 `0.0.0-dev.unbuilt`——若有人绕过 Makefile 直接 `go build`，得到一个明确"不可辨识/未走构建流程"的标记，而非看似正常的 `0.0.0-dev`

## ADDED Requirements

### Requirement: 版本号来源与计算

所有构建路径 SHALL 经 Makefile 单一计算 `PLUGIN_VERSION`：显式 `VERSION` 环境变量优先（去 `v` 前缀）；否则 HEAD 精确指向 git tag 时取 tag（去 `v` 前缀，多 tag 取第一个）；否则 `0.0.0-dev.<shortSHA>`（工作区含 untracked 或 tracked 改动追加 `.dirty`，无 git 降级 `0.0.0-dev.unknown`）。`pluginVersion` SHALL 始终非空（CPA 强制非空否则拒绝加载）。

#### Scenario: HEAD tag 去 v 前缀

- **GIVEN** HEAD 精确指向 tag `v1.2.3`，未传 `VERSION`
- **WHEN** 执行 `make build`
- **THEN** `pluginVersion` 被注入为 `1.2.3`

#### Scenario: tag 无 v 前缀保持原样

- **GIVEN** HEAD 精确指向 tag `1.2.3`（无 `v`），未传 `VERSION`
- **WHEN** 执行 `make build`
- **THEN** `pluginVersion` 为 `1.2.3`

#### Scenario: dev 构建用 SHA

- **GIVEN** HEAD 无精确 tag、工作区干净、`git rev-parse --short HEAD` 输出 `<shortSHA>`，未传 `VERSION`
- **WHEN** 执行 `make dev-so`
- **THEN** `pluginVersion` 为 `0.0.0-dev.<shortSHA>`（长度由 git 决定，7-12 位）

#### Scenario: 已跟踪文件改动标记 dirty

- **GIVEN** HEAD 无 tag、某已跟踪文件有未提交改动
- **WHEN** 执行任意构建目标
- **THEN** `pluginVersion` 形如 `0.0.0-dev.<shortSHA>.dirty`

#### Scenario: untracked 新文件标记 dirty

- **GIVEN** HEAD 无 tag、工作区存在未 `git add` 的新文件
- **WHEN** 执行任意构建目标
- **THEN** `pluginVersion` 形如 `0.0.0-dev.<shortSHA>.dirty`

#### Scenario: 无 git 环境降级

- **GIVEN** 构建环境无 git，未传 `VERSION`
- **WHEN** 执行任意构建目标
- **THEN** `pluginVersion` 为 `0.0.0-dev.unknown`，构建不失败

#### Scenario: VERSION 环境变量优先于 tag

- **GIVEN** HEAD 精确指向 tag `v1.2.3`
- **WHEN** 执行 `make build VERSION=9.9.9`
- **THEN** `pluginVersion` 为 `9.9.9`（VERSION 覆盖 tag）

#### Scenario: VERSION 带 v 前缀被剥离

- **GIVEN** 执行 `make build VERSION=v1.2.4`
- **WHEN** 构建完成
- **THEN** `pluginVersion` 为 `1.2.4`（去 v 前缀，避免被 CPA 判非法）

### Requirement: 发版产物文件名遵循 CPA 规范

`build` / `build-platform` / `build-windows-amd64` / `build-linux-amd64*` / `package*` 产出的 dist 中间产物文件名 SHALL 为 `<PLUGIN_NAME>-v<PLUGIN_VERSION><ext>`。release zip 内的动态库文件名 SHALL 同步携带 `-v<version>`。

#### Scenario: 发版构建产物文件名

- **GIVEN** `VERSION=1.2.3`、`make build`
- **WHEN** 查看产物
- **THEN** 文件名为 `model-mapper-plus-v1.2.3.so`（linux）/ `.dll`（windows）/ `.dylib`（darwin）

#### Scenario: make package 扫描模式找到版本化产物

- **GIVEN** `VERSION=1.2.3`、已 `make build`
- **WHEN** 执行 `make package VERSION=1.2.3`
- **THEN** package-release.go 按 `model-mapper-plus-v1.2.3.<ext>` 路径找到产物并打入 zip（zip 内动态库同名）

#### Scenario（依赖前提，跨仓库 e2e 验收）: CPA 正确解析发版产物身份

- **GIVEN** 发版产物 `model-mapper-plus-v1.2.3.so` 部署到 CPA `plugins/linux/amd64/`
- **WHEN** CPA 扫描并解析文件名（platform.go:84 按 `-v` 切分）
- **THEN** 插件 ID = `model-mapper-plus`、Version = `1.2.3`，插件被正常加载（此为 CPA 行为，本 spec 以依赖前提记录，验收走跨仓库 e2e）

### Requirement: dev-so 注入可辨识版本号且部署固定名

`make dev-so` SHALL 不再清空 `VERSION_LDFLAGS`，注入计算所得的 dev 版本号（`0.0.0-dev.<sha>`）；部署到 CPA 的文件名 SHALL 保持固定 `model-mapper-plus.so`（cp 覆盖）。`install-local`/`install-linux-amd64` 部署到 `CPA_PLUGINS_DIR` 的文件名 SHALL 同为固定名覆写（dist 中间产物仍为版本化名）。

#### Scenario: dev-so 不再是 0.0.0-dev

- **GIVEN** HEAD 无 tag、`<shortSHA>`
- **WHEN** 执行 `make dev-so`
- **THEN** 生成的 .so 内嵌 `pluginVersion = 0.0.0-dev.<shortSHA>`（非 `0.0.0-dev`），部署文件名为 `model-mapper-plus.so`

#### Scenario: dev-so 部署前清理 release 残留

- **GIVEN** `CPA_PLUGINS_DIR/linux/amd64/` 存在旧的 `model-mapper-plus-v1.2.3.so`
- **WHEN** 执行 `make dev-so`
- **THEN** 部署脚本先 `rm -f model-mapper-plus-v*.so` 再 cp 固定名 .so（避免 CPA cleanup 误删或选错）

### Requirement: /state 响应暴露 plugin_version

`GET /state` 响应 SHALL 新增 `plugin_version` 字段（放 `stateResponse` 响应层，不进 `State` 持久化结构、不写 state_file），值为当前 `pluginVersion`。

#### Scenario: /state 返回插件版本且不持久化

- **GIVEN** 插件已加载、`pluginVersion = 0.0.0-dev.<shortSHA>`
- **WHEN** `GET /v0/management/plugins/model-mapper-plus/state`
- **THEN** 响应 JSON 含 `"plugin_version": "0.0.0-dev.<shortSHA>"`，且 state_file 内容不含该字段

### Requirement: 前端管理页展示版本号

前端管理页 SHALL 在顶部状态栏展示从 `/state` 获取的当前版本号，格式为字面量 `v` + `plugin_version` 字符串（`plugin_version` 本身不含 v 前缀）。旧版 .so 无该字段时 SHALL 不显示版本段且不报错。

#### Scenario: 前端展示版本号

- **GIVEN** 管理页已登录、`/state` 返回 `plugin_version = "0.0.0-dev.abc1234"`
- **WHEN** 顶部状态栏渲染
- **THEN** 显示 `v0.0.0-dev.abc1234`

#### Scenario: 旧 .so 无该字段时前端容错

- **GIVEN** 加载的旧版 .so 的 /state 响应不含 `plugin_version`
- **WHEN** 前端渲染状态栏
- **THEN** 不显示版本段，不报错

### Requirement: CI 全平台版本与文件名同步

CI（`.github/workflows/build.yml`）SHALL 在 tag 推送时用 tag、非 tag 时用 `0.0.0-dev.<shortSHA>`（与 Makefile 同算法 `git rev-parse --short HEAD`）作为版本；经 cgo-actions 构建的 windows-arm64/freebsd 平台 SHALL 同时注入该版本到 ldflags **并** 输出文件名为 `<name>-v<version><ext>`。

#### Scenario: CI 非 tag 用 dev SHA

- **GIVEN** push 到 main（非 tag）、`<shortSHA>`
- **WHEN** CI 构建产物
- **THEN** 各平台产物的 `pluginVersion` 均为 `0.0.0-dev.<shortSHA>`

#### Scenario: CI windows-arm64 产物文件名版本化

- **GIVEN** 非 tag push、`<shortSHA>`
- **WHEN** cgo-actions 构建 windows-arm64
- **THEN** 产物文件名为 `model-mapper-plus-v0.0.0-dev.<shortSHA>.dll`（非无版本号）

## 方案设计

### 架构与组件

| 单元 | 改动 |
|------|------|
| `Makefile` | 新增 `PLUGIN_VERSION` 计算块（VERSION/tag 去 v、git status 判 dirty、多 tag 取首）；`dev-so` 不清空改用 `VERSION_LDFLAGS` + 部署前清理 release 残留；产物输出名带 `-v<version>` |
| `main.go` | `pluginVersion` 默认值 `0.0.0-dev` → `0.0.0-dev.unbuilt` |
| `management.go` | `stateResponse` 加 `PluginVersion string`；`managementGetState` 填 `pluginVersion` |
| `.github/scripts/package-release.go` | `binaryPath` 接收 version，返回 `<name>-v<version><ext>`（`resolveVersion` 已去 v 前缀，无需改） |
| `.github/workflows/build.yml` | release_metadata 非 tag 计算 `0.0.0-dev.<shortSHA>`；cgo-actions `output:` 改 `<name>-v<version><ext>` |
| `web/src/api.ts` | `StateResponse` 加 `plugin_version?: string` |
| `web/src/App.tsx` | footer 拼 `· v{plugin_version}` |

### 数据流

```
make 计算 PLUGIN_VERSION（VERSION > tag > SHA，均去 v）
  └─ go build -ldflags "-X main.pluginVersion=$(PLUGIN_VERSION)"  → <name>-v<version>.so/.dll（发版）/ model-mapper-plus.so（dev-so 固定名）

运行时：
  GET /state → managementGetState 读 main.pluginVersion → stateResponse.plugin_version
  前端 App.tsx → getState() → footer 显示 v{plugin_version}
```

### 关键接口/数据结构

**Makefile 版本计算（核心）**：
```makefile
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
```

产物输出名（build-platform-go）：
```makefile
out := "$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)-v$(PLUGIN_VERSION)$$ext"
```
dev-so 部署（清理 release 残留 + 固定名覆写）：
```makefile
dev-so: web-build
	@$(MAKE) --no-print-directory build-platform-go ... VERSION_LDFLAGS="$(VERSION_LDFLAGS)" ...
	@rm -f "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME)-v"*.so
	cp $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).so "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME).so"
```

**package-release.go binaryPath（接收 version）**：
```go
func (a artifactSpec) binaryPath(distDir, version string) string {
    return filepath.Join(distDir, a.osName+"_"+a.arch,
        fmt.Sprintf("%s-v%s%s", pluginName, version, libraryExtension(a.osName)))
}
```

**management.go stateResponse（新增字段）**：
```go
type stateResponse struct {
    Version       int          `json:"version"`         // state schema 版本（不变）
    Rules         RuleSet      `json:"rules"`
    KeyBindings   []KeyBinding `json:"key_bindings"`
    UpdatedAt     string       `json:"updated_at,omitempty"`
    Persisted     bool         `json:"persisted"`
    StateFile     string       `json:"state_file"`
    PluginVersion string       `json:"plugin_version"`  // 新增：仅响应层，不持久化
}
// managementGetState: PluginVersion: pluginVersion
```

**CI release_metadata（build.yml，非 tag 计算 SHA）**：
```yaml
- id: release_metadata
  run: |
    if [[ "$GITHUB_REF" == refs/tags/v* ]]; then VERSION="${GITHUB_REF_NAME#v}"
    else VERSION="0.0.0-dev.$(git rev-parse --short HEAD)"; fi
    echo "version=$VERSION" >> "$GITHUB_OUTPUT"
```
cgo-actions output：`output: ${{ env.PLUGIN_NAME }}-v${{ steps.release_metadata.outputs.version }}.dll`（freebsd 同理 `.so`）

**前端**：`api.ts` 加 `plugin_version?: string`；`App.tsx` footer `{state.plugin_version ? ` · v${state.plugin_version}` : ''}`

### 错误处理

- 无 git：降级 `0.0.0-dev.unknown`，构建不失败
- dirty（含 untracked）：追加 `.dirty`
- tag 恰为 `v`（异常用法）：`patsubst` 退化为空串 → CPA 判 Version 非法（边缘，spec 不特判，由发版规范避免）
- dev-ui（vite dev server，不经 Makefile）：/state 仍返回 .so 的 pluginVersion（来自后端），footer 显示正常；前端 bundle 不注入版本（非目标）
- 旧 .so 无 `plugin_version` 字段：前端可选字段 + 判空，不显示
- 所有路径产出均非空（含 `dev.unknown`/`dev.unbuilt`），满足 CPA `validPlugin` 强制非空

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| VERSION 覆盖 / VERSION 带 v / HEAD tag / tag 无 v / 多 tag / dev SHA / 无 git / dirty(tracked) / dirty(untracked) | unit | Makefile 逻辑 shell 断言 `$(PLUGIN_VERSION)` | 各情形值正确 |
| /state 返回 plugin_version 且不持久化 | unit | `management_test.go` 增用例 | 响应含字段、state_file 不含 |
| 前端 footer 渲染版本 + 旧 .so 容错 | unit | vitest（App.tsx，复用 ResizeObserver polyfill） | 显示/不显示两种情形 |
| 发版产物文件名 / make package 打包 | build | `make build VERSION=1.2.3` + `make package` | `model-mapper-plus-v1.2.3.so` 入 zip |
| dev-so 注入 dev SHA + 固定名 + 清理残留 | build | `make dev-so` 后 `strings`/读 /state + 检查部署名 | 含 `0.0.0-dev.<sha>`、部署名固定、旧 release 已清 |
| CI 全平台版本与文件名一致 | CI | 非 tag push 后检查各平台产物 | 均为 `0.0.0-dev.<sha>`、文件名版本化 |
| CPA 解析发版产物身份 | e2e（跨仓库） | 部署到 CPA 验证加载 | ID/Version 正确、插件加载 |

## 风险与边缘情况

- **Makefile 版本优先级**：VERSION > tag > SHA，须用九种情形测覆盖（含 VERSION+tag 共存、VERSION 带 v、多 tag、tag 无 v）
- **dev-so × release 共存**：CPA `cleanupUnselectedPluginFiles` 会删除未被选中的同 ID 文件（有 Version 的胜 Version="" 的）。dev-so 部署前必须清理 release 残留；release 部署前同理应清理 dev-so。两者不应共存于同一 plugins 子目录
- **x-flags 双线**：windows-arm64/freebsd 走 cgo-actions，与 Makefile 独立，build.yml 的 output 与 x-flags 须分别维护版本与文件名
- **shortSHA 长度**：`git rev-parse --short HEAD` 长度由 git 按仓库大小动态决定（7-12 位），CI 与本地可能不同长度——版本号用于人眼辨识，长度差异不影响正确性
- **`-v` 规范强制**：文件名分隔符必须 `-v`，否则 CPA 身份错配；Makefile、package-release.go、CI output 须一致使用 `-v`
- **多 tag**：`git describe --tags --exact-match` 多 tag 时返回多行，Makefile `| head -1` 取第一个；VERSION 强制覆盖可规避多 tag 歧义
