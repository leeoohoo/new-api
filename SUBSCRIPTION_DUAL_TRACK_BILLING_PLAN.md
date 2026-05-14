# 订阅双轨扣费方案设计文档

> 文档版本：v1.0  
> 目标：在现有 request_count 订阅一期方案基础上，支持订阅同时持有「次数」与「金额」两种资源池，并引入渠道计费方式类型（按次数 / 按金额），实现渠道级别的差异化扣费与回退策略。

---

## 一、背景与现状

### 1.1 现有订阅体系

当前项目已支持两种订阅计量类型（`meter_type`）：

| 计量类型 | 含义 | 结算方式 |
|---------|------|---------|
| `quota` | 按额度（金额） | 预扣估算额度，结算时按实际 `actualQuota` 补扣/返还 |
| `request_count` | 按次数 | 预扣固定 1 次，结算时固定保留 1 次，跳过 token/quota 预扣 |

关键约束：
- 用户**不能**同时持有不同 `meter_type` 的活跃订阅（`EnsureUserActiveSubscriptionMeterTypeCompatibleTx` 会拦截）。
- 若用户持有 `request_count` 订阅，`NewBillingSession` 会**强制**将计费偏好设为 `subscription_only`，屏蔽余额路径。
- 预扣费发生在渠道选择**之前**（`controller/relay.go` 中 `PreConsumeBilling` 在 `for retry` 循环之前执行）。

### 1.2 现有计费链路

```
用户请求
  → Relay()
    → 解析请求、估算 token
    → ModelPriceHelper() 产出 priceData
    → PreConsumeBilling()  // 循环外：创建 BillingSession，预扣资金
    → for retry {
        → getChannel()       // 选渠道
        → relayHandler()     // 转发请求
        → if success { return }
        → if shouldRetry { continue }
      }
    → defer: if error { Billing.Refund() }
    → SettleBilling()      // 成功时结算（普通 relay 在 handler 内部结算，task relay 在循环外结算）
```

### 1.3 核心矛盾

新需求要求「按渠道计费方式扣费」，但现有架构是**先扣费、后选渠道**。若保持现有顺序，无法在预扣费时确定应扣「次数」还是「额度」。

---

## 二、总体设计目标

1. **订阅双资源池**：一个用户可同时持有 `quota` 订阅与 `request_count` 订阅，两者独立计费、独立过期。
2. **渠道计费方式**：每个渠道新增 `billing_type`（`quota` / `request_count`），默认 `quota`。
3. **按次数渠道（`request_count`）**：
   - 只能消耗订阅的「次数」资源池。
   - 次数耗尽后，该类渠道对该用户**不可用**（不能回退到余额）。
4. **按金额渠道（`quota`）**：
   - 优先消耗订阅的「金额」资源池（`quota` 类型订阅）。
   - 订阅金额耗尽后，回退扣用户余额（wallet quota）。
5. **最小侵入**：兼容旧渠道（默认 `quota`）、旧订阅（`meter_type` 语义不变），平滑迁移。

---

## 三、数据模型变更

### 3.1 Channel（渠道）

在 `model/channel.go` 的 `Channel` 结构体新增字段：

```go
type Channel struct {
    // ... 现有字段 ...

    // 渠道计费方式类型：quota（按金额，默认）/ request_count（按次数）
    BillingType string `json:"billing_type" gorm:"type:varchar(32);not null;default:'quota'"`
}
```

**说明**：
- 默认值 `quota`，确保存量渠道无需人工干预即可兼容。
- 该字段与 `Type`（Azure/OpenAI/Vertex 等渠道类型）正交，一个渠道可以是 Azure 且按次数计费。

### 3.2 SubscriptionPlan（订阅套餐模板）

现有 `SubscriptionPlan` 已含 `MeterType`（`quota` / `request_count`），保持语义不变。

**新增支持**：允许创建「双资源池套餐」——即一个套餐同时包含 `quota_amount` 与 `request_count_amount`。

可选方案（推荐**方案 A**，改动最小）：

