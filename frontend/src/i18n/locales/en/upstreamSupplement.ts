// Generated from origin/main at 5a6143097. Local messages override this supplement.
export default {
  "batchImageGuide": {
    "title": "Batch Image Generation",
    "description": "Submit multiple prompts in one job and download the generated images when complete"
  },
  "home": {
    "heroSubtitle": "One Key, All AI Models",
    "heroDescription": "No need to manage multiple subscriptions. Access Claude, GPT, Gemini and more with a single API key",
    "tags": {
      "subscriptionToApi": "Subscription to API",
      "stickySession": "Session Persistence"
    },
    "painPoints": {
      "title": "Sound Familiar?",
      "items": {
        "expensive": {
          "title": "High Subscription Costs",
          "desc": "Paying for multiple AI subscriptions that add up every month"
        },
        "complex": {
          "title": "Account Chaos",
          "desc": "Managing scattered accounts and API keys across different platforms"
        },
        "unstable": {
          "title": "Service Interruptions",
          "desc": "Single accounts hitting rate limits and disrupting your workflow"
        },
        "noControl": {
          "title": "No Usage Control",
          "desc": "Can't track where your money goes or limit team member usage"
        }
      }
    },
    "solutions": {
      "title": "We Solve These Problems",
      "subtitle": "Three simple steps to stress-free AI access"
    },
    "providers": {
      "soon": "Soon"
    }
  },
  "setup": {
    "redis": {
      "username": "Username (optional)",
      "usernamePlaceholder": "Leave empty for default user"
    }
  },
  "common": {
    "toggleMenu": "Toggle menu",
    "userMenu": "User menu",
    "pageNotFound": "Page not found"
  },
  "nav": {
    "batchImage": "Batch Images",
    "modelPlaza": "Model Plaza",
    "securityAudit": "Security Audit",
    "contentModeration": "Content Moderation",
    "promptAudit": "Prompt Audit",
    "auditLogs": "Audit Logs"
  },
  "auth": {
    "passkeySignIn": "Sign in with a passkey",
    "passkeySigningIn": "Waiting for passkey...",
    "passkeyCancelled": "Passkey sign-in was cancelled.",
    "passkeyFailed": "Passkey sign-in failed. Please try again.",
    "dingtalk": {
      "backToLogin": "Back to Login",
      "invalidPendingToken": "The registration token has expired. Please sign in with DingTalk again.",
      "error": {
        "title": "DingTalk Sign-in Failed",
        "csrf": "Login session expired, please scan again",
        "corp_rejected": "Your DingTalk account is not part of this organization. Please contact administrator",
        "dingtalk_not_enabled": "DingTalk login is not enabled",
        "upstream_error": "DingTalk service is temporarily unavailable. Please try again later",
        "missing_browser_session": "Browser session lost. Please login again",
        "missing_params": "Request parameters are incomplete",
        "invalid_state": "Invalid login state",
        "provider_error": "DingTalk authorization failed",
        "session_error": "Failed to create session. Please retry",
        "retry": "Retry Login"
      }
    },
    "oauthFlow": {
      "backToOptions": "Back to options"
    },
    "linuxdoCallbackPageTitle": "LinuxDo Sign-In Callback",
    "dingtalkCallbackPageTitle": "DingTalk Sign-In Callback",
    "oidcCallbackPageTitle": "OIDC Sign-In Callback",
    "oauthCallbackPageTitle": "OAuth Callback",
    "wechatCallbackPageTitle": "WeChat Sign-In Callback",
    "wechatPaymentCallbackPageTitle": "WeChat Payment Callback"
  },
  "stepUp": {
    "title": "Two-Factor Verification Required",
    "hint": "Enter the 6-digit code from your authenticator app to continue this sensitive operation.",
    "verifyFailed": "Verification failed, please try again",
    "notEnabled": "This operation requires two-factor authentication. Please enable TOTP in your profile first.",
    "adminApiKeyForbidden": "Admin API keys cannot perform this operation. Use a two-factor verified admin session."
  },
  "dashboard": {
    "platformBreakdownEmpty": "No platform usage yet",
    "platformQuota": {
      "noLimit": "unlimited"
    }
  },
  "keys": {
    "id": "ID",
    "useKeyModal": {
      "claudeSettingsHint": "User-level persistent configuration. Do not commit this file containing your API key to a project repository.",
      "openai": {
        "authModeTitle": "Codex authentication mode",
        "authModeDescription": "Compatibility mode keeps the existing setup for older Codex clients. API Key Mode authorizes the client-side image executor.",
        "authModeLegacy": "Compatibility mode",
        "authModeApiKey": "API Key Mode",
        "authModeApiKeyRestartNotice": "After saving this configuration, completely quit and restart Codex Desktop or CLI, then create a new task so the client can rebuild its tool registry."
      },
      "cliTabs": {
        "grokCli": "Grok CLI"
      },
      "grok": {
        "description": "Configure Grok Build, Claude Code, Codex, or OpenCode to send requests through your Sub2API Grok group.",
        "claudeDescription": "Configure Claude Code to send Messages API traffic through your Sub2API Grok group.",
        "codexDescription": "Configure Codex to send Responses API traffic through your Sub2API Grok group.",
        "configTomlHint": "Back up an existing config.toml before merging this model entry. Run grok inspect after saving to verify the effective configuration.",
        "codexConfigTomlHint": "Back up an existing config.toml before merging this provider configuration.",
        "note": "Save the file as ~/.grok/config.toml, then run grok inspect and select grok from /model.",
        "noteWindows": "Save the file as %USERPROFILE%\\.grok\\config.toml, then run grok inspect and select grok from /model.",
        "claudeNote": "Choose one method: run the terminal commands for the current session, or save settings.json for user-level persistent configuration.",
        "codexNote": "Save config.toml under ~/.codex and set SUB2API_API_KEY before starting Codex.",
        "codexNoteWindows": "Save config.toml under %USERPROFILE%\\.codex and set SUB2API_API_KEY in PowerShell before starting Codex."
      }
    }
  },
  "usage": {
    "live": "Live",
    "imageInputTokens": "Image Input Tokens",
    "imageInputTokenPrice": "Image Input Price",
    "imageInputCost": "Image Input Cost"
  },
  "availableChannels": {
    "pricing": {
      "billingModeVideo": "Per Video",
      "imageInputPrice": "Image Input"
    }
  },
  "modelPlaza": {
    "title": "Model Plaza",
    "description": "Browse available models and pricing by group",
    "loading": "Loading...",
    "empty": "No groups to display",
    "loadFailed": "Failed to load model plaza",
    "noSearchResult": "No matching models",
    "anonymousHint": "Sign in to see your exclusive groups and personal rates",
    "filters": {
      "platformLabel": "Platform",
      "groupLabel": "Group",
      "rateLabel": "Rate",
      "modelLabel": "Model",
      "searchPlaceholder": "Search models",
      "all": "All"
    },
    "badges": {
      "exclusive": "Exclusive",
      "subscription": "Subscription"
    },
    "detail": {
      "noModels": "No models configured for this group",
      "noPricing": "Pricing not configured",
      "peakNote": "Peak hours {window}: billing rate ×{multiplier}"
    },
    "table": {
      "model": "Model",
      "input": "Input",
      "output": "Output",
      "cache": "Cache",
      "cacheWrite": "Write",
      "cacheRead": "Read",
      "paidPrice": "Your Price (Discounted)",
      "officialPrice": "Official Price",
      "rate": "Rate",
      "unitPerMillion": "$ / 1M tokens",
      "perUnitRequest": "/ request",
      "perUnitImage": "/ image",
      "perRequest": "Per request",
      "perImage": "Per image"
    },
    "nav": {
      "login": "Sign In",
      "backToDashboard": "Back to Console"
    }
  },
  "profile": {
    "overviewTitle": "Account Overview",
    "overviewDescription": "Check account status, profile sources, and common actions at a glance.",
    "securityTitle": "Security Settings",
    "securityDescription": "Password, two-factor authentication, and alerts live in the right rail.",
    "email": "Email",
    "status": "Status",
    "role": "Role",
    "passkey": {
      "title": "Passkeys",
      "description": "Use Face ID, Touch ID, Windows Hello, or a security key to sign in without a password.",
      "add": "Add passkey",
      "continue": "Create passkey",
      "name": "Passkey name",
      "namePlaceholder": "For example, MacBook Touch ID",
      "passwordPlaceholder": "Enter your current password to confirm",
      "empty": "No passkeys are registered yet.",
      "synced": "Synced",
      "createdAt": "Created {date}",
      "lastUsed": "Last used {date}",
      "featureDisabled": "Passkeys have not been configured by the administrator.",
      "unsupported": "This browser or device does not support passkeys.",
      "loadFailed": "Failed to load passkeys.",
      "added": "Passkey added.",
      "addFailed": "Failed to add passkey.",
      "renamePrompt": "Enter a new name for this passkey",
      "renamed": "Passkey renamed.",
      "renameFailed": "Failed to rename passkey.",
      "deleteTitle": "Delete passkey",
      "deleteConfirm": "Delete “{name}”? You will no longer be able to sign in with it.",
      "deleted": "Passkey deleted.",
      "deleteFailed": "Failed to delete passkey."
    },
    "balanceNotify": {
      "primaryEmail": "Primary"
    }
  },
  "admin": {
    "dashboard": {
      "totalApiKeys": "Total API Keys",
      "activeApiKeys": "Active Keys",
      "totalAccounts": "Total Accounts",
      "activeAccounts": "Active Accounts",
      "totalUsers": "Total Users",
      "totalRequests": "Total Requests",
      "todayCost": "Today Cost",
      "totalCost": "Total Cost",
      "averageTime": "Average Time",
      "last7Days": "Last 7 days",
      "noUsageRecords": "No usage records",
      "startUsingApi": "Once you start using the API, your usage history will appear here.",
      "viewAllUsage": "View all",
      "manageUsers": "Manage Users",
      "viewUserAccounts": "View and manage user accounts",
      "manageAccounts": "Manage Accounts",
      "configureAiAccounts": "Configure AI platform accounts",
      "systemSettings": "System Settings",
      "configureSystem": "Configure system settings"
    },
    "backup": {
      "imageStorage": {
        "title": "Async image object storage",
        "description": "Enables the asynchronous image endpoints and offloads generated images to object storage, keeping only short links in Redis. Shares the S3 client with backups and takes effect on save — no restart needed.",
        "enabled": "Enable async image tasks",
        "reuseBackupS3": "Reuse the backup S3 configuration above (different bucket/prefix only)",
        "bucket": "Bucket",
        "bucketInherited": "Leave empty to use the backup bucket",
        "prefix": "Key prefix",
        "publicBaseUrl": "Public base URL",
        "publicBaseUrlPlaceholder": "Leave empty to return presigned links",
        "presignExpiryHours": "Presigned link TTL (hours)",
        "saved": "Async image object storage saved"
      }
    },
    "users": {
      "bulkLimits": {
        "action": "Set limits ({count})",
        "title": "Set user limits",
        "selectedCount": "{count} users selected",
        "selectionLimit": "Select no more than {max} users at a time.",
        "selectUser": "Select {email}",
        "enableConcurrency": "Update concurrency",
        "enableRPMLimit": "Update RPM limit",
        "unlimited": "Unlimited",
        "nonNegativeInteger": "Enter a non-negative whole number.",
        "apply": "Apply limits",
        "applying": "Applying...",
        "concurrencyValue": "Concurrency: {value}",
        "rpmValue": "RPM: {value}",
        "rpmUnlimitedValue": "RPM: Unlimited",
        "confirm": "Overwrite limits for {count} users?\n{fields}",
        "success": "Updated limits for {count} users",
        "failed": "Failed to update user limits"
      },
      "deleteConfirmMessage": "Are you sure you want to delete user '{email}'? This action cannot be undone.",
      "searchPlaceholder": "Search by email, username, notes, or API key...",
      "roleFilter": "Role Filter",
      "statusFilter": "Status Filter",
      "allStatuses": "All Status",
      "form": {
        "emailLabel": "Email",
        "emailPlaceholder": "Enter email",
        "usernameLabel": "Username",
        "usernamePlaceholder": "Enter username (optional)",
        "notesLabel": "Notes",
        "notesPlaceholder": "Enter notes (admin only)",
        "notesHint": "This note is only visible to administrators",
        "passwordLabel": "Password",
        "passwordPlaceholder": "Enter password (leave empty to keep unchanged)",
        "selectRole": "Select role",
        "balanceLabel": "Balance",
        "concurrencyLabel": "Concurrency",
        "statusLabel": "Status",
        "selectStatus": "Select status"
      },
      "columns": {
        "id": "ID"
      },
      "adjustBalance": "Adjust Balance",
      "adjustConcurrency": "Adjust Concurrency",
      "adjustmentAmount": "Adjustment Amount",
      "adjustmentAmountHint": "Positive to add, negative to subtract",
      "currentConcurrency": "Current Concurrency",
      "saving": "Saving...",
      "noUsers": "No users yet",
      "noUsersDescription": "Create your first user to get started.",
      "userCreatedSuccess": "User created successfully",
      "userUpdatedSuccess": "User updated successfully",
      "userDeletedSuccess": "User deleted successfully",
      "balanceAdjustedSuccess": "Balance adjusted successfully",
      "concurrencyAdjustedSuccess": "Concurrency adjusted successfully",
      "failedToSave": "Failed to save user",
      "failedToAdjust": "Adjustment failed",
      "platformBreakdownEmpty": "No platform usage yet",
      "platformBreakdownHint": "Hover for per-platform usage"
    },
    "groups": {
      "exclusiveFilter": "Exclusive",
      "columns": {
        "id": "ID",
        "exclusive": "Exclusive",
        "priority": "Priority",
        "apiKeys": "API Keys"
      },
      "form": {
        "nameLabel": "Group Name",
        "namePlaceholder": "Enter group name",
        "descriptionLabel": "Description",
        "descriptionPlaceholder": "Enter description (optional)",
        "rateMultiplierLabel": "Rate Multiplier",
        "rateMultiplierHint": "1.0 = standard rate, 0.5 = half price, 2.0 = double",
        "maxReasoningEffort": "Max reasoning effort",
        "maxReasoningEffortUnlimited": "Unlimited (follow request)",
        "maxReasoningEffortHint": "Limits explicit OpenAI reasoning effort requests only. Higher values are capped; omitted effort stays omitted. The ceiling takes precedence over reasoning effort mappings.",
        "reasoningEffortMappings": "Reasoning effort mappings",
        "addReasoningEffortMapping": "Add mapping",
        "removeReasoningEffortMapping": "Remove mapping",
        "reasoningEffortFrom": "Request value",
        "reasoningEffortTo": "Forwarded value",
        "reasoningEffortFromPlaceholder": "Select A",
        "reasoningEffortToPlaceholder": "Select B",
        "fromRequired": "Select request value A",
        "toRequired": "Select forwarded value B",
        "unsupportedFrom": "Request value is not supported by this platform",
        "unsupportedTo": "Forwarded value is not supported by this platform",
        "duplicateFrom": "Request value A must be unique",
        "exclusiveLabel": "Exclusive Group",
        "exclusiveHint": "Exclusive group, can be manually assigned to users",
        "platformLabel": "Platform Restriction",
        "platformPlaceholder": "Select platform (leave empty for no restriction)",
        "accountsLabel": "Designated Accounts",
        "accountsPlaceholder": "Select accounts (leave empty for no restriction)",
        "priorityLabel": "Priority",
        "priorityHint": "Lower value means higher priority, used for account scheduling",
        "statusLabel": "Status"
      },
      "exclusiveObj": {
        "yes": "Yes",
        "no": "No"
      },
      "saving": "Saving...",
      "noGroups": "No groups yet",
      "noGroupsDescription": "Create a group to better manage API keys and rates.",
      "groupCreatedSuccess": "Group created successfully",
      "groupUpdatedSuccess": "Group updated successfully",
      "groupDeletedSuccess": "Group deleted successfully",
      "imagePricing": {
        "allowBatchImageGeneration": "Allow batch image generation for this group",
        "batchDiscountMultiplier": "Batch image discount",
        "batchHoldMultiplier": "Batch hold price ratio",
        "batchSectionHint": "Batch image settings only apply to batch jobs: settlement applies the batch discount, and the upfront hold is normal image price × batch hold price ratio. Reference images also create upstream input-token usage, so a batch image discount above 0.5 is recommended.",
        "batchDisabledHint": "Enable image generation for this group before enabling batch image generation.",
        "batchGeminiOnlyHint": "Batch image generation is currently available only for Gemini groups."
      },
      "webSearchPricing": {
        "title": "Codex Web Search Pricing",
        "pricePerCall": "Price per search call (USD)",
        "pricePerCallHint": "Leave empty to use the default $0.01 per call (official pricing: $10 per 1,000 calls); 0 means free. The group rate multiplier is applied on top.",
        "finalPricePreview": "Per-call price after current multiplier: {price}"
      },
      "openaiLive": {
        "title": "OpenAI Live",
        "allow": "Allow Live access",
        "hint": "When enabled, API keys in this OpenAI group can create and control Live voice sessions. Disabled by default. The Sub2API server must run on Apple Silicon macOS with the official ChatGPT app installed; client platforms are unrestricted.",
        "unsupportedTitle": "Current server does not support Live",
        "unsupportedMessage": "This Sub2API server cannot generate the required Live attestation. Live will not work even if enabled. Continue anyway?",
        "enableAnyway": "Enable anyway"
      }
    },
    "availableChannels": {
      "pricing": {
        "billingModeVideo": "Per Video"
      }
    },
    "channels": {
      "form": {
        "imageInputPrice": "Image Input",
        "bedrockCCCompat": "Bedrock CC Compatibility",
        "bedrockCCCompatHint": "⚠️ When enabled, requests to Bedrock accounts in this channel will be transformed for Claude Code compatibility (thinking type conversion, tool_use ID sanitization).",
        "applyPricingToAccountStatsDesc": "When enabled, requests not matched by custom rules will use standard model pricing for account stats calculation",
        "accountStatsPricingRules": "Custom Account Stats Pricing Rules",
        "addRule": "Add Rule",
        "noRulesConfigured": "No custom rules configured. Channel model pricing above will be used.",
        "ruleGroups": "Groups",
        "ruleAccounts": "Accounts",
        "searchAccountPlaceholder": "Search accounts...",
        "ruleAccountsHint": "Leave empty to match all accounts",
        "ruleModelPricing": "Model Pricing",
        "noGroupsInChannel": "No groups selected in platform tabs above",
        "unnamed": "Unnamed",
        "syncLatestModels": "Sync Latest Models",
        "syncingModels": "Syncing...",
        "syncModelsSuccess": "Synced {count} new model(s)",
        "syncModelsAlreadyUpToDate": "Models already up to date",
        "syncModelsError": "Failed to sync models"
      }
    },
    "channelMonitor": {
      "duplicate": "Duplicate",
      "duplicating": "Duplicating",
      "duplicateSuccess": "Monitor duplicated as \"{name}\" and disabled. Review its configuration before enabling it.",
      "duplicateFailed": "Failed to duplicate monitor",
      "duplicateKeyUnavailable": "The API key cannot be decrypted. Re-enter it before duplicating this monitor."
    },
    "accounts": {
      "columns": {
        "upstreamBillingRate": "Upstream Declared Rate",
        "schedulerScore": "Scheduler Score"
      },
      "schedulerScore": {
        "baseShort": "Base",
        "stickyShort": "Sticky"
      },
      "ollamaCloud": {
        "title": "Ollama Cloud usage",
        "sessionSecurityHint": "The browser session is encrypted at rest and sent only to the fixed official settings URL.",
        "configured": "Configured",
        "notConfigured": "Not configured",
        "notRefreshed": "Not refreshed",
        "encryptionKeyRequired": "Set a persistent TOTP_ENCRYPTION_KEY before storing a browser session.",
        "sessionLabel": "Ollama browser Cookie",
        "sessionPlaceholder": "wos-session=...; __Secure-authjs.session-token.0=...",
        "writeOnlyHint": "Write-only. The saved value cannot be viewed and an empty value never replaces it.",
        "deleteSession": "Delete session",
        "deleteConfirm": "Delete the stored Ollama browser session and its usage snapshot?",
        "refreshNow": "Refresh usage",
        "autoRefresh": "Automatic usage refresh",
        "autoRefreshHint": "Runs only when the account switch and the global switch are both enabled.",
        "plan": "Plan",
        "fiveHour": "5 hour",
        "fiveHourShort": "5h",
        "sevenDay": "7 day",
        "sevenDayShort": "7d",
        "balance": "Balance",
        "models": "Models",
        "status": "Status",
        "updatedAt": "Updated",
        "ok": "Current",
        "unauthorized": "Session expired",
        "failed": "Refresh failed",
        "windowWithReset": "{percent} used, resets {reset}",
        "loadFailed": "Failed to load Ollama Cloud usage settings",
        "sessionSaved": "Ollama browser session saved",
        "sessionSaveFailed": "Failed to save Ollama browser session",
        "sessionDeleted": "Ollama browser session deleted",
        "sessionDeleteFailed": "Failed to delete Ollama browser session",
        "autoRefreshFailed": "Failed to update automatic usage refresh",
        "refreshSuccess": "Ollama Cloud usage refreshed",
        "refreshFailed": "Failed to refresh Ollama Cloud usage",
        "errors": {
          "request_failed": "Request failed",
          "empty_response": "Empty response",
          "response_host_mismatch": "Unexpected response host",
          "redirect_blocked": "Official settings redirected the request",
          "unauthorized": "Browser session expired",
          "http_error": "Official settings returned an error",
          "response_read_failed": "Failed to read the response",
          "response_too_large": "Settings page exceeded the response limit",
          "invalid_html": "Settings page format was not recognized",
          "OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED": "Refresh is limited. Try again in {retry_after_seconds} seconds."
        }
      },
      "upstreamBilling": {
        "trustWarning": "This rate is declared by the upstream site for the current API key. Sub2API cannot verify that it matches actual charges. The upstream site or an intermediary may return forged, stale, or modified data. Verify it against bills, balance changes, and actual usage.",
        "autoProbe": "Automatically probe upstream declared rate",
        "autoProbeHint": "Probe this account's upstream declared rate on the global interval when global probing is enabled.",
        "syncRate": "Sync upstream declared rate",
        "syncRateHint": "Update the account rate after each successful probe. Failed or invalid declarations leave it unchanged.",
        "syncRateManagedHint": "The current rate is maintained automatically from the upstream declared rate.",
        "syncedRateTooltip": "This account rate is synchronized from the upstream declared rate",
        "manualProbe": "Probe upstream rate now",
        "stale": "Stale",
        "unsupported": "Unsupported",
        "failed": "Failed",
        "notProbed": "Not probed",
        "groupRate": "Group default: {value}x",
        "userRate": "User rate: {value}x",
        "peakRate": "Peak: {start}-{end}, {value}x ({timezone})",
        "noPeakRate": "Peak rate: disabled",
        "effectiveRate": "Current rate: {value}x",
        "updatedAt": "Updated: {value}",
        "nextProbeAt": "Next probe: {value}",
        "lastDetectedRate": "Last detected rate: {value}x",
        "lastDetectedAt": "Last detected: {value}",
        "elapsedSince": "Elapsed: {value}",
        "justNow": "less than 1 minute",
        "minutesAgo": "{count} minutes",
        "hoursAgo": "{count} hours",
        "daysAgo": "{count} days",
        "accountProbeState": "Automatic detection for this account:",
        "globalProbeState": "Global probe switch:",
        "enabled": "On",
        "disabled": "Off",
        "probeFailed": "Failed to probe upstream rate",
        "noEligibleAccounts": "Select OpenAI API key accounts",
        "batchLimit": "A batch can probe at most 20 accounts",
        "batchCompleted": "Probed {count} account(s)",
        "batchPartial": "Probe partially completed: {success} succeeded, {failed} failed"
      },
      "bulkActions": {
        "probeUpstreamBilling": "Probe Upstream Rate"
      },
      "duplicateAccount": "Duplicate Account",
      "duplicateSuccess": "Account duplicated as \"{name}\" and paused. Review its credentials before enabling it.",
      "duplicateFailed": "Failed to duplicate account",
      "openai": {
        "longContextBilling": "API long-context pricing",
        "longContextBillingDesc": "Disabled by default. Enable only when this account's upstream charges OpenAI API long-context rates above the model threshold.",
        "planType": "Plan tier (manual override)",
        "planTypeDesc": "Manually correct this account's ChatGPT plan tier (Plus / Pro / Free). Note: a token refresh near expiry or a 429 rate-limit response will auto-overwrite this with the real tier.",
        "planTypeClear": "Clear (auto-detect)",
        "codexImageTool": "Codex image bridge policy",
        "codexImageToolDesc": "Controls the hosted image_generation bridge and client-declared image tools on Codex /responses text requests. Hosted auto-injection applies only to non-Responses Lite requests. Account policy takes precedence over channel and global settings; standalone image-generation endpoints are unaffected.",
        "codexImageToolInherit": "Follow channel",
        "codexImageToolInheritDesc": "No account override; hosted injection for non-Lite requests follows the channel or global policy, while client-provided hosted tools and local image_gen declarations pass through.",
        "codexImageToolEnabled": "Enable hosted bridge",
        "codexImageToolEnabledDesc": "Inject the hosted image_generation tool only for non-Responses Lite requests; client-provided image tools still pass through.",
        "codexImageToolDisabled": "No hosted injection",
        "codexImageToolDisabledDesc": "Do not inject the hosted tool; client-provided hosted tools and local image_gen declarations still pass through.",
        "codexImageToolBlock": "Strip client image tools",
        "codexImageToolBlockDesc": "Do not auto-inject through the bridge, and remove client-provided hosted image_generation tools, local image_gen declarations, and matching tool_choice. Image-only model routing remains unaffected.",
        "codexImageToolBadgeInherit": "Channel policy",
        "codexImageToolBadgeEnabled": "Hosted bridge on",
        "codexImageToolBadgeDisabled": "No hosted injection",
        "codexImageToolBadgeBlock": "Client image tools stripped"
      },
      "headerOverride": {
        "importJson": "Import JSON",
        "importJsonApply": "Parse & Fill",
        "importJsonCancel": "Cancel",
        "importJsonHint": "Paste a flat JSON object (header name → value). Parsing replaces the current rows.",
        "importJsonInvalid": "Invalid JSON: expected a flat object of header name → string value",
        "copyJson": "Copy as JSON"
      },
      "grokCustomBaseUrl": {
        "title": "Custom Upstream URL",
        "hint": "When enabled, account traffic (chat/media/probes) is forwarded to the specified address. OAuth authorization and token refresh are unaffected and stay on the official endpoints.",
        "placeholder": "https://relay.example.com/v1",
        "required": "An address is required when Custom Upstream URL is enabled",
        "invalid": "Invalid upstream address (must be a full http(s):// URL)",
        "presets": {
          "cli": "Grok Build CLI",
          "official": "Official API"
        }
      },
      "grokClientToolCache": {
        "title": "Client Tool Cache (May Change Automatic Tool Selection)",
        "hint": "For detected Grok Free OAuth accounts, this is enabled by default for client function tools such as Codex and Trae. Turn it off to opt out if the automatic tool-selection behavior is not acceptable."
      },
      "oauth": {
        "openai": {
          "agentIdentityAuth": "Agent Identity auth.json",
          "agentIdentityDesc": "Import a Codex Agent Identity auth.json. No OAuth access or refresh token is stored.",
          "agentIdentityInputLabel": "Agent Identity auth.json",
          "agentIdentityPlaceholder": "Paste one Agent Identity auth.json object",
          "agentIdentityHint": "The file must use auth_mode=agentIdentity. Upstream requests are signed dynamically.",
          "agentIdentityInvalid": "Use a Codex auth.json with auth_mode=agentIdentity."
        },
        "grok": {
          "ssoCookieAuth": "SSO Cookie Import",
          "ssoCookieDesc": "Paste one Grok Web SSO key per line. The server will complete the xAI Device Flow and convert them into Grok Build OAuth credentials.",
          "ssoCookieLabel": "Grok Web SSO Key",
          "ssoCookiePlaceholder": "One SSO key per line\nSupports multiple, one per line",
          "ssoCookieHint": "One SSO key per line. Multiple keys are imported with 3-way concurrency; expect about 90 seconds per batch. Use a matching-region proxy if needed.",
          "convertingSSO": "Converting...",
          "convertSSOAndCreate": "Convert & Create Account",
          "failedToConvertSSO": "Failed to convert Grok SSO cookie",
          "errors": {
            "GROK_OAUTH_SESSION_NOT_FOUND": "Grok OAuth session was not found or has expired. Generate a new auth URL and paste the newest callback URL.",
            "GROK_OAUTH_INVALID_STATE": "Grok OAuth state does not match this session. Paste the callback URL from the same generated auth link.",
            "GROK_OAUTH_STATE_REQUIRED": "The callback URL is missing the OAuth state. Paste the full callback URL, not only the code.",
            "GROK_OAUTH_CODE_REQUIRED": "The Grok authorization code is missing. Paste the full callback URL, query string, or code value.",
            "GROK_OAUTH_NO_REFRESH_TOKEN": "The Grok response did not include a refresh token. Generate a new auth URL and approve offline access again.",
            "GROK_OAUTH_PROXY_NOT_AVAILABLE": "Grok OAuth proxy lookup is unavailable. Check the selected proxy and retry.",
            "GROK_OAUTH_PROXY_NOT_FOUND": "The selected proxy could not be found. Choose an available proxy and retry."
          }
        }
      },
      "usageWindow": {
        "grokFreeQuota24hHint": "Estimated from local token usage over the rolling 24-hour window ({limit} limit)",
        "grokWeeklyUsage": "Weekly {percent}%"
      }
    },
    "proxies": {
      "deleteConfirmMessage": "Are you sure you want to delete proxy '{name}'?",
      "testProxy": "Test Proxy",
      "columns": {
        "nameLabel": "Name",
        "namePlaceholder": "Enter proxy name",
        "protocolLabel": "Protocol",
        "selectProtocol": "Select protocol",
        "hostLabel": "Host",
        "hostPlaceholder": "Enter host address",
        "portLabel": "Port",
        "portPlaceholder": "Enter port",
        "usernameLabel": "Username (Optional)",
        "usernamePlaceholder": "Enter username",
        "passwordLabel": "Password (Optional)",
        "passwordPlaceholder": "Enter password",
        "priorityLabel": "Priority",
        "statusLabel": "Status"
      },
      "filters": {
        "protocol": "Protocol",
        "allProtocols": "All Protocols",
        "status": "Status",
        "allStatuses": "All Status"
      },
      "saving": "Saving...",
      "testing": "Testing...",
      "noProxies": "No proxies yet",
      "noProxiesDescription": "Add a proxy server to improve API access stability.",
      "proxyCreatedSuccess": "Proxy created successfully",
      "proxyUpdatedSuccess": "Proxy updated successfully",
      "proxyDeletedSuccess": "Proxy deleted successfully",
      "testSuccess": "Proxy test passed",
      "failedToSave": "Failed to save proxy"
    },
    "redeem": {
      "form": {
        "typeLabel": "Type",
        "selectType": "Select type",
        "valueLabel": "Value",
        "valuePlaceholder": "Enter value",
        "balanceHint": "Balance amount (USD)",
        "concurrencyHint": "Concurrency increment",
        "countLabel": "Count",
        "countPlaceholder": "Enter count",
        "countHint": "Number of redeem codes to generate",
        "prefixLabel": "Prefix (Optional)",
        "prefixPlaceholder": "e.g., GIFT",
        "expiresLabel": "Expires At (Optional)"
      },
      "filters": {
        "type": "Type",
        "allTypes": "All Types",
        "status": "Status",
        "allStatuses": "All Status",
        "search": "Search codes"
      },
      "copyCode": "Copy",
      "disableCode": "Disable",
      "enableCode": "Enable",
      "deleteConfirmMessage": "Are you sure you want to delete this redeem code?",
      "noCodes": "No redeem codes yet",
      "noCodesDescription": "Generate redeem codes to distribute balance or concurrency to users.",
      "codesGeneratedSuccess": "Redeem codes generated successfully, {count} total",
      "codeDisabledSuccess": "Redeem code disabled",
      "codeEnabledSuccess": "Redeem code enabled",
      "codeDeletedSuccess": "Redeem code deleted successfully",
      "failedToUpdate": "Failed to update redeem code"
    },
    "announcements": {
      "preview": "Preview"
    },
    "ops": {
      "systemLogs": {
        "host": "Host",
        "cleanupFilterRequired": "Cleanup requires at least one filter condition (start/end time or another field)"
      },
      "errorLog": {
        "typeAccountAuth": "Account Auth"
      },
      "errorDetails": {
        "phase": {
          "account_auth": "Account Auth"
        }
      }
    }
  },
  "payment": {
    "qr": {
      "alipayOpening": "Opening Alipay",
      "alipayContinueInApp": "Complete payment in Alipay",
      "alipayWaitingHint": "The server will confirm the payment and update this page automatically",
      "alipayFallbackTitle": "Alipay did not open",
      "alipayFallbackHint": "Try opening Alipay again, or save the QR code and scan it from your Alipay photo album",
      "reopenAlipay": "Open Alipay Again",
      "saveQRCode": "Save QR Code",
      "alipaySaveAndScanHint": "Save the QR code, open Alipay Scan, then select it from your photo album"
    },
    "errors": {
      "PAYMENT_PROVIDER_CONFLICT": "Another enabled provider instance is already serving this payment method. Disable it before continuing.",
      "CANCEL_RATE_LIMITED": "Too many cancellations. Please try again later.",
      "NOT_FOUND": "Order not found.",
      "FORBIDDEN": "No permission for this order.",
      "CONFLICT": "Order status has changed. Please refresh.",
      "INVALID_ORDER_TYPE": "Only balance orders can request a refund.",
      "INVALID_STATUS": "The current order status does not allow this operation.",
      "BALANCE_NOT_ENOUGH": "Refund amount exceeds balance.",
      "REFUND_AMOUNT_EXCEEDED": "Refund amount exceeds the recharge amount.",
      "REFUND_FAILED": "Refund failed."
    },
    "weeks": "weeks",
    "admin": {
      "currency": "Currency Label",
      "currencyPlaceholder": "e.g. USD / NZD / CNY",
      "currencyHint": "Display-only 3-letter ISO currency code shown next to the price; leave empty to hide, does not affect billing",
      "validity": "Validity",
      "validityRequired": "Validity must be greater than 0",
      "searchUserSubs": "Search user subscriptions...",
      "daily": "D",
      "weekly": "W",
      "monthly": "M",
      "subsStatus": {
        "active": "Active",
        "expired": "Expired",
        "revoked": "Revoked"
      }
    }
  }
} as const
