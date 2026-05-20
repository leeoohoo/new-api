# 模型补全倍率可配置实施方案

## 1. 目标

本轮改造的目标不是新增一套“价格真相”，而是基于当前代码事实，把管理台的模型计费配置统一收敛到**倍率配置（ratio）/固定价配置（model_price）**这条真实链路上，并解决 `gpt-5.4` 等模型补全倍率被后端硬锁定、无法在后台覆盖的问题。

本轮确认的实现范围如下：

1. 保持 `ModelRatio / CompletionRatio / CacheRatio / CreateCacheRatio / ImageRatio / AudioRatio / AudioCompletionRatio / ModelPrice` 仍然是唯一配置真相。
2. 取消 `gpt-5.4`、`gpt-5.4-nano` 等官方模型补全倍率的“硬编码锁定”语义，改为“默认回退值，可被显式配置覆盖”。
3. 前端主编辑页改为**直接编辑各类 ratio**，价格只作为换算结果和预览视图，不再把“价格字段”当成主编辑对象。
4. 同步扩展导入/同步链路，使其不仅覆盖 `ModelRatio / CompletionRatio / CacheRatio / ModelPrice`，还覆盖当前主编辑页支持的更多倍率字段。
5. 保持真实计费、价格展示、手动 JSON 配置、导入同步四条链路的一致性，避免出现“页面可改但计费不生效”。

---

## 2. 已确认的代码事实

以下结论均来自已读代码，而非推测。

### 2.1 当前系统的“真相源”是倍率/固定价 option JSON

价格编辑页并不存储独立价格真值，而是把倍率配置做了价格化展示。

相关文件：

- `web/src/pages/Setting/Ratio/hooks/useModelPricingEditorState.js`
- `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`

已确认行为：

- `inputPrice = modelRatio * 2`
- `completionPrice = inputPrice * completionRatio`
- `cachePrice = inputPrice * cacheRatio`
- `createCachePrice = inputPrice * createCacheRatio`
- `imagePrice = inputPrice * imageRatio`
- `audioInputPrice = inputPrice * audioRatio`
- `audioOutputPrice = audioInputPrice * audioCompletionRatio`
- 保存时再反算回：
  - `ModelRatio`
  - `CompletionRatio`
  - `CacheRatio`
  - `CreateCacheRatio`
  - `ImageRatio`
  - `AudioRatio`
  - `AudioCompletionRatio`
  - `ModelPrice`

结论：**价格页本质上是 ratio JSON 的可视化编辑器，不是另一套价格存储。**

### 2.2 `gpt-5.4` 补全倍率现在被后端硬编码并锁定

相关文件：

- `setting/ratio_setting/model_ratio.go`
- `controller/option.go`

已确认：

- `GetCompletionRatioInfo` / `getHardcodedCompletionModelRatio` 会为部分模型返回固定补全倍率和 `locked=true`
- 当前 `gpt-5.4*` 走 `6, true`
- 当前 `gpt-5.4-nano*` 走 `6.25, true`
- 前端看到 `CompletionRatioMeta.locked=true` 后，会：
  - 禁用补全相关编辑
  - 使用 `lockedCompletionRatio` 展示价格
  - 保存时跳过 `CompletionRatio` 的回写

结论：**锁定不是前端假提示，而是后端明确下发的强制语义。**

### 2.3 真实计费与对外价格展示都按倍率执行

相关文件：

- `service/quota.go`
- `model/pricing.go`

已确认：

- 文本输出成本按 `CompletionRatio` 参与计算
- 缓存读取按 `CacheRatio`
- 缓存创建按 `CreateCacheRatio`
- 音频输入按 `AudioRatio`
- 音频输出按 `AudioCompletionRatio`
- 价格接口展示同样读取 ratio_setting 中的倍率结果

结论：**只改前端价格展示而不改倍率来源，会产生“UI 改了但计费没改”的假象。**

### 2.4 手动 JSON 页与导入页本质上也在操作 ratio/fixed-price 真相

相关文件：