**方案 A：单套餐单类型，用户多订阅并行**
- 不改变 `SubscriptionPlan` 结构。
- 允许用户同时购买一个 `quota` 套餐和一个 `request_count` 套餐。
- 各自生成独立的 `UserSubscription` 记录。

**方案 B：套餐内双字段**
- `SubscriptionPlan` 增加 `RequestCountTotal int64`。
- 购买时根据套餐类型生成对应 `UserSubscription`。
- 改动面较大，需要修改购买/升级/Admin 发放全链路。

**结论：采用方案 A**。理由：
- 与现有 `meter_type` 语义完全兼容。
- 购买、发放、过期、重置逻辑无需重构，仅需解除「互斥」约束。

### 3.3 UserSubscription（用户订阅实例）

无需新增字段。但需要**移除**以下约束：

```go
// 需要修改/移除的函数
func EnsureUserActiveSubscriptionMeterTypeCompatibleTx(tx *gorm.DB, userId int, meterType string) error
```

改为：允许同一用户持有多个 `active` 且 `end_time > now` 的订阅，只要它们的 `meter_type` 不同即可共存。相同 `meter_type` 的订阅仍按现有逻辑互斥（或允许叠加，视业务而定；推荐先保持同类型互斥，避免额度计算复杂化）。

### 3.4 数据库 Migration

```sql
-- channels 表新增 billing_type
ALTER TABLE channels ADD COLUMN billing_type VARCHAR(32) NOT NULL DEFAULT 'quota';
CREATE INDEX idx_channels_billing_type ON channels(billing_type);
```

### 3.5 缓存层

`model/channel_cache.go` 中的缓存结构（`channelsIDM`、`group2model2channels`）需要重新加载以包含 `BillingType` 字段。`CacheUpdateChannel`、`CacheLoadChannels` 等函数无需改动（`Channel` 结构体直接包含新字段，GORM 加载后缓存即可）。

---

## 四、渠道计费方式字段设计

### 4.1 字段取值

| 值 | 含义 | 适用场景 |
|---|------|---------|
| `quota` | 按金额（额度）计费 | 绝大多数渠道，按 token 消耗换算额度 |
| `request_count` | 按次数计费 | 特定低价/高频模型渠道，按调用次数计费 |

### 4.2 管理端入口

- **渠道新增/编辑页面**（`controller/channel.go`）：在表单中增加 `billing_type` 下拉选择，校验同模型下不同渠道的计费方式可以混配。
- **渠道列表**：展示 `billing_type`，支持按计费方式筛选。

### 4.3 渠道与模型关系

- 一个模型可以映射到多种计费方式的渠道（例如 `gpt-3.5-turbo` 既有按额度渠道，也有按次数渠道）。
- 渠道计费方式由渠道自身决定，与模型价格配置（`priceData`）解耦。

---

## 五、扣费决策树与流程重构

### 5.1 核心调整：PreConsumeBilling 后置到渠道选择之后

由于必须根据**实际选中的渠道**决定扣费方式，需要将 `PreConsumeBilling` 从 `Relay` 函数的循环外移至循环内。

#### 5.1.1 普通 Relay（`controller/relay.go::Relay`）

**现有流程**：
```
PreConsumeBilling()  // 循环外
for retry {
    getChannel()
    relayHandler()
}
defer Refund()
```

**新流程**：
```
for retry {
    channel, err := getChannel()
    if err != nil { break }

    // 根据渠道计费类型执行预扣费
    if shouldPreConsume {
        err = PreConsumeBillingForChannel(c, priceData, relayInfo, channel)
        if err != nil {
            // 计费失败（如次数订阅不足），将该渠道标记为不可用并继续重试
            processChannelError(c, channel, err)
            if channel.BillingType == "request_count" {
                // 次数不足，跳过该渠道，尝试其他渠道
                continue
            }
            break
        }
    }

    newAPIError = relayHandler(c, relayInfo)

    if newAPIError == nil {
        // 成功：结算并返回
        SettleBilling(c, relayInfo, actualQuota)
        return
    }

    // 失败：退款，释放资金/次数，尝试下一个渠道
    if relayInfo.Billing != nil {
        relayInfo.Billing.Refund(c)
        relayInfo.Billing = nil
    }

    if !shouldRetry(...) { break }
}
```

