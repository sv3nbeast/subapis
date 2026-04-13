# Upstream Conflict Hotspots

这个文件记录“官方更新时最容易和本地二开冲突的文件与原因”。

目的不是穷举所有变更，而是让后续同步时先看这里，快速知道哪里要重点复核。

## 高冲突文件

### [backend/internal/service/gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/gateway_service.go)

本地已改内容：

- 多账号选号逻辑
- 固定顺序前排堵塞修复
- 同优先级/同 `last_used_at` 打散与失败避让
- 模型映射与计费相关修补
- 邮箱域名后缀避让逻辑

为什么容易冲突：

- 官方经常在这里改调度、fallback、dispatch、计费、选择策略
- 本地也在这里做过多次核心修补

同步时必须检查：

- 是否影响账号选择顺序
- 是否改变切号次数、切号条件、失败回退语义
- 是否覆盖本地的 Antigravity 调度修复

### [backend/internal/service/antigravity_gateway_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/antigravity_gateway_service.go)

本地已改内容：

- 429 / 503 / quota exhausted 分类
- 模型级隔离
- 单请求内的切号与重试策略
- 测试连接行为
- MODEL_CAPACITY_EXHAUSTED 相关控制流

为什么容易冲突：

- 本地对 Antigravity 的核心行为改动很多
- 上游如果继续补 Antigravity 逻辑，这里很容易互相覆盖

同步时必须检查：

- 测试连接是否还会被过滤
- 429 与 503 是否还按本地预期处理
- quota exhausted 是模型级还是账号级

### [backend/internal/service/ratelimit_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/ratelimit_service.go)

本地已改内容：

- Antigravity OAuth 401 不再直接 `SetError`
- 改成临时不可调度 + 延迟强刷 token
- 多类上游错误分类与状态处理

为什么容易冲突：

- 官方和本地都可能继续往这里加错误分类规则

同步时必须检查：

- 401 是否仍按“3 分钟后强刷”逻辑执行
- 账号是否会被误永久打错
- Anthropic / Antigravity 错误分类是否被改回去

### [backend/internal/service/setting_service.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/service/setting_service.go)

本地已改内容：

- 监控、公告、公开设置、品牌字样相关修补
- 注册邮箱域名黑名单
- 各类本地自定义设置项

为什么容易冲突：

- 官方很多后台能力、设置返回字段、支付配置、表格配置都会改这里

同步时必须检查：

- 公开设置返回字段是否兼容前端
- 是否误覆盖本地新增设置项
- 是否引入了官方 payment 相关字段

### [frontend/src/views/admin/SettingsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/SettingsView.vue)

本地已改内容：

- 黑名单配置
- 监控相关开关
- 本地品牌与说明文案调整

为什么容易冲突：

- 官方频繁改设置页
- 官方 payment / 表格分页 / 推荐链接也会改这里

同步时必须检查：

- 新增设置项是否还能显示和保存
- 表单结构是否和后端接口一致
- 是否混入不需要的 payment 入口

### [frontend/src/views/admin/AccountsView.vue](/Users/sven.sun/Desktop/Api/sub2api/frontend/src/views/admin/AccountsView.vue)

本地已改内容：

- 账号状态筛选修复
- 测试连接回显调整
- TLS 指纹相关显示
- 平台/类型状态展示修补

为什么容易冲突：

- 官方也会改筛选、搜索、表格行为

同步时必须检查：

- 状态筛选是否还准确
- 模型筛选/模糊搜索是否还在
- 测试连接提示是否符合本地预期

### [backend/internal/repository/scheduler_cache.go](/Users/sven.sun/Desktop/Api/sub2api/backend/internal/repository/scheduler_cache.go)

本地已改内容：

- 调度快照 metadata 化
- 分块 `MGET`
- hydration 模式
- `LoadFactor` 同步
- `model_rate_limits` 保留到快照 metadata

为什么容易冲突：

- 官方调度性能修复会持续动这里

同步时必须检查：

- 是否退回到老的大 `MGET`
- metadata 里是否仍保留 `LoadFactor`
- metadata 里是否仍保留 `model_rate_limits`
- hydration 流程是否被破坏

### [deploy/monitor.sh](/Users/sven.sun/Desktop/Api/sub2api/deploy/monitor.sh)

本地已改内容：

- Bark 监控与服务状态探针联动
- 支持 `BARK_NOTIFY_ALERT` / `BARK_NOTIFY_RECOVER`
- 当前生产使用“只通知恢复，不通知异常”

为什么容易冲突：

- 监控脚本属于本地运维定制，后续如果官方补监控脚本或你继续改 Bark 逻辑，容易被覆盖

同步时必须检查：

- 是否还支持“只推恢复、不推异常”
- `.monitor_state` 状态机是否仍兼容
- 是否误改了当前生产 `monitor.env` 所需变量名

## 同步时的固定检查顺序

每次准备合并官方更新时，先按这个顺序过一遍：

1. 看 [docs/upstream-sync-log.md](/Users/sven.sun/Desktop/Api/sub2api/docs/upstream-sync-log.md)
2. 看这份高冲突清单
3. 先筛官方“低风险补丁”
4. 再看是否碰到上面的高冲突文件
5. 如果碰到高冲突文件，必须读代码后再决定是否并

## 本地优先原则

下面这些行为默认以本地版本为准，除非明确重新评估：

- Antigravity 切号与限流恢复逻辑
- Antigravity OAuth 401 恢复逻辑
- 账号选择与失败避让逻辑
- 邮箱域名黑名单
- 监控与服务状态展示逻辑
- 本地品牌 `subapis`
- 与 `sub2apipay` 配套的支付使用方式

## 更新方式

如果后续某个高冲突文件被再次大改，记得把下面两件事补上：

1. 在 [docs/upstream-sync-log.md](/Users/sven.sun/Desktop/Api/sub2api/docs/upstream-sync-log.md) 追加同步记录
2. 在本文件对应条目里更新“本地已改内容”和“同步时必须检查”