- `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`
- `web/src/pages/Setting/Ratio/UpstreamRatioSync.jsx`
- `controller/ratio_sync.go`
- `controller/ratio_config.go`
- `setting/ratio_setting/exposed_cache.go`

已确认：

- 手动 JSON 页直接编辑 `ModelRatio / CompletionRatio / CacheRatio / CreateCacheRatio / ImageRatio / AudioRatio / AudioCompletionRatio / ModelPrice`
- 导入/同步页当前只覆盖：
  - `ModelRatio`
  - `CompletionRatio`
  - `CacheRatio`
  - `ModelPrice`
- 后端 `/api/ratio_config` 当前只暴露：
  - `model_ratio`
  - `completion_ratio`
  - `cache_ratio`
  - `create_cache_ratio`
  - `model_price`
- `ratio_sync` 当前比较范围写死为：
  - `model_ratio`
  - `completion_ratio`
  - `cache_ratio`
  - `model_price`
- 当上游来源为 `/api/pricing` 时，后端实际只能转换出：
  - `model_ratio`
  - `completion_ratio`
  - `model_price`

结论：

1. **导入/同步不是价格真相导入，而是 ratio/fixed-price JSON 导入。**
2. **当前导入链路覆盖范围过窄，无法匹配“前端主编辑更多倍率字段”的目标。**

### 2.5 底层 option 存储已经支持更多倍率字段

相关文件：

- `model/option.go`

已确认 `UpdateOption` / `updateOptionMap` 已支持：

- `CompletionRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`
- `ModelPrice`

结论：**问题不在持久化能力，而在默认值优先级、锁定语义，以及导入/同步链路覆盖范围。**

---

## 3. 问题定义

当前问题不是“价格页做得不好看”，而是以下三个结构性问题：

### 3.1 官方模型补全倍率被后端当成“不可覆盖规则”

这使得：

- 管理员无法直接修正官方价格比
- 官方调价后需要改代码发版
- JSON 页、价格页、导入页即使允许写值，也可能无法影响真实计费

### 3.2 前端主编辑对象与真实配置对象表达不一致

现状中页面主视觉仍偏向“编辑价格”，但真实保存的是倍率。这会带来两个误解：

1. 误以为页面价格字段是独立真相
2. 误以为只要放开补全价格输入，就等于补全倍率可配置

实际上应该把页面语义改正为：

> 主编辑的是 ratio，价格只是预览和换算结果。

### 3.3 导入/同步链路与主编辑字段集合不一致

当前主编辑页已经涉及：

- `ModelRatio`
- `CompletionRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`
- `ModelPrice`

但导入/同步只支持其中四项。这会导致：

- 页面可编辑，但无法从上游同步
- 局部字段支持导入，局部字段只能手填
- 用户难以理解哪些字段属于“可批量维护范围”

---

## 4. 改造原则

本轮推荐坚持以下原则：

### 4.1 不新增独立价格存储

继续保持：

- ratio JSON
- fixed price JSON

为唯一真相源。

### 4.2 显式配置优先于系统默认值

对于 `CompletionRatio` 这类存在官方默认值的字段：

- 未配置时：使用系统默认倍率
- 已配置时：优先使用配置值

### 4.3 前端主编辑 ratio，价格仅预览

页面应明确表达：

- 你正在编辑倍率
- 页面上看到的价格是按倍率换算出来的预览值

### 4.4 导入/同步范围与主编辑字段对齐

既然本轮已确认“主编辑 + 导入/同步一起扩”，则导入链路必须同步扩展，避免留下新的行为不一致。

### 4.5 保持 fixed-price 与 ratio 互斥

沿用当前规则：

- 选择 `model_price` 时，删除该模型的 ratio 类配置
- 选择任意 ratio 类字段时，删除该模型的 `ModelPrice`

这是必要的冲突消解规则，不能删除。

---

## 5. 目标行为

### 5.1 补全倍率的目标行为

#### 未显式配置 `CompletionRatio` 时

