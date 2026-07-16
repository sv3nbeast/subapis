package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
)

var errKiroResponseHeaderTimeout = errors.New("kiro upstream response header timeout")
var errKiroFirstSemanticTimeout = errors.New("kiro upstream first semantic output timeout")
var errKiroFailoverBudgetExceeded = errors.New("kiro failover budget exceeded")
var errKiroIncompleteCleanup = errors.New("kiro upstream cleanup did not finish within the grace period")

const defaultKiroTransportRetryAfter = time.Second

type kiroResilienceBudgetContextKey struct{}
type kiroResilienceObservationContextKey struct{}

type kiroResilienceBudget struct {
	once     sync.Once
	duration time.Duration
	deadline time.Time
}

type kiroResilienceObservation struct {
	started          time.Time
	requestID        string
	groupID          int64
	initialAccountID int64
	budget           time.Duration
	preSemantic      atomic.Bool
	budgetTimer      *time.Timer
	done             chan struct{}
}

func newKiroResilienceObservation(ctx context.Context, requestID string, groupID, accountID int64, budget time.Duration) *kiroResilienceObservation {
	if budget <= 0 {
		budget = 105 * time.Second
	}
	observation := &kiroResilienceObservation{
		started:          time.Now(),
		requestID:        requestID,
		groupID:          groupID,
		initialAccountID: accountID,
		budget:           budget,
		done:             make(chan struct{}),
	}
	observation.preSemantic.Store(true)
	observation.budgetTimer = time.AfterFunc(budget, func() {
		if !observation.preSemantic.Load() {
			return
		}
		kiroResilienceMetrics.failoverBudgetTimeoutObserved.Add(1)
		slog.Info("kiro_failover_budget_timeout_observed",
			"request_id", observation.requestID,
			"group_id", observation.groupID,
			"initial_account_id", observation.initialAccountID,
			"budget_ms", observation.budget.Milliseconds(),
		)
	})
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				observation.stop()
			case <-observation.done:
			}
		}()
	}
	return observation
}

func (o *kiroResilienceObservation) stop() {
	if o == nil || !o.preSemantic.Swap(false) {
		return
	}
	if o.budgetTimer != nil {
		o.budgetTimer.Stop()
	}
	if o.done != nil {
		close(o.done)
	}
}

func (o *kiroResilienceObservation) markSemantic() {
	o.stop()
}

func (o *kiroResilienceObservation) elapsed() time.Duration {
	if o == nil || o.started.IsZero() {
		return 0
	}
	return time.Since(o.started)
}

func (o *kiroResilienceObservation) remaining() time.Duration {
	if o == nil {
		return 0
	}
	remaining := o.budget - o.elapsed()
	if remaining < 0 {
		return 0
	}
	return remaining
}

type kiroFirstSemanticObserver struct {
	observation     *kiroResilienceObservation
	requestID       string
	groupID         int64
	accountID       int64
	accountRound    int
	started         time.Time
	timeout         time.Duration
	timer           *time.Timer
	stopped         atomic.Bool
	timeoutReported atomic.Bool
	done            chan struct{}
}

func (o *kiroFirstSemanticObserver) stop() {
	if o == nil || !o.stopped.CompareAndSwap(false, true) {
		return
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	if o.done != nil {
		close(o.done)
	}
}

func (o *kiroFirstSemanticObserver) markSemantic() {
	if o == nil || !o.stopped.CompareAndSwap(false, true) {
		return
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	if o.done != nil {
		close(o.done)
	}
	accountElapsed := time.Since(o.started)
	totalElapsed := time.Duration(0)
	remaining := time.Duration(0)
	if o.observation != nil {
		totalElapsed = o.observation.elapsed()
		remaining = o.observation.remaining()
		o.observation.markSemantic()
	}
	slog.Info("kiro_first_semantic_observed",
		"request_id", o.requestID,
		"group_id", o.groupID,
		"account_id", o.accountID,
		"account_round", o.accountRound,
		"first_semantic_ms", accountElapsed.Milliseconds(),
		"total_pre_semantic_ms", totalElapsed.Milliseconds(),
		"remaining_budget_ms", remaining.Milliseconds(),
		"would_first_semantic_timeout", accountElapsed >= o.timeout,
		"would_failover_budget_timeout", o.observation != nil && totalElapsed >= o.observation.budget,
	)
}

type kiroSemanticObserverWriter struct {
	dst      io.Writer
	observer *kiroFirstSemanticObserver
	pending  []byte
}

func (w *kiroSemanticObserverWriter) Write(p []byte) (int, error) {
	if w == nil || w.dst == nil {
		return 0, io.ErrClosedPipe
	}
	n, err := w.dst.Write(p)
	if n > 0 {
		w.observe(p[:n])
	}
	return n, err
}

func (w *kiroSemanticObserverWriter) observe(p []byte) {
	if w == nil || w.observer == nil || w.observer.stopped.Load() || len(p) == 0 {
		return
	}
	w.pending = append(w.pending, p...)
	const maxObservedBuffer = 4 * 1024 * 1024
	if len(w.pending) > maxObservedBuffer {
		w.pending = w.pending[:0]
		return
	}
	for {
		blockEnd, delimiterLen := nextKiroSSEBlock(w.pending)
		if blockEnd < 0 {
			return
		}
		block := strings.ReplaceAll(string(w.pending[:blockEnd]), "\r\n", "\n")
		w.pending = w.pending[blockEnd+delimiterLen:]
		lines := strings.Split(block, "\n")
		_, eventType, data, internalPing := parseKiroAnthropicSSEBlock(lines)
		if !internalPing && isKiroClientSemanticEvent(eventType, data) {
			w.observer.markSemantic()
			w.pending = nil
			return
		}
	}
}

func nextKiroSSEBlock(data []byte) (int, int) {
	lf := bytes.Index(data, []byte("\n\n"))
	crlf := bytes.Index(data, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return -1, 0
		}
		return crlf, 4
	case crlf < 0 || lf < crlf:
		return lf, 2
	default:
		return crlf, 4
	}
}

func (b *kiroResilienceBudget) start() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.deadline = time.Now().Add(b.duration)
	})
}

