# Admin 全维度用量报表方案（V2 Blueprint）

## 1. 背景与目标

当前 `/console/admin-usage-report` 页面已具备基础能力，但信息密度和分析深度不足，主要问题：

1. 只有单一模型柱状图 + 用户表，缺少“时间趋势”和“多维联动”。
2. 无法直接回答核心管理问题：  
   - 整体 Token 使用趋势是否异常？  
   - 哪些模型增长最快？  
   - 哪些渠道错误率高？  
   - 用量集中在哪些用户/分组？
3. 缺少下钻链路（总览 -> 维度 -> 明细），不利于定位问题。

V2 目标：构建一个“可运营、可诊断、可追踪”的全方位管理报表页。


## 2. 基于现有代码的数据能力盘点

## 2.1 现有数据源

1. `logs`（明细主数据）
- 字段已具备：`created_at`, `type`, `model_name`, `channel_id`, `user_id`, `username`, `quota`, `prompt_tokens`, `completion_tokens`, `use_time`, `group`, `request_id`。
- 当前 `type` 已覆盖消费与错误（`LogTypeConsume`, `LogTypeError`），可直接计算成功率/错误率。

2. `quota_data`（小时级聚合）
- 现有聚合维度：`user_id + username + model_name + created_at(小时桶)`。
- 指标：`count`, `quota`, `token_used`。
- 由 `RecordConsumeLog` 在 `DataExportEnabled` 时异步写入缓存并落库。

## 2.2 现有接口

1. `GET /api/log/stat`  
- 指标：`quota`, `rpm`, `tpm`（`rpm/tpm`为最近60秒窗口）。

2. `GET /api/log/usage_report/trend`  
- 指标：`request_count`, `success_count`, `error_count`, `quota`, `token_used`（按 bucket）。

3. `GET /api/log/usage_report/model`
4. `GET /api/log/usage_report/channel`
5. `GET /api/log/usage_report/user`

6. `GET /api/data`（基于 `quota_data`）
- 返回模型+小时桶聚合数据（`model_name`, `count`, `quota`, `token_used`, `created_at`）。

## 2.3 数据能力结论

现有数据足够支撑 V2 的核心目标（整体趋势、模型趋势、渠道/用户分布、明细下钻）。  
建议仅补充少量接口，用于降低前端拼装复杂度和数据库压力。


## 3. 页面信息架构（全维度）

## 3.1 顶部全局筛选区（固定吸顶）

统一参数（一次筛选，全页面联动）：

1. 时间范围：最近24h / 7d / 30d / 自定义。
2. 粒度：`5m / 1h / 1d`（按范围自动推荐）。
3. 模型（多选，支持搜索）。
4. 渠道（多选）。
5. 分组 `group`（单选/多选）。
6. 用户关键字（用户名模糊搜索）。
7. 对比开关：环比上一个同长度时间段。
8. 操作按钮：查询、重置、导出。

## 3.2 模块 A：总览 KPI（第一屏）

建议卡片：

1. 总请求数
2. 成功请求数
3. 错误请求数
4. 成功率
5. 总 Token 用量
6. 总配额消耗（quota）
7. 近60秒 RPM
8. 近60秒 TPM
9. 活跃模型数（窗口内有调用）
10. 活跃用户数（窗口内有调用）

## 3.3 模块 B：整体趋势（你最关心的 Token 趋势）

1. 图表 1：整体 Token 使用趋势（主折线/面积图）
- X：时间桶
- Y：`token_used`
- 支持对比上周期（虚线）
- 支持鼠标框选区间后下钻

2. 图表 2：请求与错误趋势（双轴）
- 柱：`request_count`
- 线：`error_rate` 或 `error_count`

3. 图表 3：配额消耗趋势
- 显示 `quota` 的时间变化，便于对账与预算追踪

## 3.4 模块 C：模型分析（模型趋势是核心）

1. 图表 4：各模型 Token 使用趋势（Top N 模型）
- 形态：堆叠面积 + 图例开关
- 指标：`token_used`
- 默认 Top 10，支持切换排序口径（按 token/request/quota）

2. 图表 5：各模型请求趋势（Top N）
- 形态：多折线
- 指标：`request_count`

3. 图表 6：模型占比（Treemap 或环图）
- 指标切换：Token / 请求 / 配额

4. 表格：模型表现榜
- 列：模型名、请求数、Token、Quota、成功率、错误率、环比增幅
- 点击模型 -> 全页面自动下钻该模型

## 3.5 模块 D：渠道分析

1. 渠道对比条形图：请求、Token、错误率。
2. 渠道趋势图：选中渠道后展示时间趋势。
3. 渠道质量表：渠道名、成功率、错误率、平均耗时（可选）。

## 3.6 模块 E：用户分析

1. 用户分布图（Top N）：
- 指标切换：请求 / Token / Quota。

2. 用户明细表：
- 列：用户ID、用户名、请求数、Token、Quota、成功率、错误率。
- 支持分页、排序、关键字搜索。

## 3.7 模块 F：明细与审计

1. 明细日志表（复用 `/api/log/`）：
- 时间、用户、模型、渠道、Token、Quota、状态、request_id。
2. 与上方筛选联动。
3. 支持导出 CSV（当前筛选结果）。


## 4. 接口方案（在现有基础上最小增量）

## 4.1 现有接口继续保留

1. `/api/log/usage_report/trend`
2. `/api/log/usage_report/model`
3. `/api/log/usage_report/channel`
4. `/api/log/usage_report/user`
5. `/api/log/stat`
6. `/api/data`

