# Upstream Sync Log

这个文件用于记录官方 `Wei-Shaw/sub2api` 的同步状态，避免后续继续靠临时记忆判断。

## 使用规则

每次准备同步官方更新前，先执行：

```bash
git fetch origin --prune
git rev-list --left-right --count HEAD...origin/main
git cherry -v HEAD origin/main
git log --cherry-pick --right-only --no-merges --oneline HEAD...origin/main
```

判定口径：

- `ahead/behind` 只看 Git 图谱，不代表真实“功能是否已同步”
- `git cherry -v HEAD origin/main`
  - `-` 表示上游补丁已经等价存在于本地
  - `+` 表示上游补丁当前仍未被本地等价吸收
- 如果本地是以 `cherry-pick`、重写提交、或后续继续演进的方式并入上游功能，`behind` 可能仍然很高，这是正常现象

## 当前基线

- 记录时间：`2026-04-13`
- 本地 `HEAD`：`936ffe0d`
- 你的 fork `sv3nbeast/main`：`936ffe0d`
- 官方 `origin/main`：`ad64190b`
- 图谱差异：
  - 相对官方：`behind 67 / ahead 57`
  - 相对 fork：`behind 0 / ahead 0`

备注：

- 当前本地与 fork 已完全对齐
- `behind 67` 仍然不能直接等同于“有 67 个功能没同步”，必须继续结合 `git cherry` 和当前代码判断

## 已同步的重要上游改动

这些改动已经实际进入本地代码，不要重复同步。

### 调度快照与缓存

- 官方 `118ff85f` `fix: 同步 LoadFactor 到调度快照缓存`
  - 本地对应提交：`3c81caef`
  - 备注：当前 [backend/internal/repository/scheduler_cache.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache.go) 已包含 `LoadFactor`

### OpenAI messages dispatch

- 官方 `23c4d592` `feat(group): 增加messages调度模型映射配置`
- 官方 `4de4823a` `feat(openai): 支持messages模型映射与instructions模板注入`
- 官方 `d765359f` `test(admin): 增加messages调度表单状态转换测试`
- 官方 `de9b9c9d` `feat(admin): 增加分组 messages 调度映射配置界面`
- 官方 `57d0f979` `fix(frontend): 补全 messages 调度国际化文案`

备注：

- 这批能力已同步
- 但上游后续还有和 messages dispatch 相关的补丁未并入，见“尚未同步但值得评估”的部分

### 低风险修复

- 官方 `ce833d91` `fix: include home_content URL in CSP frame-src origins`
- 官方 `6401dd7c` `fix(ops): increase error log request body limit from 10KB to 256KB`
- 官方 `cb016ad8` `fix: handle Anthropic credit balance exhausted (400) as account error`
- 官方 `b6bc0423` / `217b7ea6` `fix(deps): upgrade axios`

## 本地独有的重要改动

这些不是官方功能，不要在同步上游时误覆盖。

- Antigravity 多账号调度与切号优化
- `claude-haiku-4-6 -> claude-sonnet-4-6` 默认映射修复
- 模型级 cooldown / quota exhausted / 账号选择修复
- 邮箱注册域名黑名单
- 服务状态探针、Bark 监控、展示开关
- Bark 监控“仅通知恢复，不通知异常”开关
- 内置随机 TLS 指纹与账号级 TLS 相关能力
- Antigravity OAuth 401 改为“临时不可调度 3 分钟 + 到点强制刷新 token”
- 调度快照保留 `model_rate_limits`，避免反复选中已模型级冷却账号
- 品牌字样从 `sub2api` 调整为 `subapis` 的本地化改动

## 尚未同步但值得评估

这些功能后续可以继续评估，不要忘。

### 中低风险，建议后续逐步并入

- 官方 `265687b5` `fix: 优化调度快照缓存以避免 Redis 大 MGET`
- 官方 `02a66a01` `feat: support OIDC login.`
- 官方 `8e1a7bdf` `fix: fixed an issue where OIDC login consistently used a synthetic email address`
- 官方 `5f8e60a1` `feat(table): 表格排序与搜索改为后端处理`
- 官方 `66e15a54` `fix(export): 导出逻辑与当前筛选条件对齐`
- 官方 `ad80606a` `feat(settings): 增加全局表格分页配置,支持自定义`
- 官方 `13124059` `fix(settings): 补齐公开设置中的表格分页字段返回`
- 官方 `07d2add6` `fix(sidebar): smooth collapse transitions`
- 官方 `fe211fc5` `fix(ui): 修复在 macOS 系统下数据表格横向滚动条闪隐和消失的问题`
- 官方 `9648c432` `fix(frontend): resolve TS2352 type assertion error in API client`
- 官方 `67a05dfc` `fix: honor table defaults and preserve dispatch mappings`
- 官方 `7dc7ff22` `fix: preserve messages dispatch config in repository hydration`
- 官方 `f480e573` `fix: align table defaults and preserve sidebar svg colors`

备注：

- `265687b5` 当前不能再视为“已经和本地等价同步”
- 本地已有更早一版调度快照 hydration 实现，但和官方这次重构仍有较大差异，主要集中在：
  - [backend/internal/repository/scheduler_cache.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache.go)
  - [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go)
  - [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go)
  - [backend/internal/config/config.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/config/config.go)
- 这条需要专项回归后再评估是否合入，不能直接当低风险补丁处理

### 高风险，当前明确不并

- 官方 `63d1860d` 及后续整套 `payment` 系统

不并原因：

- 本地已有独立的 `sub2apipay`
- 官方 payment 会重改后台设置、支付页、Webhook、订阅体系
- 直接合入很容易和现有支付体系重叠

### 低优先级，可忽略

- sponsors 更新
- README / docs 文案更新
- 纯 lint / 纯 test 修复
- Sora 清理类提交
- 单纯版本号同步，如 `ad64190b`

## 已知注意事项

- `git cherry` 里如果上游某个提交仍显示 `+`，不一定代表本地完全没有这项能力
- 如果本地是“同功能不同实现”或“先同步后又继续演进”，`patch-id` 可能不相同
- 所以判断是否真的未同步，必须同时看：
  - 提交历史
  - 当前代码
  - 对应测试