func (b *kiroResilienceBudget) remaining() time.Duration {
	if b == nil {
		return 0
	}
	b.start()
	return time.Until(b.deadline)
}

type KiroCooldownExhaustedError struct {
	StatusCode int
	RetryAfter time.Duration
	Reason     string
}

func (e *KiroCooldownExhaustedError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("all eligible Kiro accounts are cooling down for %s", e.RetryAfter.Round(time.Second))
}

type kiroUpstreamCleanupPendingError struct {
	cause error
	done  <-chan struct{}
}

func (e *kiroUpstreamCleanupPendingError) Error() string {
	if e == nil || e.cause == nil {
		return "kiro upstream cleanup pending"
	}
	return e.cause.Error()
}

func (e *kiroUpstreamCleanupPendingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *kiroUpstreamCleanupPendingError) upstreamDone() <-chan struct{} {
	if e == nil {
		return nil
	}
	return e.done
}

type KiroResilienceMetricsSnapshot struct {
	ResponseHeaderTimeoutTotal         uint64
	FirstSemanticTimeoutTotal          uint64
	FailoverBudgetTimeoutTotal         uint64
	ResponseHeaderTimeoutObservedTotal uint64
	FirstSemanticTimeoutObservedTotal  uint64
	FailoverBudgetTimeoutObservedTotal uint64
	SlowCleanupTotal                   uint64
}

var kiroResilienceMetrics struct {
	responseHeaderTimeout         atomic.Uint64
	firstSemanticTimeout          atomic.Uint64
	failoverBudgetTimeout         atomic.Uint64
	responseHeaderTimeoutObserved atomic.Uint64
	firstSemanticTimeoutObserved  atomic.Uint64
	failoverBudgetTimeoutObserved atomic.Uint64
	slowCleanup                   atomic.Uint64
}

func SnapshotKiroResilienceMetrics() KiroResilienceMetricsSnapshot {
	return KiroResilienceMetricsSnapshot{
		ResponseHeaderTimeoutTotal:         kiroResilienceMetrics.responseHeaderTimeout.Load(),
		FirstSemanticTimeoutTotal:          kiroResilienceMetrics.firstSemanticTimeout.Load(),
		FailoverBudgetTimeoutTotal:         kiroResilienceMetrics.failoverBudgetTimeout.Load(),
		ResponseHeaderTimeoutObservedTotal: kiroResilienceMetrics.responseHeaderTimeoutObserved.Load(),
		FirstSemanticTimeoutObservedTotal:  kiroResilienceMetrics.firstSemanticTimeoutObserved.Load(),
		FailoverBudgetTimeoutObservedTotal: kiroResilienceMetrics.failoverBudgetTimeoutObserved.Load(),
		SlowCleanupTotal:                   kiroResilienceMetrics.slowCleanup.Load(),
	}
}

type kiroResponseHeaderTimeoutError struct {
	Timeout  time.Duration
	Endpoint string
}

type kiroFirstSemanticTimeoutError struct {
	Timeout time.Duration
}

func (e *kiroFirstSemanticTimeoutError) Error() string {
	return errKiroFirstSemanticTimeout.Error()
}

func (e *kiroFirstSemanticTimeoutError) Unwrap() error {
	return errKiroFirstSemanticTimeout
}

func (e *kiroResponseHeaderTimeoutError) Error() string {
	return errKiroResponseHeaderTimeout.Error()
}

func (e *kiroResponseHeaderTimeoutError) Unwrap() error {
	return errKiroResponseHeaderTimeout
}

type kiroCancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
	once   sync.Once
}

type kiroReleaseOnCloseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

type kiroStreamCompletion struct {
	service    *GatewayService
	account    *Account
	mu         sync.Mutex
	terminal   bool
	cacheUsage *kiroCacheEmulationUsage
	once       sync.Once
}

func (c *kiroStreamCompletion) markTerminal(cacheUsage *kiroCacheEmulationUsage) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.terminal = true
	c.cacheUsage = cacheUsage
	c.mu.Unlock()
}

// finalizeKiroStreamCacheUsage commits terminal cache state immediately for
// legacy off/observe streams. Enforced streams carry a tracked completion and
// defer the commit until downstream streaming and physical cleanup succeed.
func (s *GatewayService) finalizeKiroStreamCacheUsage(ctx context.Context, completion *kiroStreamCompletion, cacheUsage *kiroCacheEmulationUsage) {
	if completion != nil {
		completion.markTerminal(cacheUsage)
		return
	}
	s.commitKiroCacheEmulationUsage(ctx, cacheUsage)
}

