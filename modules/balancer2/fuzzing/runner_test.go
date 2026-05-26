package fuzzing

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// manualTicker is the test-side tickerLike. Tests drive the runner one
// operation at a time by calling Fire(); the runner's loop receives a
// tick on the next iteration of its select.
type manualTicker struct {
	ch chan time.Time
}

func newManualTicker() *manualTicker {
	return &manualTicker{ch: make(chan time.Time, 8)}
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}
func (m *manualTicker) Fire()               { m.ch <- time.Time{} }

// runnerFakeRPC is the BalancerRPC fake used by the runner tests. It is
// distinct from client_test.go's recordingRPC so each test file owns its
// own fixture. The fake records every call name in order; mutation RPCs
// apply the request onto an internal model so a subsequent GetState
// reflects the post-mutation shape automatically.
type runnerFakeRPC struct {
	mu sync.Mutex

	calls []string

	state *runnerFakeState

	failOnce        map[string]error
	updateRealsReqs []*balancerpb.UpdateRealsRequest

	overrideGetState func() (*balancerpb.GetStateResponse, error)

	stats *LatencyStats
}

// runnerFakeState is the controlplane-side view the fake maintains. It
// mirrors the candidate model exactly when mutations are well-formed so
// CompareState passes.
type runnerFakeState struct {
	configName string
	vs         map[VsKey]*runnerFakeVS
	order      []VsKey
}

type runnerFakeVS struct {
	key            VsKey
	scheduler      balancerpb.VsScheduler
	flags          VsFlags
	allowedSources []CIDR
	reals          map[RealKey]*runnerFakeReal
	realsOrder     []RealKey
}

type runnerFakeReal struct {
	enabled bool
	weight  uint32
}

func newRunnerFakeRPC(configName string) *runnerFakeRPC {
	return &runnerFakeRPC{
		state:    &runnerFakeState{configName: configName, vs: map[VsKey]*runnerFakeVS{}},
		failOnce: map[string]error{},
	}
}

func (m *runnerFakeRPC) recordCall(op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, op)
	var err error
	if injected, ok := m.failOnce[op]; ok {
		delete(m.failOnce, op)
		err = injected
	}
	if m.stats != nil {
		m.stats.Record(op, time.Microsecond, err)
	}
	return err
}

func (m *runnerFakeRPC) callSequence() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *runnerFakeRPC) UpdateConfig(
	_ context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	if err := m.recordCall(RPCUpdateConfig); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.vs = map[VsKey]*runnerFakeVS{}
	m.state.order = nil
	if req.GetVs() != nil {
		for _, vs := range req.GetVs().GetVs() {
			m.applyVsConfigLocked(vs)
		}
	}
	return &balancerpb.UpdateConfigResponse{}, nil
}

func (m *runnerFakeRPC) UpdateVS(
	_ context.Context,
	req *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	if err := m.recordCall(RPCUpdateVS); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, vs := range req.Vs {
		m.applyVsConfigLocked(vs)
	}
	return &balancerpb.UpdateVSResponse{}, nil
}

func (m *runnerFakeRPC) DeleteVS(
	_ context.Context,
	req *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	if err := m.recordCall(RPCDeleteVS); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range req.Vs {
		key := identifierToVsKey(id)
		delete(m.state.vs, key)
		for idx, k := range m.state.order {
			if k == key {
				m.state.order = append(m.state.order[:idx], m.state.order[idx+1:]...)
				break
			}
		}
	}
	return &balancerpb.DeleteVSResponse{}, nil
}

func (m *runnerFakeRPC) UpdateReals(
	_ context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	if err := m.recordCall(RPCUpdateReals); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRealsReqs = append(m.updateRealsReqs, req)
	for _, u := range req.Updates {
		vsKey := identifierToVsKey(u.RealId.Vs)
		vs, ok := m.state.vs[vsKey]
		if !ok {
			continue
		}
		realKey := identifierToRealKey(u.RealId.Real)
		real, ok := vs.reals[realKey]
		if !ok {
			continue
		}
		if u.Enable != nil {
			real.enabled = *u.Enable
		}
		if u.Weight != nil {
			real.weight = *u.Weight
		}
	}
	return &balancerpb.UpdateRealsResponse{}, nil
}

