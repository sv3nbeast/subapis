package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type InvoiceProvider interface {
	Name() string
	CreateInvoice(ctx context.Context, req InvoiceProviderCreateRequest) (*InvoiceProviderCreateResult, error)
	QueryInvoice(ctx context.Context, providerOrderID string) (*InvoiceProviderStatusResult, error)
	DownloadFile(ctx context.Context, providerOrderID, fileType string) (*InvoiceProviderFile, error)
}

func newInvoiceProvider(cfg *InvoiceConfig) (InvoiceProvider, error) {
	if cfg == nil {
		return nil, infraerrors.BadRequest("INVOICE_CONFIG_MISSING", "invoice config is missing")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case InvoiceProviderMock, "":
		return mockInvoiceProvider{}, nil
	case InvoiceProviderLexiang:
		return newLexiangInvoiceProvider(cfg), nil
	default:
		return nil, infraerrors.BadRequest("INVOICE_PROVIDER_UNSUPPORTED", "unsupported invoice provider")
	}
}

type mockInvoiceProvider struct{}

func (mockInvoiceProvider) Name() string { return InvoiceProviderMock }

func (mockInvoiceProvider) CreateInvoice(_ context.Context, req InvoiceProviderCreateRequest) (*InvoiceProviderCreateResult, error) {
	id := fmt.Sprintf("mock-inv-%d", req.ApplicationID)
	return &InvoiceProviderCreateResult{
		ProviderOrderID: id,
		ProviderOrderNo: req.LocalOrderNo,
		Raw:             map[string]any{"rtnCode": "200", "rtnMsg": "mock issued", "rtnId": id},
	}, nil
}

func (mockInvoiceProvider) QueryInvoice(_ context.Context, providerOrderID string) (*InvoiceProviderStatusResult, error) {
	now := time.Now()
	return &InvoiceProviderStatusResult{
		Status:          InvoiceStatusIssued,
		ProviderOrderID: providerOrderID,
		ProviderOrderNo: providerOrderID,
		InvoiceNo:       providerOrderID,
		IssuedAt:        &now,
		Raw:             map[string]any{"ddzt": "1", "mock": true},
	}, nil
}

func (mockInvoiceProvider) DownloadFile(_ context.Context, providerOrderID, fileType string) (*InvoiceProviderFile, error) {
	if fileType == "" {
		fileType = "pdf"
	}
	return &InvoiceProviderFile{FileType: fileType, Base64: ""}, nil
}

type lexiangInvoiceProvider struct {
	baseURL   string
	username  string
	password  string
	appKey    string
	ssqyuuid  string
	encrypted bool
	client    *http.Client

	mu        sync.Mutex
	token     string
	tokenTime time.Time
}

func newLexiangInvoiceProvider(cfg *InvoiceConfig) *lexiangInvoiceProvider {
	pc := cfg.ProviderConfig
	p := &lexiangInvoiceProvider{
		baseURL:   strings.TrimRight(strings.TrimSpace(pc["base_url"]), "/"),
		username:  strings.TrimSpace(pc["username"]),
		password:  strings.TrimSpace(pc["password"]),
		appKey:    strings.TrimSpace(invoiceFirstNonEmptyString(pc["appkey"], pc["app_key"])),
		ssqyuuid:  strings.TrimSpace(pc["ssqyuuid"]),
		encrypted: strings.EqualFold(strings.TrimSpace(pc["encrypted"]), "true"),
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	return p
}

func (p *lexiangInvoiceProvider) Name() string { return InvoiceProviderLexiang }

func (p *lexiangInvoiceProvider) CreateInvoice(ctx context.Context, req InvoiceProviderCreateRequest) (*InvoiceProviderCreateResult, error) {
	ssqyuuid, err := p.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	payload := p.buildCreatePayload(ssqyuuid, req)
	raw, err := p.callBusiness(ctx, "J02004001", payload, true)
	if err != nil {
		return nil, err
	}
	if code := responseCode(raw); code != "200" {
		return nil, fmt.Errorf("lexiang create invoice failed: %s %s", code, responseMessage(raw))
	}
	providerOrderID := strings.TrimSpace(stringField(raw, "rtnId"))
	if providerOrderID == "" {
		return nil, fmt.Errorf("lexiang create invoice missing rtnId")
	}
	return &InvoiceProviderCreateResult{
		ProviderOrderID: providerOrderID,
		ProviderOrderNo: req.LocalOrderNo,
		Raw:             raw,
	}, nil
}

func (p *lexiangInvoiceProvider) QueryInvoice(ctx context.Context, providerOrderID string) (*InvoiceProviderStatusResult, error) {
	ssqyuuid, err := p.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"ssqyuuid": ssqyuuid,
		"uuid":     providerOrderID,
		"current":  1,
		"size":     1,
	}
	raw, err := p.callBusiness(ctx, "F02004006", payload, true)
	if err != nil {
		return nil, err
	}
	if code := responseCode(raw); code != "200" {
		return nil, fmt.Errorf("lexiang query invoice failed: %s %s", code, responseMessage(raw))
	}
	record := firstRecord(raw)
	if record == nil {
		return &InvoiceProviderStatusResult{Status: InvoiceStatusProcessing, ProviderOrderID: providerOrderID, Raw: raw}, nil
	}
	status, errCode, errMsg := lexiangStatus(record)
	issuedAt := parseLexiangDateTime(invoiceFirstNonEmptyString(stringField(record, "fprq"), stringField(record, "xgsj")))
	return &InvoiceProviderStatusResult{
		Status:          status,
		ProviderOrderID: stringField(record, "uuid"),
		ProviderOrderNo: invoiceFirstNonEmptyString(stringField(record, "ddh"), stringField(record, "dsfddh")),
		InvoiceCode:     stringField(record, "fpdm"),
		InvoiceNo:       stringField(record, "fphm"),
		IssuedAt:        issuedAt,
		ErrorCode:       errCode,
		ErrorMessage:    errMsg,
		Raw:             raw,
	}, nil
}

