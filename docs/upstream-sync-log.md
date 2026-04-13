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

- 官方 `f480e573` 的安全子集 -> 本地 `a0a15694`
  - 说明：仅吸收“保留 sidebar 自定义 SVG 原始颜色”这一小段，不合入它同提交里的表格默认值相关改动

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
