# 订阅双轨扣费方案设计文档

> 文档版本：v1.1  
> 文档目标：基于当前代码真实实现，设计“订阅同时支持次数池 + 金额池，并按渠道计费类型扣费”的落地方案。本文只输出方案，不直接改业务代码。

---

## 一、需求目标与边界

### 1.1 本次目标

用户提出的新规则可以归纳为四条：

1. **订阅侧要同时支持两种资源能力**：
   - 次数池（`request_count`）
   - 金额池 / 额度池（`quota`）
2. **渠道侧新增计费方式类型**：
   - `request_count`：按次数计费
   - `quota`：按金额 / 额度计费
3. **命中按次数渠道时**：
   - 只能消耗订阅次数池
   - 次数耗尽后，该类渠道对该用户不可继续使用
   - 不能回退用户余额
4. **命中按金额渠道时**：
   - 优先消耗订阅金额池
   - 金额池耗尽后，按现有偏好规则回退用户余额

### 1.2 本文不做的事

1. 不直接修改代码。
2. 不在本轮实现新的支付商品体系。
3. 不在本轮强行重构整个 Task 计费状态机。
4. 不把“渠道余额”与“用户余额 / quota”混为一谈；本文讨论的是**用户侧扣费来源**，不是上游渠道余额扣减。

### 1.3 术语约定

| 术语 | 含义 |
|---|---|
| 次数池 | 订阅中按请求次数计量的资源池，对应 `request_count` |
| 金额池 / 额度池 | 订阅中按 quota 计量的资源池，对应 `quota` |
| 渠道计费类型 | 新增到 Channel 的 `billing_type`，决定本次请求应该使用哪条扣费规则 |
| 钱包 | 用户现有 `quota` / 余额扣费路径 |
| 外部请求 | 用户一次 API 调用 |
| 计费尝试 | 外部请求在一次具体渠道尝试上的预扣 / 结算 / 退款生命周期 |

---

## 二、当前代码现状（已复核）

本节只写已经被代码确认过的现状，用于约束后续方案。

### 2.1 订阅并不是“展示态”，而是已深度接入真实扣费链路

当前订阅已是真实资金来源之一，会与钱包并行参与：

- 预扣：`service.PreConsumeBilling(...)`
- 结算：`BillingSession.Settle(...)`
- 退款：`BillingSession.Refund(...)`

相关核心实现位于：

- `service/billing.go`
- `service/billing_session.go`
- `service/funding_source.go`
- `model/subscription.go`

### 2.2 当前已有两种订阅计量类型

`model/subscription.go` 已存在：

- `SubscriptionMeterQuota = "quota"`
- `SubscriptionMeterRequestCount = "request_count"`

且 `SubscriptionPlan.MeterType`、`UserSubscription.MeterType` 已实际生效。

### 2.3 当前系统默认“同一用户同一时刻只有一种活跃订阅计量类型”

这不是文档约定，而是当前实现前提：

- `EnsureUserActiveSubscriptionMeterTypeCompatibleTx(...)`
- `HasActiveRequestCountSubscription(...)`
- `GetActiveUserSubscriptionMeterType(...)`
- `NewBillingSession(...)` 中对 request_count 的特殊逻辑

都围绕“全局单一活跃 meter_type”设计。

### 2.4 request_count 一期方案已经真正接入运行时

当前 request_count 订阅不是空壳：

- 若用户有活跃 `request_count` 订阅，`NewBillingSession(...)` 会强制偏好为 `subscription_only`
- `trySubscription()` 命中 `request_count` 时，会固定预扣 1 次
- 该路径会跳过 token/quota 预扣
- 结算时保留这 1 次，不再走 token/quota 差额补扣
- 失败时会退款这 1 次

### 2.5 普通 Relay 当前是“先预扣，再选渠道”

这是当前最大架构约束。

普通请求主链路可概括为：

```text
用户请求
  → controller/relay.go: Relay()
  → 估算 priceData
  → service.PreConsumeBilling(...)
  → getChannel(...) / retry loop
  → relay handler
  → 成功后在 quota/text 等 helper 内 SettleBilling(...)
  → 失败时走 Refund(...)
```