- `gpt-5.4` 继续默认使用现有回退值 `6`
- `gpt-5.4-nano` 继续默认使用现有回退值 `6.25`
- 其他已有官方模型继续沿用当前默认补全倍率

即：**默认行为不变。**

#### 显式配置 `CompletionRatio` 后

- 后端真实计费优先读配置值
- 前端编辑页回显配置值
- 手动 JSON 页回显配置值
- 导入/同步写入后立即生效
- 价格接口展示优先读配置值

即：**显式配置覆盖默认值。**

### 5.2 前端主编辑页的目标行为

主编辑页应改为以各类 ratio 为主：

- `ModelRatio`
- `CompletionRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`

页面展示价格时：

- 可继续展示对应价格预览
- 但不应让“价格字段”成为唯一可编辑入口
- 不再通过“locked completion price”表达后端强制规则

### 5.3 导入/同步页的目标行为

导入页需要扩展支持：

- `model_ratio`
- `completion_ratio`
- `cache_ratio`
- `create_cache_ratio`
- `image_ratio`
- `audio_ratio`
- `audio_completion_ratio`
- `model_price`

并保持：

- price 与 ratio 互斥
- 单模型允许一次同步多个 ratio 字段
- 差异对比、选择应用、本地写回、冲突确认文案保持一致

---

## 6. 具体改造点

## 6.1 后端：把补全倍率从“硬锁定”改成“默认回退”

### 需修改文件

- `setting/ratio_setting/model_ratio.go`
- `controller/option.go`

### 当前问题

当前逻辑中：

- `GetCompletionRatio(name)` 可能直接返回硬编码补全倍率
- `GetCompletionRatioInfo(name)` 同时返回 `locked=true`
- 前端据此禁用编辑与保存

### 推荐改法

#### 1）重构补全倍率默认值语义

把现有 `getHardcodedCompletionModelRatio` 从“强制锁定规则”改成“默认回退规则”。

建议语义调整为：

- `getDefaultCompletionModelRatio(name)`：返回官方模型默认补全倍率
- 不再表达 locked 语义

#### 2）调整 `GetCompletionRatio` 优先级

推荐优先级：

1. 若 `completionRatioMap` 中存在显式配置，直接返回配置值
2. 否则使用 `getDefaultCompletionModelRatio(name)`
3. 若默认规则也未命中，再回退到 `1`

这样可以保证：

- 老模型未配置时行为不变
- 一旦后台配置，就立即覆盖默认值

#### 3）调整 `GetCompletionRatioInfo`

推荐改为返回“当前生效倍率信息”，但不再把官方默认值标记为 locked。

可选两种方式：

- 最小改动：仍返回 `{ ratio, locked }`，但统一 `locked=false`
- 更合理：扩展为 `{ ratio, locked, source }`
  - `source = configured | default | fallback`

本轮如果追求最小实现，可先去掉锁定语义；若前端文案需要区分“当前是默认值还是显式配置值”，可增加 `source` 字段。

### 预期效果

- `gpt-5.4` 默认仍是 `6`
- `gpt-5.4-nano` 默认仍是 `6.25`
- 但一旦 `CompletionRatio` JSON 中写入该模型值，后端会优先使用配置值

---

## 6.2 前端：主编辑对象改为 ratio，价格仅做换算视图

### 需修改文件

- `web/src/pages/Setting/Ratio/hooks/useModelPricingEditorState.js`
- `web/src/pages/Setting/Ratio/ModelPricingEditor.jsx`
- `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`
- 对应 i18n 文案

### 当前问题

当前实现虽然底层保存的是 ratio，但页面仍然把输入/补全/缓存等“价格字段”作为核心交互对象，并受 `completionRatioLocked` 影响。

### 推荐改法

#### 1）取消 `completionRatioLocked` 对编辑和保存的阻断

在 `useModelPricingEditorState.js` 中：

- `buildModelState` 不再依赖 locked 值覆盖 `completionRatio`
- `serializeModel` 始终根据当前编辑值回写 `CompletionRatio`
- 删除或降级 `lockedCompletionRatio`、`completionRatioLocked` 在保存逻辑中的作用