## 4.2 建议新增接口（降低前端拼装和请求数）

1. `GET /api/log/usage_report/overview`
- 返回 KPI 一次性聚合：
```json
{
  "success": true,
  "data": {
    "request_count": 0,
    "success_count": 0,
    "error_count": 0,
    "success_rate": 0,
    "error_rate": 0,
    "token_used": 0,
    "quota": 0,
    "active_model_count": 0,
    "active_user_count": 0,
    "rpm": 0,
    "tpm": 0
  }
}
```

2. `GET /api/log/usage_report/model_trend`
- 维度：`model_name + bucket`
- 用于“各模型时间趋势图”。

3. `GET /api/log/usage_report/channel_trend`
- 维度：`channel_id + bucket`
- 用于渠道趋势。

4. （可选）`GET /api/log/usage_report/export`
- 返回 CSV 文件流或下载地址。

## 4.3 统一查询参数建议

1. `start_timestamp`, `end_timestamp`
2. `bucket_seconds`
3. `model_name`（支持多值，逗号分隔）
4. `channel`（支持多值）
5. `group`
6. `user_keyword`
7. `top_n`
8. `compare`（`none|previous_period`）


## 5. 数据计算口径（统一规则）

1. 请求数：`logs.type IN (consume, error)`。
2. 成功数：`logs.type = consume`。
3. 错误数：`logs.type = error`。
4. 成功率：`success_count / request_count`。
5. Token 用量：`sum(prompt_tokens + completion_tokens)`。
6. 配额消耗：`sum(quota)`。
7. 活跃模型数：窗口内去重模型数。
8. 活跃用户数：窗口内去重用户数。
9. RPM/TPM：最近60秒窗口（复用现有逻辑）。


## 6. 性能与跨库兼容策略（SQLite / MySQL / PostgreSQL）

## 6.1 查询分层策略

1. 近7天优先走 `logs`，保证实时和精细。
2. 大时间窗（如30天/90天）优先走 `quota_data` 聚合（小时级），必要时回退 `logs`。
3. 对 `model_trend/channel_trend` 增加 `top_n` 限制，避免图表爆量。

## 6.2 跨数据库约束

1. 优先 GORM 聚合，避免方言函数。
2. 时间分桶沿用应用层 bucket 计算（避免 `date_trunc`/`from_unixtime` 方言差异）。
3. 保留字与布尔条件沿用项目已有兼容写法（`logGroupCol`、兼容常量）。

## 6.3 缓存建议

1. 对总览和趋势接口增加 10~30 秒短 TTL 缓存。
2. 缓存 key 包含完整筛选参数，避免脏读。


## 7. 前端实现方案（页面结构）

建议将页面拆成可维护模块：

1. `AdminUsageReportPage`（容器 + 全局筛选状态）
2. `UsageOverviewCards`（KPI）
3. `UsageTrendPanel`（整体趋势）
4. `ModelTrendPanel`（模型趋势与榜单）
5. `ChannelPanel`（渠道分析）
6. `UserPanel`（用户分布与明细）
7. `UsageDetailTable`（明细与导出）

交互关键点：

1. 图表点击即下钻（模型/渠道/用户）。
2. 图例开关即筛选（不额外刷新可先前端过滤）。
3. 时间框选回填筛选区，并触发全局刷新。
4. 所有模块共享同一 QueryState，避免状态分裂。


## 8. 分阶段落地计划

## Phase 1（快速升级，1~2 天）

1. 新增“总览 KPI + 整体 Token 趋势 + 请求/错误趋势”。
2. 接入现有 `/usage_report/trend` 与 `/log/stat`。
3. 保留现有模型图和用户表，增强视觉与筛选联动。

## Phase 2（核心增强，2~4 天）

1. 新增 `model_trend` 接口。
2. 完成“各模型使用趋势（TopN）+ 模型榜单 + 占比图”。
3. 支持周期对比（环比）。

## Phase 3（运营化，2~3 天）

1. 渠道趋势 + 渠道质量分析。
2. 明细导出能力。
3. 异常提示（错误率突增、Token 峰值告警）。


## 9. 验收标准（面向业务）

1. 可以直接看到“整体 Token 趋势图”并按时间/模型/渠道筛选。
2. 可以直接看到“各模型时间趋势图”并支持 TopN 和下钻。
3. KPI 与图表口径一致（同筛选条件下可互相校验）。
4. 30 天范围查询在可接受时间内返回（建议 < 3s，具体看数据量）。
5. SQLite / MySQL / PostgreSQL 三库行为一致。


## 10. 你可以先看的最终页面骨架（建议）

```text
筛选区（时间/模型/渠道/分组/用户/粒度/对比）
  └─ KPI 卡片行（请求、成功率、Token、Quota、RPM/TPM、活跃数）
      └─ 整体趋势区（Token趋势、请求&错误趋势、Quota趋势）
          └─ 模型分析区（模型趋势TopN、模型占比、模型榜单）
              └─ 渠道分析区（渠道对比、渠道趋势、渠道质量）
                  └─ 用户分析区（用户分布、用户明细）
                      └─ 明细审计区（日志明细 + 导出）
```


---

如果你认可这个方向，下一步我可以直接把 `Phase 1` 的页面改造拆成具体开发任务清单（后端接口、前端组件、联调与验收项），并按你希望的视觉风格给一个更“数据中台化”的 UI 草图说明。
