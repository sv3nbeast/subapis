# Antigravity 429 同账号延迟重试说明

## 背景

当 Antigravity 上游返回 `429 RESOURCE_EXHAUSTED` 时，这个响应只说明本次调用没有获得资源，不能据此判断：

- 账号整体已经不可用
- 当前模型在该账号上会持续不可用
- 后续请求一定还会失败

因此不能把一次 `429` 扩大为账号级长期状态。

## 本次新增行为

Antigravity 的 quota/credits 类 `429` 采用 **同账号延迟重试**：

当满足以下条件时：

- 平台是 `Antigravity`
- 上游返回 `429`
- 错误内容匹配到明确的配额耗尽关键词，例如：
  - `quota_exhausted`
  - `check quota`
  - `not enough credits`
  - `credit exhausted`

系统只会执行：

1. 保留当前账号和粘性绑定
2. 等待 `5s` 后在同一账号上重试一次
3. 重试成功则继续正常响应
4. 重试仍为同类 429，则把本次 429 返回客户端，不切换账号
5. 不写入账号级 `temp_unschedulable`
6. 不为 quota/credits 类响应写入模型级 cooldown
7. 后续新请求仍可正常调度该账号和模型

## 与现有功能的关系

其他错误保护保持独立：

- Antigravity 的 `429` 即使命中手工 `temp_unschedulable_rules`，也不持久化账号临时不可调度状态
- 明确带 `RetryInfo` 的普通 `RATE_LIMIT_EXCEEDED` 可以保留短时、模型级 cooldown
- `401`、`403`、`5xx` 等非 429 状态仍按各自现有策略处理

旧配置 `GATEWAY_ANTIGRAVITY_QUOTA_EXHAUSTED_TEMP_UNSCHED_MINUTES` 仅为兼容历史配置文件而保留，运行时不再生效。

## 适用场景

适用于以下场景：

- Antigravity 渠道账号较多
- 同一账号偶发 `RESOURCE_EXHAUSTED`，随后又能成功
- 并发请求短时间命中多个 429，但账号测试仍然正常
- 希望保留当前账号重试机会，又不希望污染后续调度状态

## 不适用场景

同账号延迟重试不会掩盖账号池整体容量不足：

- 如果同一账号重试后仍然返回同类 429，本次请求仍会失败
- 该策略不会自动探测其他账号
- 普通带明确重试窗口的模型限流仍可短时隔离该账号的对应模型

## 运维建议

建议关注：

- `quota_retry_same_account` 和 `quota_retry_same_account_exhausted` 日志
- 同账号延迟重试的成功率和最终结果
- 同一账号在后续请求中的恢复成功率

不要再把单次 Antigravity 429 作为账号整体不可用的证据。