func (c *kiroStreamCompletion) complete(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	terminal := c.terminal
	cacheUsage := c.cacheUsage
	c.mu.Unlock()
	if !terminal {
		return errors.New("kiro stream completed without a terminal upstream event")
	}

	c.once.Do(func() {
		stateParent := ctx
		if stateParent == nil {
			stateParent = context.Background()
		}
		stateCtx, cancel := context.WithTimeout(context.WithoutCancel(stateParent), 4*time.Second)
		defer cancel()
		accountID := int64(0)
		if c.account != nil {
			accountID = c.account.ID
		}
		if err := c.service.markKiroSuccessPreservingCooldown(stateCtx, buildKiroCooldownKey(c.account)); err != nil {
			slog.Warn("kiro_terminal_success_state_reset_failed", "account_id", accountID, "error", err)
		}
		c.service.commitKiroCacheEmulationUsage(stateCtx, cacheUsage)
	})
	return nil
}

type kiroTrackedStreamBody struct {
	io.ReadCloser
	cancel     context.CancelFunc
	done       <-chan struct{}
	completion *kiroStreamCompletion
	once       sync.Once
}

func startKiroFirstSemanticGate(ctx context.Context, src io.ReadCloser, dst *io.PipeWriter, maxBuffer, maxLineSize int, ready chan<- error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runKiroFirstSemanticGate(ctx, src, dst, maxBuffer, maxLineSize, ready)
	}()
	return done
}

// joinKiroGatedStreamCleanup releases the stream context only after both the
// upstream translator and the semantic gate have exited. The producer closes
// its pipe before producerDone, allowing the gate to drain a clean terminal
// event before release can turn that EOF into context.Canceled.
func joinKiroGatedStreamCleanup(producerDone, gateDone <-chan struct{}, release context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if producerDone != nil {
			<-producerDone
		}
		if gateDone != nil {
			<-gateDone
		}
		if release != nil {
			release()
		}
	}()
	return done
}

func (b *kiroTrackedStreamBody) Close() error {
	if b == nil {
		return nil
	}
	b.once.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
	})
	return b.ReadCloser.Close()
}

func trackedKiroStreamBody(resp *http.Response) *kiroTrackedStreamBody {
	if resp == nil || resp.Body == nil {
		return nil
	}
	tracked, _ := resp.Body.(*kiroTrackedStreamBody)
	return tracked
}

// finishKiroStreamResponse couples downstream success, cache state and the
// physical translator lifetime. Failed or canceled attempts never finalize
// cache emulation, and a slow cleanup keeps the account lease held via the
// completion channel carried by the returned error.
func (s *GatewayService) finishKiroStreamResponse(ctx context.Context, resp *http.Response, groupID *int64, forwardErr error) error {
	tracked := trackedKiroStreamBody(resp)
	if tracked == nil {
		return forwardErr
	}
	forwardErr = kiroPostSemanticFailure(forwardErr)
	_ = tracked.Close()
	cleanupStarted := time.Now()
	accountID := int64(0)
	if tracked.completion != nil && tracked.completion.account != nil {
		accountID = tracked.completion.account.ID
	}
	if !waitKiroUpstreamCleanup(tracked.done, s.kiroCleanupGrace(groupID)) {
		slog.Error("kiro_upstream_cleanup_exceeded_grace",
			"group_id", derefGroupID(groupID),
			"account_id", accountID,
			"cleanup_ms", time.Since(cleanupStarted).Milliseconds(),
		)
		cause := forwardErr
		if cause == nil {
			cause = errKiroIncompleteCleanup
		}
		return &kiroUpstreamCleanupPendingError{cause: cause, done: tracked.done}
	}
	slog.Debug("kiro_upstream_cleanup_completed",
		"request_id", buildKiroClientRequestID(resp),
		"group_id", derefGroupID(groupID),
		"account_id", accountID,
		"cleanup_ms", time.Since(cleanupStarted).Milliseconds(),
	)
	if forwardErr != nil {
		return forwardErr
	}
	if err := tracked.completion.complete(ctx); err != nil {
		return &UpstreamFailoverError{
			StatusCode:  http.StatusServiceUnavailable,
			FailureKind: UpstreamFailureIncompleteStream,
			RetryAfter:  defaultKiroTransportRetryAfter,
			Cause:       err,
		}
	}
	return nil
}

// A tracked body is returned only after the first semantic event passed the
// gate. From that point onward, replaying the request can duplicate model work
// even when a non-stream client has not received bytes yet.
func kiroPostSemanticFailure(err error) error {
	if err == nil || isKiroContextLimitError(err) {
		return err
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		failoverErr.FailoverProhibited = true
		if failoverErr.StatusCode == 0 || failoverErr.StatusCode == http.StatusBadGateway {
			failoverErr.StatusCode = http.StatusServiceUnavailable
		}
		if failoverErr.FailureKind == "" {
			failoverErr.FailureKind = UpstreamFailureIncompleteStream
		}
		if failoverErr.RetryAfter <= 0 {
			failoverErr.RetryAfter = defaultKiroTransportRetryAfter
		}
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "upstream_error",
			"message": "Kiro upstream stream ended after semantic output without a complete response",
		},
	})
	return &UpstreamFailoverError{
		StatusCode:         http.StatusServiceUnavailable,
		ResponseBody:       body,
		FailureKind:        UpstreamFailureIncompleteStream,
		RetryAfter:         defaultKiroTransportRetryAfter,
		FailoverProhibited: true,
		Cause:              err,
	}
}

