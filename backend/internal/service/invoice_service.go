package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/invoiceapplication"
	"github.com/Wei-Shaw/sub2api/ent/invoiceapplicationorder"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/user"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type InvoiceService struct {
	entClient   *dbent.Client
	settingRepo SettingRepository

	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewInvoiceService(entClient *dbent.Client, settingRepo SettingRepository) *InvoiceService {
	return &InvoiceService{
		entClient:   entClient,
		settingRepo: settingRepo,
		stopCh:      make(chan struct{}),
	}
}

func (s *InvoiceService) Start() {
	go s.loop()
}

func (s *InvoiceService) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *InvoiceService) loop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			if err := s.SyncProcessingInvoices(ctx, 20); err != nil {
				slog.Warn("sync processing invoices failed", "error", err)
			}
			cancel()
		case <-s.stopCh:
			return
		}
	}
}

func (s *InvoiceService) GetConfig(ctx context.Context) (*InvoiceConfig, error) {
	cfg := defaultInvoiceConfig()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyInvoiceConfig)
	if err != nil {
		return cfg, nil
	}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, fmt.Errorf("parse invoice config: %w", err)
	}
	normalizeInvoiceConfig(cfg)
	return cfg, nil
}

func (s *InvoiceService) UpdateConfig(ctx context.Context, cfg *InvoiceConfig) (*InvoiceConfig, error) {
	if cfg == nil {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "invoice config is required")
	}
	normalizeInvoiceConfig(cfg)
	if cfg.Provider == InvoiceProviderLexiang {
		cfg.ProviderConfig["base_url"] = sanitizeURL(cfg.ProviderConfig["base_url"])
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal invoice config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyInvoiceConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save invoice config: %w", err)
	}
	return cfg, nil
}

func (s *InvoiceService) ListEligibleOrders(ctx context.Context, userID int64) ([]InvoiceEligibleOrderResponse, error) {
	existingRows, err := s.entClient.InvoiceApplicationOrder.Query().
		Where(invoiceapplicationorder.UserIDEQ(userID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query invoice-linked orders: %w", err)
	}
	used := make([]int64, 0, len(existingRows))
	for _, row := range existingRows {
		used = append(used, row.PaymentOrderID)
	}
	preds := []predicate.PaymentOrder{
		paymentorder.UserIDEQ(userID),
		paymentorder.StatusEQ(OrderStatusCompleted),
		paymentorder.PayAmountGT(0),
		paymentorder.RefundAmountEQ(0),
	}
	if len(used) > 0 {
		preds = append(preds, paymentorder.IDNotIn(used...))
	}
	orders, err := s.entClient.PaymentOrder.Query().
		Where(preds...).
		Order(dbent.Desc(paymentorder.FieldCompletedAt), dbent.Desc(paymentorder.FieldCreatedAt)).
		Limit(500).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query eligible invoice orders: %w", err)
	}
	out := make([]InvoiceEligibleOrderResponse, 0, len(orders))
	for _, o := range orders {
		out = append(out, InvoiceEligibleOrderResponse{
			OrderID:      o.ID,
			OutTradeNo:   o.OutTradeNo,
			OrderType:    o.OrderType,
			PayAmount:    roundMoney(o.PayAmount),
			RefundAmount: roundMoney(o.RefundAmount),
			CreatedAt:    o.CreatedAt,
		})
	}
	return out, nil
}

func (s *InvoiceService) CreateApplication(ctx context.Context, userID int64, req CreateInvoiceApplicationRequest) (*InvoiceApplicationResponse, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, infraerrors.BadRequest("INVOICE_DISABLED", "invoice application is disabled")
	}
	if !cfg.AutoIssueEnabled {
		return nil, infraerrors.BadRequest("INVOICE_AUTO_ISSUE_DISABLED", "automatic invoice issuing is disabled")
	}
	if err := validateInvoiceRequest(req); err != nil {
		return nil, err
	}

	app, err := s.createApplicationInTx(ctx, userID, req, cfg)
	if err != nil {
		return nil, err
	}
	if err := s.IssueAndSync(ctx, app.ID); err != nil {
		slog.Warn("invoice issue attempt failed", "application_id", app.ID, "error", err)
	}
	return s.GetApplication(ctx, userID, app.ID)
}

