# `new-api` IAM 对接与改造方案

> **文档状态说明**
>
> 本文最初基于一套曾经存在的外部 SSO / Custom OAuth 实现编写，默认前提是：
> - 前端存在自定义 OAuth 登录入口、配置页、回调页与绑定页；
> - 后端存在通用 OAuth provider 模型、注册表、授权码交换链路与相关路由。
>
> **当前分支已经清理掉这套外部 SSO 的历史实现。**
>
> 因此，本文已改写为：
> 1. **说明当前代码状态**；
> 2. **保留历史方案中仍然有价值的设计原则**；
> 3. **给出未来如需重新接入企业 IAM 时的重建建议**。
>
> 文中提到的 `CustomOAuthSetting.jsx`、`OAuth2Callback.jsx`、`/api/oauth/:provider`、`/api/custom-oauth-provider/discovery`、`oauth/generic.go` 等名称，**都应视为历史实现或建议重建位置，而不是当前分支已存在能力**。

---

## 1. 当前代码状态

## 1.1 当前分支已移除的历史外部 SSO 能力

当前分支已经不再保留以下“外部 OAuth / 自定义 Provider 登录”运行时实现：

### 前端侧已移除
- 登录页 / 注册页的外部 SSO 动态按钮渲染；
- 自定义 OAuth Provider 的系统设置页；
- 外部 OAuth 回调页；
- 用户个人中心里的自定义 Provider 绑定 / 解绑入口；
- 与外部 OAuth 登录发起相关的前端 helper。

对应的历史文件与入口包括但不限于：
- `web/src/components/settings/CustomOAuthSetting.jsx`
- `web/src/components/auth/OAuth2Callback.jsx`
- `web/src/helpers/api.js` 中旧的外部 OAuth helper
- 登录 / 注册页中对 `custom_oauth_providers` 的消费逻辑

### 后端侧已移除
- 自定义 OAuth Provider 的模型、控制器与注册表；
- 通用 OAuth 授权码登录主链路；
- 历史的 `/api/oauth/state`、`/api/oauth/:provider`、`/api/custom-oauth-provider/discovery` 等接口；
- `/api/status` 中面向前端外部 SSO 按钮渲染的动态 provider 下发能力。

也就是说，**当前分支并不是“已经有外部 IAM 接入底座，只差填配置”**，而是已经完成过一轮清理，相关实现需要按需求重新设计或重建。

## 1.2 当前仍保留的认证相关能力

本次外部 SSO 清理并不代表 `new-api` 已经移除所有认证能力。当前分支仍保留并在运行中的能力包括：

- 本地账号登录；
- Passkey / WebAuthn 相关能力；
- 2FA / 二步验证；
- 邮箱绑定相关能力；
- 与渠道凭据相关的 Codex OAuth 流程。

其中 **Codex OAuth 属于渠道凭据获取 / 刷新的业务能力，不是用户侧企业 IAM / 外部 SSO 登录能力**，不要混为一谈。

## 1.3 如何理解本文后续内容

从这里开始，本文不再把“历史 Custom OAuth 实现”描述为现状，而是分成两层：

1. **哪些设计原则仍然成立**；
2. **如果未来要重新接企业 IAM，需要重建哪些前后端能力**。

换句话说：

> 本文下面讨论的是“未来怎么重新接入 IAM 更合理”，而不是“当前代码已经具备哪些现成文件可直接复用”。

---

## 2. 结论先行

### 2.1 `new-api` 仍然可以接企业 IAM

可以。

但当前分支的真实情况是：

- **没有现成的外部 SSO 前端入口**；
- **没有现成的通用 OAuth 后端主链路**；
- **没有现成的自定义 Provider 管理页**；
- **没有现成的 OAuth 回调页与绑定页**。

因此，企业 IAM 接入不应再被理解成：

> “后台补几个配置字段，前端直接复用旧页面上线。”

更准确的说法应该是：

> **如果未来要接企业 IAM，需要在当前分支上重新引入一套受控的外部 SSO 能力；历史方案可以参考，但不能当作现成实现。**

### 2.2 最合理的技术路线

如果未来确实要恢复 IAM / 外部 SSO，仍然建议优先走：

- **通用 Provider 模型**；
- **标准 OAuth 2.0 / OIDC 授权码主链路**；
- **按 provider 配置端点、字段映射、策略与退出行为**；
- **避免为单个 IAM 厂商临时硬编码一套专属登录链路**。

也就是说：

- **方向上仍然推荐 Generic OAuth / Provider Registry 思路**；
- **只是当前分支已经没有那套实现，需要重新建设**。

---

## 3. 历史方案里仍然成立的设计原则

虽然旧实现已经移除，但下面这些设计判断仍然成立，未来重建时应继续保留。

