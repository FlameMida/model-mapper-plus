---
# —— spec-dev 漂移守卫锚点（机器可校验，勿删）——
spec_dev:
  version: 1
  feature: plugin-version
  status: draft
  covers:
    - "Makefile"
    - "management.go"
    - "management_test.go"
    - "management_api_test.go"
    - ".github/scripts/package-release.go"
    - ".github/workflows/build.yml"
    - "web/src/api.ts"
    - "web/src/App.tsx"
    - "web/src/vite-env.d.ts"
---

# 插件版本号机制 设计

## 背景与目标

model-mapper-plus 的编译产物（.so/.dll/.dylib）当前默认版本号是 `0.0.0-dev`：开发热重载构建（`make dev-so`）刻意清空 `VERSION_LDFLAGS`、CI 非 tag 推送显式赋 `0.0.0-dev`、本地未传 `VERSION` 也是默认值——三者混作一团，无法辨别一个已加载的 .so 是何时、从哪个提交构建的。这曾导致一次事故：CPA 加载了一个过期（含已知 bug）的 .so，用户在管理页操作"规则映射保存后不显示"，却无从得知是旧产物所致（`state.json` 全空、updated_at 停留在旧时间）。

本特性建立完整的版本号机制，让任何编译产物的版本号都**可辨识**（dev 用 git SHA、发版用 tag）且**可查**（插件自己的管理页显示版本），使"加载了过期产物"能被及时发现。

**成功标准**：每个 .so/.dll/.dylib 内嵌的 `pluginVersion` 能区分发版（tag）与开发构建（SHA）；CPA 管理页可见当前加载的版本号；产物文件名遵循 CPA 规范携带版本号；所有构建路径（dev-so / build / install / CI）行为一致。

## 非目标

- 前后端版本不一致的主动校验/告警（YAGNI；前端 bundle 与 .so 同源即可，不做运行时比对）
- 多变量追溯（commit 完整 SHA / buildTime）——单变量 `pluginVersion`（含 shortSHA）已能定位提交，后续按需增量加
- 版本号命中统计、过期自动提醒等运营功能
- 改变 CPA 主程序的 `Metadata.Version` 消费链路（CPA 已强制 Version 非空并在 `GET /v0/management/plugins` 暴露）

## 术语表

- **pluginVersion**：插件版本号字符串，由 Go 包级变量 `main.pluginVersion`（main.go:21）持有，构建期经 `-ldflags -X` 注入，注册时上报 `pluginapi.Metadata.Version`
- **发版构建**：HEAD 精确指向一个 git tag 的构建（发版产物）
- **dev 构建**：HEAD 无精确 tag 的构建（开发/CI 主干）
- **CPA 文件名规范**：宿主通过 `<id>-v<version><ext>` 解析插件文件名的 ID 与 Version（CPA platform.go:84，分隔符为字面量 `-v`）

## 影响面

`Makefile`（版本计算单一来源 + dev-so 不再清空 + 产物文件名）；`management.go`（/state 响应增 `plugin_version`）；`.github/scripts/package-release.go`（zip 内 .so 文件名）；`.github/workflows/build.yml`（非 tag 用 dev SHA + x-flags 平台同步）；前端 `web/src/api.ts`、`web/src/App.tsx`、`web/src/vite-env.d.ts`（版本展示与类型声明）。`main.go` 的 `pluginVersion` 变量与 `-X` 注入机制已就绪，无需改动。

## 已确认的关键决策

- **版本来源单一化**：`PLUGIN_VERSION` 由 Makefile 一次性计算，优先级 `VERSION` 环境变量强制覆盖 > HEAD 精确 tag（去 `v` 前缀）> dev SHA；同一来源同时供 `go build -ldflags` 与 `vite build`，保证前后端版本同源（go:embed 的 index.html 运行时拿不到 Go 版本，必须构建期注入）
- **dev 版本格式 `0.0.0-dev.<shortSha>[.dirty]`**：简洁、人眼可辨识、SemVer 排序永远低于正式版（不会冒充发版）。舍弃 git describe 原样（多个连字符不合 SemVer BNF）与严格 git-semver（解析转换复杂度过高）
- **产物文件名 `model-mapper-plus-v<version>.so/.dll`**：CPA 强制要求 `-v` 分隔，否则文件名被整体当作插件 ID（与配置 key `model-mapper-plus` 错配，插件被默认禁用不加载）
- **dev-so 用固定文件名 `model-mapper-plus.so`**：开发热重载 cp 覆盖即可；若也带版本号会导致旧文件堆积、且 CPA 按 Version 字符串比较"最高版本"对 sha 无意义（可能选到旧 dev 构建）。dev-so 的 .so 内部仍内嵌 `pluginVersion`，靠 UI/日志辨识版本
- **plugin_version 放 /state 响应层（stateResponse），不进 state_file 持久化**：插件版本是 .so 二进制的构建期属性，与磁盘上 rules/key_bindings 持久化数据是两类事物；持久化会导致旧 state_file 在被不同版本 .so 加载时呈现错误版本
- **单变量注入**：仅 `pluginVersion`；前端 bundle 经 `VITE_PLUGIN_VERSION` 环境变量 + `import.meta.env` 取得同源版本

