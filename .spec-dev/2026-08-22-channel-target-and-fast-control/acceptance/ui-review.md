# Admin UI 视觉与交互验收

- 结果：**PARTIAL PASS**
- 执行方式：Google Chrome Headless + DevTools Protocol，1440×1100
- 证据：`ui-light.png`、`ui-dark.png`

已实际验证：

- Key 绑定表格显示安全的临时绑定、`1 个供应商 · 1 个认证文件` 摘要与 Fast 关闭状态。
- 新增弹窗存在“基础 / 渠道定向 / 规则集”三个主 Tab，已真实切换到“渠道定向”。
- 总开关关闭时 provider 与 auth 选项均 disabled；开启后两者可选。
- 选中 provider 与 auth 后再关闭总开关，选中值保留且控件恢复 disabled。
- 弹窗边界完全位于 viewport 内，页面没有水平溢出。
- 浅色与深色截图均经人工查看，未发现裁切、重叠或不可读文字。

未在 live 浏览器中人为中断 `auth-files` 接口，因此“加载失败后点击重试”仅由组件测试覆盖，本项不记为完整 live PASS。视觉临时 binding 已删除，独立 CPA 中剩余 binding 数量为 0。
