# 次数订阅实施方案

## 1. 目标

基于当前项目的订阅体系，新增一种“按请求次数扣减”的订阅套餐。

示例：

- `5 小时内最多 100 次`
- `1 天内最多 1000 次`
- `1 个月内最多 10000 次`

本方案只讨论：

1. 次数订阅
2. 加在哪一层
3. 一期如何稳妥落地

不包含：

1. Token 数量订阅
2. 每个模型独立次数上限

## 2. 先说结论

次数订阅**不要加在渠道上**。

应该加在这三层：

1. `SubscriptionPlan`：套餐模板层
2. `UserSubscription`：用户持有的订阅实例层
3. `BillingSession / SubscriptionFunding`：请求计费执行层

原因：

1. 现有订阅本来就是按用户生效，不是按渠道生效
2. 普通用户创建的是 token，不是“渠道绑定 key”
3. 一个模型可以同时来自多个渠道，运行时还会自动切换渠道
4. 如果把次数订阅绑在渠道上，同一个请求落到不同渠道会导致计费语义混乱

所以次数订阅的正确语义应该是：

- **按用户的一次外部请求计 1 次**
- 不是按内部命中的渠道计次

## 3. 现有架构事实

### 3.1 订阅绑定对象

当前订阅绑定在用户上：

- `SubscriptionPlan`
- `UserSubscription`

关键点：

1. `UserSubscription` 有 `user_id`
2. 没有 `channel_id`
3. 订阅扣减入口 `PreConsumeUserSubscription(...)` 也是按 `userId` 查活跃订阅

结论：

- 当前订阅是**用户级资金来源**
- 不是渠道级资源

### 3.2 普通用户如何使用渠道

普通用户不能直接选渠道。

普通用户创建 token 时，能配置的是：

1. `group`
2. `model_limits`

真正请求时，系统根据：

1. token 的分组
2. 请求的模型
3. 当前可用渠道能力表

自动选择渠道。

结论：

- 用户感知的是“分组 + 模型”
- 不感知具体渠道

### 3.3 多个渠道提供同一个模型时

同一个 `group + model` 可以由多个渠道提供。

系统会按：

1. `priority`
2. `weight`
3. `retry`

自动选择和切换渠道。

结论：

- 渠道只是后端路由实现
- 不适合作为次数订阅的计费归属对象

## 4. 次数订阅应该挂在哪

### 4.1 套餐定义层：挂在 `SubscriptionPlan`

建议新增字段：

- `meter_type`

一期只增加一个新值：

- `request_count`

已有套餐默认：

- `quota`

这样老逻辑保持不变。

### 4.2 用户订阅实例层：挂在 `UserSubscription`

建议同步新增：

- `meter_type`

创建订阅实例时，从套餐快照下来。

这是必须的，因为：

1. 套餐后续可能被管理员修改
2. 历史已购订阅不能跟着变计费语义

### 4.3 执行层：挂在 `BillingSession`

真正扣减逻辑要落在现有请求计费生命周期里，而不是落在渠道选择里。

最合适的入口仍然是：

1. `PreConsumeBilling`
2. `BillingSession`
3. `SubscriptionFunding`

原因：

1. 这里代表“一个外部请求”的生命周期
2. 它在渠道重试循环之外
3. 可以天然避免“同一个请求因为换渠道被重复计次”

## 5. 一期推荐语义

### 5.1 次数的定义

一次外部 API 请求，成功完成计费，算 `1` 次。

包括：

1. 普通同步请求算 1 次
2. 流式请求算 1 次
3. 异步任务提交成功并最终成功算 1 次

不包括：

1. 同一个请求内部的渠道重试
2. 最终失败并退款的请求
3. 免费模型也仍然计次，只要该请求最终走的是 `request_count` 订阅

### 5.2 周期的定义

继续复用现有订阅重置机制：

- `quota_reset_period`
- `quota_reset_custom_seconds`

例如：

- `5 小时 = 18000 秒`

所以：

- `5 小时 100 次`

可以直接表示为：

