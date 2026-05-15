# 双轨扣费实施清单（文件级）

> 文档版本：v1.0  
> 目标：把《SUBSCRIPTION_DUAL_TRACK_BILLING_PLAN.md》的方案拆成可执行的文件级实施清单，供后续按阶段开发与联调使用。  
> 本文只输出实施清单，不直接修改业务代码。

---

## 1. 背景与本次实施目标

当前系统已经支持：

- 钱包 / quota 扣费
- 订阅 quota 扣费
- 订阅 request_count 扣费

但当前真实实现仍是：

1. 普通同步 `Relay` 在**选渠道之前**先做 `PreConsumeBilling(...)`
2. 订阅系统默认用户同一时刻只有**一种活跃 meter_type**
3. `request_count` 订阅会影响 token exhausted 放行逻辑
4. Task 链路已有独立的 submit / refund / polling / recalculate 计费状态机

因此，若要实现“**按最终命中的渠道 billing_type 决定本次到底扣次数订阅还是额度订阅/钱包**”，必须按真实链路做分阶段改造，而不是只加几个字段。

### 本次实施要达成的业务规则

1. `Channel` 新增 `billing_type`
   - `quota`
   - `request_count`
2. 命中 `request_count` 渠道时：
   - 只能消耗次数订阅
   - 次数不足直接失败
   - 不能回退钱包
3. 命中 `quota` 渠道时：
   - 优先消耗额度订阅
   - 额度订阅不足时，按用户现有偏好回退钱包
4. 一期只改**普通同步 Relay**
5. 二期再改 **Task 双轨扣费**

---

## 2. 分期边界

## 2.1 一期：只做普通同步 Relay

一期必须覆盖：

- 普通同步请求主链路
- Channel billing_type 建模与管理端配置
- 按 meter_type 定向查询 / 消费订阅
- 消费日志与快照字段
- 基础 migration
- 前端渠道管理
- usage / task / error log 的 `channel_billing_type` 快照

一期明确**不做**：

- Task submit / polling / recalculate 双轨扣费
- Task 私有计费状态机重构
- 针对 token 校验阶段引入渠道感知
- 大规模重构所有日志筛选 API

## 2.2 二期：Task 双轨扣费

二期再处理：

- `relay/relay_task.go` 提交阶段预扣
- `service/task_billing.go` 退款与差额结算
- `service/task_polling.go` 轮询完成态结算
- `model/task.go` private_data / billing_context 快照扩充

原因不是“Task 当前没预扣”，而是：**Task 当前已经有预扣，只是状态机明显更复杂**。

---

## 3. 实施总顺序（推荐）

建议按以下顺序推进，避免返工：

1. **模型层与 migration 先落字段**
   - `channel.billing_type`
   - `billing_attempt_id`
   - 日志 / task 快照扩展位
2. **订阅查询与幂等能力先改造**
   - 从“全局活跃 meter_type”改为“按 meter_type 定向查询”
   - 从 `request_id` 幂等升级为 `billing_attempt_id`
3. **普通 Relay 主链路改造**
   - 从“循环外单次预扣”改成“选渠道后按 attempt 预扣”
4. **日志与前端补齐**
   - 渠道 billing_type 展示（已完成）
   - `channel_billing_type` 后端快照落库（已完成）
   - usage log 前端显示 `channel_billing_type`（待做）
5. **测试与灰度**
   - 先灰度 quota 渠道
   - 再灰度 request_count 渠道
6. **二期 Task 再启动**

---

## 4. 后端文件级改造清单

## 4.1 普通同步 Relay 主链路

### `controller/relay.go`

**当前现状**

- `Relay()` 在 `service.PreConsumeBilling(...)` 后才进入 `getChannel(...) / retry loop`
- 失败退款由外层 `defer` 统一 `relayInfo.Billing.Refund(c)` 处理
- 免费模型如果用户存在活跃 `request_count` 订阅，也会强制进入预扣链路

**一期改造目标**

把主流程调整为：

