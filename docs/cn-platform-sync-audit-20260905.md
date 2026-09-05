# 国产平台同步漏项审计（进行中）

前半部分是第一批提交 `af17c8400` 的检查点；第二轮进展、当前验证状态见文末，不能把历史失败数当作当前结果。

## 范围与状态

- 起点：`04ce360b0f20695e8b322c670efbfd16f230f184`（已发布的 0.2.0 同步修正版）。
- 官方对照：`e05e26746^2`；同步前本地：`e05e26746^1`。
- 本任务工作树：`/tmp/sub2api-fix-cn-group-platform-gaps-20260905`。
- 目标：GLM/Zhipu、Kimi、DeepSeek 管理入口及相关平台契约完整恢复。
- 当前是可审查检查点，不是发布就绪结论；没有推送、部署或改动生产配置。
- 新模型 ID、供应商价格、上游配额没有改变。没有进行生产生成探测，不能据此声称某个供应商账号的真实调用已验证。

## 已确认并修正

| 契约 | 漏项及恢复 |
| --- | --- |
| 新建/编辑分组及筛选 | GroupsView 丢失公共目录引用；恢复全部十种具体平台，保留 Kiro/Droid 与组合分组。 |
| 组合路由与账号绑定 | 抽出的 CompositeRoutesModal 仍只有旧五平台；GroupSelector 也滤掉组合分组。恢复 CN 路由目标及可调度平台的组合绑定，仍不扩展 Kiro/Droid 组合路由能力。 |
| 渠道模型映射及价格 | ChannelsView 的固定平台顺序漏 CN，编辑保存会丢这些平台配置；恢复平台目录、组合分组归属、绑定 ID 去重、用户定价与独立账号成本规则。 |
| 分组模型候选 | CN 误落到 Claude 默认目录；改为使用绑定账号的真实映射，不凭空宣称 Claude 可用。恢复 CN/Grok 自定义模型列表入口。 |
| 平台额度 | Ent 校验、用户额度弹窗、系统默认及注册来源额度表单漏 CN。恢复十平台；保留现有数据库迁移 224/234，不更改已应用迁移。 |
| 默认额度/停调阈值保存 | handler 丢失默认额度传递与阈值接收/传递，阈值默认表又漏 Kimi/Zhipu；恢复保存、响应及省略字段保留语义。 |
| 分组自定义定价 | handler/service/create repository 丢失传递，直接序列化内部结构会丢 snake_case 价格。接通创建/更新，使用独立 API 映射保留存量存储 JSON 格式；恢复校验与渠道缓存失效依赖注入。 |
| CN 兼容别名计费 | 官方过滤函数及真实 RecordUsage 调用点丢失，可能按 Claude 目录价误收 CN 请求。恢复过滤；显式分组/渠道价格保留；完全无价时保留用量并沿用缺价告警/零成本记录。 |
| 账号创建/客户端配置 | 恢复上游模型预览后的正式能力元数据同步、警告，以及已支持分组的 Codex 配置入口和目录模型/effort 选择；保留 SubAPIs 命名和自定义默认配置。 |
| 标签和显示 | 恢复 CN 配色判定、平台标签及中英动态词条，补 Droid 分组词条。 |
| 设置相关附带漏项 | SMTP 测试 TLS 的省略/显式 false 语义；腾讯/阿里验证码请求、验证、保存、读取、公开字段与密钥审计；精简首页、Grok 默认端点设置保存链。 |

这些修复没有改生产账号绑定或创建新的生产分组。GLM 平台值仍为 `zhipu`，不是 `glm`。

## 已执行证据

- 定向前端：30 文件 / 261 测试通过。包含实际渲染分组平台选择与提交、账号绑定、组合路由、6 个 CN 渠道保存往返、账号创建/编辑、额度、设置及完整 i18n 测试目录。
- 前端 `vue-tsc --noEmit` 通过；`vue-tsc -b && vite build` 通过（仍有分块大小警告）。
- 后端默认测试集：`ent/schema`、`internal/handler/admin`、`internal/service`、`internal/repository`、`migrations` 全包通过；Service 用时约 167 秒。`internal/server` 默认标签没有测试，不能算 API contract 验证。
- `go test -tags unit ./internal/handler/admin -count=1` 全包通过。
- 新增真实 GroupHandler 建组/更新 JSON 往返测试通过，验证 snake_case 价格、开关和平台不会丢失。
- 新增真实 RecordUsage CN 兼容别名测试通过，覆盖 Kimi/Zhipu/DeepSeek 的未定价与显式价格两种情况；同时运行既有 OpenAI RecordUsage、Kiro 缓存测试通过。
- `git diff --check` 通过。

临时日志位于 `/tmp/sub2api-cn-*.log`，包括基线、全量与定向输出；测试只使用合成数据。

## 尚未通过的完整门禁

完整前端命令均为 `vitest run`：

| 版本 | 结果 |
| --- | --- |
| 未修改基线 04ce360b0 | 10 个文件失败；54 失败 / 1879 通过，另有测试模块加载失败 |
| 当前检查点 | 6 个文件失败；22 失败 / 1967 通过 |