func (m *runnerFakeRPC) GetState(
	_ context.Context,
	_ *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	if err := m.recordCall(RPCGetState); err != nil {
		return nil, err
	}
	if m.overrideGetState != nil {
		return m.overrideGetState()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buildGetStateResponseLocked(), nil
}

func (m *runnerFakeRPC) applyVsConfigLocked(vs *balancerpb.VsConfig) {
	if vs == nil || vs.Id == nil {
		return
	}
	key := identifierToVsKey(vs.Id)
	existing, ok := m.state.vs[key]
	if !ok {
		existing = &runnerFakeVS{
			key:        key,
			reals:      map[RealKey]*runnerFakeReal{},
			realsOrder: nil,
		}
		m.state.vs[key] = existing
		m.state.order = append(m.state.order, key)
	}
	existing.scheduler = vs.Scheduler
	if vs.Flags != nil {
		existing.flags = VsFlags{
			Gre:    vs.Flags.Gre,
			FixMss: vs.Flags.FixMss,
			PureL3: vs.Flags.PureL3,
		}
	} else {
		existing.flags = VsFlags{}
	}
	existing.allowedSources = nil
	for _, src := range vs.AllowedSources {
		for _, n := range src.Nets {
			existing.allowedSources = append(existing.allowedSources, CIDR{
				Addr: append([]byte(nil), n.Addr...),
				Mask: append([]byte(nil), n.Mask...),
			})
		}
	}
	existing.reals = map[RealKey]*runnerFakeReal{}
	existing.realsOrder = nil
	for _, r := range vs.Reals {
		if r.Id == nil {
			continue
		}
		rk := identifierToRealKey(r.Id)
		enabled := true
		if r.Enabled != nil {
			enabled = *r.Enabled
		}
		var weight uint32 = 1
		if r.Weight != nil {
			weight = *r.Weight
		}
		existing.reals[rk] = &runnerFakeReal{enabled: enabled, weight: weight}
		existing.realsOrder = append(existing.realsOrder, rk)
	}
}

func (m *runnerFakeRPC) buildGetStateResponseLocked() *balancerpb.GetStateResponse {
	state := &balancerpb.BalancerState{ConfigName: m.state.configName}
	for _, key := range m.state.order {
		vs := m.state.vs[key]
		cfg := &balancerpb.VsConfig{
			Id:        vsKeyToIdentifier(vs.key),
			Scheduler: vs.scheduler,
			Flags: &balancerpb.VsFlags{
				Gre:    vs.flags.Gre,
				FixMss: vs.flags.FixMss,
				PureL3: vs.flags.PureL3,
			},
		}
		reals := make([]*balancerpb.RealState, 0, len(vs.realsOrder))
		for _, rk := range vs.realsOrder {
			real := vs.reals[rk]
			reals = append(reals, &balancerpb.RealState{
				Config: &balancerpb.RealConfig{
					Id: realKeyToIdentifier(rk),
				},
				EffectiveWeight: uint64(real.weight),
				Enabled:         real.enabled,
			})
		}
		vsState := &balancerpb.VsState{Config: cfg, Reals: reals}
		if len(vs.allowedSources) > 0 {
			stats := make([]*balancerpb.AllowedSourcesStats, 0, len(vs.allowedSources))
			for range vs.allowedSources {
				stats = append(stats, &balancerpb.AllowedSourcesStats{})
			}
			vsState.AllowedSourcesStats = stats
		}
		state.Vs = append(state.Vs, vsState)
	}
	return &balancerpb.GetStateResponse{States: []*balancerpb.BalancerState{state}}
}

func (m *runnerFakeRPC) seedFromModel(model *Model) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.vs = map[VsKey]*runnerFakeVS{}
	m.state.order = append([]VsKey(nil), model.ActiveOrder()...)
	for _, key := range model.ActiveOrder() {
		vs := model.ActiveVS(key)
		if vs == nil {
			continue
		}
		copyVS := &runnerFakeVS{
			key:            key,
			scheduler:      vs.Scheduler,
			flags:          vs.Flags,
			allowedSources: append([]CIDR(nil), vs.AllowedSources...),
			reals:          map[RealKey]*runnerFakeReal{},
			realsOrder:     append([]RealKey(nil), vs.Reals()...),
		}
		for _, rk := range vs.Reals() {
			real := vs.Real(rk)
			if real == nil {
				continue
			}
			copyVS.reals[rk] = &runnerFakeReal{
				enabled: real.Enabled,
				weight:  real.Weight,
			}
		}
		m.state.vs[key] = copyVS
	}
}