**关键变化**：
1. 每次重试都独立创建/销毁 `BillingSession`。
2. 若 `request_count` 渠道预扣失败（无次数订阅），**继续重试**下一个渠道（可能是 `quota` 渠道）。
3. 若 `quota` 渠道预扣失败（余额不足），直接终止请求。
4. `defer` 中的 `Refund` 逻辑需要移除或调整，因为每次循环内已手动处理退款。

#### 5.1.2 Task Relay（`controller/relay.go::RelayTask`）

**现状**：`RelayTask` 当前**没有**调用 `PreConsumeBilling`，仅在成功后 `SettleBilling`。

**分析**：Task 请求（如视频生成）是异步的，提交时不知道最终消耗。现有逻辑可能是「先提交任务，完成后按实际消耗结算」。

**新方案**：
- 若 Task 也需要支持按次数计费，需在 `RelayTask` 循环内增加预扣费逻辑（同普通 Relay）。
- 但由于 Task 的 `Quota` 在提交时未知，按次数计费更合理（固定扣 1 次）。
- 若业务上 Task 不需要按次数渠道，可保持现状（仅支持 `quota` 渠道）。

**建议**：在文档中标记 Task 一期暂不改造，后续迭代支持。

### 5.2 NewBillingSession 重构

在 `service/billing_session.go` 中，将 `NewBillingSession` 改为接受 `channelBillingType` 参数：

```go
func NewBillingSession(
    c *gin.Context,
    relayInfo *relaycommon.RelayInfo,
    preConsumedQuota int,
    channelBillingType string,  // 新增：渠道计费方式
) (*BillingSession, *types.NewAPIError) {

    switch channelBillingType {
    case "request_count":
        // 强制走 request_count 订阅
        return tryRequestCountSubscription()
    case "quota", "":
        // 走现有偏好逻辑：subscription_first / wallet_first 等
        return tryQuotaOrWallet(preConsumedQuota)
    default:
        return nil, types.NewError(fmt.Errorf("unknown channel billing type: %s", channelBillingType), ...)
    }
}
```

#### 5.2.1 request_count 路径

```go
func tryRequestCountSubscription() (*BillingSession, *types.NewAPIError) {
    // 1. 检查用户是否有活跃的 request_count 订阅
    hasSub, err := model.HasActiveRequestCountSubscription(relayInfo.UserId)
    if err != nil { return nil, ... }
    if !hasSub {
        return nil, types.NewErrorWithStatusCode(
            fmt.Errorf("按次数计费的渠道需要 request_count 订阅"),
            types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
            types.ErrOptionWithSkipRetry(),
        )
    }

    // 2. 预扣 1 次
    session := &BillingSession{
        relayInfo:      relayInfo,
        skipTokenQuota: true,
        funding: &SubscriptionFunding{
            requestId: relayInfo.RequestId,
            userId:    relayInfo.UserId,
            modelName: relayInfo.OriginModelName,
            amount:    1,
            meterType: model.SubscriptionMeterRequestCount,
        },
    }
    if apiErr := session.preConsume(c, 1); apiErr != nil {
        return nil, apiErr
    }
    return session, nil
}
```

**特点**：
- 完全跳过 token/quota 预扣（`skipTokenQuota = true`）。
- 不检查用户余额，只检查 `request_count` 订阅。
- 预扣失败时返回特定错误码，外层 `Relay` 捕获后 `continue` 重试下一个渠道。

#### 5.2.2 quota 路径

与现有 `NewBillingSession` 逻辑基本一致，但**移除** `request_count` 订阅的强制 `subscription_only` 逻辑：