剩余文件：`useModelWhitelist.spec.ts`、`UseKeyModal.spec.ts` 的 Fable 合同、`AccountsView.usageWindowsHint.spec.ts`、`HomeModelMarketEntry.spec.ts`、`PaymentView.spec.ts`、`UsageView.spec.ts`。支付页等相同断言在基线也失败；UseKeyModal 基线先被缺失 mock 阻断，因此其 Fable 断言不能凭源码相同就豁免，仍待核实。

Service 的 `unit` 标签集在基线和当前均不能编译。已经恢复部分丢失的 helper/签名/fixture，但以下类别仍需三方核对，不能仅为编译通过补无调用点的空壳：

- usage billing request ID 识别；
- Grok OAuth fixture、粘性会话种子、调度余量更新与图片 SSE fixture；
- OpenAI 原始 Chat 流错误 reader、空 completed 判断；
- 部分计费测试仍使用旧的 nullable long-context 签名；
- response-model billing 的选择/采纳逻辑测试引用缺失。

完整 `-tags unit`、server API contract、全部变更测试所在包的最终门禁与最终网关回归评审尚未完成。

## 网关评审检查点

- 主要改动在管理面、配置、定价读写及终态后的计费；没有修改流 adapter、重试循环、上下文释放或缓存指纹。
- CN 计费真实入口测试已证明不误套 Claude 目录价且显式价格保留。
- 四项不变量中，流终止/缓存连续性/重复创建在既有默认 Service 测试有覆盖；没有新增首字前网络等待。此处只是当前证据，不替代最终精确 SHA 评审。
- **Verdict: INCONCLUSIVE（禁止发布）**：完整 unit 及相关高风险测试尚未可运行，不能把默认标签通过表述成全部回归通过。

下一步先恢复剩余 Service unit 契约并验证 CN/分组定价集，再核对剩余前端失败、运行最终 gate，更新本台账后才可标记目标完成。

## 第二轮检查点（2026-09-05）

### 新确认的实际漏合并

1. CN 自适应协议分流函数存在，但 Chat 入口遗漏调用；GLM 被发到不支持的 Responses 路径。恢复 Chat/Anthropic/Responses 的分派，保留 Grok 本地通道开关。
2. Responses request builder 忽略协议专属 base URL；固定协议又回退到厂商默认地址。恢复自适应 URL 和固定自定义 URL，并保留 CN 的 max_output_tokens。
3. 通用组合分组调度未消费已解析平台/账号模型归属，可能选不到自定义别名或选中无对应映射的账号。恢复入口解析和两套调度器的归属守卫。
4. CN 批量更新事件已识别平台，但重建遍历的旧目录漏三平台；恢复九平台缓存重建。Kiro 独立调度保持不变，保留 Droid 与 OpenAI mixed 桶。生命周期断言按具体平台和查询去重规则更新，不改变租约/epoch/发布时序。
5. 组合渠道的可匹配平台目录有 CN，但价格匹配谓词仍是旧五平台；恢复统一匹配规则。
6. 批量 OpenAI 配置归一化/目标验证函数未被调用；恢复前置校验及影子账号继承计数。分组限额更新恢复 nil=省略、-1=清空；恢复 Fast 开关、组合推理策略、复制时丢失的价格/模型额度/本地字段。
7. Grok 模型隔离的粘性账号种子、quota 到停调阈值快照的写入、调度读取端存在断链；恢复真实入口调用。缺省设置不存在使用正常缓存 TTL，而不是反复按错误 TTL 查询。
8. 恢复 Responses 空 completed 检测，仅限未有输出/usage/错误的空成功事件；已写出内容的流不重放。
9. 恢复显式 response_model 计费模式的安全采纳规则：不抬价、不把付费变免费、不绕过显式分组/渠道价。恢复合成工具/任务计费 ID、搜索附加费、Fast 实际档位、失败扣费的未结算用量记录和图像规格元数据。
10. 前端余项：代理加载失败不阻断分组加载/刷新；补回支付、用量测试桩/操作；支付倍率文案使用真实币种。Fable 目录/配置按仓库已存在的供应商映射及 32K 输出上限对齐，未新增上游声明或修改价格。

### 第二轮证据

- 完整前端：305 文件、1,989 测试全部通过（`/tmp/sub2api-cn-frontend-round4.log`）。
- 前端 typecheck 与生产构建通过（`/tmp/sub2api-cn-typecheck-round2.log`、`/tmp/sub2api-cn-build-round2.log`）。
- 新增 `TestCNAdaptiveProtocolCompletionMatrix`：三平台 × 三协议 × 流式/非流式共 18 种组合均返回完整 hello；流式恰好一个正确终态。
- 分组创建/更新/复制、CN 协议、批量更新及阈值定向 unit 测试通过（`/tmp/sub2api-cn-admin-service-round5.log`）。
- 网关计费、CN 兼容、调度批量事件定向 unit 测试通过（`/tmp/sub2api-cn-billing-completion-round8.log`）。
- 调度 snapshot/lifecycle 全定向集通过（`/tmp/sub2api-cn-snapshot-unit-round3.log`）。
- `-race`：18 种 CN 组合、空 completed、Grok 真实粘性/阈值入口、Kiro 缓存、Grok 缓存、批量/分组生命周期通过（`/tmp/sub2api-cn-race-round3.log`）。
- 本轮默认标签 Service 全包（约 166 秒）、admin/dto/repository 全包通过（`/tmp/sub2api-cn-default-round2.log`）；admin unit 全包通过。

