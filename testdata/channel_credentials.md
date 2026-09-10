# 渠道凭据宿主样本

`channel_credentials.json` 来自本地 CLIProxyAPI `d1a024e9400bc65bd78ccd908945cf2eacc2835e` 的真实 ConfigSynthesizer 和七类管理 GET handler。仅含虚构 Key、`.invalid` 地址及已规范化配置。`config` 是 GET 返回的配置数组，`candidates` 是真实宿主生成结果，并非插件算法计算的预期值。

覆盖七种配置类型、请求头排序、proxy/prefix、多 Key、重复编号、无 Key 兼容供应商及禁用供应商。24 条凭据由插件解析后与宿主 ID/provider 逐条比较，再通过现有 Scheduler 验证单选；不要为迁就实现而更新预期值。CPA 算法或内部 provider 命名变化时，应重新运行真实宿主样本生成并核实差异。
