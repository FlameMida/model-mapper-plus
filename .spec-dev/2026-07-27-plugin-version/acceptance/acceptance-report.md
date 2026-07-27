# 插件版本号机制 验收报告

**特性**：plugin-version
**Spec**：`.spec-dev/2026-07-27-plugin-version/spec/plugin-version-design.md`
**日期**：2026-07-27
**验证范围**：T1-T6 实现 + 收尾审查修复（commit `b738d78`..`c8456c0`，分支 `plan/2026-07-27-plugin-version`）

## Requirement Reconciliation

| Requirement | 状态 | 证据 |
|---|---|---|
| 版本号来源与计算（VERSION>tag>SHA、去 v、dirty、无 git、非空） | DELIVERED | Makefile `PLUGIN_VERSION` 计算块；`scripts/test-version.sh` 覆盖 6 Scenario（VERSION 覆盖/带v、dev SHA、untracked dirty、HEAD tag 去 v、VERSION>tag） |
| 发版产物文件名遵循 CPA 规范（`-v<version>`） | DELIVERED | Makefile build-platform-go out / package-platform library / WINDOWS,LINUX_AMD64_OUT；`package-release.go` `binaryPath(distDir, version)`；`TestBinaryPathVersioned`；构建验证 `model-mapper-plus-v1.2.3.so` |
| dev-so 注入可辨识版本号且部署固定名 | DELIVERED | Makefile dev-so（注入 `VERSION_LDFLAGS` + 固定名覆写 + 清理 release 残留）；`scripts/test-dev-so.sh` 干净 checkout 通过 |
| /state 响应暴露 plugin_version（响应层不持久化） | DELIVERED | `management.go` stateResponse.`PluginVersion`；`TestManagementGetStateIncludesPluginVersion` + `TestStateFileExcludesPluginVersion` |
| 前端管理页展示版本号 | DELIVERED | `App.tsx` footer `· v{plugin_version}`；`App.test.tsx`（展示 + 旧 .so 容错，含正向守卫） |
| CI 全平台版本与文件名同步 | DELIVERED | `build.yml` release_metadata 非 tag 计算 `0.0.0-dev.<sha>`；cgo-actions output `-v${{ env.VERSION }}`；`scripts/test-ci-yaml.sh` 5 断言 |

**对账计数**：6 DELIVERED / 0 DEFERRED / 0 DROPPED / 0 ADDED-IN-FLIGHT

## 验证证据

- **Go 单元**：174 passed（含 `TestManagementGetStateIncludesPluginVersion`、`TestStateFileExcludesPluginVersion`、`TestBinaryPathVersioned`、`TestPackageExistingArtifactsUsesSha256sumFormat` fixture 更新）
- **前端**：6 passed（App 版本展示 + 容错；RuleSetEditor 回归）
- **构建脚本**：`scripts/test-version.sh`（6 Scenario）、`scripts/test-ci-yaml.sh`（5 结构断言）、`make test-scripts`
- **集成（dev-so）**：`scripts/test-dev-so.sh`——干净 checkout 上 dev-so 注入 `0.0.0-dev.<sha>`、固定名部署、清理 release 残留
- **构建验证**：`make build-platform-go GOOS=linux GOARCH=amd64 VERSION=1.2.3` 产出 `model-mapper-plus-v1.2.3.so`

## 跨仓库待验证（验收矩阵 e2e 行，本地无 CPA 环境）

- **CPA 正确解析发版产物身份**：需真实 CPA——部署 `model-mapper-plus-v1.2.3.so` 到 CPA `plugins/linux/amd64/`、重启、查 `GET /v0/management/plugins` 的 `metadata.version` 应为 `1.2.3`、插件正常加载。留部署后验证。

## 审查发现处置

收尾多维审查（构建+CI / Go 后端 / 前端 / completeness critic）发现 2 严重 + 2 一般 + 1 前端一般 + 多建议，**全部 fixed**：

- **严重**：① Makefile 递归 make 显式传 `PLUGIN_VERSION`（修干净 checkout 上 dev-so 必失败 + filename/内嵌版本分裂）；② `test-dev-so.sh` 加 `set -o pipefail`
- **一般**：③ 三个脚本接入 CI（`make test-scripts` + build.yml）；④ zig 缺失用 skip 码 `80`；⑤ `test-version.sh` git tag `trap` 清理
- **前端**：⑥ `App.test.tsx` Scenario 2 加正向守卫（修假阳性）；⑦ regex 锁定 ` · v\d` 契约；⑧ `vi.mocked` + `satisfies StateResponse` 类型化
- **额外发现并修复**：`dev-so` strings 检查的 `pipefail` + `grep -q` 早退致 strings SIGPIPE 误判（改用变量捕获 + `grep -E`）

## 验收矩阵覆盖

spec 验收矩阵 19 个 Scenario：14 个由自动化测试承载（go 单元 / vitest / 脚本），5 个（tag 无 v / tracked dirty / 无 git / 多 tag / CPA 跨仓库解析）按计划设计由验收任务矩阵承载——前 4 个为 `make print-version` 终端断言（实现已就绪），CPA 解析为跨仓库 e2e（见上节）。