### 仍未完成，禁止发布

- Service unit 集已能编译，但完整执行仍有历史合同失败并在监控 nil-runtime 测试 panic。一次显式跳过该单个已知 panic 的诊断运行用于发现后续问题，**不是通过证据**；后续计费 panic 已恢复并定向验证，尚须再次完整执行。
- 剩余需核实类别包括 Antigravity 映射/验证刷新、注册域名及默认值、价格/分层合同、旧主动监控默认模式、CountTokens 的历史兼容字段及混合调度。部分已是本地实现与上游测试期望的差异，不能盲目改价格或削弱守卫。
- Server unit API contract 已执行，仍有 4 个子项失败：可用分组、系统设置的两个 GET 快照，以及批量账号更新 fixture（500）。不能把默认标签的“无测试”视为通过。
- 最终精确提交的完整 backend unit/API contract 门禁和最终四项不变量评审仍待完成；目标保持 active。仍未推送、未发布、未改生产数据。

## 第三轮检查点（2026-09-05，尚非发布候选）

### 本轮恢复的调用链

- 定价：渠道区间倍率被旧 `filterValidIntervals` 丢弃；区间分支又绕过 flat 基础价覆盖。恢复先应用基础价、再应用区间，保留本地 5m/1h 价格和 Fast 比例；显式价格与倍率优先级、区间空洞回退都有真实计费探针测试。恢复官方已提供的 GPT-5.5 Pro 独立 fallback，不改本地 GPT-5.5 Fast 2.5x 策略。
- 账号恢复：显式 Antigravity validation recovery 被共享 OAuth 非 active 守卫拦下。仅该探测允许刷新 validation-error 账号；在锁内重读为 disabled 或其他错误时仍拒绝，负向测试证明不会返回旧 token 冒充恢复成功。
- Grok 凭据：恢复缺失客户端及代理查找失败的明确错误；不再将未找到代理静默变成直连。补回完整凭据守卫；转发测试的健康 OAuth fixture 相应补齐 refresh token，没有放宽生产守卫。刷新 jitter 最小窗口与现有五分钟 soft-expiry 对齐。
- Grok 错误分类：已有 classifier/apply helper 未被真实错误入口调用。恢复 body-first 分类、明确模型限额只限制该模型、容量不足只作模型短暂避让、明确 reset 优先、缺失 reset 的短探测；pool 默认不整池停调，但显式管理员规则仍有效。Spending limit 保留可恢复状态和已观察到的账期，不伪造永久禁用。旧 24h 整账号限额测试与模型作用域/短探测新合同仍需完成整包核对，未给予基线豁免。
- 配置：Grok 默认模型、跨客户端模型映射、OpenAI TTFT 模式，以及监控模式/吞吐隐藏/额度展示只读不写。补齐 handler 接收、保存、响应、公开字段及相关运行时缓存；省略字段不覆盖，显式 false 可保存。未变化的模型设置不刷新模型映射版本。
- 注册：恢复域名黑名单调用；保留当前非白名单域名限量注册策略。来源并发显式设为默认值 5 时也能覆盖全局设置，缺省继续继承。
- 协议：CountTokens 移除生成专用 max_tokens 和 literal deferred tool 的 cache_control；Fable 默认注入遵从其既有两段合同，自定义管理员提示不覆盖。工具转换拒绝尾随第二份 JSON；移除 thinking signature 时保留大整数精度。补齐空 SSE type 的 event 字段回退、语音缺失上游 ID 时的一次性计费 ID。
- CN 推理：恢复 Kimi/Moonshot/K3 独立 max 档判断；三种 CN 平台的别名通过 Anthropic native 上游转发时，流式/非流式六种入口均保留 max 记录。
- 分组统计：`GetAllGroupUsageSummary` 丢失现有 rollup/tail helper 调用，导致全量扫描且昨日金额缺失；已接回，复用已有迁移与时区/水位实现。补回用户维度统计的 native compaction 过滤。
- 清理失配测试：修正被合并串入 Kiro 断言的 Gemini 限流测试、按现有模型映射验证 Antigravity、按当前 Grok 默认 4.6 更新旧默认别名断言。数据库 SQL mock 保留现有 5m/启用字段与新增 reasoning 元数据，不修改真实列序来迁就测试。

### 本轮证据与边界

