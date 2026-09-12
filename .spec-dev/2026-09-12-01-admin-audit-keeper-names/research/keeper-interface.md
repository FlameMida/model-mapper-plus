# Keeper 名称接口核对

来源为用户提供的 /Users/maverick/cpa-usage-keeper，只读核对；未进行线上名称写入。

- internal/api/usage_identities.go:198：GET /api/v1/usage/identities 返回 identities 数组。
- 同文件:217：PATCH /api/v1/usage/identities/:id 返回单个完整 identity 对象；404 为不存在/已删除。
- 同文件:370及:382：alias 在 TrimSpace 后最多50 Unicode rune；空字符串或null清空；拒绝 unicode.IsControl 与 isDisallowedUsageIdentityAliasFormatRune 的精确集合。不能泛化成禁止全部Cf，避免误拒emoji连接字符。
- internal/service/metadata_auth_files.go:76：auth_type=1 的 identity 来自CPA auth_index。type/provider表示供应商；Keeper id为内部数据库ID，JSON字符串，不是CPA文件ID。
- internal/helper/usage_identity_display_name.go:9：Auth File displayName为非空alias→name→provider→identity。
- internal/api/auth.go:474：PATCH需Content-Type application/json及X-CPA-Usage-Keeper-Request: fetch；管理认证沿现有cookie会话。

当前 mapper T03 提供 authenticatedRequest(ctx,method,path,payload)、readKeeperBody(ctx,body)、fetchAuthNames(ctx)。T04保留全部预检/登录/PATCH共享5秒上下文，不因网络超时/5xx自动重试PATCH。

本笔记是来源指针摘要，不代替实施者读取目标校验规则；没有授权修改Keeper或宿主代码。
