# 服务状态监控设计文档

日期：2026-04-06

## 概述

为 Sub2API 平台新增按模型的服务可用性监控功能。后端通过定时探针检测各模型的可用性，前端在三个位置展示 30 天可用性时间线：独立状态页、用户 Dashboard、公开首页。管理员可在后台配置监控哪些模型。

## 后端

### 数据表：`status_probe_results`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint, PK | 自增 |
| model | varchar(128) | 探测的模型名，如 `claude-opus-4-6` |
| status | varchar(16) | `success` 或 `fail` |
| latency_ms | int | 响应耗时（毫秒） |
| error_message | text, nullable | 失败时的错误信息 |
| created_at | timestamp | 探测时间 |

索引：`(model, created_at)` 复合索引，用于按模型按时间查询。

不使用 Ent ORM 管理此表，直接用 SQL 建表，与 `ops_error_logs` 等运维表保持一致。

### 配置存储

复用现有 `settings` 表，key 为 `status_probe_config`，value 为 JSON：

```json
{
  "enabled": true,
  "interval_minutes": 5,
  "retention_days": 30,
  "models": [
    {
      "model": "claude-opus-4-6",
      "display_name": "Claude Opus 4",
      "sort_order": 1,
      "enabled": true
    },
    {
      "model": "claude-sonnet-4-6",
      "display_name": "Claude Sonnet 4",
      "sort_order": 2,
      "enabled": true
    }
  ]
}
```

### StatusProbeService

- 注册到已有的 `robfig/cron` 调度器
- 每个周期遍历配置中 `enabled: true` 的模型
- 通过内部网关发送最小请求：`max_tokens: 1`，prompt: `"hi"`
- 超时 30 秒，超时算失败
- 结果逐条写入 `status_probe_results`
- 探针请求标记 `is_probe: true`，不计入用户用量统计
- 配置变更时自动重载 cron 任务间隔

### 数据清理

复用已有 cron 调度器，每天凌晨清理超出 `retention_days`（默认 30 天）的记录。

### API

#### 公开接口（无需认证）

`GET /api/v1/status`

返回所有启用模型的当前状态 + 30 天按小时聚合数据。

响应：
```json
{
  "overall_status": "operational",
  "models": [
    {
      "model": "claude-opus-4-6",
      "display_name": "Claude Opus 4",
      "current_status": "operational",
      "uptime_percentage": 99.87,
      "hourly_stats": [
        {
          "hour": "2026-04-05T14:00:00Z",
          "success": 10,
          "total": 12,
          "avg_latency_ms": 1200,
          "failures": [
            {"time": "2026-04-05T14:23:00Z", "error": "timeout"},
            {"time": "2026-04-05T14:28:00Z", "error": "HTTP 529"}
          ]
        }
      ]
    }
  ],
  "last_updated": "2026-04-06T12:00:00Z"
}
```

- `hourly_stats` 返回 30 天数据（最多 720 条/模型），只包含有探测记录的小时
- `failures` 数组仅在该小时有失败时才包含
- `current_status` 判定：取最近 3 次探测，全部成功=`operational`，部分失败=`degraded`，全部失败=`outage`
- `overall_status`：所有模型 operational=`operational`，有 degraded=`degraded`，有 outage=`major_outage`
- 响应头 `Cache-Control: max-age=60`

#### 管理员接口

- `GET /api/v1/admin/settings/status-probe` — 读取探针配置
- `PUT /api/v1/admin/settings/status-probe` — 更新探针配置（更新后自动重载 cron 间隔）

## 前端

### 核心组件：`ServiceStatusBar.vue`

可复用组件，接收单个模型的状态数据，渲染：
- 状态灯（6px 圆点）+ 模型名 + 可用率百分比
- 30 天可用性条形图：flex 容器，每个竖条代表 1 小时，条高 12px
- 颜色规则：全部成功=绿色，有失败但 <50% 失败率=黄色，>=50% 失败率=红色
- 自适应宽度：使用 `flex:1` + `gap:1px`，容器多宽条就有多宽
- Hover tooltip 显示：
  - 时间段：`2026-04-05 14:00 - 15:00`
  - 探测概况：`12 次探测，10 次成功`
  - 失败详情（逐条）：`14:23 失败 (timeout)`
  - 持续时间：单次失败显示 `< 5 分钟`，连续失败显示精确范围 `14:23 - 14:38（15 分钟）`
  - 平均延迟：`1.2s`

支持 `compact` prop：紧凑模式下隐藏百分比和日期标注，用于 Dashboard 嵌入。

### 展示位置

#### 1. 独立状态页 `/status`

- 路由：`/status`，需要认证（`requiresAuth: true`）
- 侧边栏新增「服务状态」菜单项，所有用户可见（Simple Mode 也显示）
- 完整展示：总览状态灯 + 状态文案 + 更新时间 + 每个模型的 `ServiceStatusBar`
- 底部标注 `30 天前 ... 今天`

#### 2. Dashboard 嵌入

- 在 `DashboardView.vue` 的 stats 和 charts 之间插入精简版
- 使用 `ServiceStatusBar` 的 `compact` 模式
- 整体可点击，跳转到 `/status`

#### 3. 公开首页 `/home`

- 在 `HomeView.vue` 添加服务状态区域
- 与独立状态页相同的完整展示
- API 无需认证，未登录即可查看

### 数据获取

`api/status.ts`：
- `statusAPI.getStatus()` → `GET /api/v1/status`
- 页面加载时请求一次，无需轮询

### 边界情况

- 探针未启用：不显示服务状态区域（三个位置都不显示）
- 刚启用无数据：显示「监控数据收集中...」占位
- 页面切换不重复请求，组件 unmount 不丢弃已有数据

## 管理员配置界面

在已有 Admin Settings 页面中新增「服务状态探针」配置区域：
- 启用/禁用开关
- 探测间隔：下拉选择 1/2/3/5/10/15 分钟
- 数据保留天数：输入框，默认 30
- 监控模型列表：可增删的表格（模型 ID、显示名、排序、启用开关）

## 文件清单（预估）

### 后端新增
- `backend/internal/service/status_probe_service.go` — 探针服务
- `backend/internal/handler/status_handler.go` — 公开 status API
- `backend/internal/handler/admin/status_probe_settings_handler.go` — 管理员配置 API
- `backend/migrations/xxx_create_status_probe_results.sql` — 建表迁移

### 后端修改
- `backend/internal/server/routes/common.go` — 注册 `/api/v1/status`
- `backend/internal/server/routes/admin.go` — 注册管理员配置路由
- `backend/internal/service/cron_service.go` 或等效 — 注册探针 cron job
- 网关层 — 探针请求标记 `is_probe: true`

### 前端新增
- `frontend/src/components/status/ServiceStatusBar.vue` — 核心可用性条组件
- `frontend/src/components/status/ServiceStatusOverview.vue` — 总览+模型列表容器
- `frontend/src/views/user/StatusView.vue` — 独立状态页
- `frontend/src/api/status.ts` — status API 调用

### 前端修改
- `frontend/src/router/index.ts` — 添加 `/status` 路由
- `frontend/src/components/layout/AppSidebar.vue` — 添加「服务状态」菜单项
- `frontend/src/views/user/DashboardView.vue` — 嵌入精简版状态条
- `frontend/src/views/HomeView.vue` — 添加服务状态区域
- `frontend/src/i18n/` — 添加相关翻译 key
- 管理员设置相关组件 — 添加探针配置面板