// identifierToVsKey is the test-side inverse of vsKeyToIdentifier; the
// runner's canonical16 path is unnecessary here because the fake only
// sees addresses the runner just wrote.
func identifierToVsKey(id *balancerpb.VsIdentifier) VsKey {
	var key VsKey
	copy(key.IP[:], net.IP(id.GetAddr()).To16())
	key.Port = uint16(id.GetPort())
	key.Proto = id.GetProto()
	return key
}

func identifierToRealKey(id *balancerpb.RelativeRealIdentifier) RealKey {
	var key RealKey
	copy(key.IP[:], net.IP(id.GetIp()).To16())
	key.Port = uint16(id.GetPort())
	return key
}

// runnerTestConfig builds a deterministic RuntimeConfig the runner tests
// share. Seed and cadence are fixed so the operation stream is stable.
func runnerTestConfig() *RuntimeConfig {
	return &RuntimeConfig{
		Endpoint:          "127.0.0.1:0",
		CorpusPath:        "synthetic.conf",
		ConfigName:        "fuzz-cfg",
		OperationInterval: 10 * time.Millisecond,
		UpdateVsEvery:     2,
		StatsInterval:     time.Hour,
		RequestTimeout:    250 * time.Millisecond,
		Seed:              99,
	}
}

// syncBuffer wraps bytes.Buffer with a mutex so the runner goroutine
// and the test goroutine can read/write the log destination without
// data races.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *syncBuffer) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}

func (m *syncBuffer) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

// buildRunner wires the runner with a manual operation ticker, a manual
// stats ticker, an in-memory log buffer, and no signal handlers. The
// returned helper.Fire() advances the runner by one operation.
type runnerHarness struct {
	t           *testing.T
	runner      *Runner
	fake        *runnerFakeRPC
	logBuf      *syncBuffer
	opTicker    *manualTicker
	statsTicker *manualTicker
	stats       *LatencyStats
}

func newRunnerHarness(t *testing.T, corpusText string, options ...RunnerOption) *runnerHarness {
	t.Helper()
	corpus, err := ParseServicesCorpusFromReader("synthetic.conf", strings.NewReader(corpusText))
	require.NoError(t, err)

	cfg := runnerTestConfig()
	stats := NewLatencyStats()
	fake := newRunnerFakeRPC(cfg.ConfigName)
	fake.stats = stats
	logBuf := &syncBuffer{}
	opTicker := newManualTicker()
	statsTicker := newManualTicker()

	defaults := []RunnerOption{
		WithLogger(logBuf),
		WithSignals(),
		WithOperationTicker(func(time.Duration) tickerLike { return opTicker }),
		WithStatsTicker(func(time.Duration) tickerLike { return statsTicker }),
	}
	runner, err := NewRunner(cfg, corpus, fake, stats, append(defaults, options...)...)
	require.NoError(t, err)
	fake.seedFromModel(runner.Model())
	return &runnerHarness{
		t:           t,
		runner:      runner,
		fake:        fake,
		logBuf:      logBuf,
		opTicker:    opTicker,
		statsTicker: statsTicker,
		stats:       stats,
	}
}

// runWithSteps drives the runner through exactly steps operations using
// a bounded-operation limit so Run returns nil on graceful completion.
func (h *runnerHarness) runWithSteps(steps uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- h.runner.Run(ctx)
	}()

	for idx := uint64(0); idx < steps; idx++ {
		h.opTicker.Fire()
	}

	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		h.t.Fatal("runner did not finish within timeout")
		return nil
	}
}