## 3.1 `redirect_uri` 必须显式可控

这是企业 IAM 接入里最容易踩坑的一项。

无论未来前端按钮和后端 token exchange 怎么实现，都应该满足：

- authorize 请求里使用的 `redirect_uri`；
- token exchange 时使用的 `redirect_uri`；
- IAM 后台登记的回调地址；

三者必须严格一致。

因此，未来恢复 Provider 配置时，应支持：

- provider 级 `redirect_uri` 显式配置；
- 未显式配置时才允许使用默认推导值。

## 3.2 `validate` / `introspection` 应作为可选能力设计

不少企业 IAM 除了 token + userinfo，还要求：

- validate；或
- introspection；或
- 额外的 token 有效性校验。

因此未来若重新实现 Provider 模型，建议从一开始就为这类能力预留扩展位，例如：

- `validate_url`
- `enable_validate`
- `validate_method`
- `validate_auth_style`

不一定首批全部做完，但模型和流程设计应预留空间。

## 3.3 logout 不能只考虑本地 session

如果企业 IAM 是主身份源，只清本地 session 往往不够。

未来重建时，应把 logout 设计成：

1. 清本地 session；
2. 根据登录来源判断是否需要跳转 IAM logout；
3. 支持 `logout_redirect` / `post_logout_redirect_uri` 之类的退出回跳能力。

这意味着登录成功时应记录最少量的来源信息，例如：

- `login_provider_slug`
- 或等价的登录来源标识。

## 3.4 账号自动匹配 / 绑定是企业迁移关键项

如果企业用户历史上已经在系统里拥有本地账号，那么未来恢复 IAM 后，首次登录很可能需要：

- 自动建号；或
- 自动匹配已有账号；或
- 自动补绑定关系。

因此要预先决定：

- 是否允许按邮箱自动匹配；
- 是否允许按用户名自动匹配；
- 是否需要管理员确认；
- 发生冲突时如何阻断与告警。

## 3.5 “主身份源不可解绑”通常要做成策略能力

如果 IAM 是企业唯一身份源，允许用户手动解绑通常不合理。

未来若恢复绑定体系，建议给 provider 加上类似策略：

- `allow_manual_unbind`
- `is_primary_identity`

并把对应限制落实到前端与后端两侧，而不是只靠前端隐藏按钮。

## 3.6 discovery 更适合作为“配置辅助”，不宜首批做成运行时强依赖

即使未来恢复 discovery，也更建议把它设计为：

- 管理员配置期的辅助能力；
- 自动回填端点 / scope / 建议映射；
- 最终仍以数据库里保存的 endpoint 与字段配置为准。

这样比“每次登录都依赖远端 well-known 动态发现”更稳定，也更适合企业环境。

---

## 4. 如果未来重新接 IAM，后端需要重建什么

## 4.1 Provider 数据模型与持久化

未来至少需要重新引入一套 provider 数据模型，用来持久化：

- `name`
- `slug`
- `icon`
- `client_id`
- `client_secret`
- `authorization_endpoint`
- `token_endpoint`
- `user_info_endpoint`
- `scopes`
- 字段映射
- `redirect_uri`
- `logout_url`
- `validate_url`
- access policy 与拒绝提示
- 是否允许自动绑定 / 是否允许手动解绑等策略字段

如果想继续沿用历史命名，也可以重新创建类似：

- `model/custom_oauth_provider.go`
- `controller/custom_oauth.go`

但这些文件名现在只是**建议重建位置**，不是当前分支已有文件。

## 4.2 授权码登录主链路

后端至少需要恢复以下主流程：

1. 前端申请 state；
2. 浏览器跳 IAM authorize；
3. 回调把 `code/state` 交回后端；
4. 后端完成 state 校验；
5. code -> token；
6. token -> validate/introspection（可选）；
7. token -> userinfo；
8. user 匹配 / 建号 / 绑定；
9. 建立本地 session；
10. 返回前端可消费的登录结果。

如果仍采用历史架构思路，建议拆成：

- provider registry
- generic provider implementation
- controller / router 入口

但要明确：**这些在当前分支都不存在，需要新建。**

## 4.3 状态接口与回调接口

未来若恢复外部 IAM 登录，通常至少需要以下接口：

- 类似 `/api/oauth/state` 的 state 申请接口；
- 类似 `/api/oauth/:provider` 的回调交换接口；
- 若保留配置辅助，则再加 discovery 接口。

是否沿用历史路径命名可以再决定，但文档和代码必须保持一致，不能再出现“文档里有路由，代码里没有”的情况。

## 4.4 session 与 logout 来源识别

未来后端应明确设计：