- 完整 Service unit 已可运行到结束：第 9 轮执行 8,041 个顶层测试，66 个顶层失败，耗时约 221 秒。原始逐测试证据 `/tmp/sub2api-cn-unit-full-round9.jsonl`。这比先前在前段 panic 的诊断更完整，不代表新增了 66 个产品问题；也不能直接声称这些都是历史失败。
- 第 9 轮之后又恢复了 CN max、分组日汇总/压缩过滤及部分 SQL/mock 合同，定向 Service/Repository 验证通过：`/tmp/sub2api-cn-final-reconnect.log`、`/tmp/sub2api-cn-repo-fixtures-round6.log`。这些不能替代最终整包门禁。
- settings/admin 与 server API contract 已从前轮失败恢复，真实设置往返覆盖保存、响应及无关修改保留；日志 `/tmp/sub2api-cn-settings-entrypoint-round4.log`、`/tmp/sub2api-cn-entrypoint-round6.log`。后者 Repository 仍有两个统计入口失败，已在随后接回 helper 并定向验证。
- 定价阶梯、OAuth 恢复正反向、CountTokens builder、Grok 分类、JSON 完整性、CN 完整流、缓存与首写边界的定向 race 集通过：`/tmp/sub2api-cn-race-round4.log`。最终补充检查使用 `round5`。
- 第三批补充完成后，admin、DTO、server、repository 四个 unit 全包均通过（`/tmp/sub2api-cn-entrypoint-round7.log`）；包含 CN max 六入口的新 race 集通过（`/tmp/sub2api-cn-race-round5.log`），server 构建通过（`/tmp/sub2api-cn-build-round4.log`）。Service 全 unit 仍需在最终干净检查点复跑，不宣称整包通过。
- 本轮没有 frontend 改动；前轮 305 文件/1,989 测试、typecheck、build 通过记录保持，但仍不冒充最终发布候选验证。
- 无生产连接、数据修改、推送或部署；主工作区保持 `9d9508779`，本轮仅修改专用 worktree。

### 发布门禁结论

**Verdict: INCONCLUSIVE（禁止发布，目标仍 active）。**

Reviewed range: `04ce360b0..a13a19410` + 本轮专用 worktree 改动。高风险未决项包括 OpenAI 429 模型/账号作用域、流式 bare error 与失败透传优先级、Grok 兼容/用量提取、Kiro 重试与 token cache 合同。必须继续做三方核对及真实入口测试，不能用定向绿灯掩盖整包失败。

四项不变量：CN 完整终态、既有缓存连续性/去重、Grok first-write 定向测试通过；整个候选版本的 stream/cache/latency 仍未获得完整 PASS。没有生产 canary 或可比 TTFT 数据，且本轮未授权发布。

第 9 轮全部失败先按“未能分类”登记；其中后续已修复者以补充日志为准，仍需最终干净提交复跑。未进行严格同命令基线复现者一律不豁免。