func (s *InvoiceService) createApplicationInTx(ctx context.Context, userID int64, req CreateInvoiceApplicationRequest, cfg *InvoiceConfig) (*dbent.InvoiceApplication, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin invoice transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txClient := tx.Client()
	txCtx := dbent.NewTxContext(ctx, tx)

	u, err := txClient.User.Query().Where(user.IDEQ(userID)).Only(txCtx)
	if err != nil {
		return nil, infraerrors.NotFound("USER_NOT_FOUND", "user not found")
	}
	orderIDs := normalizeOrderIDs(req.OrderIDs)
	orders, err := txClient.PaymentOrder.Query().
		Where(paymentorder.IDIn(orderIDs...), paymentorder.UserIDEQ(userID)).
		ForUpdate().
		All(txCtx)
	if err != nil {
		return nil, fmt.Errorf("query invoice orders: %w", err)
	}
	if len(orders) != len(orderIDs) {
		return nil, infraerrors.BadRequest("INVOICE_ORDER_INVALID", "some selected orders do not exist or do not belong to current user")
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].ID < orders[j].ID })
	total := 0.0
	for _, o := range orders {
		if err := validateInvoiceOrder(o); err != nil {
			return nil, err
		}
		total += o.PayAmount
	}
	total = roundMoney(total)
	if total <= 0 {
		return nil, infraerrors.BadRequest("INVOICE_AMOUNT_INVALID", "invoice amount must be greater than zero")
	}
	exists, err := txClient.InvoiceApplicationOrder.Query().
		Where(invoiceapplicationorder.PaymentOrderIDIn(orderIDs...)).
		ForUpdate().
		Exist(txCtx)
	if err != nil {
		return nil, fmt.Errorf("check duplicate invoice orders: %w", err)
	}
	if exists {
		return nil, infraerrors.Conflict("INVOICE_ORDER_ALREADY_USED", "one or more selected orders already have invoice applications")
	}
	now := time.Now()
	app, err := txClient.InvoiceApplication.Create().
		SetUserID(userID).
		SetUserEmail(u.Email).
		SetBuyerType(normalizeBuyerType(req.BuyerType)).
		SetBuyerName(strings.TrimSpace(req.BuyerName)).
		SetBuyerTaxNo(strings.TrimSpace(req.BuyerTaxNo)).
		SetBuyerEmail(strings.TrimSpace(req.BuyerEmail)).
		SetBuyerPhone(strings.TrimSpace(req.BuyerPhone)).
		SetBuyerAddress(strings.TrimSpace(req.BuyerAddress)).
		SetBuyerBankName(strings.TrimSpace(req.BuyerBankName)).
		SetBuyerBankAccount(strings.TrimSpace(req.BuyerBankAccount)).
		SetInvoiceAmount(total).
		SetInvoiceType(cfg.DefaultInvoiceType).
		SetContent(cfg.ItemName).
		SetTaxRate(cfg.TaxRate).
		SetTaxClassificationCode(cfg.TaxClassificationCode).
		SetStatus(InvoiceStatusSubmitted).
		SetProvider(cfg.Provider).
		SetRequestPayloadSnapshot(map[string]any{"order_ids": orderIDs, "remark": strings.TrimSpace(req.Remark)}).
		SetSubmittedAt(now).
		Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("create invoice application: %w", err)
	}
	builders := make([]*dbent.InvoiceApplicationOrderCreate, 0, len(orders))
	for _, o := range orders {
		builders = append(builders, txClient.InvoiceApplicationOrder.Create().
			SetInvoiceApplicationID(app.ID).
			SetUserID(userID).
			SetPaymentOrderID(o.ID).
			SetOutTradeNo(o.OutTradeNo).
			SetOrderType(o.OrderType).
			SetOrderAmount(roundMoney(o.Amount)).
			SetPayAmount(roundMoney(o.PayAmount)).
			SetRefundAmount(roundMoney(o.RefundAmount)).
			SetInvoiceAmount(roundMoney(o.PayAmount)))
	}
	if err := txClient.InvoiceApplicationOrder.CreateBulk(builders...).Exec(txCtx); err != nil {
		if dbent.IsConstraintError(err) {
			return nil, infraerrors.Conflict("INVOICE_ORDER_ALREADY_USED", "one or more selected orders already have invoice applications")
		}
		return nil, fmt.Errorf("create invoice application orders: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit invoice transaction: %w", err)
	}
	return app, nil
}