也就是说：

- 当前普通 Relay 在**渠道选择之前**就已经建立了 BillingSession
- 这与“按最终选中的渠道计费类型决定扣次数还是扣金额”直接冲突

### 2.6 Task 链路不是“无预扣”，而是已有独立计费状态机

这点必须特别修正。

当前 Task 链路中：

- `relay/relay_task.go` 的 submit 阶段已经会做 `PreConsumeBilling`
- `service/task_billing.go` 已有：
  - `RefundTaskQuota(...)`
  - `RecalculateTaskQuota(...)`
- `service/task_polling.go` 会在失败 / 超时 / 成功轮询阶段触发退款或差额结算

因此：

> Task 不是“尚未接入预扣”的空白区，而是已经有自己的预扣、退款、异步轮询结算体系。

### 2.7 Token 鉴权已对 request_count 订阅做 exhausted 放行

`model/token.go: ValidateUserToken(...)` 当前有特殊逻辑：

- 若 token exhausted 或 `remain_quota <= 0`
- 但用户有活跃 `request_count` 订阅
- 则仍可能放行请求

这意味着 request_count 订阅不仅影响扣费来源，也影响**鉴权准入**。

### 2.8 订阅预扣幂等依赖 `request_id` 唯一记录

`model/subscription.go` 当前通过 `SubscriptionPreConsumeRecord` 做幂等：

- `PreConsumeUserSubscription(requestId, ...)`
- `RefundSubscriptionPreConsume(requestId)`

其核心前提是：

- 同一个订阅预扣动作，使用唯一 `request_id`
- 退款也依赖同一个 `request_id`

这会直接影响后续“循环内多次渠道尝试”的设计。

### 2.9 Channel 当前没有计费类型字段

`model/channel.go` 当前 `Channel` 结构里没有可直接承载本需求的 `billing_type` 字段，因此后续新增字段是合理且必要的。

---

## 三、核心架构矛盾

### 3.1 需求要求“按渠道扣费”

新规则的本质是：

- 选中 `request_count` 渠道，必须走次数池
- 选中 `quota` 渠道，必须走金额池 / 钱包

### 3.2 现状是“先扣费，后选渠道”

普通 Relay 当前在拿到最终渠道之前就已经完成预扣。

因此，若不调整链路顺序，就会出现根本矛盾：

- 预扣时还不知道该扣次数还是金额
- 渠道选出来之后，已完成的预扣可能与实际渠道不匹配

### 3.3 结论

若要严格满足需求，**普通同步 Relay 必须改成“先选渠道，再按渠道计费类型预扣”**。

这不是优化项，而是核心架构调整。

---

## 四、推荐总体方案

### 4.1 总体思路

将本次改造拆成两层：

1. **运行时双资源能力**：让用户可同时拥有可用的次数池和金额池
2. **渠道计费感知**：让普通 Relay 在选定渠道后，再决定本次走哪条资金路径

### 4.2 分期策略

#### 一期：只改普通同步 Relay

一期目标：

- Channel 新增 `billing_type`
- 普通 Relay 改成“先选渠道，再预扣”
- `request_count` 渠道只扣次数池，不回退钱包
- `quota` 渠道优先扣金额池，不足时按现有偏好回退钱包
- 补齐日志审计与幂等设计

#### 二期：再改 Task 双轨计费

Task 链路已存在独立 submit / polling / refund / recalculate 状态机，改动面更广：

- submit 阶段已预扣
- 轮询阶段仍可能失败或补扣
- 任务可能跨请求、跨时间窗口完成

因此不建议在一期混改。

### 4.3 对“订阅同时有次数和金额”的推荐落地语义

这里需要区分：

1. **运行时语义**：用户同时拥有次数池 + 金额池能力
2. **商品语义**：是否一定要“一条订阅记录 / 一个套餐记录同时持有两种池”

#### 推荐结论

**一期先实现“运行时双资源能力”，不强制把 `UserSubscription` 结构改成双字段单记录。**

