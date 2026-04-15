# Gateway 会话诊断

Gateway 会话诊断用于排查粘性会话、切号轨迹、首次 token 慢、cache billing 状态等问题。

默认不开启，不改变调度行为。只有命中指定用户 ID 或 API Key ID 时才会输出额外日志。

## 开启方式

在服务环境变量中配置：

```env
GATEWAY_SESSION_DIAG_USER_IDS=123,456
GATEWAY_SESSION_DIAG_API_KEY_IDS=789
```

支持逗号、分号、空格、换行分隔。

## 日志事件

- `gateway.session_diag.initial`：记录 session hash 来源、metadata.user_id 是否存在、是否解析成功、是否命中已有粘性会话绑定。
- `gateway.session_diag.selected`：记录本轮选中的账号、平台、切号次数、失败账号数量。
- `gateway.session_diag.failover`：记录上游失败后的切号控制流、状态码、是否触发 cache billing、失败账号数量。
- `gateway.session_diag.success`：记录最终成功账号、切号次数、首次 token 时间和 usage token 摘要。

## 使用建议

- 临时排查时只填一个用户 ID 或一个 API Key ID，避免日志量过大。
- 排查完成后移除环境变量并重启服务。
- 诊断日志不记录完整 session hash，只保留前 8 位短 hash 用于关联。
