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

- 官方 `265687b5` `fix: 优化调度快照缓存以避免 Redis 大 MGET`
  - 本地对应提交：`1f83cbeb`
  - 备注：本地实现已落地，当前代码使用快照 ID + metadata + hydration 模式，不再是老式大 `MGET`

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
