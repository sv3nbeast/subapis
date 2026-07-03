/**
 * Payment System Type Definitions
 */

// ==================== Enums / Union Types ====================

export type OrderStatus =
  | 'PENDING'
  | 'PAID'
  | 'RECHARGING'
  | 'COMPLETED'
  | 'EXPIRED'
  | 'CANCELLED'
  | 'FAILED'
  | 'REFUND_REQUESTED'
  | 'REFUNDING'
  | 'REFUND_PENDING'
  | 'PARTIALLY_REFUNDED'
  | 'REFUNDED'
  | 'REFUND_FAILED'

export type PaymentType = 'alipay' | 'wxpay' | 'alipay_direct' | 'wxpay_direct' | 'stripe' | 'easypay' | 'airwallex'

export type OrderType = 'balance' | 'subscription'

export type InvoiceBuyerType = 'individual' | 'enterprise'

export type InvoiceStatus = 'SUBMITTED' | 'PROCESSING' | 'ISSUED' | 'FAILED' | 'CANCELLED'

// ==================== Configuration ====================

export interface PaymentConfig {
  payment_enabled: boolean
  min_amount: number
  max_amount: number
  daily_limit: number
  max_pending_orders: number
  order_timeout_minutes: number
  balance_disabled: boolean
  balance_recharge_multiplier: number
  enabled_payment_types: PaymentType[]
  help_image_url: string
  help_text: string
  stripe_publishable_key: string
}

export interface MethodLimit {
  currency?: string
  daily_limit: number
  daily_used: number
  daily_remaining: number
  single_min: number
  single_max: number
  fee_rate: number
  available: boolean
}

/** Response from /payment/limits API */
export interface MethodLimitsResponse {
  methods: Record<string, MethodLimit>
  global_min: number  // widest min across all methods; 0 = no minimum
  global_max: number  // widest max across all methods; 0 = no maximum
}

/** Response from /payment/checkout-info API — single call for the payment page */
export interface CheckoutInfoResponse {
  methods: Record<string, MethodLimit>
  global_min: number
  global_max: number
  plans: SubscriptionPlan[]
  balance_disabled: boolean
  balance_recharge_multiplier: number
  recharge_fee_rate: number
  help_text: string
  help_image_url: string
  stripe_publishable_key: string
  /** When true, Alipay payments on mobile always show the QR code instead of redirecting */
  alipay_force_qrcode?: boolean
}

// ==================== Orders ====================

export interface PaymentOrder {
  id: number
  user_id: number
  amount: number
  pay_amount: number
  currency?: string
  fee_rate: number
  payment_type: string
  out_trade_no: string
  status: OrderStatus
  order_type: OrderType
  created_at: string
  expires_at: string
  paid_at?: string
  completed_at?: string
  refund_amount: number
  refund_reason?: string
  refund_requested_at?: string
  refund_requested_by?: number
  refund_request_reason?: string
  plan_id?: number
  provider_instance_id?: string
}

// ==================== Invoices ====================

export interface InvoiceEligibleOrder {
  order_id: number
  out_trade_no: string
  order_type: OrderType
  pay_amount: number
  refund_amount: number
  created_at: string
}

export interface InvoiceOrderSnapshot {
  order_id: number
  out_trade_no: string
  order_type: OrderType
  order_amount: number
  pay_amount: number
  refund_amount: number
  invoice_amount: number
  created_at: string
}

export interface InvoiceApplication {
  id: number
  user_id: number
  user_email: string
  buyer_type: InvoiceBuyerType
  buyer_name: string
  buyer_tax_no: string
  buyer_email: string
  buyer_phone: string
  buyer_address: string
  buyer_bank_name: string
  buyer_bank_account: string
  invoice_amount: number
  invoice_type: string
  content: string
  tax_rate: string
  tax_classification_code: string
  status: InvoiceStatus
  provider: string
  provider_order_id: string
  provider_order_no: string
  invoice_code: string
  invoice_no: string
  issued_at?: string
  last_error_code: string
  last_error_message: string
  retry_count: number
  orders?: InvoiceOrderSnapshot[]
  submitted_at?: string
  created_at: string
  updated_at: string
}

