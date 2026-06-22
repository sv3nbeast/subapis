package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// UAForm 是网关按入站 UA 形式分桶的枚举,用于 fingerprint cache key 与
// 上游 canonical UA / 指纹的选择。
type UAForm string

const (
	// UAFormPlainCLI 对应 plain Claude CLI 主对话形式
	// (如 "claude-cli/2.1.177 (external, cli)")
	UAFormPlainCLI UAForm = "plain"
	// UAFormAgentSDK 对应 Claude Code Task 子代理 / Agent SDK 桥接形式
	// (如 "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)")
	UAFormAgentSDK UAForm = "agent-sdk"
)

// ClassifyUAForm 根据入站 UA 字符串选择对应的 canonical 桶。
// 不带 agent-sdk/ 标识的入站(plain CLI、第三方 SDK、Desktop Electron 等)
// 一律兜底为 plain CLI 形式。
func ClassifyUAForm(ua string) UAForm {
	if strings.Contains(strings.ToLower(ua), "agent-sdk/") {
		return UAFormAgentSDK
	}
	return UAFormPlainCLI
}

// canonicalFingerprintFor 返回指定 UA 形式的 canonical 指纹模板(不含 ClientID)。
func canonicalFingerprintFor(form UAForm) claude.CanonicalFingerprint {
	if form == UAFormAgentSDK {
		return claude.AgentSDKCanonicalFingerprint
	}
	return claude.PlainCLICanonicalFingerprint
}

// Fingerprint represents account fingerprint data
type Fingerprint struct {
	ClientID                string
	UserAgent               string
	StainlessLang           string
	StainlessPackageVersion string
	StainlessOS             string
	StainlessArch           string
	StainlessRuntime        string
	StainlessRuntimeVersion string
	UpdatedAt               int64 `json:",omitempty"` // Unix timestamp，用于判断是否需要续期TTL
}

// IdentityCache defines cache operations for identity service
type IdentityCache interface {
	// GetFingerprint 按 (accountID, form) 取指纹。form 区分 plain CLI 与 agent-sdk
	// 两个 bucket,同账号两种入站形式互不污染。
	GetFingerprint(ctx context.Context, accountID int64, form UAForm) (*Fingerprint, error)
	// SetFingerprint 按 (accountID, form) 写指纹。
	SetFingerprint(ctx context.Context, accountID int64, form UAForm, fp *Fingerprint) error
	// GetMaskedSessionID 获取固定的会话ID（用于会话ID伪装功能）
	// 返回的 sessionID 是一个 UUID 格式的字符串
	// 如果不存在或已过期（15分钟无请求），返回空字符串
	GetMaskedSessionID(ctx context.Context, accountID int64) (string, error)
	// SetMaskedSessionID 设置固定的会话ID，TTL 为 15 分钟
	// 每次调用都会刷新 TTL
	SetMaskedSessionID(ctx context.Context, accountID int64, sessionID string) error
}

// IdentityService 管理OAuth账号的请求身份指纹
type IdentityService struct {
	cache IdentityCache
}

// NewIdentityService 创建新的IdentityService
func NewIdentityService(cache IdentityCache) *IdentityService {
	return &IdentityService{cache: cache}
}