// TestRunnerCommitsOnlyAfterGetStateMatch drives a small corpus through
// several operations and confirms that every mutation is paired with a
// GetState call, the model active count advances correctly, and the
// committed model matches the candidate after each successful step.
func TestRunnerCommitsOnlyAfterGetStateMatch(t *testing.T) {
	const steps = uint64(6)
	corpus := genCorpusText(5, 3)

	h := newRunnerHarness(t, corpus, WithOperationLimit(steps))
	require.NoError(t, h.runWithSteps(steps))

	calls := h.fake.callSequence()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, RPCUpdateVS, calls[0])
	assert.Equal(t, RPCGetState, calls[1])

	mutations := []string{RPCUpdateVS, RPCDeleteVS, RPCUpdateReals}
	mutationCount := 0
	for idx := 0; idx < len(calls); idx++ {
		c := calls[idx]
		if !contains(mutations, c) {
			continue
		}
		mutationCount++
		require.Less(t, idx+1, len(calls), "mutation %s at %d must be followed by GetState", c, idx)
		assert.Equal(t, RPCGetState, calls[idx+1],
			"mutation %s at index %d must be immediately followed by GetState", c, idx)
	}
	assert.Equal(t, int(steps), mutationCount, "exactly one mutation per operation")

	model := h.runner.Model()
	assert.GreaterOrEqual(t, model.ActiveCount(), model.MinActive())
	assert.LessOrEqual(t, model.ActiveCount(), model.OriginalCount())
}

// TestRunnerDoesNotCommitOnRPCFailure injects an RPC error on the first
// mutation and asserts the committed model is byte-equal to its
// pre-operation snapshot.
func TestRunnerDoesNotCommitOnRPCFailure(t *testing.T) {
	corpus := genCorpusText(5, 3)
	h := newRunnerHarness(t, corpus, WithOperationLimit(1))

	preActive := append([]VsKey(nil), h.runner.Model().ActiveOrder()...)
	preState := snapshotModel(h.runner.Model())

	h.fake.failOnce[RPCUpdateVS] = errors.New("simulated rpc failure")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.runner.Run(ctx) }()
	h.opTicker.Fire()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update_vs")
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not return on RPC failure")
	}

	assert.Equal(t, preActive, h.runner.Model().ActiveOrder(),
		"active set must be unchanged after a failed mutation")
	assert.Equal(t, preState, snapshotModel(h.runner.Model()),
		"model state must be identical after a failed mutation")

	calls := h.fake.callSequence()
	for _, c := range calls {
		assert.NotEqual(t, RPCGetState, c, "GetState must not run after a failed mutation")
	}
}

// TestRunnerReturnsMismatchError forces the fake to return a GetState
// response that does not match the candidate; the runner must surface
// the mismatch paths as an error.
func TestRunnerReturnsMismatchError(t *testing.T) {
	corpus := genCorpusText(5, 3)
	h := newRunnerHarness(t, corpus, WithOperationLimit(1))

	h.fake.overrideGetState = func() (*balancerpb.GetStateResponse, error) {
		return &balancerpb.GetStateResponse{
			States: []*balancerpb.BalancerState{{ConfigName: "fuzz-cfg"}},
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.runner.Run(ctx) }()
	h.opTicker.Fire()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "state mismatch")
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not surface mismatch error")
	}

	assert.Contains(t, h.logBuf.String(), "op=Mismatch")
}

// TestRunnerStopsOnContextCancel verifies the loop returns nil when the
// caller cancels the parent context and does not start another
// operation after cancellation.
func TestRunnerStopsOnContextCancel(t *testing.T) {
	corpus := genCorpusText(5, 3)
	h := newRunnerHarness(t, corpus)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- h.runner.Run(ctx) }()

	h.opTicker.Fire()
	require.Eventually(t, func() bool {
		return strings.Contains(h.logBuf.String(), "op=GetState")
	}, time.Second, 5*time.Millisecond,
		"first operation must complete before cancellation so the test "+
			"deterministically observes the post-op cancellation path")
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not exit on context cancellation")
	}

	calls := h.fake.callSequence()
	mutationCount := 0
	for _, c := range calls {
		if c == RPCUpdateVS || c == RPCDeleteVS || c == RPCUpdateReals {
			mutationCount++
		}
	}
	assert.LessOrEqual(t, mutationCount, 1,
		"cancellation must prevent starting more than the in-flight operation")
}

// TestRunnerEmitsStatsReportOnTicker fires the stats ticker after a
// completed operation and asserts the stats line lands in the log.
func TestRunnerEmitsStatsReportOnTicker(t *testing.T) {
	corpus := genCorpusText(5, 3)
	h := newRunnerHarness(t, corpus)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- h.runner.Run(ctx) }()

	h.opTicker.Fire()
	require.Eventually(t, func() bool {
		return strings.Contains(h.logBuf.String(), "op=GetState")
	}, time.Second, 5*time.Millisecond, "operation did not complete in time")

	h.statsTicker.Fire()
	require.Eventually(t, func() bool {
		return strings.Contains(h.logBuf.String(), "stats op=")
	}, time.Second, 5*time.Millisecond,
		"stats report must appear in log after a tick on the stats ticker")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not finish")
	}
}