```text
估算 priceData
  -> getChannel(...)
  -> SetupContextForSelectedChannel(...)
  -> 生成 billing attempt
  -> 根据 channel.billing_type 调用 PreConsumeBillingForChannel(...)
  -> 转发请求
  -> 成功 SettleBilling(...)
  -> 失败仅退款本次 attempt
```

**具体改造点**

1. 删除循环外统一 `PreConsumeBilling(...)` 入口
2. 在 retry loop 内，`getChannel(...)` 成功之后再做预扣
3. 为每次渠道尝试生成唯一 `billing_attempt_id`
4. 将 `relayInfo.Billing` 生命周期从“整次外部请求一个 session”改成“每次 attempt 一个 session”
5. 明确失败路径：
   - 当前 attempt 预扣成功但下游失败 => 只退当前 attempt
   - 下一个 attempt 重新建 session，不复用上一个 session
6. 避免以下问题：
   - 外层 defer 退款 + 显式退款双重触发
   - 不同 attempt 串用同一个 `BillingSession`
   - 第一次 request_count、第二次 quota 时的串账
7. 免费模型逻辑要改成：
   - 不能只因为“用户有 request_count 订阅”就全局决定预扣
   - 必须等选到渠道后，再根据 `channel.billing_type` 判断是否需要预扣

**额外注意**

- `service.NormalizeViolationFeeError(...)`
- `service.ChargeViolationFeeIfNeeded(...)`
- `service.ShouldSkipRetryAfterChannelAffinityFailure(...)`

这些辅助分支都要复核是否依赖旧的“循环外单次预扣”假设。

---

## 4.2 渠道选择与上下文注入

### `controller/relay.go:getChannel(...)`
### `service/channel_select.go`
### `model/channel_cache.go`
### `middleware/distributor.go`
### `constant/context_key.go`

**当前现状**

- `getChannel(...)` 只负责按模型、分组、重试状态等选择渠道
- `middleware.SetupContextForSelectedChannel(...)` 在选中渠道后写入上下文
- 当前上下文中已经包含 `channel_billing_type`
- `relay/common/relay_info.go` 已从 context 读取并快照 `channel_billing_type`

**一期改造目标**

1. 为渠道选择链路补充 `billing_type` 感知
2. 允许在选择层做“可用计费资源过滤”，减少无效尝试
3. 但必须明确：**过滤只能减少无效重试，不能替代真正的预扣判定**

**具体改造点**

1. `model/channel.go`
   - `Channel` 结构新增 `BillingType string`
   - 默认值为 `quota`
2. `middleware/distributor.go`
   - `SetupContextForSelectedChannel(...)` 中把 `channel.billing_type` 写入 context
3. `constant/context_key.go`
   - 新增 `ContextKeyChannelBillingType`
4. `relay/common/relay_info.go`
   - 从 context 读取并快照 `channel_billing_type`
5. `service/channel_select.go` / `model/channel_cache.go`
   - 可选增加过滤逻辑：
     - 若渠道是 `request_count`，而用户无可用 request_count 订阅，可提前跳过
     - 若渠道是 `quota`，则允许正常进入后续预扣判定

**注意**

- 即便选择层提前过滤，也必须保留最终 `PreConsumeBillingForChannel(...)` 原子判定
- 因为并发下订阅余额可能在选择阶段、预扣前被其他请求抢占

---

## 4.3 计费入口与 BillingSession

### `service/billing.go`
### `service/billing_session.go`
### `service/funding_source.go`
### `relay/common/relay_info.go`

这是一期的核心改造面。

### `service/billing.go`

**当前现状**

- `PreConsumeBilling(...)` 只按“用户偏好 + 当前全局订阅状态”建 session
- 不接受渠道 billing_type 参数

**一期目标**

增加新的按渠道计费入口，建议类似：

- `PreConsumeBillingForChannel(...)`
- `NewBillingSessionForChannel(...)`

**建议行为**

- 输入至少包含：
  - `preConsumedQuota`
  - `relayInfo`
  - `channelBillingType`
  - `billingAttemptId`
- 输出仍然挂在 `relayInfo.Billing`