| 第 9 轮顶层失败 | 分类 |
| --- | --- |
| `TestOpenAIGatewayGrokFreeUsageExhausted429RateLimitsForRollingWindow` | 未能分类，不豁免 |
| `TestHandleOpenAIAccountUpstreamError_Grok429UsesOnlyGrokCooldown` | 未能分类，不豁免 |
| `TestGetBaseURL_KiroAPIKeyWithoutBaseURLReturnsEmpty` | 未能分类，不豁免 |
| `TestNewKiroJSONRequestAddsConditionalHeaders` | 未能分类，不豁免 |
| `TestExecuteKiroUpstreamClears429CooldownAndContinues` | 未能分类，不豁免 |
| `TestExecuteKiroUpstreamKeepsServerErrorRetriesAtDefaultLimit` | 未能分类，不豁免 |
| `TestOpenAI429FastPath_KeepsOAuthAccountSchedulableDuringRetryWindow` | 未能分类，不豁免 |
| `TestOpenAI429FastPath_SparkQuotaOnlyBlocksSparkModel` | 未能分类，不豁免 |
| `TestOpenAI429FastPath_SparkTransient429UsesShortFallback` | 未能分类，不豁免 |
| `TestOpenAIStream429_SparkQuotaUsesQuotaHeaders` | 未能分类，不豁免 |
| `TestOpenAIStreamFailover_Spark429KeepsModelScope` | 未能分类，不豁免 |
| `TestOpenAIWSErrorEvent_OrdinaryModelIgnoresHandshakeQuotaHeaders` | 未能分类，不豁免 |
| `TestOpenAIWSErrorEvent_SparkQuotaUsesHandshakeQuotaHeaders` | 未能分类，不豁免 |
| `TestOpenAI429FastPath_SparkShadowQuotaStaysModelScoped` | 未能分类，不豁免 |
| `TestOpenAIStream429IgnoresSuccessfulQuotaSnapshotHeaders` | 未能分类，不豁免 |
| `TestOpenAIHTTP429StillUsesQuotaResetHeaders` | 未能分类，不豁免 |
| `TestShouldStopOpenAIOAuth429Failover_AfterBoundedFullWindows` | 未能分类，不豁免 |
| `TestOpenAIRefineChannelRestrictionError_AllowedConfiguredAccountKeepsCapacityError` | 未能分类，不豁免 |
| `TestOpenAIGatewayService_OAuthPassthrough_SanitizesNativeToolItemIDs` | 未能分类，不豁免 |
| `TestOpenAIGatewayService_SetupTokenLegacy_SanitizesAndTransforms` | 未能分类，不豁免 |
| `TestForwardGrokChatViaResponsesNonStreamingRejectsCompletedResponseWithoutUsage` | 未能分类，不豁免 |
| `TestForwardGrokResponsesCompactSynthesizesAndReturnsCompactionItem` | 未能分类，不豁免 |
| `TestForwardGrokChatViaResponsesDropsRedundantViewImage` | 未能分类，不豁免 |
| `TestForwardGrokMessagesDropsRedundantViewImage` | 未能分类，不豁免 |
| `TestForwardGrokResponses_PropagatesSearchCountFromJSON` | 未能分类，不豁免 |
| `TestForwardGrokResponses_PropagatesSearchCountFromSSE` | 未能分类，不豁免 |
| `TestGetSchedulableAccount_AppliesGrokFreeSoftGate` | 未能分类，不豁免 |
| `TestOpenAIGetSchedulableAccount_AppliesGrokFreeSoftGate` | 未能分类，不豁免 |
| `TestParseGrokMediaRequestBuildsMultipartModerationBody` | 未能分类，不豁免 |
| `TestForwardGrokMediaImagesEditMultipartConvertsToJSON` | 未能分类，不豁免 |
| `TestBindGrokMediaVideoRequestAccountUsesOwnerScopedStickyHash` | 未能分类，不豁免 |
| `TestForwardAsChatCompletionsForGrokUsesXAIChatCompletionsAndSnapshots` | 未能分类，不豁免 |
| `TestForwardGrokResponsesStreamingUsesXAIResponsesAndSnapshots` | 未能分类，不豁免 |
| `TestForwardAsChatCompletionsForGrokStreamingUsesRawXAIChatCompletions` | 未能分类，不豁免 |
| `TestForwardAsAnthropicForGrokUsesXAIResponses` | 未能分类，不豁免 |
| `TestForwardAsAnthropic_StreamingBareErrorAfterOutputIsVisible` | 未能分类，不豁免 |
| `TestForwardAsAnthropic_StreamingBareErrorBeforeOutputFailsOver` | 未能分类，不豁免 |
| `TestForwardAsAnthropic_StreamingGenericBareErrorBeforeOutputIsNotHiddenByFailover` | 未能分类，不豁免 |
| `TestResponsesStreamAccessStateFailoverPrecedesPassthroughRule` | 未能分类，不豁免 |
| `TestResponsesStreamCyberPolicyPrecedesPassthroughRule` | 未能分类，不豁免 |
| `TestForwardResponses_ForceChatCompletionsOmitsNoneReasoningEffort` | 未能分类，不豁免 |
| `TestOpenAIGatewayServiceForward_NormalizesResponsesLiteToolsForOAuth` | 未能分类，不豁免 |
| `TestOpenAIGatewayServiceForward_PinsParallelToolCallsForToollessResponsesLite` | 未能分类，不豁免 |
| `TestOpenAIGatewayServiceForward_DisablesParallelToolCallsForResponsesLiteAPIKey` | 未能分类，不豁免 |
| `TestHandle529_AnthropicSkipsPersistentAccountOverload` | 未能分类，不豁免 |
| `TestExecuteSubscriptionFulfillmentAppliesAffiliateRebate` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_OAuth401InvalidatorError` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_OAuth401DoesNotOverwriteCredentials` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedModelUsesModelRateLimit` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedModelIgnoresAPIKeyAccount` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelSkipsCooldown` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedTextModelStillCoolsDown` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelKeepsCooldownOnImagesEndpoint` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelSkipsCooldownOnIntentOnly` | 未能分类，不豁免 |
| `TestRateLimitService_HandleUpstreamError_CodexPlanGatedImageModelSkipsCooldownViaModelMapping` | 未能分类，不豁免 |
| `TestOpenAIGatewayServiceForwardImages_CapabilityLossCoolsImageScope` | 未能分类，不豁免 |
| `TestCompositeTokenCacheInvalidator_Kiro` | 未能分类，不豁免 |
| `TestTokenRefreshService_RefreshWithRetry_AntigravityClearsForceRefreshOnSuccess` | 未能分类，不豁免 |
| `TestTokenRefreshService_RefreshWithRetry_AntigravityNonRetryableError` | 未能分类，不豁免 |
| `TestPathA_NonRetryableError` | 未能分类，不豁免 |
| `TestPathA_DBUpdateFailed` | 未能分类，不豁免 |
| `TestGrokQuotaFetcherBuildUsageInfoFromSnapshot` | 未能分类，不豁免 |
| `TestExtractCCReasoningEffortFromBody` | 未能分类，不豁免 |
| `TestGroupMediaPricingLooksIncomplete_VideoModelPricesComplete` | 未能分类，不豁免 |
| `TestPatchGrokResponsesBodyDropsNestedUnsupportedFields` | 未能分类，不豁免 |
| `TestOAuthService_RefreshAccountToken_WithProxy` | 未能分类，不豁免 |
## 干净代码检查点复验：4dc2a8a70