即：

- 底层仍允许存在两类订阅资源实例：
  - `quota`
  - `request_count`
- 但系统层面要支持它们**同时活跃并可被定向消费**
- 若产品 later 需要“一个商品同时售卖两池”，可在购买层再做组合套餐或一次购买发两条资源实例

#### 为什么不建议本轮直接改成“单记录双字段”

直接把 `UserSubscription` 改成一条记录同时带两套总量 / 已用字段，虽然更贴近“一个订阅同时有两池”的产品表达，但会同时改动：

- 购买 / 升级 / 发放
- 重置任务
- 到期任务
- 管理后台
- 展示前端
- 历史数据迁移

本轮目标是先把**扣费链路**做对，因此推荐先解决运行时能力与渠道感知。

---

## 五、数据模型与上下文变更

## 5.1 Channel 新增 `billing_type`

在 `model/channel.go` 的 `Channel` 结构体新增字段：

```go
type Channel struct {
    // ... existing fields ...

    // quota: 按金额/额度计费（默认）
    // request_count: 按次数计费
    BillingType string `json:"billing_type" gorm:"type:varchar(32);not null;default:'quota'"`
}
```

建议常量：

```go
const (
    ChannelBillingTypeQuota        = "quota"
    ChannelBillingTypeRequestCount = "request_count"
)
```

### 5.1.1 字段语义

| 值 | 含义 | 资金来源规则 |
|---|---|---|
| `quota` | 按金额 / 额度计费 | 优先订阅金额池，不足时按偏好回退钱包 |
| `request_count` | 按次数计费 | 只能走订阅次数池，不得回退钱包 |

### 5.1.2 兼容策略

- 默认值为 `quota`
- 存量渠道不需要立即人工处理
- 未配置新值的渠道保持现有行为

## 5.2 RelayInfo / Task / 日志增加“渠道计费类型快照”

仅在 Channel 表里新增字段不够，建议同步在运行时上下文中增加：

```go
type RelayInfo struct {
    // ... existing fields ...
    ChannelBillingType string
    BillingAttemptId   string
}
```

并要求在以下位置保留快照：

1. `RelayInfo`
2. Task 的 `private_data`
3. consume log 的 `other`
4. task billing log 的 `other`

原因：

- 渠道配置可能在事后被修改
- Task 会跨阶段、跨轮询完成
- 排障时需要明确知道“本次为什么扣次数 / 为什么没回退钱包”

## 5.3 订阅查询 / 预扣接口要改为“按 meter_type 定向”

这是本方案与旧文档的关键差异。

当前不能只删除 `EnsureUserActiveSubscriptionMeterTypeCompatibleTx(...)`，还必须把订阅消费能力改成**按 meter_type 定向查询和消费**。

建议新增或改造以下接口：

```go
HasActiveSubscriptionByMeterType(userId int, meterType string) (bool, error)
GetActiveSubscriptionsByMeterType(userId int, meterType string) ([]UserSubscription, error)
PreConsumeUserSubscriptionByMeterType(requestId string, userId int, meterType string, amount int64) (*SubscriptionPreConsumeResult, error)
```

### 5.3.1 为什么必须这样做

因为未来会同时存在两类资源池：

- `request_count` 渠道只能找 `request_count` 资源池
- `quota` 渠道只能找 `quota` 资源池

如果仍沿用当前“从所有 active 订阅里找可扣的”逻辑，会让消费目标变得不明确。

## 5.4 是否立即解除 `meter_type` 活跃互斥

### 推荐策略

**可以解除“不同 meter_type 不可共存”的限制，但不能只做这一步。**

必须同步完成：

1. 按 meter_type 定向查询
2. 按 meter_type 定向预扣
3. 按 meter_type 定向退款
4. 相关鉴权 / 偏好逻辑重写

否则系统会进入“数据允许共存，但运行时不会正确消费”的半完成状态。

---

## 六、扣费决策与主链路重构

## 6.1 新的扣费决策树