### `service/billing_session.go`

**当前现状**

- `NewBillingSession(...)` 先用 `HasActiveRequestCountSubscription(userId)` 把 preference 强制改成 `subscription_only`
- `trySubscription()` 再通过 `GetActiveUserSubscriptionMeterType(...)` 取一个全局活跃 meter_type
- 若是 `request_count`，固定预扣 1 次并跳过 token quota

**一期目标**

把“按全局唯一活跃 meter_type 路由”改成“按渠道 billing_type + 用户偏好联合路由”。

### 建议拆分后的逻辑

#### A. `request_count` 渠道

规则固定：

- 只允许 request_count 订阅
- 不看钱包优先 / 订阅优先
- 不回退钱包
- 无 request_count 可用订阅 => 直接失败

#### B. `quota` 渠道

规则如下：

- 优先尝试 quota 订阅
- quota 订阅不足时，再按用户 `billing_preference` 决定是否回退钱包
- `subscription_only` 在未来只约束 quota 资金池

**需要新增/调整的内部能力**

1. 按 meter_type 查询活跃订阅
2. 按 meter_type 创建 `SubscriptionFunding`
3. `SubscriptionFunding` 幂等键不能继续只用 `RequestId`
4. 引入 `billing_attempt_id`
5. `RelayInfo` 增加以下快照字段：
   - `ChannelBillingType`
   - `BillingAttemptId`
   - 如有必要，增加 `BillingAttemptNo` / `BillingAttemptSource`

### `service/funding_source.go`

**重点风险**

- `WalletFunding.Refund()` 非幂等，不能随便重试
- `SubscriptionFunding` 现有幂等主要依赖 `requestId`
- `PreConsume` 会使用 funding 内部保存的 amount，不完全信任外部传参

**一期改造要求**

1. `SubscriptionFunding` 的幂等键升级为 `billing_attempt_id`
2. attempt 级别做到：
   - 同一个 attempt 重试调用预扣 / 退款不会重复记账
   - 不同 attempt 切换渠道时不会互相覆盖
3. 钱包退款路径保持“只执行一次”
4. 对 request_count attempt 与 quota attempt 的退款日志做区分

---

## 4.4 订阅模型与按类型定向消费

### `model/subscription.go`

这是一期另一个核心改造面。

**当前现状**

现有关键函数都围绕“用户同时只有一种活跃类型”设计：

- `GetActiveUserSubscriptionMeterType(...)`
- `HasActiveRequestCountSubscription(...)`
- `EnsureUserActiveSubscriptionMeterTypeCompatibleTx(...)`
- 预扣记录与退款路径默认以单一 meter_type 心智实现

**一期目标**

不是简单删除互斥校验，而是要补齐“按 meter_type 定向查询 / 预扣 / 退款”的完整能力。

### 必改点

1. 新增按 meter_type 取活跃订阅的方法
   - 例如：`GetActiveUserSubscriptionByMeterType(...)`
   - 或返回列表后显式筛选
2. 新增按 meter_type 判断是否有可用订阅的方法
   - 替代只看全局的 `HasActiveRequestCountSubscription(...)`
3. 预扣逻辑要显式指定 meter_type
4. 退款 / 差额结算逻辑要显式指定 meter_type
5. 审核 `EnsureUserActiveSubscriptionMeterTypeCompatibleTx(...)`
   - 一期如果允许用户同时持有 quota + request_count 订阅，则该互斥校验必须放宽或改写
6. 保留“同类型订阅如何排序命中”的规则
   - 建议沿用当前 `end_time desc, id desc` 或者重新定义为“先到期先消费”
   - 文档与代码必须一致

### 幂等与记录

当前订阅预扣记录若仍只按 `request_id` 幂等，会出现问题：

- 第一次 attempt 选到 `request_count` 渠道，预扣 1 次
- 第二次 attempt 选到 `quota` 渠道，需要再做新的 quota 预扣
- 若仍共用同一个 request_id，会导致二次预扣被错误视为重复请求

因此必须：

