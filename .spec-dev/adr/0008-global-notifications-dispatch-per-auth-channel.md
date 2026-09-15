# ADR-0008: 全局通知按认证渠道调度，不按 Key 复制

**Status**: Accepted (2026-09-15)

## 背景

用量通知上线后，全局通知通过「无专属通知的启用 Key」循环复制发送。正文又把 Keeper `api_key_composition` 全部 API Key 当成渠道行，导致一条消息混入其他 Key，且同一渠道可能对多个 Key 重复投递。产品要求全局按认证渠道各发一条。

## 决定

全局通知的生成单位是认证渠道（Keeper `auth_files_composition` ∪ `ai_provider_composition` 的 identity），不再按客户端 API Key 复制。Key 级通知仍按 Key 绑定生成，正文只含该 Key 实际产生用量的认证身份。

## 理由

渠道粒度与「本 Key 拥有的渠道」口语、Keeper 身份拆分和群里可读性一致；继续按 Key 循环即使用滤正文，也会对每个空列表 Key 重复同一渠道。备选「单独渠道摘要调度器」能隔离循环，但在单插件内置服务（ADR-0007）下多一套生命周期，属于投机分层。
