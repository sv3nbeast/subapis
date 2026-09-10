//go:build integration

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type usageLogStreamFixture struct {
	user    *service.User
	apiKey  *service.APIKey
	filters usagestats.UsageLogFilters
	// matching is the number of rows that satisfy filters (model-a within the range).
	matching int
}

// seedUsageLogStreamFixture inserts many rows for one user that collide on
// created_at (only seven distinct timestamps) so keyset paging must rely on the id
// tie-breaker, plus rows outside the filter (other model, other user, other range).
func (s *UsageLogRepoSuite) seedUsageLogStreamFixture(total int) usageLogStreamFixture {
	user := mustCreateUser(s.T(), s.client, &service.User{Email: "usage-stream-" + uuid.NewString() + "@example.com"})
	other := mustCreateUser(s.T(), s.client, &service.User{Email: "usage-stream-other-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-stream-" + uuid.NewString(), Name: "stream-key"})
	otherKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: other.ID, Key: "sk-stream-other-" + uuid.NewString(), Name: "other-key"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "usage-stream-account-" + uuid.NewString()})

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	matching := 0
	for i := 0; i < total; i++ {
		model := "model-a"
		if i%3 == 0 {
			model = "model-b"
		} else {
			matching++
		}
		_, err := s.repo.Create(s.ctx, &service.UsageLog{
			UserID:         user.ID,
			APIKeyID:       apiKey.ID,
			AccountID:      account.ID,
			RequestID:      uuid.New().String(),
			Model:          model,
			RequestedModel: model,
			InputTokens:    i,
			OutputTokens:   1,
			TotalCost:      0.001,
			ActualCost:     0.001,
			RequestType:    service.RequestTypeStream,
			CreatedAt:      base.Add(time.Duration(i%7) * time.Minute),
		})
		s.Require().NoError(err)
	}
	// Out-of-range row for the same user and a row for another user must never leak.
	for _, extra := range []*service.UsageLog{
		{UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID, Model: "model-a", RequestedModel: "model-a", CreatedAt: base.AddDate(0, 0, 10)},
		{UserID: other.ID, APIKeyID: otherKey.ID, AccountID: account.ID, Model: "model-a", RequestedModel: "model-a", CreatedAt: base},
	} {
		extra.RequestID = uuid.New().String()
		extra.OutputTokens = 1
		extra.TotalCost = 0.001
		extra.ActualCost = 0.001
		_, err := s.repo.Create(s.ctx, extra)
		s.Require().NoError(err)
	}

	start := base.Add(-time.Hour)
	end := base.AddDate(0, 0, 1)
	return usageLogStreamFixture{
		user:   user,
		apiKey: apiKey,
		filters: usagestats.UsageLogFilters{
			UserID:            user.ID,
			Model:             "model-a",
			ModelFilterSource: usagestats.ModelSourceRequested,
			StartTime:         &start,
			EndTime:           &end,
		},
		matching: matching,
	}
}

func (s *UsageLogRepoSuite) listAllIDs(filters usagestats.UsageLogFilters, sortBy, sortOrder string) []int64 {
	var ids []int64
	for page := 1; ; page++ {
		logs, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: page, PageSize: 1000, SortBy: sortBy, SortOrder: sortOrder}, filters)
		s.Require().NoError(err)
		if len(logs) == 0 {
			return ids
		}
		for i := range logs {
			ids = append(ids, logs[i].ID)
		}
		if len(logs) < 1000 {
			return ids
		}
	}
}