```text
用户请求
  → 选中渠道
    → channel.billing_type = request_count ?
        → 是：只允许消费 request_count 订阅
              → 成功：固定预扣 1 次
              → 失败：本渠道不可用，不得回退钱包
        → 否（quota）：
              → 优先消费 quota 订阅
              → 订阅金额不足时，再按现有偏好回退钱包
```

## 6.2 普通 Relay 一期改造：改成“先选渠道，再预扣”

### 6.2.1 现有流程（简化）

```text
计算 priceData
→ 预扣 PreConsumeBilling
→ 重试选渠道
→ 转发
→ 成功结算 / 失败退款
```

### 6.2.2 目标流程（一期）

```text
计算 priceData
→ for retry {
     选渠道
     生成本次 billing attempt
     按 channel.billing_type 预扣
     转发
     成功：结算并返回
     失败：退款本次 attempt，继续重试
   }
```

### 6.2.3 伪代码

```go
for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
    channel, channelErr := getChannel(c, relayInfo, retryParam)
    if channelErr != nil {
        break
    }

    relayInfo.ChannelBillingType = normalizeChannelBillingType(channel.BillingType)
    relayInfo.BillingAttemptId = buildBillingAttemptId(relayInfo.RequestId, retryParam.GetRetry(), channel.Id)

    if shouldPreConsume(priceData, relayInfo) {
        apiErr := service.PreConsumeBillingForChannel(
            c,
            priceData.Quota,
            relayInfo,
            channel,
            relayInfo.BillingAttemptId,
        )
        if apiErr != nil {
            if isChannelBillingMismatchOrResourceExhausted(apiErr, relayInfo.ChannelBillingType) {
                recordChannelBillingSkip(...)
                continue
            }
            break
        }
    }

    newAPIError := relayHandler(c, relayInfo)
    if newAPIError == nil {
        return
    }

    if relayInfo.Billing != nil {
        _ = relayInfo.Billing.Refund(c)
        relayInfo.Billing = nil
    }

    if !shouldRetry(newAPIError, ...) {
        break
    }
}
```

### 6.2.4 为什么要按“attempt”管理 BillingSession

因为一次外部请求可能经历多个渠道尝试：

- 第一次选到 `request_count` 渠道，预扣 1 次，转发失败，退款
- 第二次选到 `quota` 渠道，再重新预扣金额池 / 钱包

因此：

- 每次渠道尝试都必须有独立的 BillingSession 生命周期
- 不能复用旧的预扣状态

## 6.3 渠道过滤只能做“减少无效尝试”，不能替代最终预扣

可以在 `getChannel(...)` 后加一层轻量过滤，例如：

- 用户没有任何可用次数池时，尽量不要返回 `request_count` 渠道
- 用户没有任何金额池且钱包不足时，尽量不要返回 `quota` 渠道

但这只能做**优化**，不能作为最终扣费判定。真正的权威判断仍应是：

- `PreConsumeBillingForChannel(...)`

原因：

- 并发下资源可能在选中渠道后被其他请求占用
- 过滤阶段拿到的是“可用性快照”，不是锁定结果
- 最终成功与否仍需依赖真实预扣

## 6.4 request_count 渠道失败后的行为

当命中 `request_count` 渠道时：

- 无 request_count 订阅
- 或次数池已耗尽
- 或预扣 1 次失败

则该次渠道尝试应视为：

- **本渠道对该用户不可用**
- 不得 fallback 到钱包
- 可以继续尝试其他渠道（例如同模型下的 `quota` 渠道）

这正是“渠道计费方式优先于全局资金偏好”的核心体现。

## 6.5 quota 渠道失败后的行为

命中 `quota` 渠道后，优先扣金额池，不足时按现有偏好决定是否回退钱包：

- `subscription_first`：先订阅金额池，再钱包
- `wallet_first`：先钱包，再订阅金额池（如果系统保留该偏好）
- `subscription_only`：只能订阅金额池，不能钱包回退

注意：

> `subscription_only` 在双轨方案中，应只约束 **quota 资金路径**；对于 `request_count` 渠道，本身就始终强制走订阅次数池。