// GetOrCreateFingerprint 获取或创建账号在指定 UA 形式下的指纹。
// 行为升级(2026-06-22 修 4-8 死循环):
//   - cache key 升级为 (accountID, form),plain CLI 与 agent-sdk 互不污染。
//   - 新建指纹时不再读入站 X-Stainless-* 头,直接套用 form 对应的
//     canonical 模板(死写 MacOS/arm64),避免首位入站客户端 OS 污染整账号。
//   - 命中缓存时也强制把 X-Stainless-* 字段拉回 canonical(允许向后兼容
//     旧 Windows 缓存,但下次读取会被 canonical 覆写)。
//   - UserAgent 一律以 canonical 为准,客户端的具体版本号不影响上游 UA。
//
// 仅 ClientID 沿用缓存值,以保持 metadata.user_id 在该账号 + 该形式下稳定。
func (s *IdentityService) GetOrCreateFingerprint(ctx context.Context, accountID int64, headers http.Header, form UAForm) (*Fingerprint, error) {
	canonical := canonicalFingerprintFor(form)

	// 尝试从缓存获取指纹
	cached, err := s.cache.GetFingerprint(ctx, accountID, form)
	if err == nil && cached != nil {
		needWrite := false

		// 把缓存里的 X-Stainless-* / UserAgent 拉回 canonical。
		// 旧缓存可能因首位入站客户端污染(如 Windows/x64)而与 canonical 不一致,
		// 一次性覆写后续都稳定。ClientID / UpdatedAt 保留。
		if applyCanonicalToFingerprint(cached, canonical) {
			needWrite = true
			logger.LegacyPrintf("service.identity", "Normalized cached fingerprint to canonical for account %d form=%s", accountID, form)
		} else if time.Since(time.Unix(cached.UpdatedAt, 0)) > 24*time.Hour {
			// 距上次写入超过24小时，续期TTL
			needWrite = true
		}

		if needWrite {
			cached.UpdatedAt = time.Now().Unix()
			if err := s.cache.SetFingerprint(ctx, accountID, form, cached); err != nil {
				logger.LegacyPrintf("service.identity", "Warning: failed to refresh fingerprint for account %d form=%s: %v", accountID, form, err)
			}
		}
		return cached, nil
	}

	// 缓存不存在或解析失败，按 form 创建 canonical 指纹
	fp := newFingerprintFromCanonical(canonical)

	// 生成随机ClientID
	fp.ClientID = generateClientID()
	fp.UpdatedAt = time.Now().Unix()

	// 保存到缓存（7天TTL，每24小时自动续期）
	if err := s.cache.SetFingerprint(ctx, accountID, form, fp); err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to cache fingerprint for account %d form=%s: %v", accountID, form, err)
	}

	logger.LegacyPrintf("service.identity", "Created new canonical fingerprint for account %d form=%s with client_id: %s", accountID, form, fp.ClientID)
	return fp, nil
}

// newFingerprintFromCanonical 从 canonical 模板拷贝一份 Fingerprint(不含 ClientID/UpdatedAt)。
func newFingerprintFromCanonical(c claude.CanonicalFingerprint) *Fingerprint {
	return &Fingerprint{
		UserAgent:               c.UserAgent,
		StainlessLang:           c.StainlessLang,
		StainlessPackageVersion: c.StainlessPackageVersion,
		StainlessOS:             c.StainlessOS,
		StainlessArch:           c.StainlessArch,
		StainlessRuntime:        c.StainlessRuntime,
		StainlessRuntimeVersion: c.StainlessRuntimeVersion,
	}
}

// applyCanonicalToFingerprint 将 canonical 模板覆写到现有指纹的所有 X-Stainless-* 与
// UserAgent 字段(ClientID / UpdatedAt 保留)。返回 true 表示至少有一个字段被改变。
func applyCanonicalToFingerprint(fp *Fingerprint, c claude.CanonicalFingerprint) bool {
	if fp == nil {
		return false
	}
	changed := false
	if fp.UserAgent != c.UserAgent {
		fp.UserAgent = c.UserAgent
		changed = true
	}
	if fp.StainlessLang != c.StainlessLang {
		fp.StainlessLang = c.StainlessLang
		changed = true
	}
	if fp.StainlessPackageVersion != c.StainlessPackageVersion {
		fp.StainlessPackageVersion = c.StainlessPackageVersion
		changed = true
	}
	if fp.StainlessOS != c.StainlessOS {
		fp.StainlessOS = c.StainlessOS
		changed = true
	}
	if fp.StainlessArch != c.StainlessArch {
		fp.StainlessArch = c.StainlessArch
		changed = true
	}
	if fp.StainlessRuntime != c.StainlessRuntime {
		fp.StainlessRuntime = c.StainlessRuntime
		changed = true
	}
	if fp.StainlessRuntimeVersion != c.StainlessRuntimeVersion {
		fp.StainlessRuntimeVersion = c.StainlessRuntimeVersion
		changed = true
	}
	return changed
}