func (s *InvoiceService) IssueAndSync(ctx context.Context, applicationID int64) error {
	app, err := s.entClient.InvoiceApplication.Get(ctx, applicationID)
	if err != nil {
		return err
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	provider, err := newInvoiceProvider(cfg)
	if err != nil {
		return s.markFailed(ctx, applicationID, "PROVIDER_CONFIG", err.Error(), nil)
	}
	if app.ProviderOrderID == "" && app.Status != InvoiceStatusIssued {
		createReq := buildProviderCreateRequest(app, cfg)
		result, issueErr := provider.CreateInvoice(ctx, createReq)
		if issueErr != nil {
			return s.markFailed(ctx, applicationID, "PROVIDER_CREATE_FAILED", issueErr.Error(), nil)
		}
		_, err = s.entClient.InvoiceApplication.UpdateOneID(applicationID).
			SetStatus(InvoiceStatusProcessing).
			SetProvider(provider.Name()).
			SetProviderOrderID(result.ProviderOrderID).
			SetProviderOrderNo(result.ProviderOrderNo).
			SetLastErrorCode("").
			SetLastErrorMessage("").
			SetResponsePayloadSnapshot(result.Raw).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("update invoice provider result: %w", err)
		}
		app.ProviderOrderID = result.ProviderOrderID
	}
	return s.syncOne(ctx, applicationID, provider)
}

func (s *InvoiceService) SyncProcessingInvoices(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 20
	}
	apps, err := s.entClient.InvoiceApplication.Query().
		Where(invoiceapplication.StatusIn(InvoiceStatusSubmitted, InvoiceStatusProcessing)).
		Order(dbent.Asc(invoiceapplication.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return fmt.Errorf("query processing invoices: %w", err)
	}
	if len(apps) == 0 {
		return nil
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	provider, err := newInvoiceProvider(cfg)
	if err != nil {
		return err
	}
	for _, app := range apps {
		if err := s.syncOne(ctx, app.ID, provider); err != nil {
			slog.Warn("sync invoice failed", "application_id", app.ID, "error", err)
		}
	}
	return nil
}

func (s *InvoiceService) syncOne(ctx context.Context, applicationID int64, provider InvoiceProvider) error {
	app, err := s.entClient.InvoiceApplication.Get(ctx, applicationID)
	if err != nil {
		return err
	}
	if app.ProviderOrderID == "" {
		return nil
	}
	status, err := provider.QueryInvoice(ctx, app.ProviderOrderID)
	if err != nil {
		return err
	}
	upd := s.entClient.InvoiceApplication.UpdateOneID(applicationID).
		SetStatus(status.Status).
		SetProviderOrderNo(invoiceFirstNonEmptyString(status.ProviderOrderNo, app.ProviderOrderNo)).
		SetInvoiceCode(status.InvoiceCode).
		SetInvoiceNo(status.InvoiceNo).
		SetLastErrorCode(status.ErrorCode).
		SetLastErrorMessage(status.ErrorMessage).
		SetResponsePayloadSnapshot(status.Raw)
	if status.IssuedAt != nil {
		upd.SetIssuedAt(*status.IssuedAt)
	}
	if _, err := upd.Save(ctx); err != nil {
		return fmt.Errorf("update invoice status: %w", err)
	}
	if status.Status == InvoiceStatusIssued {
		_ = s.fetchAndSaveFile(ctx, applicationID, provider, app.ProviderOrderID, "pdf")
	}
	return nil
}

func (s *InvoiceService) fetchAndSaveFile(ctx context.Context, applicationID int64, provider InvoiceProvider, providerOrderID, fileType string) error {
	fileType = sanitizeFileType(fileType)
	if fileType == "" {
		return infraerrors.BadRequest("INVALID_FILE_TYPE", "invalid invoice file type")
	}
	file, err := provider.DownloadFile(ctx, providerOrderID, fileType)
	if err != nil {
		return err
	}
	if strings.TrimSpace(file.Base64) == "" {
		return nil
	}
	upd := s.entClient.InvoiceApplication.UpdateOneID(applicationID)
	switch fileType {
	case "pdf":
		upd.SetPdfFileData(file.Base64)
	case "ofd":
		upd.SetOfdFileData(file.Base64)
	case "xml":
		upd.SetXMLFileData(file.Base64)
	}
	_, err = upd.Save(ctx)
	return err
}

func (s *InvoiceService) GetApplication(ctx context.Context, userID, applicationID int64) (*InvoiceApplicationResponse, error) {
	app, err := s.entClient.InvoiceApplication.Get(ctx, applicationID)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "invoice application not found")
	}
	if userID > 0 && app.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this invoice application")
	}
	orders, err := s.entClient.InvoiceApplicationOrder.Query().
		Where(invoiceapplicationorder.InvoiceApplicationIDEQ(applicationID)).
		Order(dbent.Asc(invoiceapplicationorder.FieldPaymentOrderID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query invoice application orders: %w", err)
	}
	return mapInvoiceApplication(app, orders), nil
}