func (b *kiroReleaseOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func (b *kiroCancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.cancel(context.Canceled) })
	return err
}

func doKiroWithResponseHeaderTimeout(
	req *http.Request,
	timeout time.Duration,
	do func(*http.Request) (*http.Response, error),
) (*http.Response, time.Duration, <-chan struct{}, error) {
	start := time.Now()
	if req == nil || timeout <= 0 {
		resp, err := do(req)
		return resp, time.Since(start), nil, err
	}

	requestCtx, cancel := context.WithCancelCause(req.Context())
	type result struct {
		resp *http.Response
		err  error
	}
	resultCh := make(chan result, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := do(req.WithContext(requestCtx))
		if context.Cause(requestCtx) != nil && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
			resp = nil
		}
		resultCh <- result{resp: resp, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-resultCh:
		elapsed := time.Since(start)
		if outcome.err != nil {
			cancel(outcome.err)
			if outcome.resp != nil && outcome.resp.Body != nil {
				_ = outcome.resp.Body.Close()
			}
			return nil, elapsed, done, outcome.err
		}
		if outcome.resp != nil && outcome.resp.Body != nil {
			outcome.resp.Body = &kiroCancelOnCloseBody{ReadCloser: outcome.resp.Body, cancel: cancel}
		} else {
			cancel(nil)
		}
		return outcome.resp, elapsed, done, nil
	case <-timer.C:
		cancel(errKiroResponseHeaderTimeout)
		return nil, time.Since(start), done, &kiroResponseHeaderTimeoutError{Timeout: timeout, Endpoint: req.URL.Host}
	case <-req.Context().Done():
		cause := context.Cause(req.Context())
		if cause == nil {
			cause = req.Context().Err()
		}
		cancel(cause)
		return nil, time.Since(start), done, cause
	}
}

func runKiroFirstSemanticGate(ctx context.Context, src io.ReadCloser, dst *io.PipeWriter, maxBuffer, maxLineSize int, ready chan<- error) {
	defer func() { _ = src.Close() }()
	readySent := false
	signalReady := func(err error) {
		if readySent {
			return
		}
		readySent = true
		// ready is a one-element channel owned by the caller. The notification
		// must not be dropped when ctx is already canceled, otherwise the caller
		// can remain blocked while the gate and translator have both exited.
		ready <- err
	}
	closeWithError := func(err error) {
		if err == nil {
			_ = dst.Close()
			return
		}
		_ = dst.CloseWithError(err)
	}

	if maxBuffer <= 0 {
		maxBuffer = 256 * 1024
	}
	if maxLineSize < maxBuffer {
		maxLineSize = maxBuffer
	}
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	var buffered bytes.Buffer
	lines := make([]string, 0, 4)
	released := false

	writeBlock := func(block []byte) error {
		if len(block) == 0 {
			return nil
		}
		if !released {
			if buffered.Len()+len(block) > maxBuffer {
				return fmt.Errorf("kiro pre-semantic buffer exceeded %d bytes", maxBuffer)
			}
			_, _ = buffered.Write(block)
			return nil
		}
		_, err := dst.Write(block)
		return err
	}

	processEvent := func() error {
		if len(lines) == 0 {
			return nil
		}
		block, eventType, data, internalPing := parseKiroAnthropicSSEBlock(lines)
		lines = lines[:0]
		if internalPing {
			return nil
		}
		if eventType == "error" {
			return &sseStreamErrorEventError{RawData: data}
		}
		if err := writeBlock(block); err != nil {
			return err
		}
		if !released && isKiroClientSemanticEvent(eventType, data) {
			released = true
			signalReady(nil)
			if buffered.Len() > 0 {
				if _, err := io.Copy(dst, &buffered); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			err := context.Cause(ctx)
			if err == nil {
				err = ctx.Err()
			}
			signalReady(err)
			closeWithError(err)
			return
		default:
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := processEvent(); err != nil {
				signalReady(err)
				closeWithError(err)
				return
			}
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		if err := processEvent(); err != nil {
			signalReady(err)
			closeWithError(err)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		signalReady(err)
		closeWithError(err)
		return
	}
	if cause := context.Cause(ctx); cause != nil {
		signalReady(cause)
		closeWithError(cause)
		return
	}
	if !released {
		err := errors.New("empty kiro event stream before semantic output")
		signalReady(err)
		closeWithError(err)
		return
	}
	closeWithError(nil)
}

func parseKiroAnthropicSSEBlock(lines []string) ([]byte, string, string, bool) {
	eventType := ""
	data := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		}
		if data == "" && strings.HasPrefix(trimmed, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		}
	}
	if eventType == "sub2api_internal_kiro_ping" {
		return nil, eventType, data, true
	}
	if eventType == "" && data != "" {
		var event map[string]any
		if json.Unmarshal([]byte(data), &event) == nil {
			eventType, _ = event["type"].(string)
		}
	}
	block := []byte(strings.Join(lines, "\n") + "\n\n")
	return block, eventType, data, false
}

func isKiroClientSemanticEvent(eventType, data string) bool {
	if data == "" || data == "{}" {
		return false
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false
	}
	if eventType == "" {
		eventType, _ = event["type"].(string)
	}
	switch eventType {
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		for _, key := range []string{"text", "thinking", "partial_json"} {
			if value, _ := delta[key].(string); value != "" {
				return true
			}
		}
	case "content_block_start":
		block, _ := event["content_block"].(map[string]any)
		blockType, _ := block["type"].(string)
		name, _ := block["name"].(string)
		return blockType == "tool_use" && name != ""
	case "message_delta":
		delta, _ := event["delta"].(map[string]any)
		stopReason, _ := delta["stop_reason"].(string)
		return stopReason != ""
	case "message_stop":
		return true
	}
	return false
}

func waitKiroUpstreamCleanup(done <-chan struct{}, grace time.Duration) bool {
	if done == nil {
		return true
	}
	if grace <= 0 {
		grace = 3 * time.Second
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		kiroResilienceMetrics.slowCleanup.Add(1)
		return false
	}
}

type kiroCooldownPrefetchContextKey struct{}

type kiroCooldownPrefetch struct {
	states   map[int64]*kirocooldown.State
	enforced bool
}

func parsedGroupID(parsed *ParsedRequest) *int64 {
	if parsed == nil {
		return nil
	}
	return parsed.GroupID
}

func (s *GatewayService) kiroResilienceMode(groupID *int64) string {
	if s == nil || s.cfg == nil {
		return config.KiroResilienceModeOff
	}
	cfg := s.cfg.Gateway.KiroResilience
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = config.KiroResilienceModeOff
	}
	if len(cfg.GroupIDs) == 0 {
		return mode
	}
	if groupID == nil {
		return config.KiroResilienceModeOff
	}
	for _, allowedID := range cfg.GroupIDs {
		if allowedID == *groupID {
			return mode
		}
	}
	return config.KiroResilienceModeOff
}