#### 2）把表单主字段切换为各类 ratio

主编辑字段应直接对应：

- `modelRatio`
- `completionRatio`
- `cacheRatio`
- `createCacheRatio`
- `imageRatio`
- `audioRatio`
- `audioCompletionRatio`

价格相关字段保留为：

- 自动换算预览
- 便于运营理解大致价格
- 非真相字段

#### 3）修正文案

尤其需要修正以下误导性认知：

- `CompletionRatio` “仅对自定义模型有效” → 删除
- “补全价格已锁定” → 删除
- 可改为：
  - “补全倍率”
  - “当前价格为根据倍率换算的预览值”
  - “若未显式配置，将使用系统默认倍率”

### 预期效果

页面语义将与真实存储对象一致：

- 用户改的是倍率
- 系统存的是倍率
- 计费用的是倍率
- 价格只是解释视图

---

## 6.3 导入/同步：把字段覆盖范围扩到与主编辑页一致

### 需修改文件

- `controller/ratio_sync.go`
- `controller/ratio_config.go`（如需配合暴露更多字段）
- `setting/ratio_setting/exposed_cache.go`
- `web/src/pages/Setting/Ratio/UpstreamRatioSync.jsx`

### 当前问题

当前导入/同步只覆盖：

- `model_ratio`
- `completion_ratio`
- `cache_ratio`
- `model_price`

但当前主编辑页已涉及更多字段，因此导入范围必须补齐。

### 推荐改法

#### 1）扩展后端 `ratio_sync` 的 `ratioTypes`

扩展为：

- `model_ratio`
- `completion_ratio`
- `cache_ratio`
- `create_cache_ratio`
- `image_ratio`
- `audio_ratio`
- `audio_completion_ratio`
- `model_price`

#### 2）扩展 `/api/ratio_config` 暴露数据

`setting/ratio_setting/exposed_cache.go` 当前只返回：

- `model_ratio`
- `completion_ratio`
- `cache_ratio`
- `create_cache_ratio`
- `model_price`

本轮应补充：

- `image_ratio`
- `audio_ratio`
- `audio_completion_ratio`

否则：

- 本地差异计算无法覆盖这些字段
- 上游就算提供了这些字段，也无法和本地完整比较

#### 3）扩展前端 `UpstreamRatioSync` 的本地读写集合

当前 `applySync / performSync` 只处理四类字段，本轮需扩展为：

- `ModelRatio`
- `CompletionRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`
- `ModelPrice`

#### 4）保留互斥删除规则

推荐继续保持：

- 若导入 `model_price`：删除该模型所有 ratio 字段
- 若导入任意 ratio 字段：删除该模型 `ModelPrice`

需要注意的是，“所有 ratio 字段”在本轮扩展后不再只包含三个，而应覆盖：

- `ModelRatio`
- `CompletionRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`

#### 5）兼容 `/api/pricing` 数据源能力边界

即便扩展本地同步能力，也要明确：

- 若上游返回的是 `/api/pricing`，当前可稳定推导的仍主要是：
  - `model_ratio`
  - `completion_ratio`
  - `model_price`
- 其他字段能否同步，取决于上游是否改为返回 `ratio_config` 风格的全量倍率数据

因此本轮导入扩展包含两层含义：

1. **本地链路支持更多字段**
2. **若上游确实提供这些字段，则可完整导入**

但不能承诺从旧版 `/api/pricing` 自动凭空得到全部高级倍率字段。

---

## 6.4 手动 JSON 页：文案与字段解释要与新语义一致

### 需修改文件

- `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`

### 需要调整的内容

1. 删除 `CompletionRatio`“仅对自定义模型有效”的表述
2. 明确官方模型也可以通过 JSON 显式覆盖默认补全倍率
3. 如页面有价格型描述，应统一改成“倍率配置，对应价格仅为换算结果”
4. 若保留示例 JSON，应补充新增同步支持字段的示例

---

## 7. 兼容规则