1. `meter_type = request_count`
2. `total_amount = 100`
3. `quota_reset_period = custom`
4. `quota_reset_custom_seconds = 18000`

### 5.3 扣减方式

一期建议非常简单：

- 每个成功请求固定扣 `1`

不要一开始就引入：

1. 不同模型扣不同次数单位
2. 每个模型独立次数上限

原因：

1. 一期先把“次数订阅”主链路跑通更稳
2. 当前架构对“统一次数池”天然友好
3. 复杂模型规则应当作为后续增强

## 6. 后端设计

### 6.1 数据结构改造

涉及文件：

- `model/subscription.go`
- `model/main.go`

建议新增字段：

1. `subscription_plans.meter_type`
2. `user_subscriptions.meter_type`

可选值：

1. `quota`
2. `request_count`

建议默认值：

- `quota`

### 6.2 套餐创建与更新

涉及文件：

- `controller/subscription.go`

需要做的事：

1. 创建套餐时允许传 `meter_type`
2. 更新套餐时允许更新 `meter_type`
3. 若为 `request_count`，校验 `total_amount > 0`

建议再加一条业务校验：

- `request_count` 套餐必须有明确的 `total_amount`

### 6.3 创建用户订阅实例

涉及文件：

- `model/subscription.go`

在 `CreateUserSubscriptionFromPlanTx` 中：

1. 将 `plan.MeterType` 写入 `UserSubscription`
2. 继续沿用 `AmountTotal / AmountUsed`

这样：

1. `quota` 套餐时，`AmountUsed` 表示已消费额度
2. `request_count` 套餐时，`AmountUsed` 表示已消费次数

## 7. 请求计费怎么接

### 7.1 同步请求

入口仍然是：

- `controller/relay.go`

当前特点很好：

1. 先建立 `BillingSession`
2. 然后才进入渠道选择和重试

这正适合次数订阅。

建议语义：

1. 如果本次订阅类型是 `request_count`
2. 请求进入时预扣 `1`
3. 如果最终失败，退款 `1`
4. 如果最终成功，保留 `1`

这样内部无论重试多少个渠道，都只算用户发起的这 1 次请求。

### 7.2 异步任务

入口仍然是：

- `relay/relay_task.go`
- `service/task_billing.go`
- `service/task_polling.go`

建议语义：

1. 任务提交前预扣 `1`
2. 提交失败则退款 `1`
3. 提交成功后，如果后续轮询判定最终失败，则退款 `1`
4. 最终成功则保留 `1`

这样异步任务也和同步请求保持一致。

## 8. Billing 层建议改法

### 8.1 新增“订阅计量类型识别”

建议在订阅资金来源上能拿到当前订阅实例或其 `meter_type`。

推荐做法：

1. 在 `SubscriptionFunding.PreConsume` 成功后拿到订阅实例信息
2. 将 `meter_type` 同步到 `relayInfo` 或 `BillingSession`

### 8.2 `request_count` 的预扣值

对于次数订阅：

- 固定预扣 `1`

而不是使用当前 `priceData.QuotaToPreConsume`

因为次数订阅与模型价格、token 数、倍率无关。

### 8.3 结算行为

对于次数订阅：

- 结算时不需要按 `actualQuota - preConsumedQuota` 再做差额

原因：

1. 次数就是固定的 `1`
2. 成功保留，失败退款即可

所以对 `request_count` 而言：

1. `Settle` 基本不做增量补扣
2. `Refund` 仍然重要
3. 外层日志和订阅剩余通知也应按“固定消耗 1 次”处理

## 9. 和现有 token 额度的冲突

这是一期必须正视的问题。

当前 token 还有自己的：

1. `remain_quota`
2. `unlimited_quota`

并且 token 额度校验发生得很早。

但次数订阅的单位不是 quota，所以会产生语义冲突：

1. 用户次数订阅还剩 80 次
2. token key 自己的 quota 却先耗尽
3. 请求被 token 额度拦截

### 一期实现

一期已经直接在运行时处理这类冲突：