func (s *GatewayService) kiroResilienceEnforced(groupID *int64) bool {
	return s.kiroResilienceMode(groupID) == config.KiroResilienceModeEnforce
}

func (s *GatewayService) kiroResilienceObserved(groupID *int64) bool {
	return s.kiroResilienceMode(groupID) == config.KiroResilienceModeObserve
}

func (s *GatewayService) KiroResilienceEnforced(groupID *int64) bool {
	return s.kiroResilienceEnforced(groupID)
}

// StartKiroResilienceTracking starts either the enforce budget or passive
// observe timers. Observe mode never adds a deadline or changes selection.
func (s *GatewayService) StartKiroResilienceTracking(ctx context.Context, groupID *int64, account *Account) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.kiroResilienceEnforced(groupID) {
		return s.StartKiroResilienceBudget(ctx, groupID)
	}
	if !s.kiroResilienceObserved(groupID) {
		return ctx, nil
	}
	if existing, _ := ctx.Value(kiroResilienceObservationContextKey{}).(*kiroResilienceObservation); existing != nil {
		return ctx, nil
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	budget := time.Duration(s.cfg.Gateway.KiroResilience.FailoverBudgetSeconds) * time.Second
	observation := newKiroResilienceObservation(
		ctx,
		resolveUsageBillingRequestID(ctx, ""),
		derefGroupID(groupID),
		accountID,
		budget,
	)
	return context.WithValue(ctx, kiroResilienceObservationContextKey{}, observation), nil
}

func (s *GatewayService) startKiroFirstSemanticObservation(ctx context.Context, groupID *int64, account *Account, started time.Time, inputTokens int) *kiroFirstSemanticObserver {
	if !s.kiroResilienceObserved(groupID) {
		return nil
	}
	timeout := s.kiroFirstSemanticTimeoutForInput(groupID, inputTokens)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	accountRound, _ := AccountSwitchCountFromContext(ctx)
	observer := &kiroFirstSemanticObserver{
		observation:  kiroObservationFromContext(ctx),
		requestID:    resolveUsageBillingRequestID(ctx, ""),
		groupID:      derefGroupID(groupID),
		accountID:    accountID,
		accountRound: accountRound + 1,
		started:      started,
		timeout:      timeout,
		done:         make(chan struct{}),
	}
	observer.timer = time.AfterFunc(timeout, func() {
		if observer.stopped.Load() || !observer.timeoutReported.CompareAndSwap(false, true) {
			return
		}
		kiroResilienceMetrics.firstSemanticTimeoutObserved.Add(1)
		totalElapsed := time.Duration(0)
		remaining := time.Duration(0)
		if observer.observation != nil {
			totalElapsed = observer.observation.elapsed()
			remaining = observer.observation.remaining()
		}
		slog.Info("kiro_first_semantic_timeout_observed",
			"request_id", observer.requestID,
			"group_id", observer.groupID,
			"account_id", observer.accountID,
			"account_round", observer.accountRound,
			"timeout_ms", observer.timeout.Milliseconds(),
			"total_pre_semantic_ms", totalElapsed.Milliseconds(),
			"remaining_budget_ms", remaining.Milliseconds(),
		)
	})
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				observer.stop()
			case <-observer.done:
			}
		}()
	}
	return observer
}

