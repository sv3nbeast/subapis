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