// TestRunnerDoesNotMutateConfigOnStartup verifies that runner execution
// starts directly with a mutation operation.
func TestRunnerDoesNotMutateConfigOnStartup(t *testing.T) {
	corpus := genCorpusText(5, 3)
	h := newRunnerHarness(t, corpus, WithOperationLimit(1))
	require.NoError(t, h.runWithSteps(1))

	calls := h.fake.callSequence()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, RPCUpdateVS, calls[0])
	assert.Equal(t, RPCGetState, calls[1])
}

func TestRunnerUpdateRealsSendsMultipleVSInSingleRPC(t *testing.T) {
	corpus := genCorpusText(6, 4)
	h := newRunnerHarness(t, corpus, WithOperationLimit(3))
	require.NoError(t, h.runWithSteps(3))

	calls := h.fake.callSequence()
	require.GreaterOrEqual(t, len(calls), 6)
	assert.Equal(t, RPCUpdateVS, calls[0])
	assert.Equal(t, RPCGetState, calls[1])
	assert.Equal(t, RPCUpdateVS, calls[2])
	assert.Equal(t, RPCGetState, calls[3])
	assert.Equal(t, RPCUpdateReals, calls[4])
	assert.Equal(t, RPCGetState, calls[5])

	require.Len(t, h.fake.updateRealsReqs, 1)
	req := h.fake.updateRealsReqs[0]
	require.NotEmpty(t, req.Updates)

	seen := map[VsKey]bool{}
	for _, update := range req.Updates {
		require.NotNil(t, update.RealId)
		require.NotNil(t, update.RealId.Vs)
		seen[identifierToVsKey(update.RealId.Vs)] = true
	}
	assert.GreaterOrEqual(t, len(seen), 2, "single UpdateReals RPC must include multiple VS")
}

// TestRunnerValidatesArguments pins the constructor's nil-input handling.
func TestRunnerValidatesArguments(t *testing.T) {
	cfg := runnerTestConfig()
	corpus, err := ParseServicesCorpusFromReader("synthetic.conf",
		strings.NewReader(genCorpusText(1, 1)))
	require.NoError(t, err)
	stats := NewLatencyStats()
	fake := newRunnerFakeRPC(cfg.ConfigName)

	_, err = NewRunner(nil, corpus, fake, stats)
	require.Error(t, err)

	_, err = NewRunner(cfg, nil, fake, stats)
	require.Error(t, err)

	_, err = NewRunner(cfg, corpus, nil, stats)
	require.Error(t, err)

	_, err = NewRunner(cfg, corpus, fake, nil)
	require.Error(t, err)
}

// modelSnapshot is a compact representation of a model used by tests
// that need to assert byte-for-byte equality across a failed operation.
type modelSnapshot struct {
	active []VsKey
	vs     map[VsKey]vsSnapshot
}

type vsSnapshot struct {
	scheduler      balancerpb.VsScheduler
	flags          VsFlags
	allowedSources []CIDR
	reals          []realSnapshot
}

type realSnapshot struct {
	key     RealKey
	enabled bool
	weight  uint32
}

func snapshotModel(m *Model) modelSnapshot {
	snap := modelSnapshot{
		active: append([]VsKey(nil), m.ActiveOrder()...),
		vs:     map[VsKey]vsSnapshot{},
	}
	for _, key := range m.ActiveOrder() {
		vs := m.ActiveVS(key)
		vsSnap := vsSnapshot{
			scheduler:      vs.Scheduler,
			flags:          vs.Flags,
			allowedSources: append([]CIDR(nil), vs.AllowedSources...),
		}
		for _, rk := range vs.Reals() {
			r := vs.Real(rk)
			vsSnap.reals = append(vsSnap.reals, realSnapshot{
				key:     rk,
				enabled: r.Enabled,
				weight:  r.Weight,
			})
		}
		snap.vs[key] = vsSnap
	}
	return snap
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
