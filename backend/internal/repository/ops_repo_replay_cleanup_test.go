package repository

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestOpsErrorLogInsertDoesNotPersistRequestReplayFields(t *testing.T) {
	disallowedColumns := []string{
		"request_body",
		"request_headers",
		"request_body_truncated",
		"request_body_bytes",
		"is_retryable",
		"retry_count",
		"resolved_retry_id",
	}

	insertSQL := strings.ToLower(insertOpsErrorLogSQL)
	for _, column := range disallowedColumns {
		if strings.Contains(insertSQL, column) {
			t.Fatalf("ops error log insert still references dropped replay column %q", column)
		}
	}

	inputType := reflect.TypeOf(service.OpsInsertErrorLogInput{})
	disallowedFields := []string{
		"RequestBodyJSON",
		"RequestBodyTruncated",
		"RequestBodyBytes",
		"RequestHeadersJSON",
		"IsRetryable",
		"RetryCount",
		"ResolvedRetryID",
	}
	for _, field := range disallowedFields {
		if _, ok := inputType.FieldByName(field); ok {
			t.Fatalf("OpsInsertErrorLogInput still carries replay field %q", field)
		}
	}
}

func TestOpsErrorLogInsertPersistsNetworkErrorType(t *testing.T) {
	if !strings.Contains(strings.ToLower(insertOpsErrorLogSQL), "network_error_type") {
		t.Fatal("ops error insert must persist network_error_type")
	}
	args := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{NetworkErrorType: "proxy_connect"})
	if len(args) != 42 {
		t.Fatalf("ops insert arg count = %d, want 42", len(args))
	}
	value, ok := args[len(args)-1].(sql.NullString)
	if !ok || !value.Valid || value.String != "proxy_connect" {
		t.Fatalf("network_error_type arg = %#v", args[len(args)-1])
	}
}