## 最近本地新增记录

### 2026-04-13

- 本地新增提交：`cc1a48dc`
  - `fix: 保留调度快照模型级限流状态`
  - 说明：修复调度快照 metadata 丢失 `model_rate_limits`，避免不同请求反复撞同一批已模型级冷却账号
  - 状态：已发布生产，已推送 fork

- 本地新增提交：`936ffe0d`
  - `feat: 支持 Bark 仅通知恢复状态`
  - 说明：为 `deploy/monitor.sh` 增加 `BARK_NOTIFY_ALERT` / `BARK_NOTIFY_RECOVER` 开关，当前生产配置为“异常静默、恢复通知”
  - 状态：已发布生产，已推送 fork

### 2026-04-14

- 本地 HEAD：`66e49494`
- 官方 `origin/main`：`e534e9ba`
- 本次关注范围：`ad64190b..e534e9ba`

已同步：

- 官方 `b7edc3ed` -> 本地 `d026fbb8`
  - `fix(gateway): 兼容 Cursor /v1/chat/completions 的 Responses API body`
  - 说明：补了 Cursor 把 Responses API 形状 body 打到 `/v1/chat/completions` 时的兼容逻辑

- 官方 `422e25c9` -> 本地 `992d80de`
  - `fix(gateway): 剥离 Cursor raw body 透传路径中 Codex 不支持的 Responses API 参数`
  - 说明：补了 `prompt_cache_retention` 等字段过滤，避免 Codex upstream 报 unsupported parameter

- 官方 `3a113481` -> 本地 `7a6d0d1b`
  - `fix(frontend): avoid mounting hidden mobile table`
  - 说明：移动端/桌面端表格改为按视口切换挂载，减少隐藏表格的额外渲染

- 官方 `abe42675` -> 本地 `b61aba5a`
  - `fix(frontend): lazy load mobile account usage cells`
  - 说明：账号用量卡片在移动端改为进入视口后再加载，降低管理页初始压力

- 官方 `a1e299a3` -> 本地 `66e49494`
  - `fix: Anthropic 非流式路径在上游终态事件 output 为空时从 delta 事件重建响应内容`
  - 说明：修复 `/v1/messages` 非流式路径在上游只给 delta、不在终态事件带 output 时返回空内容的问题

- 官方 `f9f57e95` -> 本地 `a5c95d1c`
  - `fix(migrations): add 097 to restore settings.updated_at default`
  - 说明：补了 `backend/migrations/097_fix_settings_updated_at_default.sql`，避免历史实例在后续原生 SQL 插入 `settings` 时触发 `updated_at` 非空约束错误

明确跳过：

- 官方 `f498eb8f`
  - 原因：属于官方 payment 系统补丁，本地继续使用独立 `sub2apipay`

- 官方 `24f0eebc`
  - 原因：只改官方 payment 二维码页面，本地当前仓库没有对应文件

- 官方 `92f4a6bb`
  - 原因：README 更新，非功能变更

- 官方 `e534e9ba`
  - 原因：仅同步版本号，不影响功能

备注：

- 本次同步未碰 `gateway_service.go`、`antigravity_gateway_service.go`、`ratelimit_service.go` 这些本地高冲突核心文件
- 后端验证已通过：
  - `go test ./internal/service -run 'Test.*(Cursor|Codex|Anthropic|BufferedResponseAccumulator|ChatCompletions).*'`
- 前端构建验证已通过：
  - `corepack pnpm build`
- 本次是“同步到本地项目”级别，是否推送 fork / 发布生产需单独执行

继续同步：

- 官方 `7dc7ff22` -> 本地 `042017e7`
  - `fix: preserve messages dispatch config in repository hydration`
  - 说明：补齐 repository hydration 路径中的 `messages_dispatch_model_config` 保留逻辑

- 官方 `fe211fc5` -> 本地 `c1f5fbef`
  - `fix(ui): 修复在 macOS 系统下数据表格横向滚动条闪隐和消失的问题`
  - 说明：补了全局表格横向滚动条样式修复

- 官方 `07d2add6` -> 本地 `e0765b92`
  - `fix(sidebar): smooth collapse transitions`
  - 说明：补了侧边栏折叠动画与过渡细节

- 官方 `9648c432` -> 本地 `dc057b5e`
  - `fix(frontend): resolve TS2352 type assertion error in API client`
  - 说明：补了 API client 的类型安全处理，同时在同次提交里手工回补了官方 `67a05dfc` 的后端安全部分：
    - `APIKeyAuthSnapshot.Version`
    - `MessagesDispatchModelConfig` 的缓存快照保留与恢复
    - 旧快照版本失效保护

- 官方 `b9b52e74` -> 本地 `26145582`
  - `fix(sidebar): prevent version dropdown clipping`
  - 说明：补入了上游新增的侧边栏回归测试文件；功能修复点本地此前已经等价存在

- 官方 `d8fa38d5` -> 本地 `70881959`
  - `fix(account): 修复账号管理中的状态筛选`
  - 说明：补齐“不可调度”状态筛选选项，并在后端过滤中增加 `unschedulable` 语义

- 官方 `265687b5` 的安全子集 -> 本地 `ece52804`
  - 说明：调度快照读取过程中若发现缺失条目或解码失败，视为缓存未命中，触发回源重建，避免半拉子快照

### 2026-04-16

- 本次同步范围先进入 `websearch / notify` 大链中的 `websearch` 基线层，暂不触碰 payment。

已同步到本地工作区：

- 官方 `1b53ffca`
  - `feat(gateway): add web search emulation for Anthropic API Key accounts`
  - 合入内容：
    - 新增 `backend/internal/pkg/websearch/*`
    - 新增网关侧 `gateway_websearch_emulation.go`
    - 新增全局 web search emulation 配置读写与管理接口
    - 渠道 `features_config` 新增 `web_search_emulation` 承载能力
    - 账号创建/编辑、渠道编辑、后台设置页补齐对应前端入口
  - 本地化处理：
    - 保留本地已有 `account_stats_pricing_rules` / `apply_pricing_to_account_stats`
    - 保留本地 `SettingsView`、`ChannelsView`、`EditAccountModal` 结构，不引入官方 payment UI
    - `EditAccountModal`、`ChannelsView`、`SettingsView` 的 websearch UI 为手工兼容合入，不是整文件覆盖

