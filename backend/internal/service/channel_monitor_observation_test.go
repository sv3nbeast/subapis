package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observationProbeStub struct {
	results []*CheckResult
	err     error
}

type observationHistoryRepo struct {
	ChannelMonitorRepository
	insertErr error
	markErr   error
	marked    bool
}

func (r *observationHistoryRepo) GetByID(context.Context, int64) (*ChannelMonitor, error) {
	return &ChannelMonitor{ID: 1, CheckMode: MonitorCheckModeQuota, PrimaryModel: "quota"}, nil
}

type observationRuntime struct{}

func (observationRuntime) GetChannelMonitorRuntime(context.Context) ChannelMonitorRuntime {
	return ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}
}

func TestMonitorObservationRunCheckReportsPersistenceFailure(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	repo := &observationHistoryRepo{insertErr: storageErr}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetRuntimeReader(observationRuntime{})
	results, err := svc.RunCheck(context.Background(), 1)
	require.ErrorIs(t, err, storageErr)
	require.Len(t, results, 1)
	require.False(t, repo.marked)
}

func (r *observationHistoryRepo) InsertHistoryBatch(context.Context, []*ChannelMonitorHistoryRow) error {
	return r.insertErr
}
func (r *observationHistoryRepo) MarkChecked(context.Context, int64, time.Time) error {
	r.marked = true
	return r.markErr
}
func TestMonitorObservationPersistenceCannotRefreshOldFailure(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	repo := &observationHistoryRepo{insertErr: storageErr}
	svc := NewChannelMonitorService(repo, nil)
	err := svc.persistCheckResults(context.Background(), &ChannelMonitor{ID: 1}, []*CheckResult{{Status: MonitorStatusOperational}})
	require.ErrorIs(t, err, storageErr)
	require.False(t, repo.marked, "a failed history insert must not advance freshness")
	repo.insertErr = nil
	repo.markErr = storageErr
	require.ErrorIs(t, svc.persistCheckResults(context.Background(), &ChannelMonitor{ID: 1}, nil), storageErr)
}

func (s observationProbeStub) ListEnabledMonitors(context.Context) ([]*ChannelMonitor, error) {
	return nil, nil
}
func (s observationProbeStub) RunCheck(context.Context, int64) ([]*CheckResult, error) {
	return s.results, s.err
}

func TestMonitorRecoveryNotificationUsesPersistedResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []*CheckResult
		err     error
		failed  bool
	}{
		{"failure", []*CheckResult{{Status: MonitorStatusError}}, nil, true},
		{"challenge", []*CheckResult{{Status: MonitorStatusFailed}}, nil, true},
		{"slow", []*CheckResult{{Status: MonitorStatusDegraded}}, nil, false},
		{"success", []*CheckResult{{Status: MonitorStatusOperational}}, nil, false},
		{"storage_error", nil, errors.New("storage unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newChannelMonitorRunner(observationProbeStub{tc.results, tc.err}, nil)
			defer r.Stop()
			task := &scheduledMonitor{id: 1, completed: make(chan monitorProbeOutcome, 1)}
			r.fire(context.Background(), task)
			select {
			case outcome := <-task.completed:
				require.Equal(t, tc.failed, outcome.failed)
				require.Equal(t, tc.err == nil && len(tc.results) > 0, outcome.observed)
				require.True(t, r.tryAcquireInFlight(1), "release request before publishing completion")
				r.releaseInFlight(1)
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not report persisted outcome")
			}
		})
	}
}

func TestMonitorObservationUsesOneLatestSnapshot(t *testing.T) {
	now := time.Now().UTC()
	latency := 7000
	m := &ChannelMonitor{ID: 1, PrimaryModel: "any-model", Provider: MonitorProviderOpenAI,
		APIMode: MonitorAPIModeResponses, IntervalSeconds: 3600, JitterSeconds: 10}
	view := buildUserViewFromSummary(m, MonitorStatusSummary{PrimaryStatus: MonitorStatusError},
		&ChannelMonitorLatest{Status: MonitorStatusDegraded, LatencyMs: &latency, CheckedAt: now}, nil)
	require.Equal(t, MonitorStatusDegraded, view.PrimaryStatus)
	require.Equal(t, &now, view.PrimaryCheckedAt)
	require.Equal(t, &latency, view.PrimaryLatencyMs)
	require.Equal(t, "/v1/responses", view.ProbePath)
	require.Equal(t, 3600, view.IntervalSeconds)
	require.Equal(t, 10, view.JitterSeconds)
	m.CheckMode = MonitorCheckModeQuota
	require.Empty(t, monitorProbePath(m))
	m.CheckMode = MonitorCheckModeProbe
	m.Provider = MonitorProviderAnthropic
	require.Equal(t, "/v1/messages", monitorProbePath(m))
}

func TestMonitorRecoveryCheckIsBoundedAndPreservesNormalRate(t *testing.T) {
	task := &scheduledMonitor{interval: time.Hour}
	delay, expedite := task.recoveryDelay(true)
	require.True(t, expedite)
	require.Equal(t, time.Minute, delay)
	for i := 0; i < 20; i++ {
		_, expedite = task.recoveryDelay(true)
		require.False(t, expedite, "persistent outage must not become a one-minute paid probe loop")
	}
	_, expedite = task.recoveryDelay(false)
	require.False(t, expedite, "success and slow success do not trigger extra requests")
	_, expedite = task.recoveryDelay(true)
	require.True(t, expedite, "a new failure streak can receive one recovery check")
	for _, interval := range []time.Duration{15 * time.Second, time.Minute} {
		short := &scheduledMonitor{interval: interval}
		_, expedite = short.recoveryDelay(true)
		require.False(t, expedite, "do not delay an already-frequent probe")
	}
	jitter := &scheduledMonitor{interval: 90 * time.Second, jitter: 40 * time.Second}
	_, expedite = jitter.recoveryDelay(true)
	require.False(t, expedite)
}