## 7.1 默认值兼容

对于未显式配置的模型：

- `gpt-5.4`、`gpt-5.4-nano` 等继续沿用现有默认补全倍率
- 避免上线后出现大范围计费突变

## 7.2 显式配置优先

一旦管理员通过以下任一入口写入：

- 主编辑页
- JSON 页
- 导入/同步页

则：

- `CompletionRatio` 及其他倍率字段都应以配置值优先
- 后端计费与价格展示应立即读取新值

## 7.3 fixed price 与 ratio 互斥

同一模型应保持以下规则：

- 若使用 `ModelPrice`，则不再保留 ratio 计费配置
- 若使用任意 ratio 配置，则移除 `ModelPrice`

这是现有系统已经采用的规则，本轮继续沿用，只是扩展“ratio 字段集合”。

## 7.4 导入能力的上游边界

本轮会把本地系统改造成“支持导入更多倍率字段”，但最终能导入哪些字段，还取决于上游端点实际是否提供这些字段。

需要在文档和发布说明中明确：

- 本地系统已支持全量写回
- 老旧上游如果仍只提供 `/api/pricing` 三类/四类数据，则高级倍率字段仍然可能缺失

---

## 8. 实施顺序

推荐按以下顺序落地：

### 第一步：更新方案与文案

先把文档、页面语义、字段说明统一到“ratio 为主、价格为预览”。

### 第二步：后端去锁定、保留默认回退

先改：

- `setting/ratio_setting/model_ratio.go`
- `controller/option.go`

保证后端真实计费和前端读取的补全倍率都支持“显式配置优先”。

### 第三步：前端主编辑链路改为 ratio 主模式

再改：

- `useModelPricingEditorState.js`
- `ModelPricingEditor.jsx`
- `ModelRatioSettings.jsx`

保证前端不会再因为 locked 语义阻断保存。

### 第四步：扩展导入/同步链路

最后改：

- `ratio_sync.go`
- `exposed_cache.go`
- `UpstreamRatioSync.jsx`

使差异对比、本地应用、互斥删除规则全部覆盖新增倍率字段。

### 第五步：做联调验证

覆盖：

- 主编辑页
- JSON 页
- 导入页
- 后端计费
- 对外价格展示

---

## 9. 验证清单

## 9.1 后端默认值与覆盖逻辑

验证点：

1. 未配置 `CompletionRatio.gpt-5.4` 时，读取结果仍为 `6`
2. 未配置 `CompletionRatio.gpt-5.4-nano` 时，读取结果仍为 `6.25`
3. 写入 `CompletionRatio["gpt-5.4"] = 7` 后：
   - 价格页读取为 `7`
   - 真实计费读取为 `7`
   - 对外价格展示读取为 `7`

## 9.2 主编辑页验证

验证点：

1. `gpt-5.4` 不再显示“补全价格已锁定”
2. 可直接编辑 `CompletionRatio`
3. 保存后 `CompletionRatio` JSON 正确更新
4. 其他倍率字段仍能正常回写：
   - `CacheRatio`
   - `CreateCacheRatio`
   - `ImageRatio`
   - `AudioRatio`
   - `AudioCompletionRatio`
5. 价格预览随 ratio 变化实时更新

## 9.3 JSON 页验证

验证点：

1. 直接编辑 `CompletionRatio` 后，主编辑页正确回显
2. 直接编辑 `CreateCacheRatio / ImageRatio / AudioRatio / AudioCompletionRatio` 后，主编辑页与价格预览正确回显
3. 文案不再误导“仅自定义模型可用”

## 9.4 导入/同步验证

验证点：

1. `ratio_sync` 差异结果中可出现：
   - `create_cache_ratio`
   - `image_ratio`
   - `audio_ratio`
   - `audio_completion_ratio`
2. `UpstreamRatioSync` 可选择并写回这些字段
3. 选择 `model_price` 时，会删除该模型全部 ratio 字段
4. 选择任意 ratio 字段时，会删除该模型 `ModelPrice`
5. 同步完成后主编辑页、JSON 页、价格展示三处回显一致