验证结果：

- 后端：
  - `go test ./internal/service -run 'Test(Account_IsWebSearchEmulationEnabled|Channel_IsWebSearchEmulationEnabled|GetWebSearchEmulationConfig|SaveWebSearchEmulationConfig|PopulateWebSearchUsage|ResetWebSearchUsage|TestWebSearch)' -count=1`
- 前端：
  - `corepack pnpm build`

待继续：

- 官方 `fda61b06` / `60b0fa81` 这组 websearch manager 强化
- 官方 `5df73099` / `49281bbe` / `889b5b4f` / `9e0d12d3` 这组 websearch UI 与测试细化
- 后续再进入 `notify` 大链，避免本次把两类功能混在一个提交里

- 后续补充：
  - 本地新增 `739089c0`
    - `feat(websearch): merge upstream emulation base`
    - 状态：仅本地提交，尚未推 fork / 未发布生产

- 本次继续吸收：
  - 官方 `fda61b06`
    - `feat(websearch): proxy failover, timeout, quota-weighted load balancing`
    - 已合入：
      - `backend/internal/pkg/websearch/manager.go`
      - `backend/internal/service/gateway_websearch_emulation.go`
  - 官方 `60b0fa81`
    - `fix(websearch): improve isProxyError detection and add manager tests`
    - 已合入：
      - `backend/internal/pkg/websearch/manager_test.go`
      - `isProxyError` 相关修正
  - 官方 `5df73099`
    - `fix(websearch): add 15s timeout for admin test search`
    - 已等价吸收：
      - 后台 websearch 管理测试增加 15s 超时
      - 回补 `/admin/settings/web-search-emulation/test`
      - 回补 `/admin/settings/web-search-emulation/reset-usage`
      - 回补 `PopulateWebSearchUsage / ResetWebSearchUsage / TestWebSearch`

验证结果：

- 后端：
  - `go test ./internal/service -run 'Test(Account_IsWebSearchEmulationEnabled|Channel_IsWebSearchEmulationEnabled|GetWebSearchEmulationConfig|SaveWebSearchEmulationConfig|PopulateWebSearchUsage|ResetWebSearchUsage|TestWebSearch)' -count=1`
  - `go test ./internal/handler/admin -run 'Test.*WebSearch.*' -count=1`
- 前端：
  - `corepack pnpm build`
  - 后续补充：
    - 本地为该行为补了集成测试，覆盖 Redis 快照元数据缺失时的回源退化路径
    - 本地将 `snapshot_mget_chunk_size` / `snapshot_write_chunk_size` 补进了 [deploy/config.example.yaml](/Users/sven.sun/Desktop/Api/sub2api/deploy/config.example.yaml)

- 官方 `f480e573` 的安全子集 -> 本地 `a0a15694`
  - 说明：仅吸收“保留 sidebar 自定义 SVG 原始颜色”这一小段，不合入它同提交里的表格默认值相关改动

### 2026-04-15

- 本地 HEAD：`b0f0071a`
- 官方 `origin/main`：`7c671b53`
- 图谱差异：
  - 相对官方：`behind 175 / ahead 83`

本轮重新核对后，以下结论需要更新：

- 官方 `8548a130`
  - 结论：本地已等价覆盖，不需要再同步
  - 依据：
    - [backend/internal/handler/openai_gateway_handler.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/handler/openai_gateway_handler.go) 已包含 `NormalizeOpenAICompatRequestedModel(reqModel)`、`resolveOpenAIMessagesDispatchMappedModel(apiKey, reqModel)`、`effectiveMappedModel`

### 2026-04-16

- 本地 HEAD：`f8c12915`
- 官方 `origin/main`：`be7551b9`
- 图谱差异：
  - 相对官方：`behind 197 / ahead 95`

本轮核验结论：

- 官方 `38c00872`
  - 结论：本地已等价覆盖，不需要再同步
  - 依据：
    - [frontend/src/components/account/AccountTestModal.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/account/AccountTestModal.vue) 与 [frontend/src/components/admin/account/AccountTestModal.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/admin/account/AccountTestModal.vue)
    - 当前实现已经是 `AbortController + fetch stream`，关闭弹窗会中止请求，关闭按钮未在连接中禁用

- 官方 `6ade6d30` / `22680dc6` / `db27e8f0` / `a7dd535d` / `e180dd07`
  - 结论：本地已基本吸收，仅补最后一个 UI 细节
  - 依据：
    - 后端已存在账号成本聚合与展示字段：
      - [backend/internal/repository/usage_log_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/usage_log_repo.go)
      - [backend/internal/repository/dashboard_aggregation_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/dashboard_aggregation_repo.go)
      - [backend/migrations/099_add_account_cost_to_dashboard_tables.sql](/Users/sven.sun/Desktop/Api/sub2api/backend/migrations/099_add_account_cost_to_dashboard_tables.sql)
    - 前端已存在 dashboard / usage / breakdown 的账号成本展示：
      - [frontend/src/views/admin/DashboardView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/DashboardView.vue)
      - [frontend/src/components/charts/ModelDistributionChart.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/charts/ModelDistributionChart.vue)
      - [frontend/src/components/charts/GroupDistributionChart.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/charts/GroupDistributionChart.vue)
      - [frontend/src/components/charts/UserBreakdownSubTable.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/charts/UserBreakdownSubTable.vue)
      - [frontend/src/components/admin/usage/UsageTable.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/admin/usage/UsageTable.vue)
    - 本轮仅把 [frontend/src/components/admin/usage/UsageStatsCards.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/admin/usage/UsageStatsCards.vue) 的账号成本/标准成本文案顺序对齐到上游最终态

- 官方 `7451b6f9`
  - 结论：本地已等价覆盖，不需要再同步
  - 依据：
    - 当前代码已不再把 Codex 5h/7d extra 快照自动提升为运行时 `RateLimitResetAt`
    - 已核对：
      - [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go)
      - [backend/internal/service/account_usage_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/account_usage_service.go)
      - [backend/internal/service/account_test_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/account_test_service.go)
      - [backend/internal/service/admin_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/admin_service.go)
      - [backend/internal/service/openai_ws_ratelimit_signal_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_ws_ratelimit_signal_test.go)
      - [backend/internal/service/account_usage_service_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/account_usage_service_test.go)