- 登录完成后，session 中记录登录来源；
- `/api/user/logout` 根据登录来源决定是否返回 `logout_redirect`；
- 普通本地登录、Passkey、2FA、邮箱绑定等现有流程不受影响。

建议目标是：

- 本地用户继续只走本地 logout；
- IAM 用户可选跳转企业退出地址；
- 退出后能稳定回到系统登录页或企业指定页面。

## 4.5 账号绑定与身份迁移策略

未来如果需要“已有本地账号自动匹配 IAM”，建议把规则放在后端统一处理，而不是散落在前端。

重点要先明确：

- 以邮箱匹配是否可信；
- 以用户名匹配是否稳定；
- 冲突时是拒绝登录、要求人工处理，还是允许管理员介入；
- 绑定表是否需要保留 provider user id、原始 claims、上次登录时间等字段。

---

## 5. 如果未来重新接 IAM，前端需要重建什么

## 5.1 登录页 / 注册页入口

当前分支已没有“从状态接口动态渲染外部 Provider 按钮”的实现。

因此未来要恢复 IAM 登录，前端至少要重新建设以下能力之一：

### 方案 A：动态 provider 按钮
- 后端返回可用 provider 列表；
- 登录页 / 注册页动态渲染；
- 适合保留多 provider 扩展能力。

### 方案 B：固定企业入口
- 明确只接一个企业 IAM；
- 登录页展示固定“企业登录”按钮；
- 后端根据固定 provider 处理。

如果业务只需要单一企业 IAM，方案 B 可以更简单；如果需要平台化扩展，方案 A 更合适。

## 5.2 授权发起 helper

当前分支已不再保留历史的外部 OAuth 发起 helper。

未来需要重新实现：

- state 获取；
- authorize URL 拼接；
- `client_id` / `scope` / `response_type` / `redirect_uri` / `state` 携带；
- 失败时的错误提示与回退行为。

这一层不一定非要继续放在 `web/src/helpers/api.js`，但无论放在哪里，都需要满足：

- `redirect_uri` 可控；
- provider 参数来源清晰；
- 错误处理和 loading 状态完整。

## 5.3 回调页

当前分支已没有历史的 `OAuth2Callback.jsx` 与 `/oauth/:provider` 页面实现。

如果未来继续采用“前端接回调，再调用后端完成登录”的模式，那么需要重建：

- 回调路由；
- 页面级 loading / error / success 状态；
- `code/state` 提交；
- 登录成功后的本地用户态写入与跳转。

如果未来改为“后端直接处理回调后再 302 到前端”，也可以不复用旧模式，但必须在设计阶段提前定清楚。

## 5.4 Provider 管理页

当前分支已没有历史的 `CustomOAuthSetting.jsx`。

如果未来要让管理员自行配置企业 IAM，前端需要重建一个配置页，至少覆盖：

- 基本信息：名称、slug、图标；
- OAuth 端点：authorize、token、userinfo；
- scope 与字段映射；
- `redirect_uri`；
- `validate_url` / `enable_validate`；
- `logout_url`；
- 自动绑定策略；
- access policy 与拒绝提示。

如果短期内只接一个固定 IAM，且所有配置都由后端或环境变量托管，也可以先不做通用配置页。

## 5.5 个人中心绑定 / 解绑 UI

当前分支已没有自定义 Provider 绑定 / 解绑 UI。

因此未来要不要恢复这块，应该由业务决定：

- 如果 IAM 是唯一登录方式，用户未必需要手动绑定入口；
- 如果 IAM 只是附加登录方式，则可能需要恢复绑定管理；
- 如果需要“主身份源不可解绑”，前端和后端都要一起加限制。

不要再默认假设“当前个人中心已经有现成绑定弹窗可复用”。

## 5.6 logout 交互

当前分支的前端 logout 仍主要围绕本地 session。

未来如果恢复企业 IAM，应补上：

1. 调本地 logout 接口；
2. 清本地用户态；
3. 若响应里有 `logout_redirect`，执行浏览器跳转；
4. 否则回到本地登录页。

---

## 6. 推荐实施顺序

## 6.1 先做范围决策

在重新上马 IAM 之前，先回答清楚这几个问题：

1. IAM 是唯一登录方式，还是附加登录方式？
2. 是否还允许本地用户名 / 密码登录？
3. 是否还允许注册？
4. 是否需要自动绑定已有本地账号？
5. 是否需要管理员配置多个 provider？
6. 是否必须支持单点退出？

这些问题不先定清楚，前后端实现会反复返工。

## 6.2 第一阶段：打通最小闭环

建议第一阶段只做“能安全登录、能安全退出”的最小闭环。

