// Generated from origin/main at 5a6143097. Local messages override this supplement.
export default {
  "batchImageGuide": {
    "title": "图片批量生成",
    "description": "一次提交多条提示词，任务完成后可统一下载图片结果"
  },
  "home": {
    "heroSubtitle": "一个密钥，畅用多个 AI 模型",
    "heroDescription": "无需管理多个订阅账号，一站式接入 Claude、GPT、Gemini 等主流 AI 服务",
    "tags": {
      "subscriptionToApi": "订阅转 API",
      "stickySession": "会话保持"
    },
    "painPoints": {
      "title": "你是否也遇到这些问题？",
      "items": {
        "expensive": {
          "title": "订阅费用高",
          "desc": "每个 AI 服务都要单独订阅，每月支出越来越多"
        },
        "complex": {
          "title": "多账号难管理",
          "desc": "不同平台的账号、密钥分散各处，管理起来很麻烦"
        },
        "unstable": {
          "title": "服务不稳定",
          "desc": "单一账号容易触发限制，影响正常使用"
        },
        "noControl": {
          "title": "用量无法控制",
          "desc": "不知道钱花在哪了，也无法限制团队成员的使用"
        }
      }
    },
    "solutions": {
      "title": "我们帮你解决",
      "subtitle": "简单三步，开始省心使用 AI"
    },
    "providers": {
      "soon": "即将推出"
    }
  },
  "setup": {
    "redis": {
      "username": "用户名（可选）",
      "usernamePlaceholder": "默认用户留空"
    }
  },
  "common": {
    "toggleMenu": "切换菜单",
    "userMenu": "用户菜单",
    "pageNotFound": "页面不存在"
  },
  "nav": {
    "batchImage": "批量生图",
    "modelPlaza": "模型广场",
    "securityAudit": "安全审计",
    "contentModeration": "内容审核",
    "promptAudit": "提示词审计",
    "auditLogs": "操作日志"
  },
  "auth": {
    "passkeySignIn": "使用 Passkey 登录",
    "passkeySigningIn": "正在等待 Passkey...",
    "passkeyCancelled": "已取消 Passkey 登录。",
    "passkeyFailed": "Passkey 登录失败，请重试。",
    "dingtalk": {
      "backToLogin": "返回登录",
      "invalidPendingToken": "注册凭证已失效，请重新使用钉钉登录。",
      "error": {
        "title": "钉钉登录失败",
        "csrf": "登录会话已过期，请重新扫码登录",
        "corp_rejected": "您的钉钉账号不属于本企业，请联系管理员",
        "dingtalk_not_enabled": "钉钉登录暂未启用",
        "upstream_error": "钉钉服务暂时不可用，请稍后重试",
        "missing_browser_session": "浏览器会话丢失，请重新登录",
        "missing_params": "请求参数不完整",
        "invalid_state": "登录状态异常",
        "provider_error": "钉钉授权失败",
        "session_error": "会话创建失败，请重试",
        "retry": "重新登录"
      }
    },
    "oauthFlow": {
      "backToOptions": "返回选项"
    }
  },
  "stepUp": {
    "title": "需要二次验证",
    "hint": "请输入身份验证器应用中的 6 位验证码以继续此敏感操作。",
    "verifyFailed": "验证失败，请重试",
    "notEnabled": "此操作需要开启二次验证，请先在个人资料中启用 TOTP。",
    "adminApiKeyForbidden": "管理 API Key 无法执行此操作，请使用已通过二次验证的管理员会话。"
  },
  "dashboard": {
    "platformBreakdownEmpty": "暂无平台用量",
    "platformQuota": {
      "noLimit": "不限制"
    }
  },
  "keys": {
    "id": "ID",
    "useKeyModal": {
      "claudeSettingsHint": "用户级持久配置。此文件包含 API 密钥，请勿提交到项目仓库。",
      "openai": {
        "authModeTitle": "Codex 认证模式",
        "authModeDescription": "兼容模式保留旧版 Codex 配置；API Key Mode 用于授权客户端图片执行器。",
        "authModeLegacy": "兼容模式",
        "authModeApiKey": "API Key Mode",
        "authModeApiKeyRestartNotice": "保存此配置后，必须完全退出并重启 Codex Desktop 或 CLI，然后新建 task，让客户端重新构建工具注册表。"
      },
      "cliTabs": {
        "grokCli": "Grok CLI"
      },
      "grok": {
        "description": "配置 Grok Build、Claude Code、Codex 或 OpenCode，让请求通过当前 Sub2API Grok 分组发送。",
        "claudeDescription": "配置 Claude Code，让 Messages API 请求通过当前 Sub2API Grok 分组发送。",
        "codexDescription": "配置 Codex，让 Responses API 请求通过当前 Sub2API Grok 分组发送。",
        "configTomlHint": "如已有 config.toml，请先备份再合并此模型配置。保存后运行 grok inspect 验证生效配置。",
        "codexConfigTomlHint": "如已有 config.toml，请先备份再合并此服务商配置。",
        "note": "保存为 ~/.grok/config.toml，然后运行 grok inspect，并在 /model 中选择 grok。",
        "noteWindows": "保存为 %USERPROFILE%\\.grok\\config.toml，然后运行 grok inspect，并在 /model 中选择 grok。",
        "claudeNote": "二选一即可：终端命令仅在当前会话生效；保存 settings.json 可作为用户级持久配置。",
        "codexNote": "将 config.toml 保存到 ~/.codex，并在启动 Codex 前设置 SUB2API_API_KEY。",
        "codexNoteWindows": "将 config.toml 保存到 %USERPROFILE%\\.codex，并在 PowerShell 中设置 SUB2API_API_KEY 后启动 Codex。"
      }
    }
  },
  "usage": {
    "live": "Live",
    "imageInputTokens": "图片输入 Token",
    "imageInputTokenPrice": "图片输入单价",
    "imageInputCost": "图片输入费用"
  },
  "availableChannels": {
    "pricing": {
      "billingModeVideo": "按视频",
      "imageInputPrice": "图片输入"
    }
  },
  "modelPlaza": {
    "title": "模型广场",
    "description": "按分组浏览可用模型与价格",
    "loading": "加载中...",
    "empty": "暂无可展示的分组",
    "loadFailed": "加载模型广场失败",
    "noSearchResult": "没有匹配的模型",
    "anonymousHint": "登录后可查看你的专属分组与专属倍率",
    "filters": {
      "platformLabel": "平台",
      "groupLabel": "分组",
      "rateLabel": "倍率",
      "modelLabel": "模型",
      "searchPlaceholder": "搜索模型名称",
      "all": "全部"
    },
    "badges": {
      "exclusive": "专属分组",
      "subscription": "订阅"
    },
    "detail": {
      "noModels": "该分组暂未配置模型",
      "noPricing": "未配置定价",
      "peakNote": "高峰时段 {window} 计费倍率 ×{multiplier}"
    },
    "table": {
      "model": "模型",
      "input": "输入",
      "output": "输出",
      "cache": "缓存",
      "cacheWrite": "写入",
      "cacheRead": "读取",
      "paidPrice": "实付价格(折后)",
      "officialPrice": "官方价格",
      "rate": "折扣倍率",
      "unitPerMillion": "$ / 1M token",
      "perUnitRequest": "/ 次",
      "perUnitImage": "/ 张",
      "perRequest": "按次计费",
      "perImage": "按图片计费"
    },
    "nav": {
      "login": "登录",
      "backToDashboard": "回到后台"
    }
  },
  "profile": {
    "overviewTitle": "账户总览",
    "overviewDescription": "快速查看账号状态、资料来源与常用设置。",
    "securityTitle": "安全设置",
    "securityDescription": "密码、双因素认证和通知提醒集中放在右侧。",
    "email": "邮箱",
    "status": "状态",
    "role": "角色",
    "passkey": {
      "title": "Passkey",
      "description": "使用面容 ID、触控 ID、Windows Hello 或安全密钥免密码登录。",
      "add": "添加 Passkey",
      "continue": "创建 Passkey",
      "name": "Passkey 名称",
      "namePlaceholder": "例如：MacBook 触控 ID",
      "passwordPlaceholder": "输入当前登录密码以确认",
      "empty": "尚未添加任何 Passkey。",
      "synced": "已同步",
      "createdAt": "创建于 {date}",
      "lastUsed": "上次使用 {date}",
      "featureDisabled": "管理员尚未配置 Passkey 功能。",
      "unsupported": "当前浏览器或设备不支持 Passkey。",
      "loadFailed": "加载 Passkey 失败。",
      "added": "Passkey 已添加。",
      "addFailed": "添加 Passkey 失败。",
      "renamePrompt": "请输入新的 Passkey 名称",
      "renamed": "Passkey 已重命名。",
      "renameFailed": "重命名 Passkey 失败。",
      "deleteTitle": "删除 Passkey",
      "deleteConfirm": "删除“{name}”？删除后将无法再使用它登录。",
      "deleted": "Passkey 已删除。",
      "deleteFailed": "删除 Passkey 失败。"
    },
    "balanceNotify": {
      "primaryEmail": "主邮箱"
    }
  },
  "admin": {
    "backup": {
      "imageStorage": {
        "title": "异步生图对象存储",
        "description": "开启后，异步生图接口可用，生成结果转存到对象存储，只把短链接写入 Redis。与备份共用同一套 S3 客户端，保存后立即生效，无需重启。",
        "enabled": "启用异步生图",
        "reuseBackupS3": "复用上方备份的 S3 配置（只用不同的存储桶/前缀）",
        "bucket": "存储桶",
        "bucketInherited": "留空则沿用备份存储桶",
        "prefix": "Key 前缀",
        "publicBaseUrl": "公开访问域名",
        "publicBaseUrlPlaceholder": "留空则返回预签名临时链接",
        "presignExpiryHours": "预签名链接有效期（小时）",
        "saved": "异步生图对象存储配置已保存"
      }
    },
    "users": {
      "bulkLimits": {
        "action": "批量设置限制（{count}）",
        "title": "批量设置用户限制",
        "selectedCount": "已选择 {count} 个用户",
        "selectionLimit": "一次最多选择 {max} 个用户。",
        "selectUser": "选择 {email}",
        "enableConcurrency": "修改并发数",
        "enableRPMLimit": "修改 RPM 限制",
        "unlimited": "不限制",
        "nonNegativeInteger": "请输入非负整数。",
        "apply": "应用限制",
        "applying": "应用中...",
        "concurrencyValue": "并发数：{value}",
        "rpmValue": "RPM：{value}",
        "rpmUnlimitedValue": "RPM：不限制",
        "confirm": "确定覆盖 {count} 个用户的限制吗？\n{fields}",
        "success": "已更新 {count} 个用户的限制",
        "failed": "批量更新用户限制失败"
      },
      "columns": {
        "id": "ID"
      },
      "platformBreakdownEmpty": "暂无平台明细",
      "platformBreakdownHint": "悬浮查看各平台用量"
    },
    "groups": {
      "columns": {
        "id": "ID"
      },
      "form": {
        "maxReasoningEffort": "推理强度上限",
        "maxReasoningEffortUnlimited": "不限制（跟随请求）",
        "maxReasoningEffortHint": "仅限制客户端主动请求的 OpenAI reasoning effort；超过上限时自动降档，不会为缺省请求主动开启推理。上限优先级高于推理强度映射。",
        "reasoningEffortMappings": "推理强度映射",
        "addReasoningEffortMapping": "添加映射",
        "removeReasoningEffortMapping": "删除映射",
        "reasoningEffortFrom": "请求值",
        "reasoningEffortTo": "转发值",
        "reasoningEffortFromPlaceholder": "请选择 A",
        "reasoningEffortToPlaceholder": "请选择 B",
        "fromRequired": "请选择请求值 A",
        "toRequired": "请选择转发值 B",
        "unsupportedFrom": "请求值不受当前平台支持",
        "unsupportedTo": "转发值不受当前平台支持",
        "duplicateFrom": "请求值 A 不能重复"
      },
      "imagePricing": {
        "allowBatchImageGeneration": "允许当前分组批量生图",
        "batchDiscountMultiplier": "批量生图折扣倍率",
        "batchHoldMultiplier": "批量冻结价格比例",
        "batchSectionHint": "批量生图仅影响批量任务：结算价格会叠加批量折扣倍率，提交时冻结金额按普通生图原价 × 批量冻结价格比例计算。参考图也会产生上游输入 token 消耗，建议批量生图折扣倍率设置大于 0.5。",
        "batchDisabledHint": "请先开启当前分组生图，才能开启批量生图。",
        "batchGeminiOnlyHint": "批量生图当前仅支持 Gemini 分组。"
      },
      "webSearchPricing": {
        "title": "Codex 网页搜索计费",
        "pricePerCall": "搜索单次价格（USD/次）",
        "pricePerCallHint": "留空使用默认价 $0.01/次（官方定价 $10/1000 次）；填 0 表示免费。实际扣费会叠加分组费率倍数。",
        "finalPricePreview": "应用当前倍率后的单次价格：{price}"
      },
      "openaiLive": {
        "title": "OpenAI Live",
        "allow": "允许访问 Live",
        "hint": "启用后，此 OpenAI 分组的 API Key 可以创建并控制 Live 语音会话。默认关闭。运行 Sub2API 的服务端必须是 Apple Silicon Mac，并安装官方 ChatGPT App；客户端平台不受限制。",
        "unsupportedTitle": "当前服务端不支持 Live",
        "unsupportedMessage": "当前 Sub2API 服务端无法生成 Live 所需的设备证明，即使开启也不能使用。是否仍然开启？",
        "enableAnyway": "仍然开启"
      },
      "modelRouting": {
        "claudeMaxSimulation": {
          "title": "Claude Max 用量模拟",
          "tooltip": "启用后，对于没有上游缓存写入用量的 Claude 模型，系统会确定性地将 token 映射为少量输入加 1h 缓存创建，同时保持总 token 不变。",
          "enabled": "已启用（模拟 1h 缓存）",
          "disabled": "已禁用",
          "hint": "仅调整用量计费日志中的 token 类别。不会持久化每个请求的映射状态。"
        }
      }
    },
    "availableChannels": {
      "pricing": {
        "billingModeVideo": "按视频"
      }
    },
    "channels": {
      "form": {
        "imageInputPrice": "图片输入",
        "bedrockCCCompat": "Bedrock CC 兼容",
        "bedrockCCCompatHint": "⚠️ 开启后，该渠道下 Bedrock 账号的请求将进行 Claude Code 兼容处理（thinking 类型转换、tool_use ID 清理）",
        "applyPricingToAccountStatsDesc": "启用后，未被自定义规则匹配的请求将使用模型定价文件中的标准价格计算账号统计费用",
        "accountStatsPricingRules": "自定义账号统计定价规则",
        "addRule": "添加规则",
        "noRulesConfigured": "未配置自定义规则，将使用上方的模型定价。",
        "ruleGroups": "分组",
        "ruleAccounts": "账号",
        "searchAccountPlaceholder": "搜索账号...",
        "ruleAccountsHint": "留空表示匹配所有账号",
        "ruleModelPricing": "模型定价",
        "noGroupsInChannel": "上方平台标签页中未选择分组",
        "unnamed": "未命名",
        "syncLatestModels": "同步最新模型",
        "syncingModels": "同步中...",
        "syncModelsSuccess": "已同步 {count} 个新模型",
        "syncModelsAlreadyUpToDate": "模型列表已是最新",
        "syncModelsError": "同步模型失败"
      }
    },
    "channelMonitor": {
      "duplicate": "复制",
      "duplicating": "复制中",
      "duplicateSuccess": "监控已复制为「{name}」，已默认停用，请确认配置后再启用",
      "duplicateFailed": "复制监控失败",
      "duplicateKeyUnavailable": "API Key 无法解密，请先编辑并重新填写 Key 后再复制"
    },
    "accounts": {
      "columns": {
        "upstreamBillingRate": "上游声明倍率",
        "schedulerScore": "调度权值"
      },
      "schedulerScore": {
        "baseShort": "普通",
        "stickyShort": "粘性"
      },
      "ollamaCloud": {
        "title": "Ollama Cloud 用量",
        "sessionSecurityHint": "浏览器会话会加密落库，且只发送到固定的 Ollama 官方设置页。",
        "configured": "已配置",
        "notConfigured": "未配置",
        "notRefreshed": "尚未刷新",
        "encryptionKeyRequired": "请先配置持久 TOTP_ENCRYPTION_KEY，再保存浏览器会话。",
        "sessionLabel": "Ollama 浏览器 Cookie",
        "sessionPlaceholder": "wos-session=...; __Secure-authjs.session-token.0=...",
        "writeOnlyHint": "仅写入。已保存内容不可查看，留空不会覆盖。",
        "deleteSession": "删除会话",
        "deleteConfirm": "确定删除已保存的 Ollama 浏览器会话及其用量快照？",
        "refreshNow": "刷新用量",
        "autoRefresh": "自动刷新用量",
        "autoRefreshHint": "只有账号开关和全局开关同时启用时才会定时刷新。",
        "plan": "套餐",
        "fiveHour": "5 小时",
        "fiveHourShort": "5h",
        "sevenDay": "7 天",
        "sevenDayShort": "7d",
        "balance": "余额",
        "models": "模型",
        "status": "状态",
        "updatedAt": "更新时间",
        "ok": "正常",
        "unauthorized": "会话已过期",
        "failed": "刷新失败",
        "windowWithReset": "已用 {percent}，{reset} 重置",
        "loadFailed": "加载 Ollama Cloud 用量设置失败",
        "sessionSaved": "Ollama 浏览器会话已保存",
        "sessionSaveFailed": "保存 Ollama 浏览器会话失败",
        "sessionDeleted": "Ollama 浏览器会话已删除",
        "sessionDeleteFailed": "删除 Ollama 浏览器会话失败",
        "autoRefreshFailed": "更新自动刷新设置失败",
        "refreshSuccess": "Ollama Cloud 用量已刷新",
        "refreshFailed": "刷新 Ollama Cloud 用量失败",
        "errors": {
          "request_failed": "请求失败",
          "empty_response": "响应为空",
          "response_host_mismatch": "响应主机不符合安全边界",
          "redirect_blocked": "官方设置页发生重定向",
          "unauthorized": "浏览器会话已过期",
          "http_error": "官方设置页返回错误",
          "response_read_failed": "读取响应失败",
          "response_too_large": "设置页超过响应大小限制",
          "invalid_html": "无法识别设置页格式",
          "OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED": "刷新过于频繁，请在 {retry_after_seconds} 秒后重试。"
        }
      },
      "upstreamBilling": {
        "trustWarning": "此倍率由上游站点针对当前 API Key 自行声明。Sub2API 无法验证该值是否与实际扣费一致；上游站点或中间代理可能返回伪造、过期或被篡改的数据。请结合账单、余额变化和实际用量自行核验。",
        "autoProbe": "自动探测上游声明倍率",
        "autoProbeHint": "启用后按全局探测周期查询此账号的上游声明倍率；全局探测关闭时不会执行。",
        "manualProbe": "立即探测上游倍率",
        "stale": "已过期",
        "unsupported": "不支持",
        "failed": "失败",
        "notProbed": "未探测",
        "groupRate": "分组默认：{value}x",
        "userRate": "用户专属倍率：{value}x",
        "peakRate": "高峰：{start}-{end}，{value}x（{timezone}）",
        "noPeakRate": "高峰倍率：未启用",
        "effectiveRate": "当前倍率：{value}x",
        "updatedAt": "更新时间：{value}",
        "nextProbeAt": "下一次探测：{value}",
        "lastDetectedRate": "上次探测倍率：{value}x",
        "lastDetectedAt": "上次探测时间：{value}",
        "elapsedSince": "已过去：{value}",
        "justNow": "不足 1 分钟",
        "minutesAgo": "{count} 分钟",
        "hoursAgo": "{count} 小时",
        "daysAgo": "{count} 天",
        "accountProbeState": "当前账号自动检测：",
        "globalProbeState": "全局探测开关：",
        "enabled": "打开",
        "disabled": "关闭",
        "probeFailed": "探测上游倍率失败",
        "noEligibleAccounts": "请选择 OpenAI API Key 账号",
        "batchLimit": "每次最多探测 20 个账号",
        "batchCompleted": "已完成 {count} 个账号的倍率探测",
        "batchPartial": "倍率探测部分完成：成功 {success} 个，失败 {failed} 个"
      },
      "usageWindow": {
        "grokFreeQuota24hHint": "按 sub2api 近 24 小时本地 Token 用量估算（上限 {limit}）",
        "grokWeeklyUsage": "周额度已用 {percent}%"
      },
      "bulkActions": {
        "probeUpstreamBilling": "探测上游倍率"
      },
      "duplicateAccount": "复制账号",
      "duplicateSuccess": "账号已复制为「{name}」，已暂停调度，请确认凭据后再启用",
      "duplicateFailed": "复制账号失败",
      "openai": {
        "longContextBilling": "API 长上下文计费",
        "longContextBillingDesc": "默认关闭。仅当该账号的上游会按模型阈值收取 OpenAI API 长上下文费率时开启。",
        "planType": "订阅档位（手动覆盖）",
        "planTypeDesc": "手动纠正本账号的 ChatGPT 订阅档位（Plus / Pro / Free）。注意：令牌临期刷新或命中 429 限流时，会用真实档位自动覆盖此处设置。",
        "planTypeClear": "清空（自动识别）",
        "codexImageTool": "Codex 图片桥接策略",
        "codexImageToolDesc": "统一控制 Codex /responses 文本请求的 hosted image_generation 桥接和客户端图片工具声明。hosted 工具自动注入仅适用于非 Responses Lite 请求；账号级策略优先于渠道和全局配置，不影响独立图片生成接口。",
        "codexImageToolInherit": "跟随渠道",
        "codexImageToolInheritDesc": "不写入账号覆盖；非 Lite 请求是否注入 hosted 工具由渠道或全局策略决定，客户端显式携带的 hosted 工具和本地 image_gen 声明照常放行。",
        "codexImageToolEnabled": "启用 Hosted 桥接",
        "codexImageToolEnabledDesc": "仅为非 Responses Lite 请求注入 hosted image_generation 工具；客户端显式携带的图片工具仍会放行。",
        "codexImageToolDisabled": "不注入 Hosted 工具",
        "codexImageToolDisabledDesc": "不注入 hosted 工具；客户端显式携带的 hosted 工具和本地 image_gen 声明仍会放行。",
        "codexImageToolBlock": "移除客户端图片工具",
        "codexImageToolBlockDesc": "不通过桥接自动注入 hosted 工具，并移除客户端显式携带的 hosted image_generation 工具、本地 image_gen 声明及相关 tool_choice；image-only 模型路由不受影响。",
        "codexImageToolBadgeInherit": "渠道策略",
        "codexImageToolBadgeEnabled": "Hosted 桥接已开启",
        "codexImageToolBadgeDisabled": "不注入 Hosted 工具",
        "codexImageToolBadgeBlock": "客户端图片工具已移除"
      },
      "headerOverride": {
        "importJson": "JSON 导入",
        "importJsonApply": "解析并填入",
        "importJsonCancel": "取消",
        "importJsonHint": "粘贴扁平 JSON 对象（请求头名 → 值），解析后将整体替换当前列表。",
        "importJsonInvalid": "JSON 格式不正确：需要\"请求头名 → 字符串值\"的扁平对象",
        "copyJson": "复制为 JSON"
      },
      "grokCustomBaseUrl": {
        "title": "自定义上游地址",
        "hint": "开启后账号流量（对话/媒体/探测）改发指定地址；OAuth 授权与令牌刷新不受影响，仍走官方端点。",
        "placeholder": "https://relay.example.com/v1",
        "required": "开启自定义上游地址后必须填写地址",
        "invalid": "上游地址格式不正确（需为 http(s):// 开头的完整地址）",
        "presets": {
          "cli": "Grok Build CLI",
          "official": "官方 API"
        }
      },
      "grokClientToolCache": {
        "title": "客户端工具缓存（可能改变自动工具选择）",
        "hint": "仅对已识别为 Free 的 Grok OAuth 账号生效，默认会为 Codex、Trae 等客户端函数工具请求启用上游提示缓存；如不接受自动工具选择行为，可关闭此开关退出。"
      },
      "oauth": {
        "openai": {
          "agentIdentityAuth": "Agent Identity auth.json",
          "agentIdentityDesc": "导入 Codex Agent Identity auth.json，不保存 OAuth access token 或 refresh token。",
          "agentIdentityInputLabel": "Agent Identity auth.json",
          "agentIdentityPlaceholder": "粘贴一个 Agent Identity auth.json 对象",
          "agentIdentityHint": "文件必须使用 auth_mode=agentIdentity；每次上游请求都会动态签名。",
          "agentIdentityInvalid": "请选择 auth_mode=agentIdentity 的 Codex auth.json。"
        },
        "grok": {
          "ssoCookieAuth": "SSO Cookie 导入",
          "ssoCookieDesc": "每行粘贴一个 Grok Web SSO key，系统会自动走 xAI Device Flow 并转换为 Grok Build OAuth 凭据。",
          "ssoCookieLabel": "Grok Web SSO Key",
          "ssoCookiePlaceholder": "每行一个 SSO key\n支持多个，每行一个",
          "ssoCookieHint": "每行一个 SSO key；多个 key 会 3 路并发导入，耗时约 90 秒 × 批次数，建议使用对应地区代理。",
          "convertingSSO": "转换中...",
          "convertSSOAndCreate": "转换并创建账号",
          "failedToConvertSSO": "Grok SSO 转换失败",
          "errors": {
            "GROK_OAUTH_SESSION_NOT_FOUND": "Grok OAuth 会话不存在或已过期。请重新生成授权链接，并粘贴最新的回调链接。",
            "GROK_OAUTH_INVALID_STATE": "Grok OAuth state 与当前会话不匹配。请粘贴同一次生成的授权链接返回的回调 URL。",
            "GROK_OAUTH_STATE_REQUIRED": "回调链接缺少 OAuth state。请粘贴完整 callback URL，不要只粘贴 code。",
            "GROK_OAUTH_CODE_REQUIRED": "缺少 Grok 授权码。请粘贴完整 callback URL、查询字符串或 code 值。",
            "GROK_OAUTH_NO_REFRESH_TOKEN": "Grok 响应未返回 refresh token。请重新生成授权链接，并再次确认 offline access 授权。",
            "GROK_OAUTH_PROXY_NOT_AVAILABLE": "无法查询 Grok OAuth 代理配置。请检查选择的代理后重试。",
            "GROK_OAUTH_PROXY_NOT_FOUND": "找不到所选代理。请选择可用代理后重试。"
          }
        }
      }
    },
    "announcements": {
      "preview": "预览"
    },
    "ops": {
      "systemLogs": {
        "host": "Host",
        "cleanupFilterRequired": "清理需要至少一个筛选条件（起止时间或其他字段）"
      },
      "errorLog": {
        "typeAccountAuth": "账号认证"
      },
      "errorDetails": {
        "phase": {
          "account_auth": "账号认证"
        }
      }
    }
  },
  "payment": {
    "qr": {
      "alipayOpening": "正在打开支付宝",
      "alipayContinueInApp": "请在支付宝中完成支付",
      "alipayWaitingHint": "支付结果将由服务端确认，本页面会自动更新",
      "alipayFallbackTitle": "打开支付宝未成功",
      "alipayFallbackHint": "可重新打开支付宝，或保存下方二维码后从支付宝相册识别",
      "reopenAlipay": "重新打开支付宝",
      "saveQRCode": "保存二维码",
      "alipaySaveAndScanHint": "保存二维码后，打开支付宝扫一扫，从相册选择二维码"
    },
    "errors": {
      "PAYMENT_PROVIDER_CONFLICT": "该支付方式已有其他启用中的服务商实例，请先停用后再继续。",
      "CANCEL_RATE_LIMITED": "取消订单过于频繁，请稍后再试",
      "NOT_FOUND": "订单不存在",
      "FORBIDDEN": "无权限操作此订单",
      "CONFLICT": "订单状态已变更，请刷新",
      "INVALID_ORDER_TYPE": "仅余额订单可申请退款",
      "INVALID_STATUS": "当前订单状态不允许此操作",
      "BALANCE_NOT_ENOUGH": "退款金额超过余额",
      "REFUND_AMOUNT_EXCEEDED": "退款金额超过充值金额",
      "REFUND_FAILED": "退款失败"
    },
    "weeks": "周",
    "admin": {
      "currency": "币种标注",
      "currencyPlaceholder": "如 USD / NZD / CNY",
      "currencyHint": "仅用于价格展示的 ISO 三字母币种码，留空不展示，不影响实际扣款",
      "validity": "有效期",
      "validityRequired": "有效期必须大于 0",
      "searchUserSubs": "搜索用户订阅...",
      "daily": "日",
      "weekly": "周",
      "monthly": "月",
      "subsStatus": {
        "active": "生效中",
        "expired": "已过期",
        "revoked": "已撤销"
      }
    }
  }
} as const