```go
// 移除以下强制逻辑：
// requestCountActive, requestCountErr := model.HasActiveRequestCountSubscription(relayInfo.UserId)
// if requestCountActive { pref = "subscription_only" }
```

改为：
- `subscription_first`：优先使用 `quota` 类型订阅（而非 `request_count`）。
- 若 `quota` 订阅不足，回退到 wallet。
- `request_count` 订阅在 `quota` 路径中**不参与**（因为 `request_count` 订阅只用于 `request_count` 渠道）。

**需要新增**：`GetActiveUserQuotaSubscription(userId)` —— 只查询 `meter_type = 'quota'` 的活跃订阅。

### 5.3 渠道过滤（getChannel 阶段）

在 `CacheGetRandomSatisfiedChannel` 或 `getChannel` 返回渠道后，增加一层过滤：

```go
func filterChannelByBillingType(
    channel *model.Channel,
    userId int,
) (*model.Channel, *types.NewAPIError) {
    if channel.BillingType == model.ChannelBillingTypeRequestCount {
        hasSub, err := model.HasActiveRequestCountSubscription(userId)
        if err != nil {
            return nil, types.NewError(err, types.ErrorCodeQueryDataError)
        }
        if !hasSub {
            // 返回 nil，外层会继续重试下一个渠道
            return nil, types.NewErrorWithStatusCode(
                fmt.Errorf("用户无按次数订阅，跳过按次数渠道 #%d", channel.Id),
                types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
                types.ErrOptionWithSkipRetry(),
            )
        }
    }
    return channel, nil
}
```

**注意**：
- 过滤逻辑放在 `getChannel` 返回之后、`SetupContextForSelectedChannel` 之前。
- 若过滤后无可用渠道，外层重试机制会继续尝试（可能跨分组）。
- 若所有渠道都被过滤掉，最终返回「无可用渠道」错误。

### 5.4 扣费决策树（总览）

```
用户发起请求
  │
  ▼
[选渠道] getChannel()
  │
  ├── 渠道 BillingType = request_count
  │     │
  │     ├── 用户有活跃 request_count 订阅？
  │     │     ├── YES → 预扣 1 次 → 转发请求 → 成功结算（保留1次）/ 失败退款
  │     │     └── NO  → 跳过该渠道，尝试下一个渠道
  │     │
  │     └── 次数耗尽？
  │           └── YES → PreConsume 失败 → 跳过该渠道
  │
  └── 渠道 BillingType = quota（默认）
        │
        ├── 用户有 quota 订阅？
        │     ├── YES → 预扣 quota 订阅额度 → 转发请求 → 结算（按实际消耗）
        │     └── NO  → 预扣用户余额（wallet）
        │
        └── 订阅额度不足？
              └── YES → 若 preference = subscription_first，回退 wallet；
                        若 preference = subscription_only，失败；
                        若 preference = wallet_first，直接 wallet。
```

---

## 六、兼容旧订阅与旧渠道

### 6.1 旧渠道兼容

- 存量渠道 `billing_type` 默认为 `quota`。
- 无需人工修改，行为与改造前完全一致。

### 6.2 旧订阅兼容

- 存量 `quota` 订阅：在 `quota` 渠道中正常使用。
- 存量 `request_count` 订阅：
  - 改造前：强制 `subscription_only`，所有请求都走 `request_count` 订阅（即使渠道本身不支持按次数）。
  - 改造后：只在 `request_count` 渠道中使用；在 `quota` 渠道中不再被使用。
  - **注意**：这会导致存量 `request_count` 订阅用户的体验变化——如果该用户请求的模型只有 `quota` 渠道，则 `request_count` 订阅不会被消耗，用户需要额外有余额或 `quota` 订阅。
  - **缓解措施**：上线前建议将高频模型的存量渠道批量设为 `request_count`（若业务上它们确实按次计费），或引导用户购买 `quota` 订阅。

### 6.3 计费偏好兼容

- 用户设置中的 `billing_preference`（`subscription_first` / `wallet_first` / `subscription_only` / `wallet_only`）仅影响 `quota` 渠道。
- `request_count` 渠道**无视**该偏好，强制使用 `request_count` 订阅。