### 后端最小闭环
- provider 配置持久化；
- state 接口；
- callback / token exchange 接口；
- userinfo 获取；
- 本地 session 创建；
- 登录来源记录；
- logout 返回可选 `logout_redirect`。

### 前端最小闭环
- 登录入口；
- authorize 发起；
- 回调页或回调承接逻辑；
- 本地用户态写入；
- logout 跳转处理。

## 6.3 第二阶段：补企业化能力

在最小闭环稳定后，再补：

- validate / introspection；
- 自动匹配已有账号；
- 主 provider 不可解绑；
- access policy；
- provider 配置页高级字段；
- discovery 辅助配置。

## 6.4 第三阶段：做 UX 收口与运维收口

如果最终目标是企业内网统一登录，还建议继续做：

- 隐藏注册入口；
- 隐藏或弱化本地密码登录；
- 突出主 IAM provider；
- 明确账号冲突与封禁提示；
- 同步更新 API 文档、运维文档、接入手册与测试用例。

---

## 7. 明确不要再假设的“现成能力”

为了避免后续开发和联调继续踩坑，这里单独列出**当前分支不应再被当作现成能力**的内容：

- 不要再假设 `/api/status` 会下发 `custom_oauth_providers`；
- 不要再假设登录页 / 注册页已经能自动渲染企业 IAM 按钮；
- 不要再假设前端已有 `OAuth2Callback.jsx`；
- 不要再假设路由里已有 `/oauth/:provider`；
- 不要再假设系统设置里已有 `CustomOAuthSetting.jsx`；
- 不要再假设个人中心已有自定义 Provider 绑定 / 解绑入口；
- 不要再假设后端已有 `/api/oauth/state` 与 `/api/oauth/:provider`；
- 不要再假设仓库里已有 `controller/custom_oauth.go`、`model/custom_oauth_provider.go`、`oauth/generic.go` 等运行时代码。

如果未来确实需要这些能力，应该明确地：

1. 重新设计；
2. 明确落到哪些文件；
3. 更新路由与前端页面；
4. 再同步更新文档和 OpenAPI。

---

## 8. 未来重建 IAM 时的验收清单

## 8.1 文档与代码一致性
1. 文档中提到的路由在代码里真实存在；
2. 文档中提到的页面和组件在前端真实存在；
3. OpenAPI / 接口文档与路由实现一致；
4. 不再把历史文件写成“当前现状”。

## 8.2 登录主链路
1. 登录页能看到 IAM 入口；
2. 点击后能正确跳转 authorize；
3. `redirect_uri` 与 IAM 后台登记一致；
4. 回调后能正确提交 `code/state`；
5. 后端能成功 exchange token；
6. 后端能成功获取 userinfo；
7. 用户能成功建立 session 并进入系统。

## 8.3 账号迁移与绑定
1. 首次 IAM 登录能正确建号或绑定；
2. 已有绑定账号再次登录不会重复建号；
3. 自动匹配策略不会误绑到其他用户；
4. 冲突场景有明确错误提示与处理路径。

## 8.4 logout
1. 本地 session 能被正确清理；
2. IAM 用户可按策略跳转企业退出地址；
3. 退出后能回到指定页面；
4. 普通本地用户 logout 行为不受影响。

## 8.5 构建与回归
1. 后端 `go build ./...` 通过；
2. 前端 `npm run build` 通过；
3. Passkey、2FA、邮箱绑定、本地登录等现有能力未被回归破坏；
4. Codex OAuth 等非用户 SSO 能力未受影响。

---

## 9. 最终建议

这件事现在不应该再被理解成：

> “把现有 Custom OAuth 页面再补几个字段，顺手接个企业 IAM。”

因为当前分支已经没有那套现成实现。

更合理的判断是：

> **`new-api` 仍然完全可以接企业 IAM，但应当把它当成一项“受控重建外部 SSO 能力”的工作来做。**

建议路线如下：

### 必须先做
1. 明确 IAM 是否为唯一登录方式；
2. 重建最小可用的 provider 模型、授权码链路与回调承接；
3. 把 `redirect_uri`、logout 来源识别、`logout_redirect` 设计正确。

### 强烈建议紧跟着做
4. validate / introspection；
5. 已有本地账号自动匹配 / 绑定；
6. 主 IAM provider 的不可解绑策略；
7. 文档、OpenAPI 与实际路由同步更新。

### 可以参考但不要误判为现成实现的历史思路
- Generic OAuth / Provider Registry 的建模方式；
- provider 级 `redirect_uri`；
- discovery 仅做配置辅助；
- access policy；
- 绑定策略与企业化退出行为。

**一句话结论：历史方案里的设计思想仍然有价值，但当前分支没有现成的外部 SSO 实现，未来如需对接 IAM，应按“重建”而不是“直接复用”来推进。**