func kiroObservationFromContext(ctx context.Context) *kiroResilienceObservation {
	if ctx == nil {
		return nil
	}
	observation, _ := ctx.Value(kiroResilienceObservationContextKey{}).(*kiroResilienceObservation)
	return observation
}

func (s *GatewayService) startKiroResponseHeaderObservation(
	ctx context.Context,
	groupID *int64,
	account *Account,
	endpoint string,
	accountRound int,
	endpointAttempt int,
	timeout time.Duration,
) func() {
	if !s.kiroResilienceObserved(groupID) {
		return func() {}
	}
	if timeout <= 0 {
		timeout = time.Duration(s.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	requestID := resolveUsageBillingRequestID(ctx, "")
	observation := kiroObservationFromContext(ctx)
	var stopped atomic.Bool
	timer := time.AfterFunc(timeout, func() {
		if stopped.Load() {
			return
		}
		kiroResilienceMetrics.responseHeaderTimeoutObserved.Add(1)
		remaining := time.Duration(0)
		if observation != nil {
			remaining = observation.remaining()
		}
		slog.Info("kiro_response_header_timeout_observed",
			"request_id", requestID,
			"group_id", derefGroupID(groupID),
			"account_id", accountID,
			"endpoint", endpoint,
			"account_round", accountRound,
			"endpoint_attempt", endpointAttempt,
			"timeout_ms", timeout.Milliseconds(),
			"remaining_budget_ms", remaining.Milliseconds(),
		)
	})
	if ctx != nil {
		context.AfterFunc(ctx, func() {
			if stopped.CompareAndSwap(false, true) {
				timer.Stop()
			}
		})
	}
	return func() {
		if stopped.CompareAndSwap(false, true) {
			timer.Stop()
		}
	}
}

func (s *GatewayService) applyKiroSelectionBindingPolicy(selection *AccountSelectionResult, groupID *int64, sessionHash string, preserve, deferMigration bool) {
	if selection == nil {
		return
	}
	selection.PreserveStickyBinding = preserve
	selection.DeferStickyMigration = !preserve && (deferMigration ||
		(strings.TrimSpace(sessionHash) != "" && selection.Account != nil && selection.Account.Platform == PlatformKiro && s.kiroResilienceEnforced(groupID)))
}

func (s *GatewayService) shouldBindSelectionBeforeSuccess(account *Account, groupID *int64, sessionHash string, preserve, deferMigration bool) bool {
	if strings.TrimSpace(sessionHash) == "" || preserve || deferMigration {
		return false
	}
	return account == nil || account.Platform != PlatformKiro || !s.kiroResilienceEnforced(groupID)
}

// StartKiroResilienceBudget lazily starts the request-wide Kiro budget. The
// returned context must replace the caller's request context so queue waits and
// every subsequent Kiro account attempt share the same deadline.
func (s *GatewayService) StartKiroResilienceBudget(ctx context.Context, groupID *int64) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.kiroResilienceEnforced(groupID) {
		return ctx, nil
	}
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	if budget == nil {
		duration := time.Duration(s.cfg.Gateway.KiroResilience.FailoverBudgetSeconds) * time.Second
		if duration <= 0 {
			duration = 105 * time.Second
		}
		budget = &kiroResilienceBudget{duration: duration}
		ctx = context.WithValue(ctx, kiroResilienceBudgetContextKey{}, budget)
	}
	if budget.remaining() <= 0 {
		return ctx, errKiroFailoverBudgetExceeded
	}
	return ctx, nil
}

func (s *GatewayService) KiroWaitTimeoutWithinBudget(ctx context.Context, configured time.Duration) (time.Duration, error) {
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	if budget == nil {
		return configured, nil
	}
	remaining := budget.remaining()
	if remaining <= 0 {
		return 0, errKiroFailoverBudgetExceeded
	}
	if configured <= 0 || remaining < configured {
		return remaining, nil
	}
	return configured, nil
}

func IsKiroFailoverBudgetExceeded(err error) bool {
	return errors.Is(err, errKiroFailoverBudgetExceeded)
}

func kiroResilienceBudgetActive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	return budget != nil
}

func (s *GatewayService) kiroContextWithinBudget(ctx context.Context, groupID *int64) (context.Context, context.CancelFunc, error) {
	ctx, err := s.StartKiroResilienceBudget(ctx, groupID)
	if err != nil {
		return ctx, func() {}, err
	}
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	if budget == nil {
		return ctx, func() {}, nil
	}
	if budget.remaining() <= 0 {
		return ctx, func() {}, errKiroFailoverBudgetExceeded
	}
	budgetCtx, cancel := context.WithDeadlineCause(ctx, budget.deadline, errKiroFailoverBudgetExceeded)
	return budgetCtx, cancel, nil
}

