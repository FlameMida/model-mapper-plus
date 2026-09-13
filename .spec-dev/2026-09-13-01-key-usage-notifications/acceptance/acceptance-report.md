# T16 验收报告（key-usage-notifications）

执行时间：2026-09-13 · 集成基线：78bc1de · 执行者：主线程（acceptance-qa，无浏览器自动化工具环境）

## 命令类（final，全绿）

| 检查 | 命令 | 结果 | 证据 |
|---|---|---|---|
| Go vet | `go vet .` | exit 0 | vet.log |
| Go 全量 race | `go test -race . -count=1` | ok 17.6s | race.log |
| 前端 typecheck | `pnpm --dir web run typecheck` | exit 0 | web-typecheck.log |
| 前端全量测试 | `pnpm --dir web test` | 27 文件 / 169 测试 PASS | web-test.log |
| 内联构建 | `VITE_HOSTED=1 pnpm --dir web run build` | ✓ built（1.74MB） | web-build.log |
| ABI 构建冒烟 | `CGO_ENABLED=1 go build -buildmode=c-shared`（zig 交叉编译的本机降级替代） | 10.5MB .so 构建成功 | abi-smoke.log |

降级：`make build-windows-amd64`（zig 交叉编译）本机未装 zig 未验证——CI release 管线（build.yml）覆盖；本机以 darwin c-shared 构建验证 ABI 管线本身。

## 内联产物冒烟

web/dist/index.html 含关键 UI 串：通知服务 / 全局默认通知 / 新增通知 / 平台身份配置 / 测试发送（各 ≥1 次）。

## 浏览器交互项（browser-pending——无浏览器自动化工具，不伪造结果）

- 管理页面桌面/移动布局与 Tab 联动（Key 弹窗 4 页签、抽屉两 Tab、平台 card Tabs、全局面板三区块）
- 模块拖拽排序（dnd-kit）与计划三态字段联动（jsdom 已数据断言 + Semi popup 交互单测，拖拽本体待浏览器）
- 消息预览与明文回显的页面级核验

注：`MAPPER_ACCEPTANCE_SERVE=1` fixture 服务的启动/退出不构成浏览器验收结果（CLAUDE.md 明示）。

## manual（manual-pending，需真实 webhook 与群）

- 企业微信 / 飞书 / 钉钉真实群消息送达与 @ 生效；不满足平台条件时的受控失败展示。

## fast lane 溯源

spec 矩阵 fast 行已在 T02–T15 各票 TDD 覆盖（红→绿证据见 execution/TNN-a1/），此处不重复。