func (s *UsageLogRepoSuite) TestStreamWithFilters_MatchesListAcrossKeysetBatchesWithTies() {
	fixture := s.seedUsageLogStreamFixture(1205)
	expected := s.listAllIDs(fixture.filters, "created_at", "desc")
	s.Require().Len(expected, fixture.matching)

	var (
		streamed []int64
		batches  int
		names    = map[string]int{}
	)
	err := s.repo.StreamWithFilters(s.ctx, fixture.filters, usagestats.UsageLogStreamOptions{BatchSize: 100}, func(batch []service.UsageLog) error {
		batches++
		s.Require().LessOrEqual(len(batch), 100)
		for i := range batch {
			streamed = append(streamed, batch[i].ID)
			if batch[i].APIKey != nil {
				names[batch[i].APIKey.Name]++
			}
		}
		return nil
	})
	s.Require().NoError(err)

	s.Require().Equal(expected, streamed, "stream must deliver exactly the list rows in the same order without skips or duplicates")
	s.Require().Equal((fixture.matching+99)/100, batches)
	s.Require().Equal(fixture.matching, names["stream-key"], "API key association must be hydrated for every row")
}

func (s *UsageLogRepoSuite) TestStreamWithFilters_AscendingAndIDOrders() {
	fixture := s.seedUsageLogStreamFixture(350)

	collect := func(opts usagestats.UsageLogStreamOptions) []int64 {
		var ids []int64
		err := s.repo.StreamWithFilters(s.ctx, fixture.filters, opts, func(batch []service.UsageLog) error {
			for i := range batch {
				ids = append(ids, batch[i].ID)
			}
			return nil
		})
		s.Require().NoError(err)
		return ids
	}

	s.Require().Equal(s.listAllIDs(fixture.filters, "created_at", "asc"), collect(usagestats.UsageLogStreamOptions{SortBy: "created_at", SortOrder: "asc", BatchSize: 100}))
	s.Require().Equal(s.listAllIDs(fixture.filters, "id", "desc"), collect(usagestats.UsageLogStreamOptions{SortBy: "id", SortOrder: "desc", BatchSize: 100}))
	s.Require().Equal(s.listAllIDs(fixture.filters, "id", "asc"), collect(usagestats.UsageLogStreamOptions{SortBy: "id", SortOrder: "asc", BatchSize: 100}))
}

func (s *UsageLogRepoSuite) TestStreamWithFilters_RowLimitGuard() {
	fixture := s.seedUsageLogStreamFixture(120)

	called := false
	err := s.repo.StreamWithFilters(s.ctx, fixture.filters, usagestats.UsageLogStreamOptions{BatchSize: 100, MaxRows: int64(fixture.matching - 1)}, func([]service.UsageLog) error {
		called = true
		return nil
	})
	s.Require().ErrorIs(err, usagestats.ErrUsageLogStreamTooLarge)
	s.Require().False(called, "the guard must trip before any batch is delivered")

	delivered := 0
	err = s.repo.StreamWithFilters(s.ctx, fixture.filters, usagestats.UsageLogStreamOptions{BatchSize: 100, MaxRows: int64(fixture.matching)}, func(batch []service.UsageLog) error {
		delivered += len(batch)
		return nil
	})
	s.Require().NoError(err)
	s.Require().Equal(fixture.matching, delivered)
}

func (s *UsageLogRepoSuite) TestStreamWithFilters_StopsOnHandlerErrorAndCancellation() {
	fixture := s.seedUsageLogStreamFixture(350)

	handlerErr := errors.New("client gone")
	batches := 0
	err := s.repo.StreamWithFilters(s.ctx, fixture.filters, usagestats.UsageLogStreamOptions{BatchSize: 100}, func([]service.UsageLog) error {
		batches++
		return handlerErr
	})
	s.Require().ErrorIs(err, handlerErr)
	s.Require().Equal(1, batches)

	ctx, cancel := context.WithCancel(s.ctx)
	batches = 0
	err = s.repo.StreamWithFilters(ctx, fixture.filters, usagestats.UsageLogStreamOptions{BatchSize: 100}, func([]service.UsageLog) error {
		batches++
		cancel()
		return nil
	})
	s.Require().ErrorIs(err, context.Canceled)
	s.Require().Equal(1, batches, "cancellation must be observed before the next batch is queried")
}