// kiroPreSemanticContext applies the account and request budgets only until
// the first semantic event. stopTimer must be called as soon as that event is
// accepted so a long, healthy generation is not canceled by a failover timer.
func (s *GatewayService) kiroPreSemanticContext(ctx context.Context, groupID *int64, inputTokens int) (context.Context, context.CancelCauseFunc, func(), error) {
	ctx, err := s.StartKiroResilienceBudget(ctx, groupID)
	if err != nil {
		return ctx, func(error) {}, func() {}, err
	}
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	if budget == nil {
		child, cancel := context.WithCancelCause(ctx)
		return child, cancel, func() {}, nil
	}
	remaining := budget.remaining()
	if remaining <= 0 {
		return ctx, func(error) {}, func() {}, errKiroFailoverBudgetExceeded
	}
	timeout := s.kiroFirstSemanticTimeoutForInput(groupID, inputTokens)
	cause := error(errKiroFirstSemanticTimeout)
	if timeout <= 0 || remaining < timeout {
		timeout = remaining
		cause = errKiroFailoverBudgetExceeded
	}
	child, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(timeout, func() { cancel(cause) })
	return child, cancel, func() { timer.Stop() }, nil
}

func kiroResilienceBudgetRemaining(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	budget, _ := ctx.Value(kiroResilienceBudgetContextKey{}).(*kiroResilienceBudget)
	if budget == nil {
		return 0
	}
	remaining := budget.remaining()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func kiroBudgetCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if errors.Is(context.Cause(ctx), errKiroFailoverBudgetExceeded) {
		return errKiroFailoverBudgetExceeded
	}
	return nil
}

func (s *GatewayService) kiroResponseHeaderTimeout(groupID *int64) time.Duration {
	if !s.kiroResilienceEnforced(groupID) || s.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds) * time.Second
}

// kiroResponseHeaderTimeoutForInput gives large prompt uploads enough time to
// reach Kiro without weakening the request-wide failover budget. The scaled
// first-semantic deadline remains authoritative and keeps parsing headroom.
func (s *GatewayService) kiroResponseHeaderTimeoutForInput(groupID *int64, payloadInputTokens int) time.Duration {
	base := s.kiroResponseHeaderTimeout(groupID)
	if base <= 0 || payloadInputTokens <= 200_000 {
		return base
	}
	const (
		tokenStep        = 100_000
		timeoutStep      = 15 * time.Second
		semanticHeadroom = 5 * time.Second
	)
	steps := (payloadInputTokens - 200_000 + tokenStep - 1) / tokenStep
	timeout := base + time.Duration(steps)*timeoutStep
	if semanticTimeout := s.kiroFirstSemanticTimeoutForInput(groupID, payloadInputTokens); semanticTimeout > semanticHeadroom {
		maxTimeout := semanticTimeout - semanticHeadroom
		if maxTimeout > base && timeout > maxTimeout {
			timeout = maxTimeout
		}
	}
	return timeout
}

func (s *GatewayService) kiroFirstSemanticTimeout(groupID *int64) time.Duration {
	if !s.kiroResilienceEnforced(groupID) || s.cfg.Gateway.KiroResilience.FirstSemanticTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.KiroResilience.FirstSemanticTimeoutSeconds) * time.Second
}

func (s *GatewayService) kiroFirstSemanticTimeoutForInput(groupID *int64, inputTokens int) time.Duration {
	base := s.kiroFirstSemanticTimeout(groupID)
	if base <= 0 || inputTokens <= 200_000 {
		return base
	}
	const (
		tokenStep   = 100_000
		timeoutStep = 15 * time.Second
		budgetGuard = 5 * time.Second
	)
	steps := (inputTokens - 200_000 + tokenStep - 1) / tokenStep
	timeout := base + time.Duration(steps)*timeoutStep
	budget := time.Duration(s.cfg.Gateway.KiroResilience.FailoverBudgetSeconds) * time.Second
	if maximum := budget - budgetGuard; maximum > base && timeout > maximum {
		timeout = maximum
	}
	return timeout
}

func (s *GatewayService) kiroPreSemanticBufferBytes(groupID *int64) int {
	if !s.kiroResilienceEnforced(groupID) {
		return 0
	}
	if size := s.cfg.Gateway.KiroResilience.PreSemanticBufferBytes; size > 0 {
		return size
	}
	return 256 * 1024
}

func (s *GatewayService) kiroSemanticGateMaxLineSize() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		return s.cfg.Gateway.MaxLineSize
	}
	return defaultMaxLineSize
}

func (s *GatewayService) kiroCleanupGrace(groupID *int64) time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.KiroResilience.CleanupGraceSeconds <= 0 {
		return 3 * time.Second
	}
	return time.Duration(s.cfg.Gateway.KiroResilience.CleanupGraceSeconds) * time.Second
}

func (s *GatewayService) kiroUnresponsiveCooldown(groupID *int64) (time.Duration, time.Duration) {
	if !s.kiroResilienceEnforced(groupID) {
		return 0, 0
	}
	base := time.Duration(s.cfg.Gateway.KiroResilience.UnresponsiveCooldownSeconds) * time.Second
	maximum := time.Duration(s.cfg.Gateway.KiroResilience.UnresponsiveCooldownMaxSecs) * time.Second
	if base <= 0 {
		base = 30 * time.Second
	}
	if maximum < base {
		maximum = base
	}
	return base, maximum
}

