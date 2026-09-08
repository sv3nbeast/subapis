package service

import "context"

const WebAgentStorageReservationBytes int64 = (32 + 1) << 20
const WebAgentUserStorageBytes int64 = 500 << 20

type WebAgentBlobStage struct {
	ID, TaskID, UserID                     int64
	LeaseToken, BlobKey, PreviewKey, State string
	ReservedBytes                          int64
}
type WebAgentStorageUsage struct {
	UsedBytes            int64 `json:"used_bytes"`
	LimitBytes           int64 `json:"limit_bytes"`
	TaskReservationBytes int64 `json:"task_reservation_bytes"`
}
type WebAgentStorageRepository interface {
	RegisterArtifactStore(context.Context, string) error
	ReserveArtifactStorage(context.Context, *WebAgentTask) (*WebAgentBlobStage, error)
	CheckArtifactStorage(context.Context, *WebAgentTask, *WebAgentBlobStage) error
	ReadyArtifactStorage(context.Context, *WebAgentTask, *WebAgentBlobStage, *WebAgentArtifact) error
	AbandonArtifactStorage(context.Context, int64, int64, string) error
	// Must run inside the store's exclusive lock. Only registered keys are passed
	// to remove; publication locks/rechecks the same journal row.
	CollectArtifactStorage(context.Context, func(*WebAgentBlobStage) error) (int, error)
	DeleteArtifact(context.Context, int64, int64) error
	ArtifactStorageUsage(context.Context, int64) (int64, error)
}
type WebAgentStagedBlobStore interface {
	WebAgentBlobStore
	StorageID() string
	PutKey(context.Context, string, []byte) error
	WithLock(context.Context, bool, func() error) error
}

func CollectWebAgentStorage(ctx context.Context, repo WebAgentStorageRepository, store WebAgentStagedBlobStore) (count int, err error) {
	if repo == nil || store == nil {
		return 0, ErrWebAgentUnavailable
	}
	if err = repo.RegisterArtifactStore(ctx, store.StorageID()); err != nil {
		return 0, err
	}
	err = store.WithLock(ctx, true, func() error {
		var e error
		count, e = repo.CollectArtifactStorage(ctx, func(stage *WebAgentBlobStage) error {
			if e := store.Remove(ctx, stage.BlobKey); e != nil {
				return e
			}
			return store.Remove(ctx, stage.PreviewKey)
		})
		return e
	})
	return count, err
}