func (s *InvoiceService) ListUserApplications(ctx context.Context, userID int64, page, pageSize int) ([]*InvoiceApplicationResponse, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := s.entClient.InvoiceApplication.Query().Where(invoiceapplication.UserIDEQ(userID))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count user invoice applications: %w", err)
	}
	apps, err := q.Order(dbent.Desc(invoiceapplication.FieldCreatedAt)).Limit(pageSize).Offset((page - 1) * pageSize).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query user invoice applications: %w", err)
	}
	return s.mapApplicationsWithOrders(ctx, apps), total, nil
}

func (s *InvoiceService) AdminListApplications(ctx context.Context, page, pageSize int, status, keyword string) ([]*InvoiceApplicationResponse, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := s.entClient.InvoiceApplication.Query()
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where(invoiceapplication.StatusEQ(status))
	}
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		q = q.Where(invoiceapplication.Or(
			invoiceapplication.BuyerNameContainsFold(keyword),
			invoiceapplication.UserEmailContainsFold(keyword),
			invoiceapplication.ProviderOrderIDContainsFold(keyword),
			invoiceapplication.InvoiceNoContainsFold(keyword),
		))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count invoice applications: %w", err)
	}
	apps, err := q.Order(dbent.Desc(invoiceapplication.FieldCreatedAt)).Limit(pageSize).Offset((page - 1) * pageSize).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query invoice applications: %w", err)
	}
	return s.mapApplicationsWithOrders(ctx, apps), total, nil
}

func (s *InvoiceService) AdminRetry(ctx context.Context, applicationID int64) (*InvoiceApplicationResponse, error) {
	if _, err := s.entClient.InvoiceApplication.UpdateOneID(applicationID).
		SetStatus(InvoiceStatusSubmitted).
		AddRetryCount(1).
		SetLastErrorCode("").
		SetLastErrorMessage("").
		Save(ctx); err != nil {
		return nil, err
	}
	if err := s.IssueAndSync(ctx, applicationID); err != nil {
		return nil, err
	}
	return s.GetApplication(ctx, 0, applicationID)
}

func (s *InvoiceService) DownloadFile(ctx context.Context, userID, applicationID int64, fileType string) ([]byte, string, error) {
	fileType = sanitizeFileType(fileType)
	if fileType == "" {
		return nil, "", infraerrors.BadRequest("INVALID_FILE_TYPE", "invalid invoice file type")
	}
	app, err := s.entClient.InvoiceApplication.Get(ctx, applicationID)
	if err != nil {
		return nil, "", infraerrors.NotFound("NOT_FOUND", "invoice application not found")
	}
	if userID > 0 && app.UserID != userID {
		return nil, "", infraerrors.Forbidden("FORBIDDEN", "no permission for this invoice application")
	}
	if app.Status != InvoiceStatusIssued {
		return nil, "", infraerrors.BadRequest("INVOICE_NOT_ISSUED", "invoice has not been issued")
	}
	data := invoiceFileData(app, fileType)
	if data == "" && app.ProviderOrderID != "" {
		cfg, err := s.GetConfig(ctx)
		if err != nil {
			return nil, "", err
		}
		provider, err := newInvoiceProvider(cfg)
		if err != nil {
			return nil, "", err
		}
		if err := s.fetchAndSaveFile(ctx, applicationID, provider, app.ProviderOrderID, fileType); err != nil {
			return nil, "", err
		}
		app, _ = s.entClient.InvoiceApplication.Get(ctx, applicationID)
		data = invoiceFileData(app, fileType)
	}
	if data == "" {
		return nil, "", infraerrors.NotFound("INVOICE_FILE_NOT_FOUND", "invoice file is not available")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, "", fmt.Errorf("decode invoice file: %w", err)
	}
	return decoded, invoiceContentType(fileType), nil
}

