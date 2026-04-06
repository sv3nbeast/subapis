# Antigravity 429 配额耗尽自动临时不可调度说明

## 背景

当 Antigravity 上游返回 `429 RESOURCE_EXHAUSTED` 且错误内容明确表示账号当前配额/额度已耗尽时，原有逻辑主要依赖：

- 模型级 cooldown
- 手工配置的 `temp_unschedulable_rules`
- 调度器切换下一个账号

这对“单个账号短时限流”有效，但对“同一批账号在一段时间内陆续进入额度耗尽状态”的场景不够稳定。线上会出现持续切号，但仍频繁撞到已耗尽额度账号，导致对话中断。

## 本次新增行为

本次新增了 **Antigravity 配额耗尽自动临时不可调度** 机制。

当满足以下条件时，系统会自动把当前账号标记为临时不可调度：

- 平台是 `Antigravity`
- 上游返回 `429`
- 错误内容匹配到明确的配额耗尽关键词，例如：
  - `quota_exhausted`
  - `check quota`
  - `not enough credits`
  - `credit exhausted`

命中后会执行：

1. 将账号写入 `temp_unschedulable`
2. 当前请求立刻切换下一个账号
3. 调度缓存立即同步，后续请求也会避开该账号

## 与现有功能的关系

本次不是重复造功能，而是在现有框架上补充“系统级自动触发”：

- **手工配置的 `temp_unschedulable_rules` 仍然优先**
- **普通 `RATE_LIMIT_EXCEEDED` 429 仍然走原有模型级 cooldown**
- **只有明确的 quota exhausted / credits exhausted 类 429，才会触发这次新增的自动临时不可调度**

也就是说：

- 你已有的手工规则不会失效
- 现有模型级 cooldown 不会被替代
- 这次只是补上“明确额度耗尽”这一类场景的自动隔离

## 默认时长

默认自动临时不可调度时长为 `60` 分钟。

可通过环境变量调整：

```env
GATEWAY_ANTIGRAVITY_QUOTA_EXHAUSTED_TEMP_UNSCHED_MINUTES=60
```

说明：

- `0`：关闭这项自动摘除
- 正整数：表示临时不可调度分钟数

## 恢复方式

账号不会被永久停用。

恢复方式与现有机制保持一致：

- 到达 `temp_unschedulable_until` 后自动恢复参与调度
- 如果你启用了定时测试并开启 `auto_recover`，测试成功后也可以提前清理运行时状态

## 适用场景

推荐用于以下场景：

- Antigravity 渠道账号较多
- 同一组里混有大量会阶段性额度耗尽的账号
- 线上高峰期经常出现大量 `429 RESOURCE_EXHAUSTED`

## 不适用场景

这项功能不能解决所有 429：

- 如果是普通短时限流，仍主要依赖原有模型级 cooldown
- 如果生产组里真正健康可用的账号本来就不够，自动摘除只能缓解，不能凭空增加额度

## 建议配置

建议先从下面的值开始：

```env
GATEWAY_ANTIGRAVITY_QUOTA_EXHAUSTED_TEMP_UNSCHED_MINUTES=60
```

如果你的账号恢复速度更快，可以尝试：

```env
GATEWAY_ANTIGRAVITY_QUOTA_EXHAUSTED_TEMP_UNSCHED_MINUTES=30
```

如果你希望极度保守、尽量避免反复命中，可提高到：

```env
GATEWAY_ANTIGRAVITY_QUOTA_EXHAUSTED_TEMP_UNSCHED_MINUTES=90
```

## 运维建议

这项功能最适合作为“自动隔离层”，建议搭配以下实践一起使用：

- 生产组尽量只保留近期稳定成功的账号
- 恢复中的账号通过定时测试观察后再放回生产
- 持续关注日志中的 `quota_exhausted_temp_unsched`

这样可以比单纯依赖切号或短 cooldown 更稳定。