## 9.5 真实计费验证

验证点：

1. 发起 `gpt-5.4` 请求，确认输出倍率使用配置值而非旧硬编码值
2. 发起带缓存/音频场景请求，确认对应倍率生效
3. 检查消费日志或调试信息，确认真实参与计算的倍率与后台配置一致

---

## 10. 风险与控制

## 风险 1：去掉锁定后默认倍率丢失

### 风险描述

若优先级改错，未配置模型可能从历史默认值退回 `1`，导致计费突变。

### 控制措施

- 保留默认补全倍率回退函数
- 对 `gpt-5.4 / gpt-5.4-nano / gpt-5` 做专项回归

## 风险 2：只改前端语义，未改后端读取优先级

### 风险描述

页面显示可改，但真实扣费仍按旧值。

### 控制措施

- 后端逻辑必须优先上线或与前端同步上线
- 用真实请求验证 `completion_ratio` 生效结果

## 风险 3：导入扩展后互斥删除范围不完整

### 风险描述

若只删除旧三类 ratio，而漏删 `CreateCacheRatio / ImageRatio / AudioRatio / AudioCompletionRatio`，同一模型可能同时残留 fixed price 与部分 ratio 配置。

### 控制措施

- 将“全量 ratio 字段集合”抽成统一常量
- 在导入写回前统一处理互斥删除

## 风险 4：上游仍是旧版 `/api/pricing`，导致部分字段无数据

### 风险描述

本地虽然已支持更多字段，但旧上游接口没有提供这些值，运营可能误以为“系统没同步成功”。

### 控制措施

- 在同步页或发布说明中明确区分：
  - 本地系统已支持更多字段
  - 上游是否提供取决于上游接口能力
- 优先推荐上游提供 `ratio_config` 风格全量数据

## 风险 5：旧文案继续误导运营

### 风险描述

如果页面仍然出现“仅自定义模型有效”“补全价格已锁定”等旧说法，运营会误解新行为。

### 控制措施

- 同步修改主编辑页、JSON 页、导入页文案
- 在发布说明中明确“官方模型补全倍率现在可被后台覆盖”

---

## 11. 推荐落地结论

本轮推荐结论如下：

> **继续保持 ratio/fixed-price JSON 为唯一真相源；把官方模型补全倍率从“硬编码锁定”改成“默认回退、可被显式配置覆盖”；并将前端主编辑、手动 JSON、导入/同步三条管理链路统一到“直接编辑倍率”这一语义下。**

这比“新增价格真相”更符合当前架构，也能最大限度复用现有计费与 option 持久化链路。

### 本轮最小完整改动集合

后端：

- `setting/ratio_setting/model_ratio.go`
- `controller/option.go`
- `controller/ratio_sync.go`
- `setting/ratio_setting/exposed_cache.go`

前端：

- `web/src/pages/Setting/Ratio/hooks/useModelPricingEditorState.js`
- `web/src/pages/Setting/Ratio/ModelPricingEditor.jsx`
- `web/src/pages/Setting/Ratio/ModelRatioSettings.jsx`
- `web/src/pages/Setting/Ratio/UpstreamRatioSync.jsx`
- 相关 i18n 文案

### 本轮交付后的最终语义

1. 后台真正编辑的是 ratio / fixed price 配置
2. 价格只是换算出来的预览视图
3. 官方模型补全倍率可以覆盖
4. 导入/同步能覆盖与主编辑页一致的倍率字段集合
5. 真实计费、价格展示、配置回显保持同源一致

---

## 12. 一句话结论

当前系统已经确认：**价格页、JSON 页、导入页、真实计费，本质上都以 ratio/fixed-price option JSON 为真相。**

因此本轮正确的改法不是“让价格页看起来能改”，而是：

> **把前端主编辑对象明确为各类 ratio，取消官方模型补全倍率的后端硬锁定，并把导入/同步链路一起扩到相同字段集合，确保配置、展示与计费完全一致。**