func (p *lexiangInvoiceProvider) DownloadFile(ctx context.Context, providerOrderID, fileType string) (*InvoiceProviderFile, error) {
	if fileType == "" {
		fileType = "pdf"
	}
	payload := map[string]any{"uuids": providerOrderID, "wjlx": strings.ToLower(fileType)}
	raw, err := p.callBusiness(ctx, "F02004007", payload, true)
	if err != nil {
		return nil, err
	}
	if code := responseCode(raw); code != "200" {
		return nil, fmt.Errorf("lexiang download invoice file failed: %s %s", code, responseMessage(raw))
	}
	if data, ok := raw["data"].([]any); ok && len(data) > 0 {
		if item, ok := data[0].(map[string]any); ok {
			if code := responseCode(item); code != "" && code != "200" {
				return nil, fmt.Errorf("lexiang invoice file failed: %s %s", code, responseMessage(item))
			}
			return &InvoiceProviderFile{FileType: fileType, Base64: stringField(item, "fileData")}, nil
		}
	}
	return &InvoiceProviderFile{FileType: fileType}, nil
}

func (p *lexiangInvoiceProvider) buildCreatePayload(ssqyuuid string, req InvoiceProviderCreateRequest) map[string]any {
	invoiceType := "82"
	if strings.EqualFold(req.InvoiceType, "digital_special") {
		invoiceType = "81"
	}
	buyerNatural := "N"
	if req.BuyerType == InvoiceBuyerTypeIndividual {
		buyerNatural = "Y"
	}
	payload := map[string]any{
		"ssqyuuid":    ssqyuuid,
		"dsfddh":      req.LocalOrderNo,
		"fplxdm":      invoiceType,
		"jyzdshbz":    "2",
		"jyzdkpbz":    "2",
		"jysdzhyz":    "N",
		"sdbz":        "1",
		"xfnsrmc":     req.SellerName,
		"xfnsrsbh":    req.SellerTaxNo,
		"xfnsrdz":     req.SellerAddress,
		"xfnsrdh":     req.SellerPhone,
		"xfnsrkhh":    req.SellerBankName,
		"xfnsryhzh":   req.SellerBankAccount,
		"zrrbz":       buyerNatural,
		"gfnsrmc":     req.BuyerName,
		"gfnsrsbh":    req.BuyerTaxNo,
		"gfnsrdz":     req.BuyerAddress,
		"gfnsrdh":     req.BuyerPhone,
		"gfnsrkhh":    req.BuyerBankName,
		"gfnsryhzh":   req.BuyerBankAccount,
		"jsryxdz":     req.BuyerEmail,
		"jsrsjh":      req.BuyerPhone,
		"kprmc":       req.DrawerName,
		"skrmc":       req.PayeeName,
		"fhrmc":       req.ReviewerName,
		"bz":          req.Remark,
		"fpbz":        req.Remark,
		"sfzsgmfyhzh": boolFlag(req.BuyerBankName != "" || req.BuyerBankAccount != ""),
		"sfzsgmfdzdh": boolFlag(req.BuyerAddress != "" || req.BuyerPhone != ""),
		"sfzsxsfyhzh": boolFlag(req.SellerBankName != "" || req.SellerBankAccount != ""),
		"sfzsxsfdzdh": boolFlag(req.SellerAddress != "" || req.SellerPhone != ""),
		"detail": []map[string]any{
			{
				"spxh":    "1",
				"spmc":    req.ItemName,
				"ssflbm":  req.TaxClassificationCode,
				"fphxz":   "0",
				"je":      formatMoney(req.InvoiceAmount),
				"jshj":    formatMoney(req.InvoiceAmount),
				"hsbz":    "1",
				"sl":      req.TaxRate,
				"zzstsgl": "",
			},
		},
	}
	return payload
}