---

## 七、NewBillingSession / FundingSource 重构建议

## 7.1 新增按渠道类型的创建入口

建议新增：

```go
func PreConsumeBillingForChannel(
    c *gin.Context,
    preConsumedQuota int,
    relayInfo *relaycommon.RelayInfo,
    channel *model.Channel,
    billingAttemptId string,
) *types.NewAPIError
```

内部再调用：

```go
func NewBillingSessionForChannel(
    c *gin.Context,
    relayInfo *relaycommon.RelayInfo,
    preConsumedQuota int,
    channelBillingType string,
    billingAttemptId string,
) (*BillingSession, *types.NewAPIError)
```

## 7.2 request_count 渠道路径

当 `channelBillingType == request_count` 时：

1. 强制忽略钱包
2. 强制忽略 quota 订阅
3. 只查找 `request_count` 资源池
4. 固定预扣 1 次
5. 成功后：
   - `relayInfo.BillingSource = subscription`
   - `relayInfo.SubscriptionMeterType = request_count`
   - `relayInfo.SubscriptionPreConsumed = 1`
6. 失败后：返回可识别的“该渠道计费资源不足”错误，供外层继续尝试其他渠道

## 7.3 quota 渠道路径

当 `channelBillingType == quota` 时：

1. 先查找 `quota` 资源池
2. 若 quota 订阅不足，再按用户偏好决定是否走钱包
3. 结算和退款保持现有 quota 逻辑，但资金来源必须明确记录

## 7.4 FundingSource 层的关键要求

### SubscriptionFunding

当前 `SubscriptionFunding` 及相关函数不能完全“无改动复用”，至少需要补齐：

- 按 `meter_type` 定向预扣
- attempt 级 `request_id`
- 更清晰的错误码区分：
  - 无该类型资源池
  - 有该类型资源池但不足
  - 并发冲突 / DB 错误

### WalletFunding

钱包退款当前不应被设计成“任意重试都安全”的幂等路径。因此普通 Relay 一期改造时要特别注意：

- 不要让同一个 attempt 的钱包退款被多次触发
- `defer` 和显式退款逻辑要避免重复执行

---

## 八、`request_id` 与幂等设计（必须补齐）

这是旧版方案遗漏最大的点之一。

## 8.1 问题来源

当前订阅预扣幂等绑定在单一 `request_id` 上。

但普通 Relay 改成循环内预扣后，同一个外部请求会有多个渠道尝试：

- 请求本身只有一个 `request_id`
- 但每次渠道尝试都需要独立的预扣 / 退款对象

如果继续复用同一个 `request_id`，会与：

- 第一次预扣
- 第一次退款
- 第二次预扣

之间的关系发生耦合，容易串账。

## 8.2 推荐设计

区分两个维度：

### 外部请求 ID

```text
request_id
```

表示用户的一次 API 请求，用于日志归并和端到端追踪。

### 计费尝试 ID

```text
billing_attempt_id = request_id + ":retry:" + retryIndex + ":channel:" + channelId
```

表示一次具体的渠道计费尝试。

## 8.3 使用规则

1. 订阅预扣 / 退款幂等统一使用 `billing_attempt_id`
2. 日志中同时记录：
   - `request_id`
   - `billing_attempt_id`
3. 成功时只保留最终成功 attempt 的结算记录
4. 失败重试时，必须先结束上一个 attempt 的退款生命周期，再开始下一个 attempt

## 8.4 这样设计的好处

- 避免多渠道尝试串账
- 保留一次外部请求的整体关联性
- 保证订阅预扣记录可独立退款
- 便于后续 Task 二期复用同样的 attempt 概念

---

## 九、Task 二期方案（本轮只设计，不落地）

## 9.1 为什么不建议一期就改 Task

因为 Task 当前并不是普通 Relay 的简化版，而是已存在完整状态机：

1. submit 阶段已预扣
2. 任务可能异步完成
3. polling 阶段可能失败并退款
4. 成功后可能还要基于实际 token / adaptor 结果做差额结算

