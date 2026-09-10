# AI Providers 凭据统一勾选：修复验证

## 结果

PASS（本地源码、受控宿主样本及真实浏览器）；未部署、未测试真实上游请求。CLIProxyAPI 工作区无修改。以下为发版前验证记录，提交与发布结果以 Git 历史及 GitHub Release 为准。

- 认证文件与 AI Providers 共用供应商整选/凭据单选的并集语义。无 AI Providers 豁免分支，无按模型分类。Scheduler 原有过滤逻辑无需修改。
- 新增插件 `POST /channel-credentials`，用 Go 标准库按宿主规则计算 ID 与内部 provider。前端仍直读宿主七类配置和 auth-files；普通 HTTP 页面可工作。配置不写状态，目录返回脱敏展示值，保存绑定只包含 ID。
- 统一凭据文案、来源标签、配置地址搜索与「已配置」状态。弹窗最大宽度限制为视口减 32px，桌面保留 860px。

## TDD 与回归

| 命令（注明目录） | 结果 |
|---|---|
| 根目录 `rtk go test . -run 'TestChannelCredentials' -count=1`，新增测试后首次运行 | RED：新目录路由 404，两个测试失败；混选现有行为通过 |
| 同上，实现后 | PASS：27 项（含 24 条宿主凭据子测试） |
| web：`rtk npm test -- src/channelCredentials.test.ts src/components/ChannelTargetEditor.test.tsx`，新测试首次运行 | RED：缺少统一目录函数和来源展示，7 失败 / 7 通过 |
| 根目录 `rtk go test ./...` | PASS：346 项，exit 0 |
| web：`rtk npm run typecheck` | PASS，exit 0 |
| web：`rtk npm test` | PASS：16 文件 / 85 测试，exit 0 |
| web：`rtk proxy env VITE_HOSTED=1 npm run build` | PASS，exit 0；更新 web/dist/index.html |
| 弹窗宽度修正后 `rtk npm run typecheck` 与 `rtk npm test -- src/panels/KeysPanel.channel-target.test.tsx` | PASS：8 测试，exit 0；构建重新通过 |
| 根目录 `go build -buildmode=c-shared -o <临时目录>/model-mapper-plus.dylib .` | PASS，exit 0 |
| 两个仓库最终差异检查 | 插件 diff --check 通过；CLIProxyAPI git status --short 为空 |

锁文件安装 `npm ci` 报告依赖树已有 34 项审计提示（32 moderate / 2 high）；未变更依赖或锁文件。Vite 构建有既有 inlineDynamicImports 和 lottie-web eval 警告，不影响本次构建结果。

## 宿主契约证据

`testdata/channel_credentials.json` 包含 CPA `d1a024e9400bc65bd78ccd908945cf2eacc2835e` 的真实配置 GET 响应与 ConfigSynthesizer 结果，仅用虚构凭据。覆盖 7 类接口、24 条凭据、4 条重复后缀、多 Key、前缀、代理、请求头、禁用与无 Key 兼容供应商。插件 ID/provider 逐条一致，24 条均可通过现有 Scheduler 单选。

源码兼容目标是该本地 CPA；插件编译 SDK 仍为 v7.2.119。不能据此声称任意 CPA 版本都采用相同 ID/provider 规则。

## 浏览器验证

Chrome 打开构建后的单文件 HTML，由隔离 Go HTTP fixture 提供宿主管理样本，并直接调用本插件真实管理 handler/临时状态文件。无真实 API Key、无上游调用。

1. 打开 Key 绑定 → 编辑 → 渠道定向：八个供应商分组、认证文件和 AI Providers 同列展示。
2. Codex 认证文件已选，再勾选一条 Codex 配置凭据；按地址搜索后认证文件暂时隐藏，选择仍保留。
3. 点击确定，出现「绑定已保存」；重开后两个 ID 均勾选，其他配置凭据未选。实际落盘内容见 [saved-state.json](saved-state.json)。
4. 桌面截图见 [desktop.png](desktop.png)。
5. 390×844 视口修复前测得 modal=860px；加视口上限后实测 modal=358px、editor=308px，来源标签和开关不再被右侧裁切，见 [mobile.png](mobile.png)。验证后恢复浏览器尺寸并关闭临时页。

## 边界与资源

- 未跑真实 CPA 网络鉴权、真实上游或线上部署。管理接口样本来自真实宿主 handler；浏览器通过隔离 fixture 调用插件。
- 禁用 OpenAI Compatibility 不生成运行时凭据，目录不提供其勾选项；旧保存 ID 保留为当前未返回。配置变化引发 ID 变化不自动转绑。
- 保留宿主模型能力、cooldown、优先级预过滤，不召回 Candidates 之外的凭据。
- 两次临时 HTTP fixture 已通过停止端点关闭，测试临时 state 已随 t.TempDir 清理。无运行中的测试服务或容器；Go/npm 共享缓存保留。
- 临时验证脚本与本地动态库位于 `/var/folders/gb/6mr3jmcs06dcbxm5c85pdyyw0000gp/T/mapper-credentials-ui-1om4k7ly`，只含本任务虚构数据和构建产物；重要截图与保存证据已复制至此目录。