func (s *InvoiceService) mapApplicationsWithOrders(ctx context.Context, apps []*dbent.InvoiceApplication) []*InvoiceApplicationResponse {
	out := make([]*InvoiceApplicationResponse, 0, len(apps))
	for _, app := range apps {
		orders, _ := s.entClient.InvoiceApplicationOrder.Query().
			Where(invoiceapplicationorder.InvoiceApplicationIDEQ(app.ID)).
			Order(dbent.Asc(invoiceapplicationorder.FieldPaymentOrderID)).
			All(ctx)
		out = append(out, mapInvoiceApplication(app, orders))
	}
	return out
}

func (s *InvoiceService) markFailed(ctx context.Context, applicationID int64, code, message string, raw map[string]any) error {
	_, err := s.entClient.InvoiceApplication.UpdateOneID(applicationID).
		SetStatus(InvoiceStatusFailed).
		SetLastErrorCode(code).
		SetLastErrorMessage(message).
		SetResponsePayloadSnapshot(raw).
		Save(ctx)
	return err
}

func defaultInvoiceConfig() *InvoiceConfig {
	return &InvoiceConfig{
		Enabled:               false,
		Provider:              "",
		AutoIssueEnabled:      true,
		DefaultInvoiceType:    "digital_normal",
		ItemName:              "技术服务费",
		TaxRate:               "6",
		TaxClassificationCode: "",
		ProviderConfig:        map[string]string{},
	}
}

func normalizeInvoiceConfig(cfg *InvoiceConfig) {
	if cfg.ProviderConfig == nil {
		cfg.ProviderConfig = map[string]string{}
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.DefaultInvoiceType = strings.TrimSpace(cfg.DefaultInvoiceType)
	if cfg.DefaultInvoiceType == "" {
		cfg.DefaultInvoiceType = "digital_normal"
	}
	cfg.ItemName = strings.TrimSpace(cfg.ItemName)
	if cfg.ItemName == "" {
		cfg.ItemName = "技术服务费"
	}
	cfg.TaxRate = strings.TrimSpace(cfg.TaxRate)
	if cfg.TaxRate == "" {
		cfg.TaxRate = "6"
	}
}

func validateInvoiceRequest(req CreateInvoiceApplicationRequest) error {
	if len(normalizeOrderIDs(req.OrderIDs)) == 0 {
		return infraerrors.BadRequest("INVOICE_ORDER_REQUIRED", "at least one order is required")
	}
	buyerType := normalizeBuyerType(req.BuyerType)
	if buyerType != InvoiceBuyerTypeIndividual && buyerType != InvoiceBuyerTypeEnterprise {
		return infraerrors.BadRequest("INVALID_BUYER_TYPE", "buyer type must be individual or enterprise")
	}
	if strings.TrimSpace(req.BuyerName) == "" {
		return infraerrors.BadRequest("BUYER_NAME_REQUIRED", "invoice title is required")
	}
	if buyerType == InvoiceBuyerTypeEnterprise && strings.TrimSpace(req.BuyerTaxNo) == "" {
		return infraerrors.BadRequest("BUYER_TAX_NO_REQUIRED", "enterprise taxpayer id is required")
	}
	if strings.TrimSpace(req.BuyerEmail) == "" && strings.TrimSpace(req.BuyerPhone) == "" {
		return infraerrors.BadRequest("INVOICE_RECEIVER_REQUIRED", "buyer email or phone is required")
	}
	return nil
}

func normalizeBuyerType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return InvoiceBuyerTypeIndividual
	}
	return v
}

func normalizeOrderIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func validateInvoiceOrder(o *dbent.PaymentOrder) error {
	if o.Status != OrderStatusCompleted {
		return infraerrors.BadRequest("INVOICE_ORDER_NOT_COMPLETED", "only completed orders can be invoiced")
	}
	if o.PayAmount <= 0 {
		return infraerrors.BadRequest("INVOICE_ORDER_AMOUNT_INVALID", "order paid amount must be greater than zero")
	}
	if o.RefundAmount > 0 || isRefundingStatus(o.Status) {
		return infraerrors.BadRequest("INVOICE_ORDER_REFUNDED", "refunded or refunding orders cannot be invoiced")
	}
	return nil
}

func isRefundingStatus(status string) bool {
	switch status {
	case OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusPartiallyRefunded, OrderStatusRefunded, OrderStatusRefundFailed:
		return true
	default:
		return false
	}
}

