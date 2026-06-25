# 官方同步冲突解决台账

> 生成于 2026-06-25。基于实测 merge 的逐文件冲突分析。择期执行时按本台账逐文件解决。

## 一、概览

| 项 | 值 |
|---|---|
| merge-base(共同祖先) | `f18451e5` |
| ours（本地 main / 融合线） | `2f6d1ed8` |
| theirs（官方 origin/main） | `ce6af413` |
| 官方领先 / 本地领先 | 304 / 356 commit |
| 冲突文件 | **69**（后端 service 30 + 后端其他 21 + 前端/test/杂项 18） |
| 冲突块 | ~130 |
| 自动合并文件 | **511**（大部分官方改动可自动吸收） |

**复现冲突命令**（执行时先做）：
```bash
git checkout codex/official-sync-review     # 已重置到 main(2f6d1ed8)
git merge --no-commit --no-ff origin/main   # 复现 69 冲突
# 按本台账逐文件解决 → go build ./... && go vet → 测试 → commit
# 验证通过后: bash scripts/sub2api-threeway-sync.sh --mode promote
```
> 注：本仓库 git 已设 `http.version HTTP/1.1`（规避 GitHub HTTP2 framing 报错），保留无害。

## 二、官方核心重构（多个冲突的共同根因）

官方做了一次跨切面重构（commit `d8cbf9ab`→`b1c4be4a`→`619e5ae6`）：
- `ParsedRequest.Body` 从 `[]byte` → **`*RequestBodyRef`**（带 `.Bytes()` / `.ReplaceBody()`）
- 删除本地的 `ParsedRequest.System` / `Messages` / `Group` / `CloneForBody` 等字段
- 引入 `FilterThinkingBlocks` / `NormalizeChineseLLMThinking`（国产模型 thinking）/ `readUpstreamErrorBody` / 错误处理变参化