## ADDED Requirements

### Requirement: 版本号来源与计算

所有构建路径 SHALL 经 Makefile 单一计算 `PLUGIN_VERSION` 并注入 `main.pluginVersion`：显式 `VERSION` 环境变量优先；否则 HEAD 精确指向 git tag 时取 tag（去 `v` 前缀）；否则 `0.0.0-dev.<shortSHA>`（工作区 dirty 追加 `.dirty`，无 git 降级 `0.0.0-dev.unknown`）。`pluginVersion` SHALL 始终非空字符串（CPA 强制 Version 非空，否则拒绝加载插件）。

#### Scenario: 发版构建用 HEAD tag

- **GIVEN** HEAD 精确指向 tag `v1.2.3`，未传 `VERSION`
- **WHEN** 执行 `make build`
- **THEN** `pluginVersion` 被注入为 `1.2.3`，产物文件名为 `model-mapper-plus-v1.2.3.so`

#### Scenario: dev 构建用 SHA

- **GIVEN** HEAD 无精确 tag、工作区干净、HEAD short SHA 为 `abc1234`，未传 `VERSION`
- **WHEN** 执行 `make dev-so`
- **THEN** `pluginVersion` 被注入为 `0.0.0-dev.abc1234`

#### Scenario: dirty 工作区标记

- **GIVEN** HEAD 无精确 tag、工作区有未提交改动、short SHA 为 `abc1234`
- **WHEN** 执行任意构建目标
- **THEN** `pluginVersion` 为 `0.0.0-dev.abc1234.dirty`

#### Scenario: 无 git 环境降级

- **GIVEN** 构建环境无 git（如浅克隆裸环境），未传 `VERSION`
- **WHEN** 执行任意构建目标
- **THEN** `pluginVersion` 为 `0.0.0-dev.unknown`，构建不失败

#### Scenario: VERSION 环境变量强制覆盖

- **GIVEN** HEAD 无 tag
- **WHEN** 执行 `make build VERSION=9.9.9-rc1`
- **THEN** `pluginVersion` 为 `9.9.9-rc1`（VERSION 覆盖 tag 与 SHA 逻辑）

### Requirement: 发版产物文件名遵循 CPA 规范

发版构建产物（`build` / `build-platform` / `build-windows-amd64` / `build-linux-amd64*` / `install-*` / `package*`）的文件名 SHALL 为 `<PLUGIN_NAME>-v<PLUGIN_VERSION><ext>`，使 CPA 能正确解析插件 ID 为 `model-mapper-plus`、Version 为 `PLUGIN_VERSION`。release zip 内的动态库文件名 SHALL 同步携带 `-v<version>`。

#### Scenario: CPA 正确解析发版产物身份

- **GIVEN** 发版产物 `model-mapper-plus-v1.2.3.so` 部署到 CPA `plugins/linux/amd64/`
- **WHEN** CPA 扫描并解析文件名
- **THEN** 插件 ID = `model-mapper-plus`（与配置 key 匹配）、Version = `1.2.3`，插件被正常加载

#### Scenario: dev-so 保持固定文件名

- **GIVEN** 执行 `make dev-so`
- **WHEN** 交叉编译并部署
- **THEN** 部署到 `$(CPA_PLUGINS_DIR)/linux/amd64/` 的文件名为固定的 `model-mapper-plus.so`（cp 覆盖），但 .so 内部 `pluginVersion` 已注入 dev SHA

### Requirement: dev-so 注入可辨识版本号

`make dev-so` SHALL 不再清空 `VERSION_LDFLAGS`，而是注入计算所得的 dev 版本号（`0.0.0-dev.<sha>`），使开发热重载的 .so 同样可辨识。