- 完整 SHA：`4dc2a8a70ff62c39343c1d48f879d54fae298946`；开始与结束时专用 worktree 均干净，主工作区仍为 `9d9508779` 且干净。
- 命令：`GOCACHE=/tmp/sub2api-cn-go-validation.TG4x3Q GOMAXPROCS=4 go -C backend test -json -tags unit ./internal/service ./internal/handler/admin ./internal/handler/dto ./internal/server ./internal/repository -timeout 6m -count=1`。
- admin、DTO、server、repository 四包全部 PASS；Service 执行 8,042 个顶层测试，7,982 PASS / 60 FAIL，耗时 220.195 秒。未跳过任何测试，未发生阻断测试进程的 panic。
- 逐项原始证据：`/tmp/sub2api-cn-checkpoint-4dc2a8a70.jsonl`。前述第 9 轮 66 失败是较早工作树快照，不是本提交最终计数；本节以 60 失败为准。
- 本代码检查点定向 race `/tmp/sub2api-cn-race-round5.log` PASS，构建 `/tmp/sub2api-cn-build-round4.log` PASS；frontend 与 `a13a19410` 的代码差异为零。
- 结论仍 **INCONCLUSIVE / 禁止发布**，目标未完成。下一批应先处理余下 429 作用域/重试合同、裸 SSE 错误、Grok 兼容与搜索用量调用点，再复验 Kiro/token cache 等；模型级限额还需核实上游派生名字与真实调度模型键的一致性。不得在没有逐项证据时把这 60 项统称历史失败。
## 第四轮：清除 Service 回归并验证真实 SQL（2026-09-05）

本轮从 `dd93c07d8` 继续；没有推送、发布或修改生产。上一目标轮是实质进展，本轮也有代码、测试和 PostgreSQL 证据。

### 恢复与修正

1. OpenAI 429：恢复 Spark 独立模型限额调用；普通 OAuth 请求保留本地有界重试窗口，不再被通用持久冷却覆盖；保留全局风暴遥测但不提前耗尽本地最多三个账号的尝试预算。HTTP 与 SSE/WS quota header 的不同作用域保持不变。
2. OpenAI 流错误：Messages 转换器原本将裸 `error` 视为终态却不返回错误，现正确发出错误。已经输出内容后，即使错误本可重试，也不再返回可重放信号；新增真实入口测试同时覆盖 `error` / `response.failed`，没有伪造 `message_stop`。账号停用及 cyber policy 不能被普通展示透传规则掩盖。
3. Codex 能力：区分 OAuth 套餐拒绝与 API-key 请求；错误端点上直接使用图像模型不污染实际 images 能力，专用 images 端点的真实拒绝仍冷却；恢复 self-built 图像能力丢失处理。
4. Responses-Lite：接回显式 header、工具 namespace/additional_tools、reasoning context 和 parallel_tool_calls 规则，HTTP managed/passthrough 与 WebSocket normalization 对齐。未更改本地模型目录的默认 Lite 关闭策略，也没有把 Lite header 加进全局 images/raw-Chat 白名单。setup-token 兼容转发补齐 ID 清理及 reasoning.mode 转换。
5. Grok：消除 model-not-found 的重复写入；模型级免费额度的内部 `-build-free` 桶名与调度模型键对齐；未命名模型的泛化提示不误判为模型级；短 reset 不再膨胀成两小时，保留未观察到绝对 reset 时的短探测。恢复 JSON/SSE 原生搜索调用数及跨事件去重、旧粘性调度的免费额度门限（异步查询限时，不阻塞首个请求）。
6. Grok 内容保真：内联图片下冗余 view_image 处理扩展到 Responses 转换；unsupported 字段清理只作用于协议对象/Schema 关键字，不再递归删除 input/tool output、Schema 属性名、default/enum 用户数据。增加大整数及同名字段保真测试。Chat→Responses 测试显式开启分组路由，不改变默认 raw-Chat 政策。
7. 401 并发安全：旧逻辑将请求开始时的 credentials 整列回写，可能回滚另一 worker 刚旋转的 refresh token。新增生产仓库条件更新，只在完整凭据、代理、平台/类型仍相符时修改 expires_at，并在同一 SQL 写 scheduler outbox；保留当前强制刷新意图，禁止覆盖新 token 或绕过新的受管账号状态。真实 PostgreSQL 验证旧快照/新代理/受管账号均不会被误写，成功更新不改请求对象。
8. 刷新与限流仓库：集成测试暴露 `ListOAuthRefreshCandidatePage`、`SetRateLimitedIfLater`、`ClearRateLimitIfObserved` 被漏合并。特别是生产刷新服务缺失 pager 时会 fail-closed，不能只看 mock 单测！现接回注册平台驱动的有界游标扫描、NULL 安全冷却过滤、限流单调延长与按观测代次清除。Grok 硬额度入口使用单调方法，不再随后用旧对象覆盖仓库刚发布的新快照。
9. 订阅窗口：`periodicStart` 参数已存在但 SQL 完全未使用，周/月被错误写成日窗口起点。现分开写入，PostgreSQL 测试验证不同起点及模型额度维度重置。
10. 合同修正而非改业务：Kiro 缓存继续按账号隔离；OAuth header 测试不混入 ksk CLI 凭据；Kiro 5xx 每默认端点最多三次（总九次）的现有行为不改；保留忽略而不删除共享 legacy 429 记录的策略；订阅返利保留现有 GMV 5% 初始档位、不是全局 15% fallback；指纹 integration fixture 补 UAForm，snapshot fixture 使用真实 write token。异步 Grok 模型发现的测试桩增加同步等待和 map 锁，未让生产探测变成同步等待。

### 已获得的证据

