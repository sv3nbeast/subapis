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

// InvoiceApplicationOrder links invoice applications to paid orders.
type InvoiceApplicationOrder struct {
	ent.Schema
}

func (InvoiceApplicationOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "invoice_application_orders"},
	}
}

func (InvoiceApplicationOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("invoice_application_id"),
		field.Int64("user_id"),
		field.Int64("payment_order_id"),
		field.String("out_trade_no").
			MaxLen(64).
			Default(""),
		field.String("order_type").
			MaxLen(20).
			Default(""),
		field.Float("order_amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).
			Default(0),
		field.Float("pay_amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).
			Default(0),
		field.Float("refund_amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).
			Default(0),
		field.Float("invoice_amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).
			Default(0),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (InvoiceApplicationOrder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("invoice_application", InvoiceApplication.Type).
			Ref("orders").
			Field("invoice_application_id").
			Unique().
			Required(),
		edge.From("user", User.Type).
			Ref("invoice_application_orders").
			Field("user_id").
			Unique().
			Required(),
		edge.From("payment_order", PaymentOrder.Type).
			Ref("invoice_application_order").
			Field("payment_order_id").
			Unique().
			Required(),
	}
}

func (InvoiceApplicationOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("invoice_application_id"),
		index.Fields("user_id"),
		index.Fields("payment_order_id").Unique(),
	}
}
