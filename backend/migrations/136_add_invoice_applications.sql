-- User invoice applications and multi-order invoice links.

INSERT INTO settings (key, value, updated_at)
VALUES (
    'invoice_config',
    '{"enabled":false,"provider":"","auto_issue_enabled":true,"seller_name":"","seller_tax_no":"","seller_address":"","seller_phone":"","seller_bank_name":"","seller_bank_account":"","drawer_name":"","payee_name":"","reviewer_name":"","default_invoice_type":"digital_normal","item_name":"技术服务费","tax_rate":"6","tax_classification_code":"","remark":"","provider_config":{}}',
    NOW()
)
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS invoice_applications (
    id                         BIGSERIAL PRIMARY KEY,
    user_id                    BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    user_email                 VARCHAR(255) NOT NULL DEFAULT '',
    buyer_type                 VARCHAR(20) NOT NULL DEFAULT 'individual',
    buyer_name                 VARCHAR(255) NOT NULL,
    buyer_tax_no               VARCHAR(255) NOT NULL DEFAULT '',
    buyer_email                VARCHAR(255) NOT NULL DEFAULT '',
    buyer_phone                VARCHAR(50) NOT NULL DEFAULT '',
    buyer_address              TEXT NOT NULL DEFAULT '',
    buyer_bank_name            VARCHAR(255) NOT NULL DEFAULT '',
    buyer_bank_account         VARCHAR(255) NOT NULL DEFAULT '',
    invoice_amount             DECIMAL(20,2) NOT NULL,
    invoice_type               VARCHAR(50) NOT NULL DEFAULT 'digital_normal',
    content                    VARCHAR(255) NOT NULL DEFAULT '',
    tax_rate                   VARCHAR(20) NOT NULL DEFAULT '',
    tax_classification_code    VARCHAR(64) NOT NULL DEFAULT '',
    status                     VARCHAR(30) NOT NULL DEFAULT 'SUBMITTED',
    provider                   VARCHAR(50) NOT NULL DEFAULT '',
    provider_order_id          VARCHAR(128) NOT NULL DEFAULT '',
    provider_order_no          VARCHAR(128) NOT NULL DEFAULT '',
    invoice_code               VARCHAR(128) NOT NULL DEFAULT '',
    invoice_no                 VARCHAR(128) NOT NULL DEFAULT '',
    issued_at                  TIMESTAMPTZ,
    last_error_code            VARCHAR(64) NOT NULL DEFAULT '',
    last_error_message         TEXT NOT NULL DEFAULT '',
    request_payload_snapshot   JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_payload_snapshot  JSONB NOT NULL DEFAULT '{}'::jsonb,
    retry_count                INT NOT NULL DEFAULT 0,
    pdf_file_data              TEXT NOT NULL DEFAULT '',
    ofd_file_data              TEXT NOT NULL DEFAULT '',
    xml_file_data              TEXT NOT NULL DEFAULT '',
    submitted_at               TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS invoice_application_orders (
    id                         BIGSERIAL PRIMARY KEY,
    invoice_application_id     BIGINT NOT NULL REFERENCES invoice_applications(id) ON DELETE CASCADE,
    user_id                    BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    payment_order_id           BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    out_trade_no               VARCHAR(64) NOT NULL DEFAULT '',
    order_type                 VARCHAR(20) NOT NULL DEFAULT '',
    order_amount               DECIMAL(20,2) NOT NULL DEFAULT 0,
    pay_amount                 DECIMAL(20,2) NOT NULL DEFAULT 0,
    refund_amount              DECIMAL(20,2) NOT NULL DEFAULT 0,
    invoice_amount             DECIMAL(20,2) NOT NULL DEFAULT 0,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invoice_applications_user_id ON invoice_applications(user_id);
CREATE INDEX IF NOT EXISTS idx_invoice_applications_status ON invoice_applications(status);
CREATE INDEX IF NOT EXISTS idx_invoice_applications_provider_order_id ON invoice_applications(provider_order_id);
CREATE INDEX IF NOT EXISTS idx_invoice_applications_created_at ON invoice_applications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_invoice_application_orders_application_id ON invoice_application_orders(invoice_application_id);
CREATE INDEX IF NOT EXISTS idx_invoice_application_orders_user_id ON invoice_application_orders(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoice_application_orders_payment_order_id ON invoice_application_orders(payment_order_id);