- 订阅预扣记录主幂等键切换到 `billing_attempt_id`
- `request_id` 仅作为外部请求关联字段保留

---

## 4.5 日志快照与审计字段

### `relay/common/relay_info.go`
### `service/log_info_generate.go`
### `service/quota.go`
### `service/text_quota.go`
### `service/task_billing.go`
### `model/log.go`

**当前现状**

- usage log 已记录：
  - `billing_source`
  - `subscription_meter_type`
  - `subscription_id`
  - `subscription_pre_consumed`
  - `subscription_post_delta`
- consume / task / error log 已补齐 `channel_billing_type`
- `task.private_data.channel_billing_type` 已持久化
- 也没有 attempt 级 `billing_attempt_id`

**一期目标**

让后端日志能回答以下问题：

1. 本次请求最终命中了什么渠道计费类型？
2. 为什么扣的是次数，而不是额度？
3. 为什么没回退钱包？
4. 多次重试时，每次 attempt 的计费轨迹是什么？

### 当前已完成字段 / 后续建议

当前已完成：

- `channel_billing_type`

后续仍建议补充：

- `billing_attempt_id`
- `billing_attempt_index`
- 如有需要：`billing_decision`

### 普通同步请求日志

修改点：

- `service/log_info_generate.go:appendBillingInfo(...)`
- `service/quota.go`
- `service/text_quota.go`

要求：

- usage log / error log / refund log 都尽量带上 `channel_billing_type`
- 如果发生跨渠道重试，`admin_info.use_channel` 与 `billing_attempt_*` 要能对应起来

### Task 二期预留

- `service/task_billing.go`
- `model/task.go`

当前已完成：

- `task.private_data.channel_billing_type`

二期建议继续补齐：

- `task.private_data.billing_attempt_id`
- `task.private_data.subscription_meter_type`

---

## 4.6 Token 校验层

### `model/token.go`

**当前现状**

`ValidateUserToken(...)` 中，若用户存在活跃 `request_count` 订阅，则：

- token exhausted
- 或 `remain_quota <= 0`

仍可能被特殊放行。

**这会带来的结果**

未来若请求最终命中的是 `quota` 渠道，就可能出现：

- token 校验通过
- 但真正进入渠道计费判定后，因为没有 quota 订阅、也没有钱包余额而失败

这属于**延迟失败**，不是遗漏。

**一期处理策略**

- 暂不把渠道感知前移到 token 校验层
- 在实施文档、测试用例、交付说明里明确这是预期行为
- 错误文案应尽量清晰，避免用户误解为 token 校验 bug

---

## 4.7 违反策略附加扣费与异常路径

### `service/violation_fee.go`

**为什么要复核**

普通同步 Relay 从“循环外统一 session”切到“循环内 attempt session”后，异常路径要确认不会出现：

- 已退款后再次追加不正确扣费
- attempt 失败但仍沿用旧 session 状态

**实施要求**

1. 复核 `NormalizeViolationFeeError(...)`
2. 复核 `ChargeViolationFeeIfNeeded(...)`
3. 确认该逻辑与 request_count 渠道是否兼容
4. 明确 violation fee 是否只适用于 quota 路径

---

## 4.8 Task 二期预留文件

以下文件一期不改业务逻辑，但实施清单必须标记为**二期重点**：

- `relay/relay_task.go`
- `service/task_billing.go`
- `service/task_polling.go`
- `model/task.go`

### 二期要处理的关键问题

1. submit 阶段已预扣，不能照搬同步 Relay 方案
2. request_count 渠道是否跳过轮询阶段 quota 差额重算
3. submit 失败换渠道时，旧 attempt 与新 attempt 如何拆分
4. `TaskPrivateData` 是否足够保存双轨扣费快照
5. 退款 / 完成 / polling 幂等是否受影响

---

## 5. 数据模型与 migration 清单

## 5.1 `model/channel.go`

**新增字段**

建议新增：

- `BillingType string `json:"billing_type" gorm:"type:varchar(32);default:'quota';index"``

**字段约束建议**