// ApplyFingerprint 将指纹应用到请求头（覆盖原有的x-stainless-*头）
// 使用 setHeaderRaw 保持原始大小写（如 X-Stainless-OS 而非 X-Stainless-Os）
func (s *IdentityService) ApplyFingerprint(req *http.Request, fp *Fingerprint) {
	if fp == nil {
		return
	}

	// 设置user-agent
	if fp.UserAgent != "" {
		setHeaderRaw(req.Header, "User-Agent", fp.UserAgent)
	}
	s.applyFingerprintWithoutUserAgent(req, fp)
}

// ApplyFingerprintWithoutUserAgent 将指纹应用到请求头，但不修改 User-Agent。
// Claude 上游 UA 由后台统一配置控制，不能被客户端指纹缓存拆成多个值。
func (s *IdentityService) ApplyFingerprintWithoutUserAgent(req *http.Request, fp *Fingerprint) {
	if fp == nil {
		return
	}
	s.applyFingerprintWithoutUserAgent(req, fp)
}

func (s *IdentityService) applyFingerprintWithoutUserAgent(req *http.Request, fp *Fingerprint) {
	if req == nil || fp == nil {
		return
	}

	// 设置x-stainless-*头（保持与 claude.DefaultHeaders 一致的大小写）
	if fp.StainlessLang != "" {
		setHeaderRaw(req.Header, "X-Stainless-Lang", fp.StainlessLang)
	}
	if fp.StainlessPackageVersion != "" {
		setHeaderRaw(req.Header, "X-Stainless-Package-Version", fp.StainlessPackageVersion)
	}
	if fp.StainlessOS != "" {
		setHeaderRaw(req.Header, "X-Stainless-OS", fp.StainlessOS)
	}
	if fp.StainlessArch != "" {
		setHeaderRaw(req.Header, "X-Stainless-Arch", fp.StainlessArch)
	}
	if fp.StainlessRuntime != "" {
		setHeaderRaw(req.Header, "X-Stainless-Runtime", fp.StainlessRuntime)
	}
	if fp.StainlessRuntimeVersion != "" {
		setHeaderRaw(req.Header, "X-Stainless-Runtime-Version", fp.StainlessRuntimeVersion)
	}
}

// RewriteUserID 重写body中的metadata.user_id
// 支持旧拼接格式和新 JSON 格式的 user_id 解析，
// 根据 fingerprintUA 版本选择输出格式。
//
// 重要：此函数使用 json.RawMessage 保留其他字段的原始字节，
// 避免重新序列化导致 thinking 块等内容被修改。
func (s *IdentityService) RewriteUserID(body []byte, accountID int64, accountUUID, cachedClientID, fingerprintUA string) ([]byte, error) {
	if len(body) == 0 || accountUUID == "" || cachedClientID == "" {
		return body, nil
	}

	metadata := gjson.GetBytes(body, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return body, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(metadata.Raw), "{") {
		return body, nil
	}

	userIDResult := metadata.Get("user_id")
	if !userIDResult.Exists() || userIDResult.Type != gjson.String {
		return body, nil
	}
	userID := userIDResult.String()
	if userID == "" {
		return body, nil
	}

	// 解析 user_id（兼容旧拼接格式和新 JSON 格式）
	parsed := ParseMetadataUserID(userID)
	if parsed == nil {
		return body, nil
	}

	sessionTail := parsed.SessionID // 原始session UUID

	// 生成新的session hash: SHA256(accountID::sessionTail) -> UUID格式
	seed := fmt.Sprintf("%d::%s", accountID, sessionTail)
	newSessionHash := generateUUIDFromSeed(seed)

	// 根据客户端版本选择输出格式
	version := ExtractCLIVersion(fingerprintUA)
	newUserID := FormatMetadataUserID(cachedClientID, accountUUID, newSessionHash, version)
	if newUserID == userID {
		return body, nil
	}

	newBody, err := sjson.SetBytes(body, "metadata.user_id", newUserID)
	if err != nil {
		return body, nil
	}
	return newBody, nil
}