- 官方 `3de77130`
  - 结论：本地关键修复已存在，不需要再同步
  - 依据：
    - [frontend/src/views/admin/ChannelsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/ChannelsView.vue) 当前已使用 `splice(idx, 1, updated)` 更新 `model_pricing`
    - 同提交附带的 `console.log` 仅是调试输出，不引入

- 官方 `2dce4306` / `160903fc` / `e3748741`
  - 结论：本地已等价覆盖，不需要再同步
  - 依据：
    - [frontend/src/views/admin/ChannelsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/ChannelsView.vue) 已有 `restrict_models`、`apply_pricing_to_account_stats`、`account_stats_pricing_rules`
    - [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go) 与 [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go) 已在调度阶段做渠道限制预检查与 upstream 逐账号限制检查
    - `-tags unit` 已通过：
      - `TestCheckChannelPricingRestriction_*`
      - `TestOpenAISelectAccountForModelWithExclusions_*`
      - `TestAccountStatsPricing_*`
      - `TestResolveChannelMapping_*BillingModelSource`
      - `TestCreate_DefaultBillingModelSource`
    - `-tags unit` 已通过 [backend/internal/handler/admin/channel_handler_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/handler/admin/channel_handler_test.go) 的渠道接口用例

- 官方 `1c63ea14`
  - 结论：对当前分叉结构不适用
  - 原因：
    - 该修复依赖上游后续把 payment channel 合并回通用 `Channel.features` 字段的结构调整
    - 当前本地 [backend/internal/service/channel.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/channel.go) 不存在 `Features` 字段，[backend/internal/repository/channel_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/channel_repo.go) 也未采用该列扫描路径
    - 你的分叉继续使用本地支付体系，不需要引入这条上游 payment 结构链

- 官方 `2dce4306` / `3d202722`
  - 结论：本地已等价覆盖，不需要再同步
  - 依据：
    - [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go) 已在调度阶段执行渠道限制检查
    - [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go) 已有等价前置检查
    - [backend/internal/repository/wire.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/wire.go) 与 [backend/cmd/server/wire_gen.go](/Users/sven.sun/Desktop/Api/sub2api/backend/cmd/server/wire_gen.go) 已按配置注入 `ProvideSchedulerCache`

- 官方 `265687b5`
  - 结论：应降级为“已基本吸收”，不再作为主要待同步专题
  - 原因：
    - [backend/internal/repository/scheduler_cache.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache.go) 已有 chunked `MGET`、快照 metadata 和缺失元数据回源退化
    - [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go) 与 [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go) 已具备 snapshot hydration 路径
    - [backend/internal/service/scheduler_snapshot_hydration_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/scheduler_snapshot_hydration_test.go) 与 [backend/internal/repository/scheduler_cache_integration_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache_integration_test.go) 已覆盖核心行为
    - [backend/internal/config/config.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/config/config.go) 与 [deploy/config.example.yaml](/Users/sven.sun/Desktop/Api/sub2api/deploy/config.example.yaml) 已暴露 `snapshot_mget_chunk_size` / `snapshot_write_chunk_size`
  - 备注：
    - `git cherry` 仍显示 `+`，但这是 patch-id 已不相等，不代表功能仍缺失

- 官方 `7535e312` 及后续 account stats pricing 修补
  - 结论：这是本轮评估时确认存在、且尚未同步的一条真实功能线，后续已在本节“继续同步”中完成兼容移植
  - 移植前本地缺失：
    - [backend/internal/service/channel.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/channel.go) 没有 `ApplyPricingToAccountStats` / `AccountStatsPricingRules`
    - [backend/internal/service/channel_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/channel_service.go) 没有对应的 create/update 输入字段
    - [backend/internal/handler/admin/channel_handler.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/handler/admin/channel_handler.go) 没有对应 DTO / 序列化
    - [backend/internal/repository/usage_log_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/usage_log_repo.go) 仍使用 `SUM(total_cost * COALESCE(account_rate_multiplier, 1))` 作为账号统计费用
    - [frontend/src/views/admin/ChannelsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/ChannelsView.vue) 没有账号统计定价相关 UI
  - 需要连带考虑的后续提交：
    - `80fa4844`：仅前端布局重构，把规则从 basic tab 挪到 platform tab
    - `11c46068`：改为使用最终 upstream model 匹配规则，并移除旧的 channel pricing fallback
    - `98c9d517`：修正优先级，`ApplyPricingToAccountStats` 开启时优先直接使用本次请求 `totalCost`
    - `1262654d`：其中 account stats pricing 子集继续修正这条线，但该提交本身混有 WebSearch / notify 其他改动，不能整体同步
  - 风险判断：
    - 这是“有明确产品价值，但不是低风险补丁”的中大型专题
    - 需要新增数据库表和 `usage_logs.account_stats_cost` 列
    - 本地 migration 当前只到 `097`，不能直接照搬上游 `101`
    - 后台渠道页本地已经二开，前端需要人工移植，不能直接 cherry-pick
  - 当前建议：
    - 如果你近期没有“账号统计按独立价格口径核算”的明确需求，这条先不做
    - 如果后面要做，应该按独立专题手工移植，最小安全批次至少要覆盖：
      - `7535e312`
      - `11c46068`
      - `98c9d517`
      - `1262654d` 中仅 account stats pricing 相关子块

截至本轮，真正仍值得继续评估的主线，收敛为：

- OIDC 登录
- Anthropic API Key WebSearch emulation
- balance / quota notify
- custom account stats pricing rules

继续同步：