- 合法值：`quota` / `request_count`
- 空值迁移后默认补 `quota`

## 5.2 `model/main.go`

**迁移入口**

当前 migration 入口在：

- `migrateDB()`
- `migrateDBFast()`

**实施要求**

1. `AutoMigrate(&Channel{})` 能补充新列
2. 若 SQLite 对默认值 / 索引兼容不足，补专门迁移函数
3. 若需要修历史脏数据，新增类似：
   - `migrateChannelBillingTypeDefaults()`

## 5.3 订阅预扣记录表

需要评估 `SubscriptionPreConsumeRecord` 对幂等键的支持是否足够。

### 建议新增字段

- `billing_attempt_id`
- 如保留 `request_id`，则两者职责区分为：
  - `request_id`：一次外部请求关联
  - `billing_attempt_id`：一次实际渠道预扣 attempt 幂等键

## 5.4 Task 私有数据结构（二期）

### `model/task.go`

建议二期为 `TaskPrivateData` 增加：

- `BillingType string`
- `BillingAttemptId string`
- 如有需要，增加 `BillingAttemptIndex int`

以便跨 submit / polling / refund 排查。

---

## 6. 前端文件级改造清单

## 6.1 渠道管理页

### `web/src/components/table/channels/modals/EditChannelModal.jsx`
### `web/src/components/table/channels/ChannelsColumnDefs.jsx`
### `web/src/components/table/channels/ChannelsTable.jsx`

**目标**

让管理员可以配置、查看 `channel.billing_type`。

### 改造点

1. `EditChannelModal.jsx`
   - 表单新增 `billing_type`
   - 默认值为 `quota`
   - 帮助文案要明确：
     - `quota`：优先额度订阅，不足可回退钱包
     - `request_count`：仅次数订阅，不可回退钱包
2. `ChannelsColumnDefs.jsx`
   - 新增“计费类型”列
   - 可显示成标签：`额度` / `计次`
3. `ChannelsTable.jsx`
   - 表格数据透传新列
4. 如已有列可见性选择器，也要把新列加进去

---

## 6.2 订阅管理与购买页

### `web/src/components/table/subscriptions/modals/AddEditSubscriptionModal.jsx`
### `web/src/components/table/users/modals/UserSubscriptionsModal.jsx`
### `web/src/components/topup/SubscriptionPlansCard.jsx`

**一期目标**

这些页面不一定需要结构性重写，但必须确认文案与展示符合双轨心智。

### 改造点

1. `AddEditSubscriptionModal.jsx`
   - 已支持 `meter_type`，但文案要更明确：
     - quota 订阅是“额度池”
     - request_count 订阅是“次数池”
2. `UserSubscriptionsModal.jsx`
   - 用户同时持有多类订阅时，展示不要误导成“只有一个总订阅”
3. `SubscriptionPlansCard.jsx`
   - 当前有“存在 request_count 订阅时会优先按订阅计次”的说明
   - 一期后要修正文案为：
     - 是否计次由最终命中的**渠道 billing_type** 决定
     - 不是所有请求只要有 request_count 订阅就一定按次扣

---

## 6.3 使用日志页面

### `web/src/components/table/usage-logs/UsageLogsColumnDefs.jsx`
### `web/src/hooks/usage-logs/useUsageLogsData.jsx`

**当前现状**

前端已识别：

- `billing_source`
- `subscription_meter_type`

但还不知道：

- `channel_billing_type`
- `billing_attempt_id`

### 改造点

1. 在日志详情中新增：
   - 渠道计费类型
   - attempt id
2. `renderBillingTag(...)` 可按后端快照显示：
   - `订阅计次`
   - `订阅抵扣`
   - 必要时增加 `钱包扣费`
3. 展开面板中增加说明：
   - 这是因为命中了 `request_count` 渠道
   - 或命中了 `quota` 渠道
4. 若后端返回重试 attempt 信息，可增加更细的排错展示

---

## 7. 推荐开发顺序（工程视角）

## 第 1 步：数据结构先行

优先改：

