# Admin UI 视觉与交互验收

- 时间：2026-08-24（Asia/Shanghai）
- 结果：**PASS**
- 执行方式：Google Chrome Headless + DevTools Protocol，1440×1100
- 证据：`ui-run.json`、`ui-light.png`、`ui-dark.png`

确定性操作与断言（任一条件不满足时 CDP 脚本直接失败；本次 10/10 通过）：

- 实际拦截 `GET /v0/management/auth-files`，页面出现“认证文件加载失败”和“重试”；解除拦截后点击重试，auth 文件恢复展示。
- 弹窗存在“基础 / 渠道定向 / 规则集”三个主 Tab。
- wire fixture 使用 `suppliers=[" KIMI "]`、带空白 auth ID；页面只显示一个 `KIMI` provider 分组，目标 auth 正确勾选且无“已保存但 CPA 当前未返回”误报。
- 关闭总开关后 provider 与 auth 均 disabled，但两个勾选状态保留；重新开启后恢复可操作。
- 弹窗边界为 `left=290, top=80, right=1150, bottom=531`，完全位于 1440×1100 viewport；document 无横向溢出。
- 浏览器页面运行期未捕获 console error。

视觉判读：

- 浅色与深色弹窗均无裁切、重叠或文字不可读。
- 三个主 Tab、双区块标题、组选状态、状态 Tag、取消/确定按钮层级清楚。
- 深色背景、遮罩、边框与正文对比未出现明显失配。

本轮没有 Playwright 视觉基线，因此截图用于 Tier A 当前状态判读，不声称像素级回归比较；组件行为另由 Vitest 确定性断言覆盖。