#### Scenario: dev-so 不再是 0.0.0-dev

- **GIVEN** HEAD 无 tag、short SHA `abc1234`
- **WHEN** 执行 `make dev-so`
- **THEN** 生成的 `model-mapper-plus.so` 内嵌 `pluginVersion = 0.0.0-dev.abc1234`（非 `0.0.0-dev`）

### Requirement: 版本号在管理页可见

`GET /state` 响应 SHALL 新增 `plugin_version` 字段（放 `stateResponse` 响应层，不进 `State` 持久化结构、不写 state_file），值为当前 `pluginVersion`。前端管理页 SHALL 在顶部状态栏展示当前版本号。

#### Scenario: /state 返回插件版本

- **GIVEN** 插件已加载、`pluginVersion = 0.0.0-dev.abc1234`
- **WHEN** `GET /v0/management/plugins/model-mapper-plus/state`
- **THEN** 响应 JSON 含 `"plugin_version": "0.0.0-dev.abc1234"`，且 state_file 内容不含该字段

#### Scenario: 前端展示版本号

- **GIVEN** 管理页已登录并加载 state
- **WHEN** 顶部状态栏渲染
- **THEN** 展示 `v0.0.0-dev.abc1234`（或发版本号）

#### Scenario: 旧 .so 无该字段时前端容错

- **GIVEN** 加载的旧版 .so 的 /state 响应不含 `plugin_version`
- **WHEN** 前端渲染状态栏
- **THEN** 不显示版本段，不报错

### Requirement: 前端 bundle 版本同源

前端构建 SHALL 经 `VITE_PLUGIN_VERSION` 环境变量（Makefile 单一来源传入）在构建期把版本号静态注入 `import.meta.env.VITE_PLUGIN_VERSION`，与 .so 内嵌的 `pluginVersion` 同源。

#### Scenario: web-build 传递版本

- **GIVEN** `PLUGIN_VERSION = 0.0.0-dev.abc1234`
- **WHEN** `make web-build` 执行 `vite build`
- **THEN** 前端 bundle 内 `import.meta.env.VITE_PLUGIN_VERSION` 被静态替换为 `0.0.0-dev.abc1234`

### Requirement: CI 全平台版本同步

CI（`.github/workflows/build.yml`）SHALL 在 tag 推送时用 tag 作为版本、非 tag 时用 `0.0.0-dev.<shortSHA>`，且经 cgo-actions `x-flags` 构建的 windows-arm64/freebsd 平台 SHALL 注入与 Makefile 路径一致的版本号。

#### Scenario: CI 非 tag 用 dev SHA

- **GIVEN** push 到 main（非 tag）、short SHA `abc1234`
- **WHEN** CI 构建产物
- **THEN** 各平台产物的 `pluginVersion` 均为 `0.0.0-dev.abc1234`（非 `0.0.0-dev`）

## 方案设计

### 架构与组件

| 单元 | 职责 | 改动 |
|------|------|------|
| `Makefile` | 版本号单一来源计算；统一注入 go ldflags + vite env；产物文件名；dev-so 不清空 | 新增 `PLUGIN_VERSION` 计算块；`dev-so` 改用 `VERSION_LDFLAGS="$(VERSION_LDFLAGS)"`；`web-build` 传 `VITE_PLUGIN_VERSION`；产物输出名带 `-v<version>` |
| `main.go` | 持有 `pluginVersion`、注册上报 | 无改动（已就绪） |
| `management.go` | /state 响应暴露版本 | `stateResponse` 加 `PluginVersion string` 字段；`managementGetState` 填 `pluginVersion` |
| `.github/scripts/package-release.go` | release zip 内动态库文件名 | 打包时使用 `<name>-v<version><ext>` |
| `.github/workflows/build.yml` | CI 版本计算 + x-flags 同步 | release_metadata 非 tag 输出 `0.0.0-dev.<sha>`；x-flags 平台沿用该 VERSION |
| `web/src/vite-env.d.ts` | TS 类型声明 | 声明 `ImportMetaEnv.VITE_PLUGIN_VERSION` |
| `web/src/api.ts` | StateResponse 类型 | 加 `plugin_version?: string` |
| `web/src/App.tsx` | 版本展示 | 顶部状态栏 footer 拼接 `· v{plugin_version}` |

### 数据流

