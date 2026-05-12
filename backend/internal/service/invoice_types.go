package service

import "time"

const (
	SettingKeyInvoiceConfig = "invoice_config"

	InvoiceProviderMock    = "mock"
	InvoiceProviderLexiang = "lexiang"

	InvoiceStatusSubmitted  = "SUBMITTED"
	InvoiceStatusProcessing = "PROCESSING"
	InvoiceStatusIssued     = "ISSUED"
	InvoiceStatusFailed     = "FAILED"
	InvoiceStatusCancelled  = "CANCELLED"

	InvoiceBuyerTypeIndividual = "individual"
	InvoiceBuyerTypeEnterprise = "enterprise"
)

type InvoiceConfig struct {
	Enabled               bool              `json:"enabled"`
	Provider              string            `json:"provider"`
	AutoIssueEnabled      bool              `json:"auto_issue_enabled"`
	SellerName            string            `json:"seller_name"`
	SellerTaxNo           string            `json:"seller_tax_no"`
	SellerAddress         string            `json:"seller_address"`
	SellerPhone           string            `json:"seller_phone"`
	SellerBankName        string            `json:"seller_bank_name"`
	SellerBankAccount     string            `json:"seller_bank_account"`
	DrawerName            string            `json:"drawer_name"`
	PayeeName             string            `json:"payee_name"`
	ReviewerName          string            `json:"reviewer_name"`
	DefaultInvoiceType    string            `json:"default_invoice_type"`
	ItemName              string            `json:"item_name"`
	TaxRate               string            `json:"tax_rate"`
	TaxClassificationCode string            `json:"tax_classification_code"`
	Remark                string            `json:"remark"`
	ProviderConfig        map[string]string `json:"provider_config"`
}

type InvoicePublicConfigResponse struct {
	Enabled          bool `json:"enabled"`
	AutoIssueEnabled bool `json:"auto_issue_enabled"`
}

type InvoiceOrderSnapshot struct {
	OrderID       int64     `json:"order_id"`
	OutTradeNo    string    `json:"out_trade_no"`
	OrderType     string    `json:"order_type"`
	OrderAmount   float64   `json:"order_amount"`
	PayAmount     float64   `json:"pay_amount"`
	RefundAmount  float64   `json:"refund_amount"`
	InvoiceAmount float64   `json:"invoice_amount"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateInvoiceApplicationRequest struct {
	OrderIDs         []int64 `json:"order_ids"`
	BuyerType        string  `json:"buyer_type"`
	BuyerName        string  `json:"buyer_name"`
	BuyerTaxNo       string  `json:"buyer_tax_no"`
	BuyerEmail       string  `json:"buyer_email"`
	BuyerPhone       string  `json:"buyer_phone"`
	BuyerAddress     string  `json:"buyer_address"`
	BuyerBankName    string  `json:"buyer_bank_name"`
	BuyerBankAccount string  `json:"buyer_bank_account"`
	Remark           string  `json:"remark"`
}

type InvoiceApplicationResponse struct {
	ID                    int64                  `json:"id"`
	UserID                int64                  `json:"user_id"`
	UserEmail             string                 `json:"user_email"`
	BuyerType             string                 `json:"buyer_type"`
	BuyerName             string                 `json:"buyer_name"`
	BuyerTaxNo            string                 `json:"buyer_tax_no"`
	BuyerEmail            string                 `json:"buyer_email"`
	BuyerPhone            string                 `json:"buyer_phone"`
	BuyerAddress          string                 `json:"buyer_address"`
	BuyerBankName         string                 `json:"buyer_bank_name"`
	BuyerBankAccount      string                 `json:"buyer_bank_account"`
	InvoiceAmount         float64                `json:"invoice_amount"`
	InvoiceType           string                 `json:"invoice_type"`
	Content               string                 `json:"content"`
	TaxRate               string                 `json:"tax_rate"`
	TaxClassificationCode string                 `json:"tax_classification_code"`
	Status                string                 `json:"status"`
	Provider              string                 `json:"provider"`
	ProviderOrderID       string                 `json:"provider_order_id"`
	ProviderOrderNo       string                 `json:"provider_order_no"`
	InvoiceCode           string                 `json:"invoice_code"`
	InvoiceNo             string                 `json:"invoice_no"`
	IssuedAt              *time.Time             `json:"issued_at,omitempty"`
	LastErrorCode         string                 `json:"last_error_code"`
	LastErrorMessage      string                 `json:"last_error_message"`
	RetryCount            int                    `json:"retry_count"`
	Orders                []InvoiceOrderSnapshot `json:"orders,omitempty"`
	SubmittedAt           *time.Time             `json:"submitted_at,omitempty"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

type InvoiceEligibleOrderResponse struct {
	OrderID      int64     `json:"order_id"`
	OutTradeNo   string    `json:"out_trade_no"`
	OrderType    string    `json:"order_type"`
	PayAmount    float64   `json:"pay_amount"`
	RefundAmount float64   `json:"refund_amount"`
	CreatedAt    time.Time `json:"created_at"`
}

type InvoiceProviderCreateRequest struct {
	ApplicationID         int64
	LocalOrderNo          string
	InvoiceAmount         float64
	BuyerType             string
	BuyerName             string
	BuyerTaxNo            string
	BuyerEmail            string
	BuyerPhone            string
	BuyerAddress          string
	BuyerBankName         string
	BuyerBankAccount      string
	SellerName            string
	SellerTaxNo           string
	SellerAddress         string
	SellerPhone           string
	SellerBankName        string
	SellerBankAccount     string
	DrawerName            string
	PayeeName             string
	ReviewerName          string
	InvoiceType           string
	ItemName              string
	TaxRate               string
	TaxClassificationCode string
	Remark                string
}

type InvoiceProviderCreateResult struct {
	ProviderOrderID string         `json:"provider_order_id"`
	ProviderOrderNo string         `json:"provider_order_no"`
	Raw             map[string]any `json:"raw,omitempty"`
}

type InvoiceProviderStatusResult struct {
	Status          string         `json:"status"`
	ProviderOrderID string         `json:"provider_order_id"`
	ProviderOrderNo string         `json:"provider_order_no"`
	InvoiceCode     string         `json:"invoice_code"`
	InvoiceNo       string         `json:"invoice_no"`
	IssuedAt        *time.Time     `json:"issued_at,omitempty"`
	ErrorCode       string         `json:"error_code"`
	ErrorMessage    string         `json:"error_message"`
	Raw             map[string]any `json:"raw,omitempty"`
}

type InvoiceProviderFile struct {
	FileType string
	Base64   string
}
