# Internal Payment Cutover

目标：

- 将当前 `sub2api` 从外挂 `sub2apipay` iframe 方案切换到官方内置 payment 系统
- 保留现有 `sub2api` 生产数据
- 保留现有 `sub2apipay` 订单 / 审计 / 套餐 / provider 原始数据

## 当前策略

首期切换只迁移：

- 支付 provider 实例
- 订阅套餐
- 支付运行配置

首期不迁移：

- `sub2apipay.orders`
- `sub2apipay.audit_logs`

原因：

- 旧 `sub2apipay` 使用字符串主键（`cuid`）
- 官方内置 payment 使用 `BIGSERIAL / int64`
- 如果首期强迁历史订单，需要额外做：
  - 订单 ID 重映射
  - 审计日志 `order_id` 重绑
  - 套餐 ID 重映射
  - 历史退款 / 履约状态一致性校验

这会显著提高切换风险，不适合和“启用新支付系统”放在同一刀执行。

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
   - 关键支付环境变量
3. 将旧 provider 配置：
   - 用旧 `sub2apipay` 的 `ADMIN_TOKEN` 解密
   - 用新 `sub2api` 的 `TOTP_ENCRYPTION_KEY` 重新加密
4. 写入内置 payment 所需的新表和 `settings`
5. 自动关闭旧 iframe 配置：
   - `purchase_subscription_enabled=false`
   - `purchase_subscription_url=''`

## 当前状态

这份替换工作目前仍在隔离 worktree 分支中实施：

- worktree: `/tmp/subapi-payment-yizJZ3`
- branch: `payment-replace`

尚未合并回主工作区
尚未推送
尚未发布生产