---

## 七、失败回滚与幂等

### 7.1 请求失败回滚

- **普通 Relay**：每次重试前，若 `relayInfo.Billing != nil`，调用 `Billing.Refund(c)` 异步退款。
  - `SubscriptionFunding.Refund()` → `RefundSubscriptionPreConsume(requestId)`（幂等）。
  - `WalletFunding.Refund()` → `IncreaseUserQuota()`（非幂等，不能重试，已有注释说明）。
- **Task Relay**：`defer` 中在 `taskErr != nil` 时 `Refund`。

### 7.2 request_count 渠道重试时的回滚

当 `request_count` 渠道请求失败并需要重试到 `quota` 渠道时：

1. 第一次 `request_count` 渠道：预扣 1 次 → 请求失败 → `Refund` 退回 1 次。
2. 第二次 `quota` 渠道：预扣额度 → 请求成功 → `Settle` 按实际消耗结算。

由于 `RefundSubscriptionPreConsume` 是幂等的（基于 `request_id`），即使退款异步执行稍慢，也不会导致资金错乱。

### 7.3 结算后不可回滚

- 一旦 `SettleBilling` 成功，资金已最终消耗，不再回滚。
- 若下游返回成功但上游实际失败（如网络断开），由现有 `relayHandler` 内部的错误处理机制兜底。

---

## 八、日志审计

### 8.1 需要记录的字段

沿用并扩展现有 `RelayInfo` 的订阅相关字段：

| 字段 | 来源 | 用途 |
|-----|------|------|
| `BillingSource` | BillingSession | `wallet` / `subscription` |
| `SubscriptionId` | SubscriptionFunding | 实际扣费的订阅 ID |
| `SubscriptionMeterType` | SubscriptionFunding | `quota` / `request_count` |
| `SubscriptionPreConsumed` | SubscriptionFunding | 预扣值（quota 或 1） |
| `SubscriptionPostDelta` | Settle | 结算差额 |
| `ChannelBillingType` | Channel | 新增：渠道计费方式 |
| `ChannelId` | Channel | 实际使用的渠道 |

### 8.2 日志输出

在 `SettleBilling` 和 `Refund` 中增加 `channelBillingType` 上下文：

```go
logger.LogInfo(ctx, fmt.Sprintf(
    "结算完成：渠道 #%d (billing_type=%s), 来源=%s, meter_type=%s, 预扣=%d, 实际=%d",
    relayInfo.ChannelId, relayInfo.ChannelBillingType,
    relayInfo.BillingSource, relayInfo.SubscriptionMeterType,
    relayInfo.SubscriptionPreConsumed, actualQuota,
))
```

### 8.3 消费记录表

现有 `SubscriptionPreConsumeRecord` 已记录每次预扣的 `request_id`、`user_subscription_id`、`pre_consumed`。无需改动。

---

## 九、前后端改造点

### 9.1 后端改造清单

| 文件 | 改造内容 |
|-----|---------|
| `model/channel.go` | `Channel` 新增 `BillingType` 字段 |
| `model/main.go` | 增加 `channels.billing_type` 的 migration |
| `model/subscription.go` | 移除/放宽 `EnsureUserActiveSubscriptionMeterTypeCompatibleTx` 的互斥限制；新增 `GetActiveUserQuotaSubscription` |
| `model/channel_cache.go` | 确认缓存加载包含新字段（通常无需改动） |
| `service/billing_session.go` | 重构 `NewBillingSession`，增加 `channelBillingType` 参数；`trySubscription` 区分 quota/request_count |
| `service/funding_source.go` | `SubscriptionFunding` 保持现状（已支持 `meterType`） |
| `service/billing.go` | `PreConsumeBilling` 改为 `PreConsumeBillingForChannel`，增加 `channel` 参数；`SettleBilling` 增加 `channelBillingType` 日志 |
| `controller/relay.go` | `Relay` 将 `PreConsumeBilling` 移入 `for retry` 循环内；`getChannel` 后增加 `filterChannelByBillingType`；手动管理 `Billing.Refund` |
| `controller/channel.go` | 渠道 CRUD 接口增加 `billing_type` 参数校验与透传 |
| `middleware/distributor.go` | 可选：在 `SetupContextForSelectedChannel` 中将 `billing_type` 写入 context |

