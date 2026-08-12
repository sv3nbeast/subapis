# Kiro nianzs 双引擎切换与回退

Sub2API 的 Kiro 主路径默认使用固定快照：

- 仓库：`https://github.com/nianzs/sub2api.git`
- Commit：`d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb`
- 校验清单：`scripts/nianzs_kiro/upstream_manifest.json`

旧实现没有删除，而是作为 `legacy` 回退引擎保留。一次请求只会选择一个
引擎，开始访问上游后不会跨引擎重放，避免工具调用重复执行或同一轮重复计费。

## 默认行为

没有额外环境变量时，`gateway.kiro_engine` 的默认值为 `nianzs`。以下 Kiro
入口使用同一个全局引擎：

- `/v1/messages`（流式、非流式）
- OpenAI Chat Completions / Responses 到 Kiro 的协议桥接
- `/v1/messages/count_tokens`
- Kiro 账号调度、429/暂停冷却状态
- OAuth 授权、Token 导入、手动刷新和后台刷新
- 账号连接测试、额度查询和上游模型同步

现网历史上允许 OAuth 账号附带 `kiro_api_key` / `kiroApiKey` CLI Key。
进入 nianzs 生成链路时会在内存中把这类账号适配为其原生 API Key 形态：
固定走 Q endpoint、发送 `TokenType: API_KEY`，且不携带 OAuth/profile header；
数据库中的账号类型和凭据不会被改写。额度查询仍使用该账号的 OAuth 凭据。

nianzs 转换器的异常通过 pipe error 交给本项目现有 SSE 生命周期处理器，不直接
注入上游 `event:error`。这是协议适配而非重试策略改写：首个客户端输出前允许
账号故障转移；输出后只发送一次客户端可见错误并禁止重放，避免半截会话静默结束。

## 完整回退

设置：

```env
GATEWAY_KIRO_ENGINE=legacy
GATEWAY_KIRO_NIANZS_GROUP_IDS=
```

然后重启 Sub2API 进程或容器。配置在进程启动时读取，不能只修改 `.env`
而不重启。

旧实现与 nianzs 使用不同的 Redis 冷却命名空间，因此 Redis 中的 429 失败
计数、冷却和暂停状态不会跨引擎继承。两套引擎仍共用账号表中的
`rate_limit_reset_at`：这是上游健康状态，不是引擎私有状态。若在 nianzs
触发冷却后立即切回 `legacy`，该字段可能继续生效到当前 1–5 分钟冷却窗口
结束；确认属于误冷却时，可在后台清除该账号的限流状态以立即恢复调度。

## 分组灰度

需要先验证少量分组时：

```env
GATEWAY_KIRO_ENGINE=legacy
GATEWAY_KIRO_NIANZS_GROUP_IDS=29,33
```

列表中的 Kiro 分组走 nianzs，其余分组走 legacy。OAuth、后台刷新、额度查询
等没有分组上下文的操作仍跟随全局 `legacy`；因此灰度只用于网关请求路径。

## 观测与确认

网关为 Kiro 请求记录 `kiro.engine_selected` debug 日志，并把 `kiro_engine`
写入上游错误事件。可据此确认请求实际使用 `nianzs` 还是 `legacy`。

固定快照一致性检查：

```bash
python3 scripts/verify_nianzs_kiro_parity.py \
  --upstream /path/to/nianzs-sub2api-at-d483aefe
```

校验器会检查：

1. 输入源码是否匹配固定 commit 的 SHA-256 清单；
2. `kiro` 和 `kirocooldown` 包是否逐字节一致；
3. 13 个服务层文件在机械命名空间转换后是否一致；
4. 缓存连续性测试是否与上游测试文件机械转换后一致；
5. 账号连接测试的 3 个 Kiro 函数是否一致；
6. 是否只存在已登记的双引擎依赖注入、现网 CLI Key 账号适配与状态隔离；
7. 缓存模式迁移和前端控件 fixture 是否一致。

## 快照后的兼容补丁

2026-08-12 已审计 nianzs `63f014369cd33d02115a745e7838edd632695736`：

- 固定快照覆盖的 `kiro`、`kirocooldown` 和机械命名空间服务文件自
  `d483aefe` 后没有新的核心实现变更，因此快照 commit 与校验清单保持不变；
- 单独移植 `13cd8c4fc` 中 Codex 0.147+ namespace/custom 子工具修复，并适配
  本项目 Kiro Responses 直连路径，避免 `functions.exec` 被丢弃或以摊平名回传；
- `7ef6a364f` 只有 README、测试构造参数和前端测试调整，没有可移植的运行时修复；
- Kiro GPT 的 `remote_compaction_v2` 由本项目在服务层模拟为单个 compaction
  item，因为 nianzs 到上述审计 commit 仍未实现该协议适配。