- 官方 `7535e312` / `11c46068` / `98c9d517` / `1262654d` 的 account stats pricing 子集 -> 本地兼容移植
  - 说明：已接入“账号统计定价”能力，用于将管理员账号成本统计从用户实际计费中解耦
  - 本地实现要点：
    - 新增 [backend/migrations/098_add_account_stats_pricing.sql](/Users/sven.sun/Desktop/Api/sub2api/backend/migrations/098_add_account_stats_pricing.sql)，避免直接照搬官方 `101` migration 造成编号冲突
    - 新增 `channels.apply_pricing_to_account_stats`
    - 新增 `channel_account_stats_pricing_rules` / `channel_account_stats_model_pricing`
    - 新增 `channel_account_stats_pricing_intervals`，兼容官方后续 `106_add_account_stats_pricing_intervals.sql`
    - 新增 `usage_logs.account_stats_cost`
    - [backend/internal/repository/usage_log_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/usage_log_repo.go) 的账号统计成本改为 `COALESCE(account_stats_cost, total_cost) * account_rate_multiplier`
    - [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go) 和 [backend/internal/service/openai_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_gateway_service.go) 在落 usage log 前计算 `AccountStatsCost`
    - [backend/internal/service/account_stats_pricing.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/account_stats_pricing.go) 已支持自定义规则、`ApplyPricingToAccountStats`、模型定价文件 fallback 和 token 区间定价
    - [frontend/src/views/admin/ChannelsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/ChannelsView.vue) 增加后台渠道页配置入口
  - 兼容处理：
    - 不改变用户实际扣费，不影响余额、订阅和 API Key 计费
    - 官方 `1262654d` 中混杂的 WebSearch / notify 子块没有合入，只吸收 account stats pricing 相关语义
    - 前端没有直接硬合官方渠道页大改，而是在本地已有 platform tab 结构中手工接入规则 UI
  - 验证：
    - `go test ./...`
    - `corepack pnpm build`
    - `go test -tags=unit ./internal/service -run 'TestHandleSmartRetry_503_ModelCapacityExhausted_RetryExhaustedBudget' -count=1`
    - `go test -tags=unit ./internal/service -run 'TestAccountStatsPricing' -count=1`
    - `go test ./internal/service ./internal/repository ./internal/handler/admin`
  - 额外修复：
    - 本轮验证时发现本地 Antigravity `MODEL_CAPACITY_EXHAUSTED` 账号级 capacity cooldown 存在读取兜底缺口：如果上游返回的模型名不在本地映射表中，cooldown 已写入但读取时可能因映射解析为空而不可见
    - 已在 [backend/internal/service/model_capacity_cooldown.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/model_capacity_cooldown.go) 补充兜底：优先按最终映射模型查，查不到时再按原始请求模型名查

## 后续同步记录模板

后面每次同步官方后，在文末追加一条：

```md
## YYYY-MM-DD

- 本地 HEAD：
- 官方 origin/main：

已同步：
- 官方 XXXXXXXX -> 本地 YYYYYYYY

明确跳过：
- 官方 ZZZZZZZZ
  - 原因：

备注：
- 是否需要生产验证：
- 是否存在高冲突文件：
```

## 2026-04-15 发布记录

- 对应同步提交：
  - 本地 / fork：`c69557f3 feat: 同步账号统计定价规则`
- 推送状态：
  - 已推送到 `sv3nbeast/main`
- 生产发布状态：
  - 已发布到生产
  - 当前生产镜像：`sub2api:prod-20260415-151930-c69557f3`
  - 发布前已完成数据库导出与部署配置备份
  - 发布过程未删除现有数据卷，未执行 `down -v`、`rm -rf`、重建数据库等破坏性操作
- 生产校验：
  - 应用容器健康检查通过
  - `098_add_account_stats_pricing.sql` 已完成迁移
  - 发布后核心 API 请求日志已出现正常 `200` 响应

## 2026-04-15 继续同步

- 同步前本地 HEAD：`aee23302`
- 官方 `origin/main`：`be7551b9`
- 官方新增标签：`v0.1.113`

已同步：

- 官方 `38c00872` `fix(ui): allow closing test dialog during active SSE stream`
  - 说明：账号测试连接弹窗的 SSE 请求改为 `AbortController` 控制，测试中也可以关闭弹窗并中止请求

- 官方 `6ade6d30` / `db27e8f0` / `a7dd535d` / `e180dd07` 的账号成本展示链路
  - 说明：后台 dashboard、usage 统计卡片、模型/分组分布、usage 明细表展示 `account_cost`
  - 本地兼容处理：
    - 官方 `107_add_account_cost_to_dashboard_tables.sql` 改为本地连续迁移 [backend/migrations/099_add_account_cost_to_dashboard_tables.sql](/Users/sven.sun/Desktop/Api/sub2api/backend/migrations/099_add_account_cost_to_dashboard_tables.sql)
    - 继续沿用本地已合入的 `account_stats_cost` 语义，账号成本按 `COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)` 计算
    - `UsageTable` 与本地已有费用 tooltip 结构手工合并，没有直接覆盖本地二开表格

- 官方 `7451b6f9` `修复 OpenAI 账号限流回流误判`
  - 说明：OpenAI Codex 账号额度快照只写回 usage extra，不再因为 5h 窗口为 0 误把账号写入 429 限流状态
  - 影响范围：OpenAI OAuth / Codex 额度展示与调度状态回写，不影响 Antigravity 调度逻辑

明确跳过：

- 官方 `60a4b931` / `98140f6c` / `e761d38f` / `d149dbc9` / `3053c56c` / `342dbd2e` / `c2108421`
  - 原因：属于官方内置 payment / recharge fee rate 线，本地继续使用独立 `sub2apipay`，不能直接混合合入

- 官方 `be7551b9`
  - 原因：仅同步官方 `VERSION` 到 `0.1.113`，本地二开版本号不直接跟随官方覆写

验证：

- `go test ./...`
- `corepack pnpm build`（在 `frontend` 目录执行）

备注：

- 本轮同步前临时保存了未提交的 gateway session 诊断改动：`stash@{0}: pre-upstream-sync-session-diag-20260415`

### 生产发布记录

- 对应同步提交：
  - 本地 / fork：`1d1be065 feat: 同步官方用量成本展示与限流修复`
- 生产发布状态：
  - 已发布到生产
  - 当前生产镜像：`sub2api:prod-20260415-215206-1d1be065`
  - 发布前备份目录：`/root/sub2api-deploy/backups/20260415-215116`
  - 回滚 compose 备份：`/root/sub2api-deploy/docker-compose.override.yml.bak-20260415-215531.deploy`
  - 发布过程未删除现有数据卷，未执行 `down -v`、`rm -rf`、重建数据库等破坏性操作