### 9.2 前端改造清单

| 页面/组件 | 改造内容 |
|----------|---------|
| 渠道新增/编辑表单 | 增加 `billing_type` 下拉选择（quota / request_count） |
| 渠道列表 | 展示 `billing_type`，支持筛选 |
| 订阅购买/展示 | 允许同时展示/购买 `quota` 和 `request_count` 套餐 |
| 用户消费日志 | 展示 `channel_billing_type` 字段 |

---

## 十、迁移步骤

### Step 1：数据库迁移（零停机）

```sql
ALTER TABLE channels ADD COLUMN billing_type VARCHAR(32) NOT NULL DEFAULT 'quota';
CREATE INDEX idx_channels_billing_type ON channels(billing_type);
```

### Step 2：后端代码上线

1. 部署含 `BillingType` 字段的新版本（GORM auto-migration 或手动执行 Step 1）。
2. 确认 `CacheLoadChannels` 已加载新字段。
3. 观察日志，确认存量渠道行为不变。

### Step 3：管理端配置

1. 在后台将需要按次数计费的渠道编辑为 `billing_type = request_count`。
2. 创建/上架 `request_count` 类型的订阅套餐。

### Step 4：用户侧引导

1. 通知已有 `request_count` 订阅的用户：
   - 改造后，`request_count` 订阅**仅对按次数渠道生效**。
   - 若需使用按金额渠道，需购买 `quota` 订阅或充值余额。
2. 对于纯 `quota` 订阅用户：无感知。

---

## 十一、风险与应对

| 风险 | 影响 | 应对措施 |
|-----|------|---------|
| **预扣费后置导致并发超卖** | 用户余额在选渠道期间被其他请求扣减，导致预扣费失败 | 1. 预扣费仍使用原子操作；2. 失败后 `continue` 尝试其他渠道；3. 对余额敏感场景可保留小额度预扣 |
| **request_count 订阅用户体验变化** | 改造前所有请求都扣次数，改造后只有 request_count 渠道扣次数 | 上线前批量将对应渠道设为 `request_count`，或引导用户购买 quota 订阅 |
| **循环内重复预扣/退款性能开销** | 高频重试场景下多次数据库操作 | 1. `RefundSubscriptionPreConsume` 是幂等且异步的；2. 监控重试率，异常时告警 |
| **Task Relay 未改造** | Task 请求暂不支持按次数渠道 | 一期明确不支持，二期评估是否将 Task 也纳入循环内预扣费 |
| **渠道缓存不一致** | 新增字段后旧缓存未刷新 | 上线后强制刷新渠道缓存（重启或调用缓存刷新接口） |
| **计费偏好语义混淆** | 用户设置 `subscription_only` 时期望所有请求走订阅，但 request_count 渠道强制 request_count 订阅 | 在文档/前端明确说明：`subscription_only` 仅影响 quota 渠道；request_count 渠道 always 走 request_count 订阅 |

---

## 十二、测试用例

### 12.1 单元测试

| 用例 | 输入 | 预期 |
|-----|------|------|
| request_count 渠道 + 有次数订阅 | `billing_type=request_count`, 用户有 request_count 订阅 | 预扣 1 次，结算保留 1 次 |
| request_count 渠道 + 无次数订阅 | `billing_type=request_count`, 用户无 request_count 订阅 | 跳过该渠道，尝试 quota 渠道；若无非 quota 渠道则返回 403 |
| request_count 渠道 + 次数耗尽 | `billing_type=request_count`, 次数已用完 | PreConsume 失败，跳过该渠道 |
| quota 渠道 + quota 订阅充足 | `billing_type=quota`, 用户有 quota 订阅 | 预扣 quota 订阅，结算按实际消耗 |
| quota 渠道 + 无订阅有余额 | `billing_type=quota`, 无订阅，余额充足 | 预扣余额，结算按实际消耗 |
| quota 渠道 + subscription_only + 无 quota 订阅 | preference=subscription_only, 无 quota 订阅 | 返回 403（不自动 fallback 到 wallet） |
| 重试：request_count 失败 → quota 成功 | 第一次 request_count 渠道失败，第二次 quota 渠道成功 | 第一次退款 1 次，第二次正常扣额度并结算 |