func buildProviderCreateRequest(app *dbent.InvoiceApplication, cfg *InvoiceConfig) InvoiceProviderCreateRequest {
	return InvoiceProviderCreateRequest{
		ApplicationID:         app.ID,
		LocalOrderNo:          fmt.Sprintf("INV-%d", app.ID),
		InvoiceAmount:         roundMoney(app.InvoiceAmount),
		BuyerType:             app.BuyerType,
		BuyerName:             app.BuyerName,
		BuyerTaxNo:            app.BuyerTaxNo,
		BuyerEmail:            app.BuyerEmail,
		BuyerPhone:            app.BuyerPhone,
		BuyerAddress:          app.BuyerAddress,
		BuyerBankName:         app.BuyerBankName,
		BuyerBankAccount:      app.BuyerBankAccount,
		SellerName:            cfg.SellerName,
		SellerTaxNo:           cfg.SellerTaxNo,
		SellerAddress:         cfg.SellerAddress,
		SellerPhone:           cfg.SellerPhone,
		SellerBankName:        cfg.SellerBankName,
		SellerBankAccount:     cfg.SellerBankAccount,
		DrawerName:            cfg.DrawerName,
		PayeeName:             cfg.PayeeName,
		ReviewerName:          cfg.ReviewerName,
		InvoiceType:           app.InvoiceType,
		ItemName:              app.Content,
		TaxRate:               app.TaxRate,
		TaxClassificationCode: app.TaxClassificationCode,
		Remark:                cfg.Remark,
	}
}

func mapInvoiceApplication(app *dbent.InvoiceApplication, rows []*dbent.InvoiceApplicationOrder) *InvoiceApplicationResponse {
	out := &InvoiceApplicationResponse{
		ID:                    app.ID,
		UserID:                app.UserID,
		UserEmail:             app.UserEmail,
		BuyerType:             app.BuyerType,
		BuyerName:             app.BuyerName,
		BuyerTaxNo:            app.BuyerTaxNo,
		BuyerEmail:            app.BuyerEmail,
		BuyerPhone:            app.BuyerPhone,
		BuyerAddress:          app.BuyerAddress,
		BuyerBankName:         app.BuyerBankName,
		BuyerBankAccount:      app.BuyerBankAccount,
		InvoiceAmount:         roundMoney(app.InvoiceAmount),
		InvoiceType:           app.InvoiceType,
		Content:               app.Content,
		TaxRate:               app.TaxRate,
		TaxClassificationCode: app.TaxClassificationCode,
		Status:                app.Status,
		Provider:              app.Provider,
		ProviderOrderID:       app.ProviderOrderID,
		ProviderOrderNo:       app.ProviderOrderNo,
		InvoiceCode:           app.InvoiceCode,
		InvoiceNo:             app.InvoiceNo,
		IssuedAt:              app.IssuedAt,
		LastErrorCode:         app.LastErrorCode,
		LastErrorMessage:      app.LastErrorMessage,
		RetryCount:            app.RetryCount,
		SubmittedAt:           app.SubmittedAt,
		CreatedAt:             app.CreatedAt,
		UpdatedAt:             app.UpdatedAt,
		Orders:                make([]InvoiceOrderSnapshot, 0, len(rows)),
	}
	for _, row := range rows {
		out.Orders = append(out.Orders, InvoiceOrderSnapshot{
			OrderID:       row.PaymentOrderID,
			OutTradeNo:    row.OutTradeNo,
			OrderType:     row.OrderType,
			OrderAmount:   roundMoney(row.OrderAmount),
			PayAmount:     roundMoney(row.PayAmount),
			RefundAmount:  roundMoney(row.RefundAmount),
			InvoiceAmount: roundMoney(row.InvoiceAmount),
			CreatedAt:     row.CreatedAt,
		})
	}
	return out
}

func invoiceFileData(app *dbent.InvoiceApplication, fileType string) string {
	switch fileType {
	case "pdf":
		return app.PdfFileData
	case "ofd":
		return app.OfdFileData
	case "xml":
		return app.XMLFileData
	default:
		return ""
	}
}

func invoiceContentType(fileType string) string {
	switch fileType {
	case "pdf":
		return "application/pdf"
	case "ofd":
		return "application/octet-stream"
	case "xml":
		return "application/xml"
	default:
		return "application/octet-stream"
	}
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
