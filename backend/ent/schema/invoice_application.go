package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InvoiceApplication stores user-submitted invoice requests.
type InvoiceApplication struct {
	ent.Schema
}

func (InvoiceApplication) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "invoice_applications"},
	}
}

func (InvoiceApplication) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.String("user_email").
			MaxLen(255).
			Default(""),
		field.String("buyer_type").
			MaxLen(20).
			Default("individual"),
		field.String("buyer_name").
			MaxLen(255),
		field.String("buyer_tax_no").
			MaxLen(255).
			Default(""),
		field.String("buyer_email").
			MaxLen(255).
			Default(""),
		field.String("buyer_phone").
			MaxLen(50).
			Default(""),
		field.String("buyer_address").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.String("buyer_bank_name").
			MaxLen(255).
			Default(""),
		field.String("buyer_bank_account").
			MaxLen(255).
			Default(""),
		field.Float("invoice_amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}),
		field.String("invoice_type").
			MaxLen(50).
			Default("digital_normal"),
		field.String("content").
			MaxLen(255).
			Default(""),
		field.String("tax_rate").
			MaxLen(20).
			Default(""),
		field.String("tax_classification_code").
			MaxLen(64).
			Default(""),
		field.String("status").
			MaxLen(30).
			Default("SUBMITTED"),
		field.String("provider").
			MaxLen(50).
			Default(""),
		field.String("provider_order_id").
			MaxLen(128).
			Default(""),
		field.String("provider_order_no").
			MaxLen(128).
			Default(""),
		field.String("invoice_code").
			MaxLen(128).
			Default(""),
		field.String("invoice_no").
			MaxLen(128).
			Default(""),
		field.Time("issued_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("last_error_code").
			MaxLen(64).
			Default(""),
		field.String("last_error_message").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.JSON("request_payload_snapshot", map[string]any{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("response_payload_snapshot", map[string]any{}).
			Optional().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Int("retry_count").
			Default(0),
		field.String("pdf_file_data").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.String("ofd_file_data").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.String("xml_file_data").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.Time("submitted_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (InvoiceApplication) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("invoice_applications").
			Field("user_id").
			Unique().
			Required(),
		edge.To("orders", InvoiceApplicationOrder.Type),
	}
}

func (InvoiceApplication) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("status"),
		index.Fields("provider_order_id"),
		index.Fields("created_at"),
	}
}