### 12.2 集成测试

1. **端到端请求**：构造一个同时映射到 `request_count` 和 `quota` 渠道的模型，验证：
   - 有 request_count 订阅时，可能命中 request_count 渠道并成功。
   - 无 request_count 订阅时，一定命中 quota 渠道。
2. **并发测试**：多个并发请求同时扣同一用户的 request_count 订阅，验证无超卖（依赖数据库行锁）。
3. **过期场景**：request_count 订阅过期瞬间发起请求，验证渠道过滤正确拦截。

### 12.3 回归测试

1. 存量 `quota` 渠道、存量 `quota` 订阅用户，请求行为与改造前完全一致。
2. 免费模型 + request_count 订阅用户，仍触发预扣费（与现有逻辑一致）。
3. Task 请求（如视频生成）不受改造影响，正常按额度结算。

---

## 十三、附录：关键代码变更示意

### 13.1 Channel 结构体

```go
// model/channel.go
type Channel struct {
    // ... 现有字段 ...
    BillingType string `json:"billing_type" gorm:"type:varchar(32);not null;default:'quota'"`
}

const (
    ChannelBillingTypeQuota        = "quota"
    ChannelBillingTypeRequestCount = "request_count"
)
```

### 13.2 NewBillingSession 签名变更

```go
// service/billing_session.go
func NewBillingSession(
    c *gin.Context,
    relayInfo *relaycommon.RelayInfo,
    preConsumedQuota int,
    channelBillingType string,
) (*BillingSession, *types.NewAPIError) {
    // ... 根据 channelBillingType 分发到不同路径 ...
}
```

### 13.3 Relay 循环内预扣费

```go
// controller/relay.go
for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
    channel, channelErr := getChannel(c, relayInfo, retryParam)
    if channelErr != nil { ... }

    if shouldPreConsume {
        newAPIError = service.PreConsumeBillingForChannel(
            c, priceData.QuotaToPreConsume, relayInfo, channel,
        )
        if newAPIError != nil {
            processChannelError(...)
            if channel.BillingType == model.ChannelBillingTypeRequestCount {
                continue // 跳过次数不足渠道，尝试下一个
            }
            break
        }
    }

    newAPIError = relayHandler(c, relayInfo)
    if newAPIError == nil {
        service.SettleBilling(c, relayInfo, actualQuota)
        return
    }

    if relayInfo.Billing != nil {
        relayInfo.Billing.Refund(c)
        relayInfo.Billing = nil
    }

    if !shouldRetry(...) { break }
}
```

---

## 十四、总结

本方案通过以下三步实现「订阅双轨 + 渠道计费方式」：

1. **数据层**：`Channel` 新增 `BillingType`；`UserSubscription` 解除互斥，允许 `quota` 与 `request_count` 并存。
2. **链路层**：将 `PreConsumeBilling` 从循环外移到循环内，使扣费逻辑能感知实际选中的渠道。
3. **策略层**：`request_count` 渠道强制走次数订阅，次数耗尽则渠道不可用；`quota` 渠道优先走金额订阅，耗尽回退余额。

该方案与现有 request_count 一期实现最大程度复用（`SubscriptionFunding`、`PreConsumeUserSubscription`、`RefundSubscriptionPreConsume` 等核心函数无需改动），主要改动集中在 `controller/relay.go` 的循环结构和 `service/billing_session.go` 的分发逻辑，风险可控。