export interface CreateInvoiceApplicationRequest {
  order_ids: number[]
  buyer_type: InvoiceBuyerType
  buyer_name: string
  buyer_tax_no?: string
  buyer_email?: string
  buyer_phone?: string
  buyer_address?: string
  buyer_bank_name?: string
  buyer_bank_account?: string
  remark?: string
}

export interface InvoicePublicConfig {
  enabled: boolean
  auto_issue_enabled: boolean
}

export type InvoiceProvider = '' | 'mock' | 'lexiang'

export interface InvoiceConfig {
  enabled: boolean
  provider: InvoiceProvider
  auto_issue_enabled: boolean
  seller_name: string
  seller_tax_no: string
  seller_address: string
  seller_phone: string
  seller_bank_name: string
  seller_bank_account: string
  drawer_name: string
  payee_name: string
  reviewer_name: string
  default_invoice_type: string
  item_name: string
  tax_rate: string
  tax_classification_code: string
  remark: string
  provider_config: Record<string, string>
}

// ==================== Plans & Channels ====================

export interface SubscriptionPlan {
  id: number
  group_id: number
  group_platform?: string
  group_name?: string
  rate_multiplier?: number
  peak_rate_enabled?: boolean
  peak_start?: string
  peak_end?: string
  peak_rate_multiplier?: number
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
  supported_model_scopes?: string[]
  name: string
  description: string
  price: number
  original_price?: number
  validity_days: number
  validity_unit: string
  /** Stored as JSON string in backend; API layer should parse before use */
  features: string[]
  for_sale: boolean
  sort_order: number
}

export interface PaymentChannel {
  id: number
  group_id?: number
  name: string
  platform: string
  rate_multiplier: number
  description: string
  models: string[]
  features: string[]
  enabled: boolean
}

// ==================== Providers ====================

export interface ProviderInstance {
  id: number
  provider_key: string
  name: string
  config: Record<string, string>
  supported_types: string[]
  enabled: boolean
  payment_mode: string
  refund_enabled: boolean
  allow_user_refund: boolean
  limits: string
  sort_order: number
}

// ==================== Request / Response ====================

export interface CreateOrderRequest {
  amount: number
  payment_type: string
  order_type: string
  plan_id?: number
  return_url?: string
  payment_source?: string
  openid?: string
  wechat_resume_token?: string
  is_mobile?: boolean
}

export type CreateOrderResultType = 'order_created' | 'oauth_required' | 'jsapi_ready'

export interface WechatOAuthInfo {
  authorize_url?: string
  appid?: string
  openid?: string
  scope?: string
  state?: string
  redirect_url?: string
}

export interface WechatJSAPIPayload {
  appId?: string
  timeStamp?: string
  nonceStr?: string
  package?: string
  signType?: string
  paySign?: string
}

export interface CreateOrderResult {
  order_id: number
  amount: number
  pay_url?: string
  qr_code?: string
  client_secret?: string
  intent_id?: string
  currency?: string
  country_code?: string
  payment_env?: string
  pay_amount: number
  fee_rate: number
  expires_at: string
  result_type?: CreateOrderResultType
  payment_type?: string
  out_trade_no?: string
  payment_mode?: string
  resume_token?: string
  oauth?: WechatOAuthInfo
  jsapi?: WechatJSAPIPayload
  jsapi_payload?: WechatJSAPIPayload
}

export interface DashboardStats {
  today_amount: number
  total_amount: number
  today_count: number
  total_count: number
  avg_amount: number
  daily_series: { date: string; amount: number; count: number }[]
  payment_methods: { type: string; amount: number; count: number }[]
  top_users: { user_id: number; email: string; amount: number }[]
}