```
make 计算 PLUGIN_VERSION（VERSION > HEAD tag > dev SHA）
  ├─ go build -ldflags "-X main.pluginVersion=$(PLUGIN_VERSION)"  → .so/.dll
  └─ vite build（VITE_PLUGIN_VERSION env → import.meta.env）       → 前端 bundle → go:embed 进 .so

运行时：
  GET /state → managementGetState 读 main.pluginVersion → stateResponse.plugin_version
  前端 App.tsx → getState() → footer 显示 v{plugin_version}
  前端 bundle 自身 → import.meta.env.VITE_PLUGIN_VERSION（与后端同源）
```

### 关键接口/数据结构

**Makefile 版本计算（核心）**：
```makefile
GIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(shell git diff --quiet HEAD 2>/dev/null || (git diff --quiet --cached HEAD 2>/dev/null || echo ".dirty"))
HEAD_TAG  := $(shell git describe --tags --exact-match HEAD 2>/dev/null)

ifneq ($(VERSION),)
  PLUGIN_VERSION := $(VERSION)
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
dev-so 部署仍 `cp ... model-mapper-plus.so`（固定名）。

**management.go stateResponse（新增字段）**：
```go
type stateResponse struct {
    Version       int          `json:"version"`
    Rules         RuleSet      `json:"rules"`
    KeyBindings   []KeyBinding `json:"key_bindings"`
    UpdatedAt     string       `json:"updated_at,omitempty"`
    Persisted     bool         `json:"persisted"`
    StateFile     string       `json:"state_file"`
    PluginVersion string       `json:"plugin_version"` // 新增：仅响应层，不持久化
}
// managementGetState: PluginVersion: pluginVersion
```

**前端**：`api.ts` 加 `plugin_version?: string`；`App.tsx` footer `{state.plugin_version ? ` · v${state.plugin_version}` : ''}`；`vite-env.d.ts` 声明 `VITE_PLUGIN_VERSION?: string`。

### 错误处理

- 无 git：降级 `0.0.0-dev.unknown`，构建不失败
- dirty：追加 `.dirty`
- dev-ui（vite dev server，不经 Makefile）：`import.meta.env.VITE_PLUGIN_VERSION` 为 undefined，footer 不显示版本——开发态可接受（用户已选不做不一致校验）
- 旧 .so 无 `plugin_version` 字段：前端可选字段 + 判空，不显示即可
- 所有路径产出均非空（含 `dev.unknown`），满足 CPA `validPlugin` 强制非空约束

## 测试与验收策略

| Scenario / 检查项 | 维度 | 执行方式 | 验收证据 |
|-------------------|------|---------|---------|
| VERSION 覆盖 / HEAD tag / dev SHA / 无 git / dirty | unit | Makefile 逻辑测试（`make -n` 或 shell 断言 `$(PLUGIN_VERSION)`） | 五种情形值正确 |
| /state 返回 plugin_version 且不持久化 | unit | `management_test.go` 增用例 | 响应含字段、state_file 不含 |
| 前端 footer 渲染版本 | unit | vitest（App.tsx，复用 ResizeObserver polyfill） | 显示 `v<version>` |
| 发版 .so 文件名符合 CPA 规范 | build | `make build VERSION=1.2.3` 后检查产物名 | `model-mapper-plus-v1.2.3.so` |
| dev-so 注入 dev SHA（非 0.0.0-dev） | build | `make dev-so` 后 `strings .so \| grep` 或读 /state | 含 `0.0.0-dev.<sha>` |
| CI 全平台版本一致 | CI | 非 tag push 后检查各平台产物 | 均为 `0.0.0-dev.<sha>` |

## 风险与边缘情况

- **Makefile 版本优先级**：`VERSION` > tag > SHA 的优先级须用五种情形测覆盖，确保发版 tag 不被 SHA 覆盖、VERSION 强制覆盖生效
- **x-flags 双线**：windows-arm64/freebsd 走 cgo-actions，与 Makefile 独立，build.yml 须分别维护版本注入（已纳入设计）
- **CPA 多版本文件共存**：发版产物带版本号后，同 ID 多版本文件 CPA 按 Version 选最高；dev-so 用固定名规避此问题。release 部署若残留旧版本文件，用户需手动清理或依赖 CPA cleanup
- **dev-ui 版本缺失**：vite dev server 不经 Makefile，版本为 undefined，footer 不显示——文档说明依赖 .so 内嵌页才准
- **`-v` 规范强制**：文件名分隔符必须是 `-v`（非 `-`），否则 CPA 身份错配；构建脚本与 package-release.go 必须一致使用 `-v`