- 生产校验：
  - 应用容器健康检查通过
  - `/health` 返回 `{"status":"ok"}`
  - `098_add_account_stats_pricing.sql` 与 `099_add_account_cost_to_dashboard_tables.sql` 已完成迁移
  - 发布后近几分钟未发现 `ERROR` / `FATAL` / `panic` 级别启动异常

## 2026-04-16 继续跟踪

- 同步前本地 HEAD：`f3ff456a`
- 官方 `origin/main`：`be7551b9`
- 结论：官方暂无比 `v0.1.113` 更新的提交，本轮继续处理历史未吸收的低风险补丁

已同步：

- 官方 `58c0f576` `fix(sidebar): prevent version dropdown clipping in expanded brand`
  - 说明：移除展开状态下 `.sidebar-brand` 的 `overflow: hidden`，只在折叠状态保留裁剪，避免版本号下拉被品牌容器裁掉
  - 兼容处理：仅影响侧边栏样式与对应前端测试，不涉及后端、数据库、调度、账号、支付和监控

继续保留为后续专题：

- OIDC 登录
- Anthropic API Key WebSearch emulation
- balance / quota notify
- 官方内置 payment 线继续跳过，避免和本地 `sub2apipay` 冲突

## 2026-04-16 继续兼容同步

- 同步前本地 HEAD：`eec0df59`
- 官方 `origin/main`：`be7551b9`
- 处理原则：先合入不触碰生产调度/支付/数据结构高风险区的官方补丁；遇到依赖后续大改的补丁，先按本地现有接口手工兼容，避免半同步导致编译失败

已同步：

- 官方 `ad80606a` / `13124059` 的全局表格分页设置
  - 说明：后台设置页新增通用表格分页配置，公开设置接口返回 `table_default_page_size` / `table_page_size_options`
  - 兼容处理：保留本地品牌 `SubAPIs`、邮箱注册黑名单、服务状态探针、监控等设置项；没有覆盖本地设置页结构

- 官方 `67a05dfc` / `f480e573` 中的表格默认值修正子集
  - 说明：采用官方最终策略，表格每页条数以系统配置为准，不再使用浏览器 localStorage 的旧分页偏好；默认可选值为 `10, 20, 50, 100`
  - 兼容处理：只吸收表格默认值相关子集；`MessagesDispatchModelConfig` 与 sidebar SVG 相关子集本地此前已处理

- 官方 `66e15a54` 的导出筛选一致性子集
  - 说明：账号、代理、兑换码导出按当前筛选条件导出，而不是无视筛选导出全部
  - 兼容处理：该官方提交原始版本依赖后续 `5f8e60a1` 的后端排序接口签名；本地当前尚未合入那条大改，因此本轮只同步筛选条件传递，不引入未完成的排序参数

验证：

- `go test ./internal/service -run 'Test.*Setting|Test.*Public|Test.*Table|Test.*Settings' -count=1`
- `go test ./internal/handler/admin -run 'Test.*Export|Test.*Data' -count=1`
- `corepack pnpm vitest run src/stores/__tests__/app.spec.ts src/utils/__tests__/tablePreferences.spec.ts src/composables/__tests__/usePersistedPageSize.spec.ts`

下一步：

- 官方 OIDC 登录专题
- 官方 WebSearch / Notify 专题

## 2026-04-16 状态校正与生产发布

- 校正时间：`2026-04-16`
- 本地 `HEAD`：`57e6e2cb`
- 你的 fork `sv3nbeast/main`：`57e6e2cb`
- 官方 `origin/main`：`e6e73b4f`
- 图谱差异：
  - 相对 fork：`behind 0 / ahead 0`
  - 相对官方：不再继续使用单纯 `ahead/behind` 判断真实同步进度，仍以 `git cherry` + 当前代码核验为准

本节用于覆盖上面 `2026-04-16 WebSearch / Notify 专题收尾` 中已过期的状态描述。

实际已完成：

- 已推送到 `sv3nbeast/main`
  - 提交：
    - `b91fcc84` `feat(notify): add balance low & account quota notification system`
    - `29e41250` `docs: 记录 WebSearch 与通知专题同步`
    - `57e6e2cb` `fix(channel): stop querying legacy features column`

- 已发布到生产
  - 首次发布镜像：`sub2api:prod-20260416-154457-29e41250`
  - 热修后当前生产镜像：`sub2api:prod-20260416-155311-57e6e2cb`
  - 当前生产容器状态：`healthy`

- 本轮额外热修：
  - 问题：
    - 发布后生产日志出现 `failed to build channel cache`
    - 真实原因不是数据库缺列，而是 [backend/internal/repository/channel_repo.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/channel_repo.go) 仍在查询旧列 `channels.features`
    - 生产数据库实际只使用 `features_config`
  - 处理：
    - 停止在 repository 层读写旧 `features` 列
    - 继续保留 `features_config` 作为实际存储
    - 不触碰 PostgreSQL / Redis 数据卷，不做破坏性迁移

生产验证：

- 发布脚本：
  - 使用 [scripts/deploy-prod.sh](/Users/sven.sun/Desktop/Api/sub2api/scripts/deploy-prod.sh)
  - 只同步源码目录并重建 `sub2api` 服务
  - 自动排除：
    - `deploy/data`
    - `deploy/postgres_data`
    - `deploy/redis_data`
    - `deploy/.env`
- 回滚点：
  - 最近 override 备份：`/root/sub2api-deploy/docker-compose.override.yml.bak-20260416-155320`
- 健康检查：
  - `/health` 返回 `200`
  - 容器健康状态为 `healthy`
- 日志核验：
  - 热修后最近日志中已不再出现：
    - `failed to build channel cache`
    - `failed to load channel cache`
- 真实请求核验：
  - 发布后已观测到真实 `/v1/messages`、`/responses` 请求持续返回 `200`

截至当前，官方最新但尚未同步到本地项目的仅剩：

- `3944b3d2` `fix: preserve openai ws flags in scheduler cache`
  - 建议优先级：高
  - 原因：涉及 OpenAI WS 调度快照 flags 保留，属于真实功能修复

