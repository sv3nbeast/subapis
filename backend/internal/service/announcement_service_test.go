package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type announcementRepoStub struct {
	item   *Announcement
	active []Announcement
}

func (s *announcementRepoStub) Create(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (s *announcementRepoStub) GetByID(_ context.Context, _ int64) (*Announcement, error) {
	if s.item == nil {
		return nil, ErrAnnouncementNotFound
	}
	return s.item, nil
}

func (s *announcementRepoStub) Update(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (*announcementRepoStub) Delete(context.Context, int64) error {
	return nil
}

func (*announcementRepoStub) List(context.Context, pagination.PaginationParams, AnnouncementListFilters) ([]Announcement, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (s *announcementRepoStub) ListActive(context.Context, time.Time) ([]Announcement, error) {
	return s.active, nil
}

func TestAnnouncementServiceCreateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)

	_, err := svc.Create(context.Background(), &CreateAnnouncementInput{
		Title:      "公告",
		Content:    "内容",
		Status:     AnnouncementStatusActive,
		NotifyMode: AnnouncementNotifyModePopup,
		StartsAt:   &now,
		EndsAt:     &now,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}

func TestAnnouncementServiceUpdateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{
		item: &Announcement{
			ID:         1,
			Title:      "公告",
			Content:    "内容",
			Status:     AnnouncementStatusActive,
			NotifyMode: AnnouncementNotifyModePopup,
		},
	}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)
	startsAt := &now
	endsAt := &now

	_, err := svc.Update(context.Background(), 1, &UpdateAnnouncementInput{
		StartsAt: &startsAt,
		EndsAt:   &endsAt,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}

func TestAnnouncementServiceListPublicReturnsOnlyUntargetedActiveAnnouncements(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	repo := &announcementRepoStub{
		active: []Announcement{
			{
				ID:         1,
				Title:      "public old",
				Content:    "visible",
				Status:     AnnouncementStatusActive,
				NotifyMode: AnnouncementNotifyModeSilent,
				StartsAt:   &past,
				EndsAt:     &future,
				CreatedAt:  past,
				UpdatedAt:  past,
			},
			{
				ID:         2,
				Title:      "targeted",
				Content:    "hidden before login",
				Status:     AnnouncementStatusActive,
				NotifyMode: AnnouncementNotifyModePopup,
				Targeting: AnnouncementTargeting{
					AnyOf: []AnnouncementConditionGroup{
						{
							AllOf: []AnnouncementCondition{
								{
									Type:     AnnouncementConditionTypeBalance,
									Operator: AnnouncementOperatorGT,
									Value:    0,
								},
							},
						},
					},
				},
				StartsAt:  &past,
				EndsAt:    &future,
				CreatedAt: past,
				UpdatedAt: past,
			},
			{
				ID:         3,
				Title:      "public newest",
				Content:    "visible",
				Status:     AnnouncementStatusActive,
				NotifyMode: AnnouncementNotifyModePopup,
				StartsAt:   &past,
				EndsAt:     &future,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
			{
				ID:         4,
				Title:      "expired",
				Content:    "hidden",
				Status:     AnnouncementStatusActive,
				NotifyMode: AnnouncementNotifyModeSilent,
				StartsAt:   &past,
				EndsAt:     &past,
				CreatedAt:  past,
				UpdatedAt:  past,
			},
		},
	}
	svc := NewAnnouncementService(repo, nil, nil, nil)

	items, err := svc.ListPublic(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, int64(3), items[0].ID)
	require.Equal(t, int64(1), items[1].ID)
}