- `/tmp/sub2api-cn-round4-final-unit.jsonl`：Service 8,045 个顶层测试全部通过（218.469 秒）；该次 Repository 因新测试桩重名未编译，随后改名并完整通过，不能隐去这个过程。
- `/tmp/sub2api-cn-round4-final-packages.log`：Repository、admin、DTO、server 四个 unit 全包通过；`/tmp/sub2api-cn-round4-ent-schema.log`：Ent schema 全包通过。
- `/tmp/sub2api-cn-round4-race.log`：Service、Repository 定向 race 通过；admin 测试桩发现并发读写，修正后 `/tmp/sub2api-cn-round4-admin-race.log` 与 `/tmp/sub2api-cn-round4-safety-race.log` 全部通过。后者还覆盖新增“已输出后不可重放”入口测试。
- Docker 上下文已核实为本机 `desktop-linux` / `unix:///Users/sven.sun/.docker/run/docker.sock`。专用临时 PostgreSQL/Redis 运行完整 migrations，未使用生产连接。
- `/tmp/sub2api-cn-round4-postgres-safety-final.jsonl`：真实 PG 下的 401 CAS/outbox/受管状态、分页、单调限流与观测代次恢复、分组 rollup/tail 今日/昨日统计、UA 指纹、写 token 快照、订阅独立窗口起点全部通过。
- 上述 PG 检查使用全部 production repository Go 文件 + 选定 integration harness/fixtures/test 文件执行，**只是定向真实集成证据**，不冒充整个 Repository integration 包通过。
- server 构建通过；本轮 frontend 与 `a13a19410` 差异为零。

### 仍未完成的门禁

**Verdict: INCONCLUSIVE / 禁止发布，目标保持 active。** 完整 Repository integration 包仍存在并发缓存活跃索引/过期槽位清扫实现缺失：`accountActiveIndexKey`、`userActiveIndexKey`、`legacyWaitSweepMarkerKey`、`redisUnixSeconds`、`CleanupExpiredAccountSlotKeys` 及服务接口/定时清扫调用链。必须下一轮整体对照官方和本地并发逻辑修复，不能只补常量或空函数让测试编译。

原始全量编译失败：`/tmp/sub2api-cn-round4-integration-compile.log`；签名与仓库接口修复后剩余项应重新编译核实。最终干净代码提交还需全包复验。四项网关不变量已有本轮分支的直接定向证据，整个融合候选仍未获得完整集成 PASS；不把“旧失败”当豁免。
## 第五轮收尾（2026-09-05）

本节取代前面各检查点的“未完成”清单；最终干净提交验证结果另附在本节末。

- 并发缓存已恢复完整活跃索引：占槽/入队标记、释放校正、Redis 服务器时间、分批过期清扫和一次性旧等待键迁移。生产 Redis 实现通过服务已有的可选 cache-wide 接口进入后台任务，不再查询全部可调度账号。保留本地 count_tokens 独立槽位，并增加独立索引；WebSocket ingress/live 租约及原 Lua 参数兼容不被覆盖。
- 完整 Repository integration 现已能编译并全部通过。补回复制分组的事务内严格 outbox：原来调用普通 Create 吞掉事件写入错误，后续 SQL 在 aborted transaction 上运行并遮蔽根因。现保留全部分组字段，记录/绑定/事件同事务，强制 outbox 失败用例证实完整回滚。
- 其余集成失败主要为已升级合同的旧 fixture：API-key 测试传入 Ent 事务上下文；指纹 UAForm、snapshot write token、subscription schema_version 和实际 WS transport 独立语义对齐；过期窗口测试以当前日为锚点，不再被正常的自动窗口推进干扰。没有降级生产事务/缓存守卫。
- 扩大到整个 backend unit 后发现环境变量可达性遗漏：80 个 scalar 配置键不在 Viper AllKeys 中，包含 image_storage。新增按 Config/mapstructure 注册缺失的 BindEnv；保留已有别名、缺省 IsSet 语义和复杂对象的 config-file-only 约定，不改当前业务默认值。
- handler 收尾：OpenAI 停用状态返回固定安全 502，容量耗尽保留 503/server_error 的语义；已开始的 Responses 流补发正确 response.failed，并避免重复终态；不同协议的 model_not_found/not_found_error 合同不混用。
- 取消不再拖住 HTTP 请求：Grok 旋转刷新已开始时，有限后台临界区仍保存唯一后继 token；请求可立即响应取消，缓冲完成通道不会阻塞后台提交。新增测试证实后继 refresh token 已持久化、原请求对象未改、不会继续选择健康账号。
- 竞态检查发现并修复 `wrapReleaseOnDone`：已取消 context 的 AfterFunc 可在 stop 回调赋值前执行；回调现在仅做 once 释放，只有返回后的闭包读取 stop，1000 次预取消回归及真实 failover 入口 -race 通过。

### 验证范围与性能边界