1. 如果用户存在激活中的 `request_count` 订阅
2. token 即使处于 `remain_quota <= 0` 或 `status = exhausted`
3. 认证层仍允许该请求继续进入订阅计次链路

同时保留这些硬限制不变：

1. `disabled` token 仍然拒绝
2. `expired` token 仍然拒绝
3. token 的 IP 限制、分组权限等其他校验仍然照常生效

这样可以避免“用户次数订阅还有剩余，但因为 token quota 为 0 被提前拦截”的问题。

## 10. 和现有 quota 订阅的关系

当前系统原本只区分：

1. 走订阅
2. 走钱包

现在已经补充了 `request_count` 的运行时语义：

1. 如果用户持有激活中的 `request_count` 订阅
2. 则本次请求会强制走订阅计次
3. 不再受 `wallet_first` / `wallet_only` 影响

这样可以避免：

1. token 因 quota 为 0 在钱包路径被拦截
2. 用户明明买了次数订阅却仍然消耗钱包额度

它仍然不区分：

1. 走 quota 订阅
2. 走 request_count 订阅

因此如果一个用户同时持有两类活跃订阅，当前架构仍然会很难判定先扣哪个。

### 一期建议

增加一个明确限制：

- **同一用户不允许同时持有不同 `meter_type` 的活跃订阅**

允许：

1. 多个 `quota` 订阅
2. 多个 `request_count` 订阅

不允许：

1. `quota` 和 `request_count` 混用

这样可以避免 Billing 层出现二义性。

## 11. 前端管理方案

涉及文件：

- `web/src/components/table/subscriptions/modals/AddEditSubscriptionModal.jsx`
- `web/src/components/table/subscriptions/SubscriptionsColumnDefs.jsx`
- `web/src/components/topup/SubscriptionPlansCard.jsx`

建议改造：

1. 套餐管理页新增“计量类型”
2. 可选值：
   - `额度`
   - `次数`

如果选择 `次数`：

1. `total_amount` 的表单标签显示为“周期总次数”
2. 用户侧套餐展示为：
   - `5 小时 / 100 次`
   - `1 天 / 1000 次`

不要继续统一显示成“额度”。

## 12. 新方案建议落地顺序

### 阶段 1：数据与展示

1. 数据库增加 `meter_type`
2. 后端 API 支持读写 `meter_type`
3. 管理端新增套餐计量类型配置
4. 用户端把 `request_count` 套餐展示为“次数”

### 阶段 2：同步请求计次

1. `BillingSession` 支持 `request_count`
2. 请求进入预扣 `1`
3. 请求失败退款 `1`
4. 请求成功保留 `1`

### 阶段 3：异步任务计次

1. 提交前预扣 `1`
2. 失败退款 `1`
3. 最终失败退款 `1`
4. 最终成功保留 `1`

### 阶段 4：约束补齐

1. 禁止同用户同时持有不同 `meter_type` 的活跃订阅
2. 如有需要，再补充 token 管理侧对 `request_count` 用户的引导或状态展示优化

## 13. 二期扩展方向

如果后面真的需要“不同模型区别对待”，建议也**不要绑渠道**。

正确扩展方向应是：

- `套餐 + 分组/模型规则`

而不是：

- `套餐 + 渠道`

原因：

1. 同一模型可能来自多个渠道
2. 用户不感知渠道
3. 渠道会被自动切换

二期可以考虑新增规则表，例如：

- `subscription_plan_request_rules`

字段可以包含：

1. `plan_id`
2. `group_name`
3. `model_pattern`
4. `consume_units`
5. `enabled`

但这不建议放进一期。

## 14. 最终建议

这次“次数订阅”最合理的落点是：

1. **定义在套餐上**
2. **快照到用户订阅实例上**
3. **执行在请求计费层上**
4. **不绑定渠道**
5. **不跟渠道选择逻辑耦合**

如果只做一期，我建议目标就收敛成一句话：

- **复用现有订阅重置机制，新增 `request_count` 类型套餐，让每个成功外部请求扣 1 次。**

这是和当前项目架构最匹配、风险最低、也最容易上线的做法。
