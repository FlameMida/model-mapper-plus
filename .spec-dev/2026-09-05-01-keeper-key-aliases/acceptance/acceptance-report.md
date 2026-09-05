# Keeper API Key 别名接入验收报告

日期：2026-09-05。依据：`../spec/keeper-key-aliases-design.md`，R1–R5 全部交付。

## 结论与环境

功能验收通过；后端、前端和覆盖审查均无未解决发现。源码和文档已同步回主工作目录，保留为未提交变更，未推送或发布。测试二进制保存在 `dist/keeper-key-aliases/model-mapper-plus.so`。

- 工作基线：`f1d3d35ac4389d4223ac20e7f6ee9699d329c936`，以当前未提交实现构建。
- CPA：Linux amd64 Docker，v7.2.119，commit `6e92e3e`；镜像 ID `sha256:0b046bc593de22b706f8d32dce45c9defe0d48ec4cc3a5188ce0ebbd99b438a8`。
- Keeper：从 `/Users/flame/cpa-usage-keeper` 的 `3e8ee04739e17a58fbb7c8b0b971a8b61d97356d` 构建原生测试服务。独立 env/SQLite，认证开启，子路径 `/keeper`。
- 浏览器：Chrome 152.0.7977.82，Playwright 驱动，1360×1000，真实 Select 动画开启。
- CPA/Keeper 测试地址分别为 127.0.0.1:18317 和 31808；只使用自造测试 Key/密码，不修改现有运行服务的数据。
- 测试二进制沿用 HEAD 的 0.1.5 元数据标记，并非新发布版本。实际二进制 SHA-256：`8c1bdf931be7add9ab78b0f0c64eba6166f79c5bc7fdd05ffe2f7838acd736f6`。
- 嵌入 `web/dist/index.html` SHA-256：`44547b931d78ff910736dfb3a75bbb4aeeffede3fa68bc6a5264957f37c37306`。

## 验收矩阵

| Requirement | 检查与证据 | 结果 |
|---|---|---|
| R1 配置和 Keeper 客户端 | JSON/base64 YAML register/reconfigure 回归；真实 CPA ConfigFields、PATCH 保存及禁用/启用热更新；空白 password_env 使用默认变量；真实 Cookie 登录与子路径 | 通过 |
| R2 管理接口、缓存及隔离 | httptest 覆盖 401 登录/续期、403、429 Retry-After、超时、重定向、格式校验；8 MiB 响应体限制经静态核查；60 秒缓存、强刷、并发成功/取消、配置竞态；在途请求不阻断 state 读写 | 通过 |
| R3 两页选择器 | 共享选项优先级、全 Key value、双别名搜索、CPA 成员限制；真实绑定/试跑选择和清空、键盘手输；切页重试 CPA 列表 | 通过 |
| R4 编辑同步 | 真实 Keeper 修改后立即同步、覆盖草稿、取消、保存、中文与特殊字符；CPA 重启保留；空别名/无匹配/手改/关窗/A→B→A 用组件回归覆盖 | 通过 |
| R5 交付 | CI 加入前端 typecheck/test，README/CLAUDE 更新，Go/前端/packager 验证，嵌入 UI 与 Linux .so 构建 | 通过 |
| 故障与现有功能 | 真实 Keeper 停机后草稿不变、CPA 登录与保存正常、CPA Key 列表仍可用；Fast 开关保存与重启；既有规则/渠道/Fast 回归 | 通过 |
| 明暗主题、长别名 | 120 字中文别名换行、保留 Key 尾号；浏览器断言 scrollWidth 不超过视口；两张截图判读无越界 | 通过 |

## 验证命令

- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- `go test .github/scripts/package-release.go .github/scripts/package-release_test.go`：通过。
- `npm --prefix web test`：15 文件，77 测试通过。
- `npm --prefix web run typecheck`：通过。
- `VITE_HOSTED=1 npm --prefix web run build`：通过；tracked dist 已同步。
- `make build-platform-go GOOS=linux GOARCH=amd64 BUILD_CC='zig cc -target x86_64-linux-gnu'`：通过。
- `live.mjs`：10 项真实 API/浏览器断言通过，无 pageerror。
- `failure-restart.mjs`：重启持久化、停机隔离、故障下列表可用三项通过。
- `git diff --check`：通过。

## 审查修复

1. 生命周期 YAML 遗漏 Keeper 字段：补 register/reconfigure 回归及解码。
2. 重配后的旧快照重新安装服务：配置读锁内校验并选择服务，配置写锁内清缓存；旧请求测试通过。
3. Semi 原生 allowCreate 选项缓存不刷新：使用普通受控 Select，显式手动 Key 选项；当前搜索也预过滤，避免键盘回车选错值。
4. 共享列表只在登录加载：切页独立重读 CPA Keys，带请求/登录会话隔离。
5. 文档旧字段描述、缺失并发隔离测试：均已补齐。
6. 浏览器长别名撑宽页面：限制下拉宽度并换行，新增真实视口宽度断言通过。

## 已知基线问题与验收边界

- 额外尝试 `make test-scripts` 时，旧 version 脚本假定 HEAD 无 tag，而当前 HEAD 位于 v0.1.5；在独立无 tag fixture 中版本测试通过。旧 `test-ci-yaml.sh` 仍断言已不存在的 Windows output 字面结构；基线 HEAD 的原始 workflow 也以同样信息失败。两者未作本功能范围外改写。当前 CI 使用的 release packager 测试通过。
- 官方 CPA 设置页的视觉输入控件未运行；已从真实宿主验证两个 string ConfigFields、相同配置读写接口和 YAML 热更新。插件自身页面已在真实浏览器操作。
- 明暗主题截图通过直接设置主题属性验证渲染；父 iframe 的主题事件同步使用既有自动化回归，未把截图等同于完整官方面板 E2E。
- HTTP 限流/错误格式/竞态采用确定性 httptest/组件测试；真实 Keeper 主要覆盖登录、读别名、更新、停机。未请求付费模型上游。
- 未执行压测或 WCAG 审计；本次变更无新增推理路径网络 IO，范围集中在管理配置和交互。
- 截图只判断布局、文本、控件可用性，不宣称建立像素级视觉回归基线。

## 证据与复跑

- `live-results.json`、`failure-restart-results.json`：本次实际执行的断言结果。
- `binding-light-long.png`、`binding-dark-long.png`、`preview-light.png`、`keeper-offline.png`：真实页面截图。
- `reviews/backend.json`、`reviews/frontend.json`、`reviews/coverage.json`、`reviews/final-evidence.json`：独立审查与最终证据复核结论。
- `live.mjs`、`failure-restart.mjs`：复跑脚本。仅对文中隔离测试地址运行：脚本会清空该测试实例绑定、修改其 Keeper 别名，并重启测试 CPA/停止 31808 的测试 Keeper。请勿改为生产地址运行。
- 复跑时将脚本复制到 `.test-cpa/keeper-aliases/browser/`，在该目录临时安装 Playwright，从项目根目录启动脚本；需先按本报告准备独立 CPA/Keeper 和自造凭据。临时依赖不加入项目 package.json。
