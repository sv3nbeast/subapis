package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type groupRepoStubForKiroCacheEmulation struct {
	created *Group
	updated *Group
	getByID *Group
}

func (s *groupRepoStubForKiroCacheEmulation) Create(_ context.Context, g *Group) error {
	s.created = g
	g.ID = 1
	return nil
}

func (s *groupRepoStubForKiroCacheEmulation) GetByID(_ context.Context, _ int64) (*Group, error) {
	return s.getByID, nil
}

func (s *groupRepoStubForKiroCacheEmulation) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return s.GetByID(context.Background(), id)
}

func (s *groupRepoStubForKiroCacheEmulation) Update(_ context.Context, g *Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForKiroCacheEmulation) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForKiroCacheEmulation) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForKiroCacheEmulation) List(_ context.Context, _ pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForKiroCacheEmulation) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _ string, _ *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStubForKiroCacheEmulation) ListActive(_ context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForKiroCacheEmulation) ListActiveByPlatform(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForKiroCacheEmulation) ExistsByName(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (s *groupRepoStubForKiroCacheEmulation) GetAccountCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForKiroCacheEmulation) DeleteAccountGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForKiroCacheEmulation) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForKiroCacheEmulation) BindAccountsToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForKiroCacheEmulation) UpdateSortOrders(_ context.Context, _ []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

func TestAdminServiceKiroCacheEmulationCreatePersistsConfig(t *testing.T) {
	repo := &groupRepoStubForKiroCacheEmulation{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                      "kiro-cache",
		Platform:                  PlatformKiro,
		RateMultiplier:            1,
		KiroCacheEmulationEnabled: true,
		KiroCacheEmulationRatio:   0.5,
		KiroEndpointMode:          KiroEndpointModeAuto,
	})
	if err != nil {
		t.Fatalf("CreateGroup error: %v", err)
	}
	if group == nil || repo.created == nil {
		t.Fatal("expected created group")
	}
	if !repo.created.KiroCacheEmulationEnabled {
		t.Fatal("expected Kiro cache emulation to be enabled")
	}
	if repo.created.KiroCacheEmulationRatio != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", repo.created.KiroCacheEmulationRatio)
	}
	if !repo.created.KiroAutoStickyEnabled {
		t.Fatal("expected Kiro auto sticky to default enabled")
	}
	if repo.created.KiroStickySessionTTLSeconds != DefaultKiroStickySessionTTLSeconds {
		t.Fatalf("ttl = %v, want %v", repo.created.KiroStickySessionTTLSeconds, DefaultKiroStickySessionTTLSeconds)
	}
	if repo.created.KiroEndpointMode != KiroEndpointModeAuto {
		t.Fatalf("endpoint mode = %q, want %q", repo.created.KiroEndpointMode, KiroEndpointModeAuto)
	}
}

func TestAdminServiceKiroCacheEmulationUpdatePersistsConfig(t *testing.T) {
	repo := &groupRepoStubForKiroCacheEmulation{
		getByID: &Group{
			ID:                      7,
			Name:                    "kiro-cache",
			Platform:                PlatformKiro,
			RateMultiplier:          1,
			Status:                  StatusActive,
			SubscriptionType:        SubscriptionTypeStandard,
			KiroCacheEmulationRatio: 1,
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}
	enabled := true
	ratio := 0.75
	endpointMode := KiroEndpointModeKRS

	group, err := svc.UpdateGroup(context.Background(), 7, &UpdateGroupInput{
		KiroCacheEmulationEnabled: &enabled,
		KiroCacheEmulationRatio:   &ratio,
		KiroEndpointMode:          &endpointMode,
	})
	if err != nil {
		t.Fatalf("UpdateGroup error: %v", err)
	}
	if group == nil || repo.updated == nil {
		t.Fatal("expected updated group")
	}
	if !repo.updated.KiroCacheEmulationEnabled {
		t.Fatal("expected Kiro cache emulation to be enabled")
	}
	if repo.updated.KiroCacheEmulationRatio != 0.75 {
		t.Fatalf("ratio = %v, want 0.75", repo.updated.KiroCacheEmulationRatio)
	}
	if repo.updated.KiroEndpointMode != KiroEndpointModeKRS {
		t.Fatalf("endpoint mode = %q, want %q", repo.updated.KiroEndpointMode, KiroEndpointModeKRS)
	}
}

func TestAdminServiceKiroStickyUpdatePersistsConfig(t *testing.T) {
	repo := &groupRepoStubForKiroCacheEmulation{
		getByID: &Group{
			ID:                          7,
			Name:                        "kiro-cache",
			Platform:                    PlatformKiro,
			RateMultiplier:              1,
			Status:                      StatusActive,
			SubscriptionType:            SubscriptionTypeStandard,
			KiroAutoStickyEnabled:       true,
			KiroStickySessionTTLSeconds: DefaultKiroStickySessionTTLSeconds,
			KiroCacheEmulationRatio:     1,
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}
	autoSticky := false
	ttl := 30

	group, err := svc.UpdateGroup(context.Background(), 7, &UpdateGroupInput{
		KiroAutoStickyEnabled:       &autoSticky,
		KiroStickySessionTTLSeconds: &ttl,
	})
	if err != nil {
		t.Fatalf("UpdateGroup error: %v", err)
	}
	if group == nil || repo.updated == nil {
		t.Fatal("expected updated group")
	}
	if repo.updated.KiroAutoStickyEnabled {
		t.Fatal("expected Kiro auto sticky to be disabled")
	}
	if repo.updated.KiroStickySessionTTLSeconds != MinKiroStickySessionTTLSeconds {
		t.Fatalf("ttl = %v, want %v", repo.updated.KiroStickySessionTTLSeconds, MinKiroStickySessionTTLSeconds)
	}
}

func TestAdminServiceKiroCacheEmulationDisabledForNonKiro(t *testing.T) {
	repo := &groupRepoStubForKiroCacheEmulation{}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                      "openai-cache",
		Platform:                  PlatformOpenAI,
		RateMultiplier:            1,
		KiroCacheEmulationEnabled: true,
		KiroCacheEmulationRatio:   0.5,
		KiroEndpointMode:          KiroEndpointModeKRS,
	})
	if err != nil {
		t.Fatalf("CreateGroup error: %v", err)
	}
	if repo.created.KiroCacheEmulationEnabled {
		t.Fatal("non-Kiro group must not keep Kiro cache emulation enabled")
	}
	if repo.created.KiroCacheEmulationRatio != 0 {
		t.Fatalf("non-Kiro ratio = %v, want 0", repo.created.KiroCacheEmulationRatio)
	}
	if repo.created.KiroAutoStickyEnabled {
		t.Fatal("non-Kiro group must not keep Kiro auto sticky enabled")
	}
	if repo.created.KiroStickySessionTTLSeconds != 0 {
		t.Fatalf("non-Kiro sticky ttl = %v, want 0", repo.created.KiroStickySessionTTLSeconds)
	}
	if repo.created.KiroEndpointMode != "" {
		t.Fatalf("non-Kiro endpoint mode = %q, want empty", repo.created.KiroEndpointMode)
	}
}