func (p *lexiangInvoiceProvider) ensureSession(ctx context.Context) (string, error) {
	if p.baseURL == "" || p.username == "" || p.password == "" || p.appKey == "" {
		return "", infraerrors.BadRequest("INVOICE_PROVIDER_MISCONFIGURED", "lexiang invoice provider is missing base_url, username, password or appkey")
	}
	p.mu.Lock()
	if p.token != "" && time.Since(p.tokenTime) < 110*time.Minute {
		ssqyuuid := p.ssqyuuid
		p.mu.Unlock()
		return ssqyuuid, nil
	}
	p.mu.Unlock()

	body := map[string]string{"username": p.username, "password": p.password}
	raw, err := p.postJSON(ctx, p.baseURL+"/userLogin", "", body)
	if err != nil {
		return "", err
	}
	if code := responseCode(raw); code != "200" {
		return "", fmt.Errorf("lexiang login failed: %s %s", code, responseMessage(raw))
	}
	data, _ := raw["data"].(map[string]any)
	token := strings.TrimSpace(stringField(data, "token"))
	ssqyuuid := strings.TrimSpace(invoiceFirstNonEmptyString(p.ssqyuuid, stringField(data, "ssqyuuid")))
	if token == "" || ssqyuuid == "" {
		return "", fmt.Errorf("lexiang login response missing token or ssqyuuid")
	}
	p.mu.Lock()
	p.token = token
	p.ssqyuuid = ssqyuuid
	p.tokenTime = time.Now()
	p.mu.Unlock()
	return ssqyuuid, nil
}

func (p *lexiangInvoiceProvider) callBusiness(ctx context.Context, serviceID string, payload map[string]any, retryOnExpired bool) (map[string]any, error) {
	if p.encrypted {
		return nil, infraerrors.BadRequest("INVOICE_PROVIDER_MISCONFIGURED", "lexiang encrypted mode is not implemented; use toEnService or set encrypted=false")
	}
	p.mu.Lock()
	token := p.token
	p.mu.Unlock()
	wrapper := map[string]any{
		"appkey":    p.appKey,
		"serviceId": serviceID,
		"timestamp": strconv.FormatInt(time.Now().UnixMilli(), 10),
		"version":   "1.0",
		"content":   mustMarshalString(payload),
	}
	raw, err := p.postJSON(ctx, p.baseURL+"/toEnService", token, wrapper)
	if err != nil {
		return nil, err
	}
	if retryOnExpired && responseCode(raw) == "2502" {
		p.mu.Lock()
		p.token = ""
		p.mu.Unlock()
		if _, err := p.ensureSession(ctx); err != nil {
			return nil, err
		}
		return p.callBusiness(ctx, serviceID, payload, false)
	}
	return raw, nil
}

func (p *lexiangInvoiceProvider) postJSON(ctx context.Context, endpoint, token string, payload any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal invoice provider request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("invoice provider http %d: %s", resp.StatusCode, string(respBody))
	}
	var raw map[string]any
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("decode invoice provider response: %w", err)
	}
	return raw, nil
}

func mustMarshalString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func responseCode(raw map[string]any) string {
	return strings.TrimSpace(stringField(raw, "rtnCode"))
}

func responseMessage(raw map[string]any) string {
	return strings.TrimSpace(stringField(raw, "rtnMsg"))
}

func stringField(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	switch v := raw[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func firstRecord(raw map[string]any) map[string]any {
	data, _ := raw["data"].(map[string]any)
	records, _ := data["records"].([]any)
	if len(records) == 0 {
		return nil
	}
	record, _ := records[0].(map[string]any)
	return record
}

func lexiangStatus(record map[string]any) (string, string, string) {
	switch strings.TrimSpace(stringField(record, "ddzt")) {
	case "1":
		return InvoiceStatusIssued, "", ""
	case "-1":
		msg := invoiceFirstNonEmptyString(stringField(record, "ycyy"), "invoice issuing failed")
		return InvoiceStatusFailed, "-1", msg
	case "2":
		return InvoiceStatusCancelled, "", ""
	default:
		return InvoiceStatusProcessing, "", ""
	}
}

func parseLexiangDateTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02", time.RFC3339}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

func invoiceFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			return s
		}
	}
	return ""
}

func boolFlag(v bool) string {
	if v {
		return "Y"
	}
	return "N"
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func sanitizeFileType(fileType string) string {
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	switch fileType {
	case "pdf", "ofd", "xml":
		return fileType
	default:
		return ""
	}
}

func sanitizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.TrimRight(raw, "/")
}