若直接照搬普通 Relay 的“循环内预扣”方案，会遇到额外问题：

- submit 失败重试换渠道时，旧预扣与新预扣如何隔离
- request_count 渠道固定 1 次，轮询成功后是否仍完全跳过 quota 差额重算
- task private data 里的 `channel_billing_type` 已落地，但 `billing_attempt_id` 仍待设计
- 超时任务 sweep 时，退款要对哪个 attempt 生效

## 9.2 二期建议范围

Task 二期再统一处理：

1. submit 阶段的按渠道预扣
2. task private data 增加：
   - `billing_attempt_id`
   - 如有需要，再补充更细的 attempt 元数据
3. polling / timeout / failure 统一退款语义
4. request_count task 的固定按次结算规则

## 9.3 一期对 Task 的处理原则

一期暂不改 Task 双轨扣费，只要求：

- 文档明确说明 Task 仍按现有逻辑运行
- 一期上线不破坏现有 Task 计费链路

---

## 十、鉴权与偏好语义修订

## 10.1 token exhausted 放行的现状与结论

当前只要用户有活跃 `request_count` 订阅，`ValidateUserToken(...)` 就可能放行 exhausted token。

在双轨方案下，这会带来一个可预期现象：

1. token exhausted
2. 因用户有 request_count 订阅而通过鉴权
3. 但最终选中的却是 `quota` 渠道
4. 若用户没有 quota 订阅且钱包不足，请求仍会在后续计费阶段失败

## 10.2 设计结论

一期先**接受这种延迟失败**，不在 token 鉴权层引入渠道感知。

原因：

- 鉴权时还不知道最终命中的渠道
- 若强行把渠道感知前置到鉴权，会把选择逻辑和鉴权逻辑耦合得非常重

但必须在文档、日志和前端提示中明确：

> “鉴权通过”不等于“一定能完成最终计费”。

## 10.3 `subscription_only` 语义修订

双轨方案下建议重新定义：

- 对 `quota` 渠道：
  - `subscription_only` 表示只能走 quota 订阅金额池，不能回退钱包
- 对 `request_count` 渠道：
  - 无论用户偏好如何，都始终只能走 request_count 次数池

即：

> `request_count` 渠道的“只订阅”是渠道规则，不是用户偏好规则。

---

## 十一、日志、审计与可观测性

## 11.1 consume log 当前已落地字段与后续建议

当前已落地到 `other` 的关键字段包括：

```json
{
  "billing_source": "subscription",
  "subscription_meter_type": "request_count",
  "channel_billing_type": "request_count",
  "request_id": "...",
  "billing_attempt_id": "..."
}
```

其中：

- `channel_billing_type` 已在 consume / task / error log 快照落地
- `billing_attempt_id` 仍未实现，示例中的该字段当前属于后续规划

对 quota 渠道也建议持续记录：

- `channel_billing_type = quota`
- 若最终扣的是钱包，也应能明确反映

## 11.2 Task billing log 当前状态与后续建议

当前已完成：

- `channel_billing_type`

Task 二期时建议继续统一记录：

- `billing_attempt_id`
- `request_id`

## 11.3 运行日志建议增加关键信息

建议打出如下维度：

- `request_id`
- `billing_attempt_id`
- `channel_id`
- `channel_billing_type`
- `billing_source`
- `subscription_meter_type`
- `pre_consumed`
- `actual_consumed`

这样排查“为什么扣了次数 / 为什么没回退钱包”时，不必再回放整条调用链。

---

## 十二、后端 / 前端改造清单

## 12.1 后端改造清单

