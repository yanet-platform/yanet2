package operator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockActuator struct {
	mu       sync.Mutex
	calls    []*StageConfig
	errs     []error
	notifyCh chan struct{}
}

func newMockActuator() *mockActuator {
	return &mockActuator{
		notifyCh: make(chan struct{}, 64),
	}
}

func (m *mockActuator) Apply(ctx context.Context, stage *StageConfig) error {
	m.mu.Lock()
	m.calls = append(m.calls, stage)
	var err error
	if len(m.errs) > 0 {
		err = m.errs[0]
		m.errs = m.errs[1:]
	}
	m.mu.Unlock()

	select {
	case m.notifyCh <- struct{}{}:
	default:
	}

	return err
}

func (m *mockActuator) Close() error { return nil }

func (m *mockActuator) CallsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.calls)
}

func (m *mockActuator) LastCall() *StageConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}

	return m.calls[len(m.calls)-1]
}

func (m *mockActuator) Calls() []*StageConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*StageConfig, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockActuator) QueueErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.errs = append(m.errs, err)
}

func waitApply(t *testing.T, a *mockActuator, timeout time.Duration) {
	t.Helper()

	select {
	case <-a.notifyCh:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for Apply")
	}
}

func runReconciler(t *testing.T, r *Reconciler) (context.CancelFunc, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	return cancel, errCh
}

// Idle Run unwinds on ctx cancel.
func TestReconciler_IdleCancellation(t *testing.T) {
	a := newMockActuator()
	r := NewReconciler(a)

	cancel, errCh := runReconciler(t, r)

	// Give the loop a moment to settle into the idle select.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 0, a.CallsCount())

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Reconciler.Run did not return after cancel")
	}
}

// Pre-Run Switch is observed on iter 1.
func TestReconciler_SwitchBeforeRun(t *testing.T) {
	a := newMockActuator()
	r := NewReconciler(
		a,
		WithReconcileInterval(time.Hour), // would block forever without preemption
	)

	stage := &StageConfig{Name: "boot"}
	r.Switch(stage)

	cancel, errCh := runReconciler(t, r)
	defer func() {
		cancel()
		<-errCh
	}()

	waitApply(t, a, time.Second)

	require.Equal(t, 1, a.CallsCount())
	require.Same(t, stage, a.LastCall())
}

// Switch wakes a post-success sleep.
func TestReconciler_SwitchPreemptsSleep(t *testing.T) {
	a := newMockActuator()
	r := NewReconciler(
		a,
		WithReconcileInterval(time.Hour), // would block forever without preemption
	)

	first := &StageConfig{Name: "first"}
	r.Switch(first)

	cancel, errCh := runReconciler(t, r)
	defer func() {
		cancel()
		<-errCh
	}()

	waitApply(t, a, time.Second)

	second := &StageConfig{Name: "second"}
	start := time.Now()
	r.Switch(second)

	waitApply(t, a, time.Second)

	require.Less(t, time.Since(start), 800*time.Millisecond,
		"Reconciler.Switch should preempt the long sleep, not wait for the interval")
	require.Equal(t, "second", a.LastCall().Name)
}

// Error backs off, success resets.
//
// The first call returns an error and the loop retries after a short
// backoff sleep (not the long success interval); the subsequent
// successful Apply resets the backoff and the loop falls into the
// configured interval.
func TestReconciler_BackoffOnErrorThenReset(t *testing.T) {
	a := newMockActuator()
	a.QueueErr(errors.New("boom")) // first call fails, second succeeds

	r := NewReconciler(
		a,
		WithReconcileInterval(time.Hour),
		WithReconcileBackoff(5*time.Millisecond, 50*time.Millisecond),
	)

	stage := &StageConfig{Name: "stage"}
	r.Switch(stage)

	cancel, errCh := runReconciler(t, r)
	defer func() {
		cancel()
		<-errCh
	}()

	// First Apply: errors out.
	waitApply(t, a, time.Second)
	require.Equal(t, 1, a.CallsCount())

	// Second Apply happens after a short backoff sleep, well under the
	// hour-long success interval.
	start := time.Now()
	waitApply(t, a, time.Second)

	require.Less(t, time.Since(start), 800*time.Millisecond,
		"retry should occur after backoff, not after interval")
	require.Equal(t, 2, a.CallsCount())

	// After success the next sleep is `interval` (one hour), so no
	// further Apply call should fire within a short window.
	select {
	case <-a.notifyCh:
		t.Fatalf("unexpected Apply: backoff should have reset to interval")
	case <-time.After(100 * time.Millisecond):
	}
}

// SetStages walks the queue: each stage is applied successfully
// before the next one is exposed, and the tail stage is retained as
// the steady-state target.
func TestReconciler_WalksQueue(t *testing.T) {
	a := newMockActuator()
	a.QueueErr(errors.New("boom")) // first apply of s1 fails

	r := NewReconciler(
		a,
		WithReconcileInterval(time.Hour),
		WithReconcileBackoff(5*time.Millisecond, 50*time.Millisecond),
	)

	s1 := &StageConfig{Name: "s1"}
	s2 := &StageConfig{Name: "s2"}
	s3 := &StageConfig{Name: "s3"}
	r.SetStages([]*StageConfig{s1, s2, s3})

	cancel, errCh := runReconciler(t, r)
	defer func() {
		cancel()
		<-errCh
	}()

	require.Eventually(t, func() bool { return a.CallsCount() >= 4 },
		time.Second, 5*time.Millisecond)

	calls := a.Calls()
	require.Equal(t, "s1", calls[0].Name, "first apply: s1 (fails)")
	require.Equal(t, "s1", calls[1].Name, "s1 must succeed before advancing")
	require.Equal(t, "s2", calls[2].Name, "advance to s2")
	require.Equal(t, "s3", calls[3].Name, "advance to s3")

	// Once the queue collapses to s3 the next sleep is the interval
	// (one hour), so no further Apply call should fire within a
	// short window.
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 4, a.CallsCount(),
		"queue should hold s3 until interval elapses")
}

// Switch atomically replaces a queue mid-walk.
func TestReconciler_SwitchReplacesQueue(t *testing.T) {
	a := newMockActuator()

	r := NewReconciler(
		a,
		WithReconcileInterval(time.Hour),
	)

	s1 := &StageConfig{Name: "s1"}
	s2 := &StageConfig{Name: "s2"}
	other := &StageConfig{Name: "other"}
	r.SetStages([]*StageConfig{s1, s2})

	cancel, errCh := runReconciler(t, r)
	defer func() {
		cancel()
		<-errCh
	}()

	require.Eventually(t, func() bool { return a.CallsCount() >= 2 },
		time.Second, 5*time.Millisecond)

	r.Switch(other)

	require.Eventually(t, func() bool {
		c := a.LastCall()
		return c != nil && c.Name == "other"
	}, time.Second, 5*time.Millisecond,
		"Switch must replace the queue, not let it advance further")

	countBefore := a.CallsCount()
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, countBefore, a.CallsCount(),
		"queue should hold other until interval elapses")
}