**影响**：`gateway_service.go`(#7/#8/#18)、`gateway_request.go`、`gateway_handler.go`(#944) 大量冲突源于此。**Body 类型决策是全局的**——一旦采纳官方 `*RequestBodyRef`，所有 `.Bytes()`/`ReplaceBody()` 调用点必须跨文件一致，否则编译失败。本地多出的 `ParsedRequest.Group`/`System`/`Messages` 需全仓搜调用点确认是否仍依赖。

## 三、★需 OWNER 决策的关键点（执行前必须先定）

| # | 决策 | 本地(ours) | 官方(theirs) | 建议 | 关联 |
|---|---|---|---|---|---|
| D1 | **Claude OAuth system prompt 架构** | 4-block（billing+AgentSDK identity+task(1h)+style(1h)）+ `cch=00000` 签名 + `cc_entrypoint=sdk-cli`，对齐 2.1.165 抓包 | 3-block（billing+CC身份+扩展）+ 无 cch（issue #3358 删）+ `cc_entrypoint=cli` + `claude_oauth_system_prompt_blocks` 可配置 | **需 live 实测决定**。官方"贴近真实 CLI"方向理论上更不易判第三方，但本地 4-block 是实测对齐结果。**任一方案都必须用真实 opus-4-8 验证不触发 400/第三方检测/缓存重建** | gateway_billing_block.go + gateway_service.go #5/#6/#14 + gateway_prompt_test + anthropic_apikey_passthrough_test |
| D2 | **CLICurrentVersion 版本号** | `2.1.156`（与 PlainCLICanonicalUserAgent 死写一致，防 Windows 指纹污染→opus-4-8 死循环） | `2.1.161` | **倾向保留本地 2.1.156**，或同步升 PlainCLICanonicalUserAgent 后重新抓包验证。机械采纳官方会破坏指纹一致性 | claude/constants.go |
| D3 | **cyber_policy 采纳** | 无 | 有（recordCyberPolicyIfMarked / CyberBlocked / cyber_session_block） | memory 标注"待决策"。helper 已在树，可采纳 | openai_chat_completions.go #215/#327, setting_service |
| D4 | **antigravity opus-4-8 映射** | 删 opus-4-8/4-7 自映射，迁移到 4-6-thinking 可用池 | 加 opus-4-8/4-7/fable-5 自映射 | **保留本地映射 + 补 fable-5**（注意 4-7 重复键） | domain/constants.go, request_transformer.go, constants_test.go |
| D5 | **inline-system 重试块** | 有（已是死代码，错误串失效，见 memory） | 改为 `shouldRectifySignatureError` | 采纳官方签名；本地冗余重试块可删（真正防 400 的 `migrateAnthropicInlineSystemMessages` 在前置 #10 保留） | gateway_service.go #11 |

> **关于缓存问题**：D1 的 system prompt 架构是与缓存最相关的决策点，但**它不解决"inline-system-in-messages 上提致 system 前缀膨胀"**（那是 `migrateAnthropicInlineSystemMessages`，#10 确认两方案都需保留以防 400）。官方无针对该问题的现成解。

## 四、逐文件台账

### A. 高危 / 需人工深判

| 文件 | 块 | 策略 | 必护本地点 |
|---|---|---|---|
| **gateway_service.go** | 20 | 见下方逐块表 | 缓存修复 #9/#19、bridge/mimicry #5/#6、CCH #14 |
| **gateway_billing_block.go** | 2 | **服从 D1 决策**，不能孤立解 | 若保 4-block 则 `buildBillingAttributionBlockJSON` |
| gateway_request.go | 1 | **采纳官方**（删 HEAD 重复 `ParseGatewayRequest` + 重复 `CloneForBody@495`）；重新嫁接本地 `SessionContext.UserID`/`ParsedRequest.Group`/`StripAnthropicBillingHeaderBlocks`/thinking-alias/`PrepareSharedAnthropicThinkingHistory` 到官方结构 | 上述本地字段/调用 |
| gateway_handler.go | 7 | 混合：签名块(206/412/775/2066)**保留本地** 5-arg+连通探测；usage块(607/1163)**融合**(本地diag+官方thinking-effort fallback)；944**采纳官方** ReplaceBody | session-diag、连通探测、邮箱域名failover、proxy脱敏日志 |
| identity_service.go | 1 | **保留本地** UAForm 重写（opus-4-8 修复）；官方版本刷新前移到 pkg/claude 模板核实 | 整个 UAForm 分桶；**不可**恢复 userAgentVersionRegex |
| model_pricing_resolver.go | 1 | **双签名真融合**：`intervalToModelPricing(base, iv, supportsCacheBreakdown, chPricing)`，clone base(本地)+5m/1h(本地)+官方 channelPricing image 覆盖 | cloneModelPricing、CacheWrite5m/1hPrice |
| antigravity/request_transformer.go | 4 | **融合本地为底**：采纳官方 `messageSystemParts`(65559ac5 merge system role)+fable-5；保留本地签名链(ToolUseSignatures/clientToOfficial)+user-system-first顺序 | 签名链、injectInterleavedThinkingHint、EnabledCreditTypes |
| apicompat/responses_to_anthropic_request.go | 1 | **两套 helper 全保留**（本地4个 firstNonEmpty等 + 官方 normalizeAnthropicToolPairing/anthropicMessageFromBlocks）——单取任意一侧都编译失败 | ResponsesInputToAnthropicForKiro + 4 helper |
| wire_gen.go | 6 | **不手改**。解完所有非生成文件后 `cd backend && go generate ./cmd/server` 重新生成；diff 确认 kiro/droid 与 ops/billing/compliance provider 并存 | kiro/droid 全链路、antigravity tlsFingerprint 入参 |

#### gateway_service.go 20 冲突块逐块策略

| # | 函数 | 策略 | 涉缓存/mimicry |
|---|---|---|---|
| 1 | system prompt 常量 | 保留本地 Agent SDK 常量（4-block 用），服从 D1 | 是 |
| 2 | 常量块 | 融合（两常量并存） | 否 |
| 3 | UpstreamFailoverError 周边 | 融合（纯新增并存） | 否 |
| 4 | 账号调度过滤链 | 保留本地（RPM均衡/后缀避让，修4-8死循环）+ 可选插官方 PreferSoonestReset | 否(调度) |
| 5 | 构造 OAuth system 数组 | **服从 D1**；保 4-block 则不采纳官方可配置壳 | **是★** |
| 6 | shouldMimic/injectBridge/UA 等 | **保留本地全部 6 函数** + 追加官方 `claudeOAuthSystemPromptInjectionSettings` | **是★** |
| 7 | shouldEmulateWebSearch | 采纳官方签名(Body重构+GroupID) | 否 |
| 8 | passthrough Body | 采纳官方 `.Bytes()`，保留本地 originalModel | 否(Body重构) |
| 9 | 缓存重写+工具名混淆 | **保留本地 *WithTTL(cacheTTLTarget1h)** + 吸收官方 replaceBody 包装。**绝不退回非TTL版** | **是★8f446c0f/1e36c114** |
| 10 | 转发前预处理 | **融合**：本地 migrate(防400)+probe+抓包 全留 + 官方 FilterThinkingBlocks/NormalizeChinese | **是★migrate必需** |
| 11 | 400重试 | 采纳官方 `shouldRectifySignatureError(reqModel)`；本地死重试块按 D5 可删 | 是(失效) |
| 12 | apikey passthrough | 融合：官方 ReplaceBody 同步 + 本地 migrate + setOps | 是 |
| 13 | ApplyBedrockCCCompat | **采纳官方**（纯新增） | 否 |
| 14 | beta/CCH | **融合，顺序关键**：官方 beta计算+body sanitize → **再**本地 CCH 签名。服从 D1 | **是★CCH** |
| 15 | Vertex body | 融合：官方 Vertex beta过滤(修#3358) + 本地 setOps 抓包 | 是(抓包) |
| 16 | handleErrorResponse | 采纳官方 readUpstreamErrorBody+变参(连锁改调用方) | 否 |
| 17 | handleFailoverSideEffects | 采纳官方（按模型限流） | 否 |
| 18 | CountTokens passthrough | 采纳官方 `.Bytes()`，保留 originalModel | 否 |
| 19 | CountTokens 缓存重写 | **保留本地 *WithTTL** + 官方 replaceBody（与 messages 同款TTL，否则前缀签名不一致） | **是★** |
| 20 | CountTokens CCH（~12124） | 同 #14，保留本地 CCH 签名 | **是★** |

### B. 采纳官方（安全，纯改进/纯新增）

| 文件 | 块 | 说明 |
|---|---|---|
| api_key_service.go | 1 | 删 HEAD 重复 Delete，采纳官方 DeleteWithAudit（用 `apiKey.Key`） |
| model_rate_limit.go | 1 | 删 HEAD 死重复，采纳官方循环（本地 resolveRequestedModelKey 已在L75调用） |
| usage_log_repo.go | 9 | 块1-8 恢复 `total_cache_tokens` + 保留拆分列（列序↔scan序对齐！）；块9 删 HEAD 重复扫描（官方已并行化） |
| usage_service.go / usage_log_types.go | 5+1 | 缓存 token 拆分字段，仅排序差异，取任一侧（字段集相同） |

### C. 机械融合（低危，字段/条目并集）

setting_service.go(3)、settings_view.go(2)、claude_code_validator.go(3)、billing_service.go(1)、ratelimit_service.go(1)、token_refresh_service.go(1)、ops_upstream_context.go(1)、antigravity_gateway_service.go(1)、domain_constants.go(2)、api_key_auth_cache.go(1)、dto/settings.go(2)、admin/setting_handler.go(3)、admin/account_data.go(3)、http_upstream.go(1)、server/router.go(1)。

要点：
- **billing_service.go**：保留本地 `gpt-5.2-codex`/`gpt-5.3-codex` + 官方国产 LLM fallback
- **ratelimit_service.go**：保留本地 anthropic no-reset 429 退避 4 常量 + 官方 OpenAI 图像限流
- **http_upstream.go**：官方 zstd 增强 + **保留本地 `case "compress"` LZW 分支**；非冲突区 DNS 防泄漏勿回退
- **server/router.go**：采纳官方 `RegisterAdminRoutes(+settingService)` + 保留本地 public 路由 + **RegisterGatewayRoutes 去重**（否则 panic）
- **token_refresh_service.go**：并集所有串，**保留官方重加的 `refresh_token_reused`** + 本地 Kiro 检查
- **domain_constants.go**：本地 strings 版 `SettingKeyAuthSourcePlatformQuotas`（改回 fmt 会编译失败）；勿重复 `SettingKeyWebSearchEmulationConfig`
- **admin/group_handler.go**：保留本地 `kiro droid` oneof

### D. ★隐性陷阱（机械解会静默丢功能/编译失败——呼应"sync merge 曾丢官方修复"教训）

| 陷阱 | 文件 | 必须 |
|---|---|---|
| 1 | ops_error_logger.go | 本地 helper 缺 `APIKeyPrefix`，机械取本地丢官方审计修复。**必须在 `applyOpsIdentityFieldsFromContext` 内补 `entry.APIKeyPrefix = keyPrefix(apiKey.Key, 8)`** |
| 2 | apicompat/responses_to_anthropic_request.go | 看似二选一，实为两套 helper 都要，单取必编译失败 |
| 3 | api_key_auth_cache_impl.go | 版本号撞车（双方都→12 但语义不同）。**必须 bump 到 13**，绝不留 12 |
| 4 | domain/constants.go | `claude-opus-4-7` map 重复键（官方自映射 vs 本地迁移映射），**只保本地** |
| 5 | claude/constants.go | CLICurrentVersion 机械采纳官方破坏指纹一致性（见 D2） |
| 6 | 前端 OpsErrorLogTable.vue | thead 已自动合并成官方 12 列，body 必须跟官方 3 列（否则列错位） |
| 7 | 前端 ChannelsView.vue | 本地已删 bedrock_cc_compat，**保留 HEAD（空）**，丢官方 bedrockCCCompatEnabled 行 |
| 8 | 前端 UsageTable.vue | imageUnitPrice 已改 import，**删本地内联版**（否则重复定义） |

### 前端 / 测试（多为 additive "keep both"）

- **i18n zh/en.ts**：keep both，**dedup 陷阱**：en.ts 的 `imageSizeSource`/`keywordBlock`、zh.ts 的 `imageSizeSource` 已存在，删重复
- **useModelWhitelist.ts**：保留本地丰富集 + 补 `claude-fable-5`，避免 `claude-opus-4-7` 数组重复
- **测试文件**：多数 keep both（Kiro/Droid 子测试已确认 source 支持）。**4 个硬耦合 source 决策**：gateway_prompt_test/anthropic_apikey_passthrough_test（4-block vs 3-block，跟 D1）、gateway_handler_intercept_test（detectInterceptType 元数，跟 gateway_handler）、constants_test（antigravity opus-4-8，跟 D4）——**这 4 个必须在 source 解完后匹配解**
- **README.md / .gitignore / config.example.yaml**：融合，本地品牌为主 + 官方新增；config 注意 `openai_http2` 别重复键

## 五、推荐执行顺序

1. **先定 D1-D5 决策**（尤其 D1 system prompt 架构，建议先 live 实测）
2. `git checkout codex/official-sync-review && git merge --no-commit --no-ff origin/main`
3. 解 C 类机械融合（低危，快速）+ B 类采纳官方
4. 解 D 类隐性陷阱（逐个核对）
5. 解 A 类高危（gateway_service.go 按逐块表，保护缓存修复；gateway_billing_block 服从 D1）
6. 解前端 + 测试（测试中 4 个硬耦合的最后解，匹配 source）
7. `cd backend && go generate ./cmd/server` 重新生成 wire_gen.go
8. `go build ./... && go vet ./...`（重点：Body RequestBodyRef 类型一致、usage_log_repo 列↔scan 对齐、wire kiro/droid+ops 并存）
9. 后端测试 + 前端 `pnpm test` + lint
10. **live 实测**（按 memory 教训）：真实 opus-4-8 验证 system prompt 不触发 400/第三方检测/缓存重建
11. `bash scripts/sub2api-threeway-sync.sh --mode promote`

## 六、本地必护清单（绝不能被官方覆盖）

- **缓存修复**：gateway_service.go #9/#19 `*WithTTL(cacheTTLTarget1h)`（8f446c0f/1e36c114）；`injectBridgeCacheBreakpoints` trailing 避 system（2f6d1ed8）；`migrateAnthropicInlineSystemMessages`（防 400 必需）
- **mimicry/指纹**：UAForm 分桶（identity_service）、CLICurrentVersion/MacOS 指纹一致性、EnableFingerprintUnification、claudeUpstreamUserAgent（修 4-8 死循环）
- **平台**：kiro/droid 全链路、antigravity tlsFingerprint 隔离
- **计费**：gpt-5.2/5.3-codex fallback、cloneModelPricing、5m/1h 缓存写分价、account_stats_cost
- **调度/failover**：RPM 均衡、邮箱域名后缀避让
- **抓包工具**：setOpsUpstreamRequestBody / OpsUpstreamRequestBody（全链路抓包）
- **审计补漏**：ops_error_logger 的 APIKeyPrefix（陷阱1）

## 七、对缓存问题的结论

官方 304 commit **无**直接解决本地缓存重建问题的实现：
- 本地 prompt-cache 断点逻辑（gateway_messages_cache.go 等）官方无对等物
- 官方 `8ce7b9a8` system prompt blocks 是注入配置化，**正交**于"inline-system 上提致前缀膨胀"
- `65559ac5` 是 antigravity 平台 merge system role，平台不同
- `migrateAnthropicInlineSystemMessages`（#10）经 live 实测确认防 400 必需，两方案都需保留

缓存重建主因（上游缓存非确定性）与同步无关，无法通过吸收官方代码解决。
