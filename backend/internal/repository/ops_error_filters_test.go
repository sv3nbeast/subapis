package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestOpsExcludeUnauthenticatedResponsesProbeClause_WithAlias(t *testing.T) {
	clause := opsExcludeUnauthenticatedResponsesProbeClause("e")
	if !strings.Contains(clause, "COALESCE(e.status_code, 0) = 401") {
		t.Fatalf("clause should reference aliased status_code: %s", clause)
	}
	if !strings.Contains(clause, "COALESCE(e.request_path, '') = '/responses'") {
		t.Fatalf("clause should reference aliased request_path: %s", clause)
	}
}

func TestBuildErrorWhere_ExcludesUnauthenticatedResponsesProbe(t *testing.T) {
	start := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	where, args, _ := buildErrorWhere(&service.OpsDashboardFilter{}, start, end, 1)
	if len(args) != 2 {
		t.Fatalf("args len = %d, want 2", len(args))
	}
	if !strings.Contains(where, "COALESCE(status_code, 0) = 401") {
		t.Fatalf("where should exclude unauthenticated responses probes: %s", where)
	}
	if !strings.Contains(where, "COALESCE(request_path, '') = '/responses'") {
		t.Fatalf("where should constrain /responses path: %s", where)
	}
}
