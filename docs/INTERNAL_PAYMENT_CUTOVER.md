# Internal Payment Cutover

目标：

- 将当前 `sub2api` 从外挂 `sub2apipay` iframe 方案切换到官方内置 payment 系统
- 保留现有 `sub2api` 生产数据
- 保留现有 `sub2apipay` 订单 / 审计 / 套餐 / provider 原始数据

## 当前策略

当前切换迁移：

- 支付 provider 实例
- 订阅套餐
- 支付运行配置
- 历史 `orders`
- 历史 `audit_logs`

迁移策略：

- 保留旧 `sub2apipay` 数据库与容器，作为回滚与审计备份
- 新内置 payment 中为历史订单重新分配数值型 `payment_orders.id`
- 将旧系统字符串订单 ID 写入新表 `out_trade_no`
- 审计日志按旧订单 ID 映射到新数值订单 ID
- 套餐 / provider 通过兼容映射表完成旧 ID 到新 ID 的绑定

## 迁移文件

本分支新增本地安全编号 payment 迁移：

- `103_payment_orders.sql`
- `104_payment_audit_logs.sql`
- `105_subscription_plans.sql`
- `106_payment_provider_instances.sql`

说明：

- 不沿用官方 `092~102` 编号，避免与当前分叉生产已跑过的本地迁移冲突
- 由于生产 `sub2api` 库当前不存在这些 payment 表，直接按最终结构创建即可

## 切换脚本

工具脚本：

- [tools/remote-cutover-internal-payment.sh](/tmp/subapi-payment-yizJZ3/tools/remote-cutover-internal-payment.sh)
- [tools/cutover-internal-payment.sh](/tmp/subapi-payment-yizJZ3/tools/cutover-internal-payment.sh)

脚本行为：

1. 备份 `sub2api` / `sub2apipay` 两边数据库
2. 从旧 `sub2apipay` 读取：
   - `payment_provider_instances`
   - `subscription_plans`
   - `orders`
   - `audit_logs`
   - 关键支付环境变量
3. 将旧 provider 配置：
   - 用旧 `sub2apipay` 的 `ADMIN_TOKEN` 解密
   - 用新 `sub2api` 的 `TOTP_ENCRYPTION_KEY` 重新加密
4. 写入内置 payment 所需的新表和 `settings`
5. 自动关闭旧 iframe 配置：
   - `purchase_subscription_enabled=false`
   - `purchase_subscription_url=''`

6. 历史订单迁移完成后，旧 `sub2apipay` 容器可以停止；用户历史订单与审计日志继续从内置 payment 读取

## 当前状态

这份替换工作目前仍在隔离 worktree 分支中实施：

- worktree: `/tmp/subapi-payment-yizJZ3`
- branch: `payment-replace`

当前文档需以实际分支与生产状态为准。