- `model/channel.go`
- `model/main.go`
- `constant/context_key.go`
- `relay/common/relay_info.go`

产物：

- 渠道 billing_type 可存、可读、可传上下文
- RelayInfo 能保存 channel billing 快照

## 第 2 步：订阅按类型能力补齐

优先改：

- `model/subscription.go`
- `service/funding_source.go`
- `service/billing_session.go`

产物：

- 可以显式按 `quota` / `request_count` 找订阅
- 可以显式按类型预扣 / 退款
- attempt 级幂等可成立

## 第 3 步：普通 Relay 主链路重排

优先改：

- `controller/relay.go`
- `service/billing.go`
- `middleware/distributor.go`
- `service/channel_select.go`

产物：

- 真正实现“先选渠道，再按渠道 billing_type 预扣”

## 第 4 步：日志与前端补齐

优先改：

- `service/log_info_generate.go`
- `service/quota.go`
- `service/text_quota.go`
- `web/src/components/table/channels/*`
- `web/src/components/table/usage-logs/*`

产物：

- 管理端可配置
- 日志可解释

## 第 5 步：灰度与回归

- 先在测试环境开启
- 先灰度 `quota` 渠道
- 再灰度少量 `request_count` 渠道

---

## 8. 测试清单

## 8.1 单元测试

### 订阅模型层

重点覆盖：

1. 用户同时持有 quota + request_count 订阅
2. 按 meter_type 查询命中正确订阅
3. 互斥校验放宽后，不影响同类型订阅逻辑
4. attempt 级幂等：
   - 同 attempt 重复预扣不重复扣
   - 不同 attempt 可分别预扣

### BillingSession

重点覆盖：

1. `request_count` 渠道：
   - 有次数订阅 => 成功预扣 1 次
   - 无次数订阅 => 失败
   - 钱包余额充足也不能回退钱包
2. `quota` 渠道：
   - 有额度订阅 => 订阅扣费
   - 额度不足 + wallet_first => 钱包扣费
   - 额度不足 + subscription_only => 失败
3. 免费模型：
   - 命中 quota 渠道是否跳过预扣
   - 命中 request_count 渠道是否仍需按次预扣

## 8.2 集成测试

### 普通同步 Relay

场景至少包括：

1. 单渠道 quota
2. 单渠道 request_count
3. 首次渠道失败，第二次切到同类型渠道
4. 首次 request_count 渠道失败退款，第二次切 quota 渠道成功
5. 首次 quota 渠道失败退款，第二次切 request_count 渠道成功
6. 渠道过滤命中后仍被并发抢占，最终预扣失败

## 8.3 回归测试

必须复测：

- 纯钱包用户不受影响
- 只有 quota 订阅用户不受影响
- 只有 request_count 订阅用户在 request_count 渠道能正常使用
- 免费模型日志与扣费逻辑
- 违规附加扣费路径
- 多 key 渠道
- channel affinity 失败跳过重试逻辑

## 8.4 前端联调测试

1. 渠道编辑页新增 billing_type 可保存 / 回显
2. 渠道列表能看到 billing_type
3. usage logs 正确显示：
   - billing_source（已支持）
   - subscription_meter_type（已支持）
   - channel_billing_type（待前端展示）
   - billing_attempt_id（待后端 + 前端补齐）
4. 同一请求多次重试时，日志说明不混乱

---

## 8.5 当前已完成验证（截至当前）

- 后端相关改动已通过 `go build ./middleware ./relay/... ./controller ./model ./service`
- 前端渠道管理相关改动已通过定向 ESLint 与 `npm run build -- --logLevel error`
- `go test ./controller ./model` 仍受外网依赖下载限制，当前以本地构建验证为主

---

## 9. 灰度与上线顺序

建议按以下顺序发布：

### 阶段 A：先上线数据结构与前端展示

包含：

- `channel.billing_type`
- 前端渠道编辑 / 展示
- 日志快照字段兼容读取

此阶段默认所有旧渠道都还是 `quota`，业务行为不变。

### 阶段 B：上线后端按类型订阅能力