| 文件 | 主要改动 |
|---|---|
| `model/channel.go` | 新增 `BillingType` 字段 |
| `model/main.go` | 增加 `channels.billing_type` migration |
| `model/subscription.go` | 解除不同 `meter_type` 活跃互斥，并新增按 meter_type 定向查询 / 预扣 / 退款接口 |
| `model/token.go` | 保持 exhausted 放行逻辑，但补充注释 / 错误提示语义 |
| `relay/common/relay_info.go` | 已增加 `ChannelMeta.BillingType` 快照；`BillingAttemptId` 待后续 |
| `service/billing.go` | 新增 `PreConsumeBillingForChannel(...)` 入口 |
| `service/billing_session.go` | 按 `channelBillingType` 分流，区分 request_count / quota 两条路径 |
| `service/funding_source.go` | 为 SubscriptionFunding 补齐 meter_type 定向能力与 attempt 级幂等 |
| `controller/relay.go` | 普通 Relay 调整为循环内预扣，显式管理 attempt 生命周期 |
| `controller/channel.go` | 渠道 CRUD 增加 `billing_type` 参数校验和透传 |
| `middleware/distributor.go` | 已在选中渠道后把 `billing_type` 写入 context |
| `service/log_info_generate.go` / `service/task_billing.go` / `controller/relay.go` | 已补齐 `channel_billing_type` 的 consume/task/error log 快照 |
| `service/task_billing.go` / `service/task_polling.go` | Task 双轨扣费仍属二期，但日志快照已做最小补齐 |

## 12.2 前端改造清单

| 页面/组件 | 主要改动 |
|---|---|
| 渠道新增 / 编辑 | 已增加 `billing_type` 下拉框 |
| 渠道列表 | 已展示 `billing_type` |
| 订阅套餐展示 | 明确区分金额池套餐、次数池套餐；若后续支持组合商品，再追加展示 |
| 用户消费日志 | `billing_source`、`subscription_meter_type` 已展示；`channel_billing_type` 待前端补齐 |
| 用户帮助说明 | 补充“按次数渠道不回退钱包、quota 渠道才可回退钱包”的说明 |

---

## 十三、迁移与上线步骤

## 13.1 Step 1：数据库增量变更

```sql
ALTER TABLE channels ADD COLUMN billing_type VARCHAR(32) NOT NULL DEFAULT 'quota';
CREATE INDEX idx_channels_billing_type ON channels (billing_type);
```

上线后立即兼容存量行为，因为默认值即为 `quota`。

## 13.2 Step 2：只上线上下文字段与日志快照（当前已完成的最小阶段）

当前已完成：

- `channel_billing_type`
- `RelayInfo.ChannelMeta.BillingType` 快照
- `task.private_data.channel_billing_type`
- consume / task / error log 扩展字段

当前仍未完成：

- `billing_attempt_id`

这一阶段已经让系统具备基础可观测性，可在此基础上再改主链路。

## 13.3 Step 3：上线普通 Relay 双轨能力

上线内容：

1. 普通 Relay 改为循环内预扣
2. request_count 渠道只扣次数池
3. quota 渠道优先扣金额池，不足再回退钱包
4. meter_type 定向订阅查询 / 预扣 / 退款

## 13.4 Step 4：后台逐步配置按次数渠道

不要一上来把所有候选渠道都改成 `request_count`，建议：

1. 小范围挑选低风险模型
2. 观察日志与退款链路
3. 确认无串账后再扩大范围

## 13.5 Step 5：二期再评估 Task 双轨

在普通 Relay 稳定后，再进入 Task 二期。

---

## 十四、风险清单与应对

| 风险 | 说明 | 应对 |
|---|---|---|
| 普通 Relay 主链路改动大 | 预扣位置从循环外改到循环内，会影响成功 / 失败 / 重试 / 退款路径 | 一期只改普通 Relay，先补日志，再做最小范围灰度 |
| request_id 幂等串账 | 同一外部请求多次渠道尝试会重复预扣 / 退款 | 引入 `billing_attempt_id`，订阅预扣幂等统一按 attempt 维度处理 |
| exhausted token 延迟失败 | 鉴权可通过，但后续命中 quota 渠道时可能因无金额池 / 无钱包而失败 | 文档明确说明，并通过日志 / 错误提示降低误解 |
| 只删互斥不改查询逻辑 | 数据层允许共存，但运行时仍按旧逻辑消费，导致扣错资源池 | 必须同步做按 meter_type 定向查询 / 预扣 / 退款 |
| 钱包退款重复执行 | defer 与显式退款可能重复触发 | 普通 Relay 重构时统一 BillingSession 生命周期，避免双重退款 |
| Task 链路被误伤 | Task 已有 submit / polling / refund 状态机 | 一期不动 Task 双轨，只保证兼容现状 |
| 渠道配置变更后难追溯 | 仅看当前 Channel 配置无法知道历史请求当时的计费类型 | 把 `channel_billing_type` 快照写入 RelayInfo / Task / consume log |

