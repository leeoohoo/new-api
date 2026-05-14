# 订阅管理业务说明（创建订阅有什么用、什么时候用）

## 1. 结论
创建出来的用户订阅（UserSubscription）不是展示数据，而是请求计费时可被真实扣减的资金来源之一（subscription），与钱包（wallet）并行。
它在两类时机被使用：
1. 用户请求模型时，计费会话按 `billing_preference` 选择 subscription 或 wallet，并执行预扣、结算、退款。
2. 后台维护任务按时间推进订阅生命周期（重置额度、到期失效、分组回退）。

## 2. 数据模型角色
1. `SubscriptionPlan`：套餐模板（价格、时长、总额度、重置周期、升级分组等）。
2. `SubscriptionOrder`：支付订单（pending/success/expired），支付成功后转换为用户订阅。
3. `UserSubscription`：用户订阅实例（active/expired/cancelled，记录总额度、已用额度、起止时间、下次重置时间、分组升级与回退信息）。

## 3. “创建订阅”入口（何时产生 UserSubscription）
1. 支付成功回调创建（主路径）
- 用户下单：`POST /api/subscription/epay|stripe|creem/pay` 创建 `SubscriptionOrder(status=pending)`。
- 回调成功后调用 `model.CompleteSubscriptionOrder(...)`，在事务里创建 `UserSubscription(source=order)`，并把订单置为 success（幂等）。
- Epay 在 `controller/subscription_payment_epay.go` 中直接完成。
- Stripe/Creem 在通用 webhook 中先尝试完成订阅订单，再回退到充值订单逻辑。
2. 管理员直接发放（免支付）
- `POST /api/subscription/admin/bind` 或 `POST /api/subscription/admin/users/:id/subscriptions`。
- 调用 `model.AdminBindSubscription` -> `CreateUserSubscriptionFromPlanTx(..., source="admin")`。
3. 创建时的附带行为
- 若套餐配置了 `UpgradeGroup`，创建时会把用户分组升级，并把原分组写入订阅用于后续回退。
- 会校验用户购买上限 `MaxPurchasePerUser`。

## 4. 创建后“什么时候会被用到”（真实计费链路）
1. 请求进入预扣费阶段时
- 入口：`service.PreConsumeBilling` -> `NewBillingSession(...)`。
- 根据用户 `billing_preference` 选择资金来源：
- `subscription_only`：只走订阅。
- `wallet_only`：只走钱包。
- `wallet_first`：钱包不足回退订阅。
- `subscription_first`（默认分支）：有活跃订阅先走订阅，失败再回退钱包。
2. 走订阅资金来源时
- `SubscriptionFunding.PreConsume` 调用 `model.PreConsumeUserSubscription(...)`：
- 只选 `status=active` 且 `end_time>now` 的订阅。
- 优先使用最早到期订阅（`end_time asc`）。
- 若到达重置点会先重置再判断可用额度。
- 预扣成功会写幂等记录（按 `request_id`）并增加 `amount_used`。
3. 请求完成结算时
- `SettleBilling` -> `BillingSession.Settle(actualQuota)`。
- 订阅路径通过 `PostConsumeUserSubscriptionDelta` 按差额补扣或返还（相对预扣）。
4. 请求失败退款时
- `BillingSession.Refund` 异步调用 `RefundSubscriptionPreConsume(request_id)`。
- 幂等回退预扣额度，避免重复退款。

## 5. 生命周期任务（运行期持续触发）
1. 启动入口：`main.go` 调用 `StartSubscriptionQuotaResetTask()`。
2. 周期（每分钟）执行两类维护：
- `ExpireDueSubscriptions`：到期订阅从 active -> expired，必要时回退用户分组。
- `ResetDueSubscriptions`：到达 `next_reset_time` 的订阅把 `amount_used` 重置为 0，并计算下一次重置时间。
3. 另有预扣幂等记录清理任务，定期删除旧记录。

## 6. 管理员“作废/删除订阅”在什么时候用
1. `AdminInvalidateUserSubscription`：立即取消并结束订阅（状态改 cancelled，`end_time=now`），必要时回退分组。
2. `AdminDeleteUserSubscription`：硬删除订阅记录，删除前也会尝试处理分组回退。

## 7. 对问题的直接回答
1. 创建的订阅有什么用：用于模型请求计费时作为真实可扣减额度来源（不是仅展示），并可附带用户分组升级能力。
2. 在什么时候用：在每次请求的预扣、结算、退款流程里按偏好被选择；在后台定时任务里按时间发生重置和过期处理。

## 8. 关键代码定位
1. 路由入口：`router/api-router.go`（`/subscription` 与 `/subscription/admin`）。
2. 管理员创建：`controller/subscription.go` + `model/subscription.go::AdminBindSubscription`。
3. 支付回调落订阅：`controller/subscription_payment_epay.go`、`controller/topup_stripe.go`、`controller/topup_creem.go`。
4. 计费选择与回退：`service/billing_session.go::NewBillingSession`。
5. 订阅预扣/结算/退款：`model/subscription.go::PreConsumeUserSubscription`、`PostConsumeUserSubscriptionDelta`、`RefundSubscriptionPreConsume`。
6. 定时重置/过期：`service/subscription_reset_task.go` + `model/subscription.go::ResetDueSubscriptions` 和 `ExpireDueSubscriptions`。