// RewriteUserIDWithMasking 重写body中的metadata.user_id，支持会话ID伪装
// 如果账号启用了会话ID伪装（session_id_masking_enabled），
// 则在完成常规重写后，将 session 部分替换为固定的伪装ID（15分钟内保持不变）
//
// 重要：此函数使用 json.RawMessage 保留其他字段的原始字节，
// 避免重新序列化导致 thinking 块等内容被修改。
func (s *IdentityService) RewriteUserIDWithMasking(ctx context.Context, body []byte, account *Account, accountUUID, cachedClientID, fingerprintUA string) ([]byte, error) {
	// 先执行常规的 RewriteUserID 逻辑
	newBody, err := s.RewriteUserID(body, account.ID, accountUUID, cachedClientID, fingerprintUA)
	if err != nil {
		return newBody, err
	}

	// 检查是否启用会话ID伪装
	if !account.IsSessionIDMaskingEnabled() {
		return newBody, nil
	}

	metadata := gjson.GetBytes(newBody, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return newBody, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(metadata.Raw), "{") {
		return newBody, nil
	}

	userIDResult := metadata.Get("user_id")
	if !userIDResult.Exists() || userIDResult.Type != gjson.String {
		return newBody, nil
	}
	userID := userIDResult.String()
	if userID == "" {
		return newBody, nil
	}

	// 解析已重写的 user_id
	uidParsed := ParseMetadataUserID(userID)
	if uidParsed == nil {
		return newBody, nil
	}

	// 获取或生成固定的伪装 session ID
	maskedSessionID, err := s.cache.GetMaskedSessionID(ctx, account.ID)
	if err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to get masked session ID for account %d: %v", account.ID, err)
		return newBody, nil
	}

	if maskedSessionID == "" {
		// 首次或已过期，生成新的伪装 session ID
		maskedSessionID = generateRandomUUID()
		logger.LegacyPrintf("service.identity", "Generated new masked session ID for account %d: %s", account.ID, maskedSessionID)
	}

	// 刷新 TTL（每次请求都刷新，保持 15 分钟有效期）
	if err := s.cache.SetMaskedSessionID(ctx, account.ID, maskedSessionID); err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to set masked session ID for account %d: %v", account.ID, err)
	}

	// 用 FormatMetadataUserID 重建（保持与 RewriteUserID 相同的格式）
	version := ExtractCLIVersion(fingerprintUA)
	newUserID := FormatMetadataUserID(uidParsed.DeviceID, uidParsed.AccountUUID, maskedSessionID, version)

	slog.Debug("session_id_masking_applied",
		"account_id", account.ID,
		"before", userID,
		"after", newUserID,
	)

	if newUserID == userID {
		return newBody, nil
	}

	maskedBody, setErr := sjson.SetBytes(newBody, "metadata.user_id", newUserID)
	if setErr != nil {
		return newBody, nil
	}
	return maskedBody, nil
}

// generateRandomUUID 生成随机 UUID v4 格式字符串
func generateRandomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback: 使用时间戳生成
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		b = h[:16]
	}

	// 设置 UUID v4 版本和变体位
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// generateClientID 生成64位十六进制客户端ID（32字节随机数）
func generateClientID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极罕见的情况，使用时间戳+固定值作为fallback
		logger.LegacyPrintf("service.identity", "Warning: crypto/rand.Read failed: %v, using fallback", err)
		// 使用SHA256(当前纳秒时间)作为fallback
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return hex.EncodeToString(h[:])
	}
	return hex.EncodeToString(b)
}

// generateUUIDFromSeed 从种子生成确定性UUID v4格式字符串
func generateUUIDFromSeed(seed string) string {
	hash := sha256.Sum256([]byte(seed))
	bytes := hash[:16]

	// 设置UUID v4版本和变体位
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}