func (s *GatewayService) withKiroCooldownPrefetch(ctx context.Context, accounts []Account, groupID *int64) context.Context {
	mode := s.kiroResilienceMode(groupID)
	if mode == config.KiroResilienceModeOff || s == nil || s.kiroCooldownStore == nil || len(accounts) == 0 {
		return ctx
	}
	accountKeys := make(map[int64]string)
	keys := make([]string, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != PlatformKiro {
			continue
		}
		key := buildKiroCooldownKey(account)
		if key == "" {
			continue
		}
		accountKeys[account.ID] = key
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ctx
	}

	statesByKey := make(map[string]*kirocooldown.State)
	var err error
	if batch, ok := s.kiroCooldownStore.(kiroCooldownBatchStore); ok {
		statesByKey, err = batch.GetStates(ctx, keys)
	} else {
		for _, key := range keys {
			state, getErr := s.kiroCooldownStore.GetState(ctx, key)
			if getErr != nil {
				err = getErr
				break
			}
			if state != nil && state.Active {
				statesByKey[key] = state
			}
		}
	}
	if err != nil {
		slog.Warn("kiro_cooldown_prefetch_failed", "group_id", derefGroupID(groupID), "error", err)
		return ctx
	}

	states := make(map[int64]*kirocooldown.State)
	for accountID, key := range accountKeys {
		if state := statesByKey[key]; state != nil && state.Active {
			states[accountID] = state
			slog.Info("kiro_scheduler_cooldown_observed",
				"group_id", derefGroupID(groupID),
				"account_id", accountID,
				"reason", state.Reason,
				"remaining_ms", state.Remaining.Milliseconds(),
				"enforced", mode == config.KiroResilienceModeEnforce,
			)
		}
	}
	return context.WithValue(ctx, kiroCooldownPrefetchContextKey{}, &kiroCooldownPrefetch{
		states:   states,
		enforced: mode == config.KiroResilienceModeEnforce,
	})
}

func kiroCooldownStateFromContext(ctx context.Context, account *Account) *kirocooldown.State {
	if account == nil || account.Platform != PlatformKiro {
		return nil
	}
	prefetch, _ := ctx.Value(kiroCooldownPrefetchContextKey{}).(*kiroCooldownPrefetch)
	if prefetch == nil || !prefetch.enforced {
		return nil
	}
	return prefetch.states[account.ID]
}

func kiroCooldownExhaustedErrorFromContext(ctx context.Context, accountIDs map[int64]struct{}) error {
	prefetch, _ := ctx.Value(kiroCooldownPrefetchContextKey{}).(*kiroCooldownPrefetch)
	if prefetch == nil || !prefetch.enforced || len(prefetch.states) == 0 || len(accountIDs) == 0 {
		return nil
	}
	statusCode := http.StatusTooManyRequests
	reason := kirocooldown.CooldownReason429
	var earliest time.Duration
	matched := 0
	for accountID, state := range prefetch.states {
		if _, ok := accountIDs[accountID]; !ok {
			continue
		}
		if state == nil || !state.Active || state.Remaining <= 0 {
			continue
		}
		matched++
		if earliest <= 0 || state.Remaining < earliest {
			earliest = state.Remaining
		}
		if state.Reason != kirocooldown.CooldownReason429 {
			statusCode = http.StatusServiceUnavailable
			reason = state.Reason
		}
	}
	if matched == 0 || earliest <= 0 {
		return nil
	}
	return &KiroCooldownExhaustedError{
		StatusCode: statusCode,
		RetryAfter: earliest,
		Reason:     reason,
	}
}

// kiroCooldownExhaustedFromRepository recovers cooldown diagnostics when the
// scheduler snapshot has already removed accounts whose runtime
// RateLimitResetAt is in the future. The Redis lookup remains fail-open.
func (s *GatewayService) kiroCooldownExhaustedFromRepository(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	excludedIDs map[int64]struct{},
	eligible func(*Account) bool,
) error {
	if s == nil || s.accountRepo == nil || !s.kiroResilienceEnforced(groupID) {
		return nil
	}
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListByGroup(ctx, *groupID)
	} else {
		accounts, err = s.accountRepo.ListByPlatform(ctx, PlatformKiro)
	}
	if err != nil {
		slog.Warn("kiro_cooldown_fallback_candidates_failed", "group_id", derefGroupID(groupID), "error", err)
		return nil
	}

	now := time.Now()
	candidates := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != PlatformKiro || !s.isAccountInGroup(account, groupID) {
			continue
		}
		if _, excluded := excludedIDs[account.ID]; excluded {
			continue
		}
		if !s.isAccountSchedulableForModelSelectionIgnoringAccountRateLimit(ctx, account, requestedModel, now) {
			continue
		}
		if eligible != nil && !eligible(account) {
			continue
		}
		candidates = append(candidates, *account)
	}
	if len(candidates) == 0 {
		return nil
	}
	prefetchedCtx := s.withKiroCooldownPrefetch(ctx, candidates, groupID)
	accountIDs := make(map[int64]struct{}, len(candidates))
	for i := range candidates {
		if kiroCooldownStateFromContext(prefetchedCtx, &candidates[i]) != nil {
			accountIDs[candidates[i].ID] = struct{}{}
		}
	}
	if len(accountIDs) == 0 {
		return nil
	}
	return kiroCooldownExhaustedErrorFromContext(prefetchedCtx, accountIDs)
}

type upstreamDoneError interface {
	error
	upstreamDone() <-chan struct{}
}

func UpstreamDoneFromError(err error) <-chan struct{} {
	if err == nil {
		return nil
	}
	var pending upstreamDoneError
	if errors.As(err, &pending) {
		return pending.upstreamDone()
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return failoverErr.UpstreamDone
	}
	return nil
}