- `836092a6` `fix: restore ctx pool ws mode option in account ui`
  - 建议优先级：中
  - 原因：恢复账号前端里的 OpenAI WS `ctx_pool` 选项

- `a55ead5e` `chore: remove empty dir Antigravity-Manager`
  - 建议优先级：低
  - 原因：仓库清理，无生产价值

- `7ea8e7e6` `chore: update sponsors`
  - 建议优先级：低
  - 原因：README / 资源更新，无功能影响

当前结论：

- `websearch / notify` 专题已完成本地、fork、生产三端闭环
- 当前真正值得继续同步的官方新增，优先只剩 OpenAI WS 相关的 `3944b3d2` / `836092a6`

## 2026-04-16 OpenAI WS 收尾

- 同步前本地 HEAD：`57e6e2cb`
- 官方 `origin/main`：`e6e73b4f`
- 处理原则：只吸收官方最新主线里仍未同步、且对现有功能有实际价值的 OpenAI WS 修复；跳过 sponsor / README 类无功能价值变更

已同步：

- 官方 `3944b3d2` `fix: preserve openai ws flags in scheduler cache`
  - 说明：调度快照 metadata 现在会继续保留 OpenAI WS 相关 extra 字段，避免从 scheduler snapshot 选账号时丢失 WS 路由语义
  - 已落地：
    - [backend/internal/repository/scheduler_cache.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache.go)
    - [backend/internal/repository/scheduler_cache_unit_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache_unit_test.go)
    - [backend/internal/service/openai_account_scheduler_ws_snapshot_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/openai_account_scheduler_ws_snapshot_test.go)

- 官方 `836092a6` `fix: restore ctx pool ws mode option in account ui`
  - 说明：恢复账号创建/编辑/批量编辑界面中的 OpenAI WS `ctx_pool` 模式选项
  - 已落地：
    - [frontend/src/components/account/CreateAccountModal.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/account/CreateAccountModal.vue)
    - [frontend/src/components/account/EditAccountModal.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/account/EditAccountModal.vue)
    - [frontend/src/components/account/BulkEditAccountModal.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/components/account/BulkEditAccountModal.vue)

兼容处理：

- 验证过程中补了本地旧测试桩：
  - [backend/internal/service/gateway_record_usage_test.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_record_usage_test.go)
  - 原因：`NewGatewayService` 签名已演进，旧单测缺一个 `nil` 参数，和本轮功能无直接业务耦合

验证：

- `go test -tags unit ./internal/repository -run 'TestBuildSchedulerMetadataAccount_KeepsOpenAIWSFlags' -count=1`
- `go test -tags unit ./internal/service -run 'TestOpenAIGatewayService_SelectAccountWithScheduler_UsesWSPassthroughSnapshotFlags' -count=1`
- `go test ./cmd/server -count=1`
- `corepack pnpm vitest run src/components/account/__tests__/BulkEditAccountModal.spec.ts`
- `corepack pnpm build`

当前剩余未同步项：

- `7ea8e7e6` `chore: update sponsors`
  - 低优先级，README / 资源更新，无功能影响

- `a55ead5e` `chore: remove empty dir Antigravity-Manager`
  - 已判定为 no-op
  - 当前本地仓库中 `Antigravity-Manager` 路径本身就不存在，也未被 git 跟踪，无需额外同步

当前结论：

- 官方最新主线中的功能性更新已全部同步到本地项目
- 真正仍未同步的仅剩 sponsor 文案 / 图片更新，可长期忽略

发布状态补充：

- 已推送到 `sv3nbeast/main`
  - 当前 fork / 本地 HEAD：`341cab46`

- 已发布到生产
  - 当前生产镜像：`sub2api:prod-20260416-181614-341cab46`
  - 发布方式：
    - 因原工作区存在其他未提交改动，使用干净临时 worktree 从提交态发布
    - 避免把原工作区中的 README / 备份 / 部署文档等未提交变更一并同步到服务器
  - 生产结果：
    - 容器健康检查通过
    - 最近日志已观测到真实 `/v1/messages` / `/responses` 成功请求

## 2026-04-16 WebSearch / Notify 专题收尾

- 同步前本地 HEAD：`b188aaac`
- 官方 `origin/main`：`be7551b9`
- 处理原则：保留本地生产已上线的调度、TLS 指纹、品牌、`sub2apipay`、服务状态探针和监控逻辑，只吸收官方 WebSearch / Notify 中不与本地实现冲突、或明显更完整的部分

已同步：

- 官方 Notify 专题主链及后续修补子集：
  - 参考提交：`48b6c481` `6e9146e7` `f571d8ff` `c1eb79e4` `216bda58` `a43da622` `ca673f98` `ed8a9d97` `9d319cfa` `74f8a30f` `a9880ee7` `0a4ece5f`
  - 落地内容：
    - 用户余额不足邮件提醒
    - 用户自定义余额提醒阈值与额外通知邮箱验证链路
    - 管理员侧账号日/周/总配额告警邮箱通知
    - 账号编辑弹窗里的配额告警阈值配置
    - 网关/OpenAI 网关扣费后触发提醒检查
  - 兼容处理：
    - 保留本地 `purchase_subscription_enabled` / `purchase_subscription_url` 作为充值入口，不回退到官方内置 payment 设置页
    - 修补本地分叉里缺失的 `web_search_emulation_config` 设置常量、`SettingService.webSearchRedis` 字段和公开设置里的 `payment_enabled` 兼容回退
    - 保留本地已有的 TLS 指纹、账号编辑扩展字段、调度和模型隔离逻辑，不覆盖生产行为

- 官方 WebSearch 后续补丁：
  - `7c729293` WebSearch 配额增强与设置页提示子集已兼容
  - `9e0d12d3` 已保留已保存 provider 的 API Key 显隐/复制支持
  - `5df73099` 管理员 WebSearch 测试增加 `15s` 超时已确认在本地代码生效

验证：

