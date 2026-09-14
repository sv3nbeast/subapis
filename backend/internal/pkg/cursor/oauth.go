package cursor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

// Cursor 的 Deep Control 登录：客户端生成 PKCE 与一个 uuid，把用户送到浏览器，
// 然后轮询 /auth/poll 换取 token。这与 Cursor CLI 的登录是同一条链路。
const (
	loginURL  = "https://cursor.com/loginDeepControl"
	pollPath  = "/auth/poll"
	authMeURL = "https://cursor.com/api/auth/me"

	// OAuthSessionTTL 限制一次登录尝试的存活时间。
	OAuthSessionTTL = 30 * time.Minute
)

// pollURL / authMeEndpoint 是测试可替换的端点。
var (
	pollURL         = BaseURLAPI + pollPath
	authMeEndpoint  = authMeURL
	loginEntrypoint = loginURL
)

// oauthHTTPTimeout 限制登录相关的短请求。
const oauthHTTPTimeout = 15 * time.Second

// oauthHTTPClient 返回登录流程用的普通 HTTP 客户端。轮询与 auth/me 是公开的
// JSON 接口，不是 Cursor 的私有 RPC，不需要（也不该用）AgentService 那套
// Chrome 指纹 h2 传输——后者只接受 TLS + h2，会让这些普通请求无谓地受限。
func oauthHTTPClient(proxyURL string) (*http.Client, error) {
	client := &http.Client{Timeout: oauthHTTPTimeout}
	_, parsed, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("cursor: parse proxy url: %w", err)
	}
	if parsed == nil {
		return client, nil
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: cursorDialTimeout}).DialContext,
		TLSHandshakeTimeout: cursorDialTimeout,
	}
	if err := proxyutil.ConfigureTransportProxy(transport, parsed); err != nil {
		return nil, fmt.Errorf("cursor: configure proxy: %w", err)
	}
	client.Transport = transport
	return client, nil
}

// OAuthSession 是一次未完成的登录尝试。
type OAuthSession struct {
	UUID      string
	Verifier  string
	Challenge string
	ProxyURL  string
	CreatedAt time.Time
}

// Expired 报告会话是否已超过存活窗口。
func (s *OAuthSession) Expired(now time.Time) bool {
	return s == nil || now.Sub(s.CreatedAt) > OAuthSessionTTL
}

// SessionStore 在内存中保存进行中的登录尝试。轮询是短时行为，重启后
// 让用户重新发起登录即可，不值得引入持久化。
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*OAuthSession
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]*OAuthSession)}
}

func (s *SessionStore) Set(sessionID string, session *OAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.sessions[sessionID] = session
}

func (s *SessionStore) Get(sessionID string) (*OAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.Expired(time.Now()) {
		return nil, false
	}
	return session, true
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// pruneLocked 顺带清理过期会话，省掉一个后台 goroutine。
func (s *SessionStore) pruneLocked() {
	now := time.Now()
	for id, session := range s.sessions {
		if session.Expired(now) {
			delete(s.sessions, id)
		}
	}
}

// GenerateSessionID 生成网关侧用于关联一次登录尝试的随机 id。
func GenerateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cursor: generate session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// GeneratePKCE 生成 Cursor 登录所需的 verifier 与其 S256 challenge。
func GeneratePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 96)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("cursor: generate pkce: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// BuildLoginURL 拼出用户需要在浏览器里打开的授权地址。
func BuildLoginURL(challenge, loginUUID string) string {
	query := url.Values{
		"challenge":      {challenge},
		"uuid":           {loginUUID},
		"mode":           {"login"},
		"redirectTarget": {"cli"},
	}
	return loginEntrypoint + "?" + query.Encode()
}

// GenerateTelemetryID 生成一个 Cursor 形态的 telemetry id（64 位十六进制）。
// Cursor 自己也是在安装时随机生成这对 id 的：它们是设备标识，不对应任何真实
// 硬件信息。OAuth 登录不下发 telemetry id，所以网关按账号生成并持久化一对，
// 等价于「一个新的 Cursor 安装」——每个账号有稳定且互不相同的设备指纹。
func GenerateTelemetryID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cursor: generate telemetry id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// TokenResponse 是 /auth/poll 与刷新端点返回的令牌对。
type TokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// PollSession 执行一次轮询。pending 为 true 表示用户尚未在浏览器完成登录。
func PollSession(ctx context.Context, session *OAuthSession) (tokens *TokenResponse, pending bool, err error) {
	if session == nil {
		return nil, false, fmt.Errorf("cursor: missing login session")
	}
	httpClient, err := oauthHTTPClient(session.ProxyURL)
	if err != nil {
		return nil, false, err
	}

	query := url.Values{"uuid": {session.UUID}, "verifier": {session.Verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL+"?"+query.Encode(), http.NoBody)
	if err != nil {
		return nil, false, fmt.Errorf("cursor: build poll request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", DefaultUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("cursor: poll login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 登录未完成时 Cursor 返回 404，这是正常的等待状态而非错误。
	if resp.StatusCode == http.StatusNotFound {
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("cursor: poll status %d: %s", resp.StatusCode, truncateForError(body))
	}

	var parsed TokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, fmt.Errorf("cursor: parse poll response: %w", err)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		// 200 但没有 token 说明这一轮还没就绪，继续等待比报错更贴合实际。
		return nil, true, nil
	}
	parsed.AccessToken = strings.TrimSpace(parsed.AccessToken)
	parsed.RefreshToken = strings.TrimSpace(parsed.RefreshToken)
	return &parsed, false, nil
}

// AccountEmail 读取登录账号的邮箱，用于在管理端标识账号。取不到不影响登录。
func AccountEmail(ctx context.Context, accessToken, proxyURL string) string {
	httpClient, err := oauthHTTPClient(proxyURL)
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authMeEndpoint, http.NoBody)
	if err != nil {
		return ""
	}
	req.Header.Set("authorization", "Bearer "+normalizeToken(accessToken))
	req.Header.Set("accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Email)
}

// UserID 从未验证的 JWT 中读取 sub，形如 "auth0|user_01H..."，取后半段。
func UserID(accessToken string) string {
	payload, ok := jwtPayload(normalizeToken(accessToken))
	if !ok {
		return ""
	}
	sub, _ := payload["sub"].(string)
	sub = strings.TrimSpace(sub)
	if i := strings.LastIndex(sub, "|"); i >= 0 && i < len(sub)-1 {
		return strings.TrimSpace(sub[i+1:])
	}
	return sub
}

func normalizeToken(token string) string {
	token = strings.TrimSpace(token)
	if i := strings.Index(token, "::"); i >= 0 && i < len(token)-2 {
		token = strings.TrimSpace(token[i+2:])
	}
	return token
}

func jwtPayload(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	raw, err := decodeJWTSegment(parts[1])
	if err != nil {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func truncateForError(body []byte) string {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		return msg[:300]
	}
	return msg
}