---

## 十五、测试建议

## 15.1 单元测试

### request_count 路径

1. `request_count` 渠道 + 有可用次数池
   - 预期：固定预扣 1 次，成功后保留 1 次
2. `request_count` 渠道 + 无次数池
   - 预期：当前渠道不可用，不回退钱包
3. `request_count` 渠道 + 次数池已耗尽
   - 预期：预扣失败，继续尝试其他兼容渠道
4. `request_count` 渠道 + 免费模型
   - 预期：若用户有次数池，仍按 1 次预扣

### quota 路径

1. `quota` 渠道 + quota 订阅充足
   - 预期：只消耗 quota 订阅
2. `quota` 渠道 + quota 订阅不足 + 钱包充足
   - 预期：按偏好回退钱包
3. `quota` 渠道 + `subscription_only` + 无 quota 订阅
   - 预期：失败，不回退钱包
4. `quota` 渠道 + 无 quota 订阅 + 钱包不足
   - 预期：失败

### attempt 幂等

1. 同一 `billing_attempt_id` 重复预扣
   - 预期：幂等
2. 同一请求不同 attempt
   - 预期：互不串账，可独立退款

## 15.2 集成测试

1. **双渠道同模型测试**
   - 一个模型同时挂 `request_count` 渠道和 `quota` 渠道
   - 验证用户不同资源状态下是否命中正确路径
2. **重试测试**
   - 第一次命中 `request_count` 渠道并失败退款
   - 第二次命中 `quota` 渠道成功
   - 验证两次 attempt 不串账
3. **并发测试**
   - 多请求同时争抢同一用户的次数池
   - 验证无超卖
4. **鉴权延迟失败测试**
   - exhausted token + 有 request_count 订阅 + 最终命中 quota 渠道
   - 预期：鉴权通过，但计费阶段失败

## 15.3 回归测试

1. 存量 `quota` 渠道 + 存量 `quota` 用户，行为与现网一致
2. 不配置 `billing_type` 的旧渠道，行为保持不变
3. Task 请求在一期上线后仍按现有逻辑工作
4. 订阅重置 / 过期任务不因普通 Relay 改造而失效

---

## 十六、结论

本次双轨方案修订后的核心结论如下：

1. **普通 Relay 必须改成“先选渠道，再按渠道预扣”**，否则无法严格满足“按渠道计费类型决定扣次数还是扣金额”的需求。
2. **不能只删除订阅 `meter_type` 互斥约束**，还必须把订阅查询、预扣、退款整体改成按 `meter_type` 定向处理。
3. **`request_count` 渠道的规则是渠道级强约束**：只能扣次数池，次数耗尽不可回退钱包。
4. **`quota` 渠道沿用现有金额路径，但要优先使用 quota 订阅**，再按偏好决定是否回退钱包。
5. **Task 不适合一期混改**，因为它当前已经有独立的 submit / polling / refund / recalculate 状态机，建议作为二期处理。
6. **必须补齐 `billing_attempt_id` 设计与 `channel_billing_type` 快照**，否则在循环内预扣架构下很容易出现幂等串账和审计困难。

相较旧版文档，v1.1 不再把该方案描述成“只给 Channel 加字段 + 把预扣挪进循环即可”的低风险改造，而是明确指出：

> 这是一项以普通 Relay 主链路重构为核心、并要求订阅消费模型从“全局单一活跃类型”升级为“按 meter_type 定向消费”的中等复杂度改造。

但只要按“一期普通 Relay、二期 Task、先补日志与 attempt 幂等、再灰度启用 request_count 渠道”的节奏推进，整体风险仍然可控。