- 整个 backend：`go test -json -tags unit ./... -timeout 8m -count=1` 已全通过（63 个有测试结果的包、11,958 个顶层 PASS；新增最后一个释放安全用例后以最终复验数字为准），证据 `/tmp/sub2api-cn-round5-backend-all2.jsonl`。
- 完整 repository integration 全通过：`/tmp/sub2api-cn-round5-repository-integration2.jsonl`；相同整包 `-race` 全通过：`/tmp/sub2api-cn-round5-repository-race.jsonl`（含 1,294 个层级 PASS 事件，不把父/子事件相加冒充独立用例数）。
- gateway/config/concurrency/取消等定向 race：Service、Config 通过；发现的 handler 释放竞态修复后 `/tmp/sub2api-cn-round5-handler-race-final.log` 全通过。Grok 后台提交取消语义独立 race 见 `/tmp/sub2api-cn-round5-grok-cancel-race.log`。
- 前端完整 305 文件 / 1,989 项通过（`/tmp/sub2api-cn-round5-frontend.log`）；typecheck、Vite 生产构建通过。`go build -tags embed ./cmd/server` 通过，日志 `/tmp/sub2api-cn-round5-embed-build.log`。构建只有已有 chunk 大小、Browserslist 数据日期等警告。
- 本机独立 Redis 基准，5×100 次：pre-index 占槽中位约 0.369 ms，indexed 约 0.637 ms，额外约 0.268 ms/次，占槽多一次辅助索引写入。基线脚本来自 `bdfc87822`，证据 `/tmp/sub2api-cn-round5-index-benchmark.log`，源码 `concurrency_index_benchmark_test.go`。这是组件耗时，不是生产 TTFT；没有声称完成线上性能验收。发布时仍按发布流程验证实际 Redis 拓扑及可比 warm 流量。
- 临时 Redis 基准容器已清理；测试容器仅用本机 Docker，未连接生产。临时 frontend/node_modules 链接已移除，主工作区依赖目录未动。

### 目标验收对应关系

| 要求 | 当前证据 |
| --- | --- |
| GLM/Zhipu、Kimi、DeepSeek 可创建分组 | 共享平台目录、GroupsView 渲染/提交测试、真实 GroupHandler JSON 往返、Ent schema 全包 |
| 账号可绑定对应分组及已支持的组合路由 | GroupSelector/composite route/渠道往返测试、两套调度器所有权校验及 snapshot 生命周期回归 |
| 不只恢复 UI | CN 三平台×三协议×流/非流 18 入口矩阵、别名 max 六入口、定价/平台额度/阈值落库与保存往返 |
| 检查其他漏更新 | 本台账逐批列出的调用链检查、完整 backend unit、完整 PG/Redis repository integration 与竞态测试 |
| 保护现有本地行为 | Kiro/Droid、count_tokens 槽位、WS 租约、模型映射/Fast、5m/1h、命名/前端功能合同均保留；测试合同差异逐项说明 |
| 可审查交付 | 独立 `codex/fix-cn-group-platform-gaps-20260905` 分支上的分批提交；未推送、未发布 |
## 最终干净代码验收：ef1ef98be（2026-09-05）

- 代码 SHA：`ef1ef98be5d7204e75232a8334c81f72ef8ad2c6`，审查范围 `04ce360b0..ef1ef98be`。所有本目标代码已提交，开始/结束核查均无源文件脏改动；Ent 生成器的临时 `.entc` 目录已自行清理。
- **完整 backend unit PASS**：63 个测试包、11,959 个顶层测试、0 失败（Service 8,047）；命令 `go -C backend test -json -tags unit ./... -timeout 8m -count=1`，原始证据 `/tmp/sub2api-cn-final-ef1ef98be-unit.jsonl`。
- **完整 Repository integration + race PASS**：真实本机 PostgreSQL/Redis、完整 migrations、整个 Repository 包，命令 `go -C backend test -json -race -tags integration ./internal/repository -timeout 12m -count=1`；证据 `/tmp/sub2api-cn-final-ef1ef98be-integration-race.jsonl`。此前定向文件集不再代替这个最终全包结果。
- **前端 PASS**：305 文件 / 1,989 测试；typecheck、生产 Vite build 均通过；证据 `/tmp/sub2api-cn-final-ef1ef98be-frontend.log`、`-typecheck.log`、`-vite.log`。
- **生产形式编译 PASS**：前端构建后 `go -C backend build -tags embed ./cmd/server` 成功，证据 `/tmp/sub2api-cn-final-ef1ef98be-embed.log`。
- **网关定向 race PASS**：取消/后继 token 提交、已输出禁止重放、预取消槽位恰好释放一次、错误终态/安全文案、CN 协议及缓存路径均有直接入口证据。最终释放竞态复验 `/tmp/sub2api-cn-round5-handler-race-final.log`；PG/Redis 的整个包也已以 -race 复验。
- 本地首写/有界等待/缓存测试及 Redis 基准支持代码级延迟审查；新增辅助索引写的本机增量约 0.268 ms/次占槽已明确记录，未把它当作线上 TTFT。实际生产 warm-canary 属于后续发布验收，此目标没有执行或声称执行部署。

**代码交付判定：PASS。** 请求范围内没有未处理的 P0/P1：流终态/不重放、缓存连续与重复创建、首写与有界等待均有对应代码和回归证据，既有业务行为的差异处置已留档。前文所有“剩余失败/禁止发布候选”是当时检查点，已被此最终代码验收取代；不代表生产已经更新，也不替代后续 production-release 的合并/回滚点/上线 canary 流程。

目标要求的建组、账号绑定、相关平台完整链路补漏、回归测试和可审查提交已完成。分支：`codex/fix-cn-group-platform-gaps-20260905`。主工作区仍为 `9d9508779e688ed0584a3a17c8c03592709c4ee1` 且干净；未推送、未修改生产数据、未部署。