- `go test ./internal/service ./internal/handler ./internal/handler/admin ./internal/server -run 'Test.*(Notify|Quota|Balance|Setting|Settings|Profile|WebSearch|OAuth|OIDC).*' -count=1`
- `go test ./internal/service ./internal/handler/admin ./cmd/server -count=1`
- `corepack pnpm build`
- `corepack pnpm vitest run src/stores/__tests__/app.spec.ts src/utils/__tests__/tablePreferences.spec.ts src/composables/__tests__/usePersistedPageSize.spec.ts`

当前状态：

- 本地工作区干净
- WebSearch / Notify 这轮历史专题已收尾
- 尚未推送到 `sv3nbeast/main`
- 尚未发布到生产

继续同步：

- 官方 `02a66a01` / `8e1a7bdf` 的通用 OIDC 登录
  - 说明：新增 OIDC OAuth 登录配置、授权/回调路由、登录/注册页入口、后台设置项和配置文件示例；同时吸收官方后续修复，避免 OIDC 登录总是使用合成邮箱
  - 兼容处理：
    - 保留现有 LinuxDo OAuth 登录和邀请/注册校验逻辑，OIDC 作为独立入口接入
    - 保留本地注册邮箱黑名单、品牌、监控、支付入口、表格设置等已有设置项
    - 官方补丁里与本地当前服务层不匹配的 `sora_client_enabled` 公开 DTO 回填未引入，避免编译失败和误恢复已不由后端设置驱动的字段
  - 验证：
    - `go test ./internal/config ./internal/handler ./internal/handler/admin ./internal/service ./internal/server -run 'Test.*(OIDC|Oidc|OAuth|Setting|Settings|Config|Contract|Route|Auth).*' -count=1`
    - `go test ./internal/config ./internal/handler ./internal/handler/admin ./internal/service ./internal/server -count=1`
    - `corepack pnpm build`

下一步：

- 官方 WebSearch / Notify 专题
- 官方 payment 线继续保持本地 `sub2apipay` 优先，逐项判断是否只吸收无冲突修复

低风险补丁核验：

- 已等价吸收，无需再次改代码：
  - `ce833d91` 首页 `home_content` URL 纳入 CSP `frame-src`
  - `fe211fc5` macOS 表格横向滚动条闪隐修复
  - `6401dd7c` ops 错误日志 request body 上限提升到 256KB
  - `b7edc3ed` / `422e25c9` Cursor `/v1/chat/completions` Responses-shaped body 兼容与 Codex 不支持字段剥离
  - `cb016ad8` Anthropic credit balance exhausted 400 识别为账号错误
  - `9648c432` 前端 API client TS2352 类型断言修复
  - `3a113481` / `abe42675` 移动端表格延迟挂载与账号用量 cell 懒加载
  - `a1e299a3` Anthropic 非流式终态空 output 时用 delta 重建响应
  - `f9f57e95` `settings.updated_at` 默认值恢复迁移
  - `118ff85f` `LoadFactor` 写入调度快照 metadata
- 官方 `265687b5` 调度快照大 `MGET` 优化已由本地等价/增强实现覆盖：
  - 本地已具备 `snapshot_mget_chunk_size` / `snapshot_write_chunk_size`
  - 本地快照使用 `sched:meta:*` metadata 轻量读取，完整账号按需 hydration
  - metadata 继续保留本地生产需要的 `LoadFactor`、`model_rate_limits`、`model_capacity_cooldowns` 等字段
- 明确跳过：
  - `24f0eebc` 只修改官方内置 payment 二维码组件，本地当前不使用官方内置 payment 页面，继续以 `sub2apipay` 链路为准

继续同步：

- 官方 `d67ecf89` / `6793503e` 的 Sora 残留清理子集
  - 说明：官方移除已废弃的 `sora_client_enabled` 公开设置残留，以及 `wire.go` 中已不存在的 `SoraMediaCleanupService` 注入参数
  - 兼容处理：只清理当前代码里确认无实际服务实现的残留引用，不改 README 中“Sora 暂不可用”的说明，也不引入官方 payment/Sora 大范围删改
  - 验证：
    - `rg -n "sora_client_enabled|SoraMediaCleanup" backend/cmd/server frontend/src backend/internal || true`
    - `go test ./cmd/server ./internal/server ./internal/handler ./internal/service -run 'Test.*(Setting|Settings|Sora|Public|Config|Wire|OAuth).*' -count=1`
    - `corepack pnpm build`

继续同步：

- 官方 `5f8e60a1` `feat(table): 表格排序与搜索改为后端处理`
  - 说明：后台与用户侧多处表格增加服务端排序参数，仓库层按白名单字段排序，避免前端只排序当前页导致全局排序不准确
  - 涉及范围：
    - 账号、用户、分组、渠道、代理、兑换码、公告、用量、API Key 等列表接口
    - `PaginationParams` 增加 `sort_by` / `sort_order`
    - 前端 `DataTable` 相关页面改为透传排序参数
  - 兼容处理：
    - `ListAccounts` 同时保留本地 `model` 模糊筛选参数和官方 `sort_by` / `sort_order`
    - 账号状态筛选继续保留本地语义：正常账号排除账号级限流、临时不可调度、手动不可调度；模型筛选仍支持模型级 cooldown 和 Antigravity 模型别名
    - 模型筛选分支同样应用官方后端排序，避免筛选模型后排序退回旧 ID 倒序
    - 渠道列表排序保留本地 `apply_pricing_to_account_stats` 字段读取，避免覆盖账号统计定价功能
    - 账号/代理/兑换码导出现在可继续传递排序参数，和本轮导出筛选修复保持一致
  - 验证：
    - `go test ./internal/service ./internal/handler/admin ./internal/repository -count=1`
    - `go test ./internal/handler ./internal/handler/admin ./internal/service ./internal/repository -run 'Test.*(Sort|Search|List|Export|Data|RequestType|Table|Setting|Public|OpenAIWS).*' -count=1`
    - `go test -tags integration ./internal/repository -run 'TestAccountRepoSuite/TestListWithFilters_(ModelFilterPreservesSort|SortByPriorityDesc)' -count=1`
    - `corepack pnpm vitest run src/views/user/__tests__/UsageView.spec.ts src/components/admin/announcements/__tests__/AnnouncementReadStatusDialog.spec.ts`
    - `corepack pnpm build`

下一步：

- 官方 OIDC 登录专题
- 官方 WebSearch / Notify 专题