包含：

- 按 meter_type 查订阅
- attempt 级幂等
- 新 `BillingSession` 分流逻辑

但先不把渠道切到 `request_count`。

### 阶段 C：上线普通 Relay 新主链路

包含：

- 先选渠道再预扣
- 失败 attempt 退款
- 新日志快照

此阶段仍建议只在测试 / 小流量分组启用。

### 阶段 D：小规模启用 request_count 渠道

选择少量渠道做试点，观察：

- 失败退款是否正确
- 钱包是否被误扣
- usage logs 是否足够解释问题

### 阶段 E：二期 Task 启动

普通 Relay 稳定后，再进入 Task 双轨扣费。

---

## 10. 风险清单

## 10.1 最大技术风险

1. **普通 Relay 从循环外单次预扣切到循环内 attempt 预扣**
   - 最容易引入双退款 / 漏退款 / 串 session
2. **订阅幂等从 request_id 升级到 billing_attempt_id**
   - 需要兼顾历史记录与新逻辑
3. **用户同时持有双类型订阅后，旧函数语义全面变化**
   - 不能只改一个函数
4. **免费模型与 request_count 订阅关系复杂**
   - 旧逻辑是“有 request_count 就预扣”，新逻辑必须变成“命中 request_count 渠道才预扣”
5. **token 放行与最终扣费解耦**
   - 会存在延迟失败，需要客服与日志可解释性支撑

## 10.2 最大产品风险

1. 管理员配置错渠道 billing_type，导致用户预期错误
2. 用户以为“买了次数订阅，所有请求都一定按次扣”
3. 旧日志无法解释新行为，导致排障困难

---

## 11. 交付物检查表

本轮实施清单完成后，后续开发前应逐项确认：

- [ ] 根目录方案文档与本实施清单一致
- [ ] Channel 新字段与 migration 方案明确
- [ ] 订阅按 meter_type 定向查询方案明确
- [ ] billing_attempt_id 幂等方案明确
- [ ] 普通 Relay 新主链路顺序明确
- [ ] 日志快照字段列表明确
- [ ] 前端渠道与日志展示落点明确
- [ ] 一期 / 二期边界明确
- [ ] 测试清单与灰度顺序明确

---

## 12. 本文对应的关键文件索引

### 后端主链路

- `controller/relay.go`
- `service/billing.go`
- `service/billing_session.go`
- `service/funding_source.go`
- `service/channel_select.go`
- `middleware/distributor.go`
- `service/violation_fee.go`

### 数据模型 / migration

- `model/channel.go`
- `model/subscription.go`
- `model/token.go`
- `model/task.go`
- `model/log.go`
- `model/main.go`
- `model/channel_cache.go`

### 日志 / 审计

- `service/log_info_generate.go`
- `service/quota.go`
- `service/text_quota.go`
- `service/task_billing.go`

### 前端

- `web/src/components/table/channels/modals/EditChannelModal.jsx`
- `web/src/components/table/channels/ChannelsColumnDefs.jsx`
- `web/src/components/table/channels/ChannelsTable.jsx`
- `web/src/components/table/usage-logs/UsageLogsColumnDefs.jsx`
- `web/src/hooks/usage-logs/useUsageLogsData.jsx`
- `web/src/components/table/subscriptions/modals/AddEditSubscriptionModal.jsx`
- `web/src/components/table/users/modals/UserSubscriptionsModal.jsx`
- `web/src/components/topup/SubscriptionPlansCard.jsx`

---

## 13. 最终建议

如果后续马上进入开发，推荐第一批 PR 按下面拆分：

1. **PR-1：数据结构 + migration + 管理端渠道 billing_type**
2. **PR-2：订阅按 meter_type 定向能力 + attempt 幂等**
3. **PR-3：普通 Relay 主链路重排 + 日志快照**
4. **PR-4：usage logs 展示与文案收口**
5. **PR-5：Task 二期（单独评审）**

这样可以把最高风险点隔离在普通 Relay 一期，避免 Task 与同步链路同时爆炸。
