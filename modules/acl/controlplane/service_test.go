package acl

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
)

const metricsConcurrencyTestTimeout = 5 * time.Second

// concurrencyWaitTimeout bounds the wait for a channel signal in a
// concurrency test. Reaching it means the operation under test stalled
// where it must not, so the test fails instead of hanging forever.
const concurrencyWaitTimeout = 5 * time.Second

// waitOnChan waits for ch to fire, failing the test if concurrencyWaitTimeout
// elapses first.
func waitOnChan(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal(msg)
	}
}

// fakeHandle is an in-memory implementation of ModuleHandle for tests.
type fakeHandle struct {
	mu          sync.Mutex
	name        string
	rules       []cacl.AclRule
	freeCount   int
	transferred bool
}

func (m *fakeHandle) Free() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.freeCount++
}

// FreeCount returns how many times Free has been called, so a test can
// assert a handle was freed exactly once and never left dangling.
func (m *fakeHandle) FreeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.freeCount
}

func (m *fakeHandle) AsFFIModule() ffi.ModuleConfig {
	return ffi.ModuleConfig{}
}

func (m *fakeHandle) UpdateRules(rules []cacl.AclRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rules = rules
	return nil
}

func (m *fakeHandle) SetFwStateConfig(_ ffi.ModuleConfig) {}

func (m *fakeHandle) TransferFwStateConfig(_ ffi.ModuleConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transferred = true
}

func (m *fakeHandle) GetInfo() *cacl.AclConfigInfo {
	return &cacl.AclConfigInfo{
		CompilationTimeNs:  42,
		FilterRuleCountIp4: 7,
	}
}

// fakeBackend is an in-memory implementation of Backend for tests.
type fakeBackend struct {
	mu           sync.Mutex
	modules      map[string]*fakeHandle
	created      []*fakeHandle
	publishCalls int
	newModuleErr error
	deleteErr    error
	memoryBytes  uint64
}

type updateBlock struct {
	entered chan struct{}
	release chan struct{}
}

// blockingUpdateBackend can pause one UpdateModule call without holding the
// fake backend mutex, allowing concurrent metrics collection in the test.
type blockingUpdateBackend struct {
	*fakeBackend

	blockMu   sync.Mutex
	nextBlock *updateBlock
}

func newBlockingUpdateBackend(memoryBytes uint64) *blockingUpdateBackend {
	return &blockingUpdateBackend{fakeBackend: newFakeBackend(memoryBytes)}
}

func (m *blockingUpdateBackend) blockNextUpdate() (<-chan struct{}, func()) {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	block := &updateBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.nextBlock = block

	return block.entered, sync.OnceFunc(func() {
		close(block.release)
	})
}

func (m *blockingUpdateBackend) UpdateModule(handle ModuleHandle) error {
	m.blockMu.Lock()
	block := m.nextBlock
	m.nextBlock = nil
	m.blockMu.Unlock()

	if block != nil {
		close(block.entered)
		<-block.release
	}

	return m.fakeBackend.UpdateModule(handle)
}

func newFakeBackend(memoryBytes uint64) *fakeBackend {
	return &fakeBackend{
		modules:     map[string]*fakeHandle{},
		memoryBytes: memoryBytes,
	}
}

func (m *fakeBackend) NewModule(name string) (ModuleHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.newModuleErr != nil {
		return nil, m.newModuleErr
	}

	h := &fakeHandle{name: name}
	m.modules[name] = h
	m.created = append(m.created, h)
	return h, nil
}

// CreatedHandles returns every handle NewModule has ever produced, in
// creation order, so a test can audit that each was freed exactly once.
func (m *fakeBackend) CreatedHandles() []*fakeHandle {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]*fakeHandle(nil), m.created...)
}

func (m *fakeBackend) UpdateModule(_ ModuleHandle) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.publishCalls++
	return nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.modules, name)
	return nil
}

func (m *fakeBackend) MemoryBytes() uint64 {
	return m.memoryBytes
}

func (m *fakeBackend) DPConfig() *ffi.DPConfig {
	return nil
}

// PublishCalls returns the number of UpdateModule calls observed.
func (m *fakeBackend) PublishCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.publishCalls
}

// SetNewModuleErr arms the next NewModule call to return err.
func (m *fakeBackend) SetNewModuleErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.newModuleErr = err
}

// compileBlock synchronizes one blocked compile with the test: entered
// signals that UpdateRules has started, release lets it return.
type compileBlock struct {
	entered chan struct{}
	release chan struct{}
}

// compileBlockingBackend wraps fakeBackend so a test can arm a specific
// config name's next UpdateRules call to block until released, proving
// overlap or ordering between compiles via channel synchronization instead
// of timing. Every UpdateRules call, blocked or not, is recorded in
// entryOrder for tests that only need to assert relative ordering.
type compileBlockingBackend struct {
	*fakeBackend

	mu      sync.Mutex
	entries []string
	blocks  map[string]*compileBlock
}

func newCompileBlockingBackend(memoryBytes uint64) *compileBlockingBackend {
	return &compileBlockingBackend{
		fakeBackend: newFakeBackend(memoryBytes),
		blocks:      map[string]*compileBlock{},
	}
}

// blockCompile arms the next UpdateRules call for name to block until the
// returned release function is called. The returned channel closes once
// that call has entered UpdateRules.
func (m *compileBlockingBackend) blockCompile(name string) (<-chan struct{}, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	block := &compileBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.blocks[name] = block

	return block.entered, sync.OnceFunc(func() {
		close(block.release)
	})
}

// recordEntry logs that UpdateRules for name has started and returns and
// consumes any block armed for name.
func (m *compileBlockingBackend) recordEntry(name string) *compileBlock {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, name)
	block := m.blocks[name]
	delete(m.blocks, name)

	return block
}

// entryOrder returns the config names in the order their UpdateRules calls
// started.
func (m *compileBlockingBackend) entryOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string(nil), m.entries...)
}

func (m *compileBlockingBackend) NewModule(name string) (ModuleHandle, error) {
	handle, err := m.fakeBackend.NewModule(name)
	if err != nil {
		return nil, err
	}

	return &compileBlockingHandle{ModuleHandle: handle, backend: m, name: name}, nil
}

// compileBlockingHandle records and optionally blocks its UpdateRules call
// before delegating to the wrapped fakeHandle.
type compileBlockingHandle struct {
	ModuleHandle
	backend *compileBlockingBackend
	name    string
}

func (m *compileBlockingHandle) UpdateRules(rules []cacl.AclRule) error {
	block := m.backend.recordEntry(m.name)
	if block != nil {
		close(block.entered)
		<-block.release
	}

	return m.ModuleHandle.UpdateRules(rules)
}

func newTestService(b Backend) *ACLService {
	return NewACLService(b)
}

// TestConvertRulesCounter verifies that the counter field from a proto Rule
// is correctly propagated to the corresponding AclRule.
func TestConvertRulesCounter(t *testing.T) {
	tests := []struct {
		name     string
		rules    []*aclpb.Rule
		wantCnts []string
	}{
		{
			name: "single rule with counter",
			rules: []*aclpb.Rule{
				{Counter: "counterA"},
			},
			wantCnts: []string{"counterA"},
		},
		{
			name: "multiple rules preserve order and values",
			rules: []*aclpb.Rule{
				{Counter: "first"},
				{Counter: "second"},
				{Counter: "third"},
			},
			wantCnts: []string{"first", "second", "third"},
		},
		{
			name: "empty counter is preserved as empty",
			rules: []*aclpb.Rule{
				{Counter: ""},
			},
			wantCnts: []string{""},
		},
		{
			name: "mixed empty and non-empty counters",
			rules: []*aclpb.Rule{
				{Counter: "named"},
				{Counter: ""},
				{Counter: "also-named"},
			},
			wantCnts: []string{"named", "", "also-named"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertRules(tc.rules)
			require.NoError(t, err)
			require.Len(t, got, len(tc.wantCnts))
			for idx, want := range tc.wantCnts {
				assert.Equal(t, want, got[idx].Counter)
			}
		})
	}
}

// TestUpdateConfig_Idempotency verifies that calling UpdateConfig twice with
// identical rules does not publish a second time.
func TestUpdateConfig_Idempotency(t *testing.T) {
	b := newFakeBackend(0)
	svc := newTestService(b)

	req := &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
	}

	_, err := svc.UpdateConfig(t.Context(), req)
	require.NoError(t, err)

	publishBefore := b.PublishCalls()

	_, err = svc.UpdateConfig(t.Context(), req)
	require.NoError(t, err)

	publishAfter := b.PublishCalls()

	assert.Equal(t, publishBefore, publishAfter, "second call with identical rules must not publish")
}

// TestUpdateConfig_ErrorPropagation verifies that a backend failure from
// NewModule returns codes.Internal and leaves the service config unchanged.
func TestUpdateConfig_ErrorPropagation(t *testing.T) {
	b := newFakeBackend(0)
	svc := newTestService(b)

	// Pre-populate a config so we can verify it is unchanged after the error.
	initialRules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: initialRules,
	})
	require.NoError(t, err)

	// Confirm the initial config exists.
	_, err = svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: "acl0"})
	require.NoError(t, err)

	// Inject an error for the next NewModule call.
	b.SetNewModuleErr(assert.AnError)

	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
	})
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())

	// The original config must still be present and unchanged.
	resp, err := svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: "acl0"})
	require.NoError(t, err)
	assert.True(t, rulesEqual(initialRules, resp.Rules), "config rules must not have changed after failed update")
}

// TestUpdateConfig_RejectsEmptyRuleset verifies that an empty ruleset is
// rejected with codes.InvalidArgument before reaching the backend, for both
// a nil and an explicitly empty rule slice.
func TestUpdateConfig_RejectsEmptyRuleset(t *testing.T) {
	tests := []struct {
		name  string
		rules []*aclpb.Rule
	}{
		{name: "nil rules", rules: nil},
		{name: "empty rules", rules: []*aclpb.Rule{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newFakeBackend(0)
			svc := newTestService(b)

			_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
				Name:  "acl0",
				Rules: tc.rules,
			})
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Equal(t, 0, b.PublishCalls(), "backend must not be asked to publish")
			assert.Empty(t, b.modules, "no module must be created")
		})
	}
}

// TestConvertRules_RejectsUnknownActionKind ensures unrecognized action kinds
// become a client error rather than silently mapping to ALLOW.
func TestConvertRules_RejectsUnknownActionKind(t *testing.T) {
	_, err := convertRules([]*aclpb.Rule{{
		Actions: []*aclpb.Action{{Kind: aclpb.ActionKind(999)}},
	}})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateConfig_RejectsNonContiguousMask verifies that a rule with a
// non-contiguous network mask is rejected with codes.InvalidArgument before
// reaching the backend.
func TestUpdateConfig_RejectsNonContiguousMask(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	req := &aclpb.UpdateConfigRequest{
		Name: "acl0",
		Rules: []*aclpb.Rule{
			{
				Srcs: []*filterpb.IPNet{
					{
						Addr: []byte{192, 0, 2, 0},
						Mask: []byte{0xff, 0x00, 0xff, 0x00},
					},
				},
			},
		},
	}

	_, err := svc.UpdateConfig(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestDeleteConfig_UnknownConfig verifies that DeleteConfig reports
// codes.NotFound for a config name that was never applied, and that the
// rejected name is not interned into svc.configs: a client that misspells a
// delete repeatedly must not grow the map.
func TestDeleteConfig_UnknownConfig(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))

	svc.mu.RLock()
	_, ok := svc.configs["missing"]
	svc.mu.RUnlock()
	assert.False(t, ok, "an unknown delete target must not create an entry")
}

// TestDeleteConfig_LiveNameTombstones verifies that deleting a name with a
// live published config still succeeds and leaves the entry in svc.configs
// with published set to nil, rather than removing the entry outright. That
// tombstone is what lets a later UpdateConfig of the same name reuse the
// entry instead of racing a fresh one into existence. It also verifies that
// a second delete of the now-tombstoned name reports codes.NotFound: the
// existence-only fast-path pre-check lets the tombstoned name through since
// its entry is still present, and the locked path's own check of published
// is what actually rejects it.
func TestDeleteConfig_LiveNameTombstones(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	name := "acl0"
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  name,
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.NoError(t, err)

	svc.mu.RLock()
	entry, ok := svc.configs[name]
	svc.mu.RUnlock()
	require.True(t, ok, "the entry for a deleted live name must be retained as a tombstone")
	assert.Nil(t, entry.published, "a tombstoned entry must have published set to nil")

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.Equal(t, codes.NotFound, status.Code(err), "deleting an already-tombstoned name must report NotFound")
}

// TestDeleteConfig_WaitsBehindInFlightCreate verifies that a DeleteConfig
// racing an UpdateConfig that is creating a brand-new name waits behind the
// name's lock instead of returning NotFound while the create's compile is
// still in flight. In that window the entry already exists but published is
// still nil, so a pre-check keyed on liveness rather than existence would
// return NotFound immediately instead of serializing behind the create and
// then acting on its result. This test forces exactly that window with
// compileBlockingBackend and asserts the delete only resolves after the
// create publishes, then removes the config the create just published.
func TestDeleteConfig_WaitsBehindInFlightCreate(t *testing.T) {
	backend := newCompileBlockingBackend(0)
	svc := newTestService(backend)

	name := "new-config"
	rules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}

	entered, release := backend.blockCompile(name)
	t.Cleanup(release)

	updateDone := make(chan error, 1)
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: name, Rules: rules})
		updateDone <- err
	}()

	waitOnChan(t, entered, "create did not reach UpdateRules")

	deleteDone := make(chan error, 1)
	go func() {
		_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
		deleteDone <- err
	}()

	// deleteDone firing here would mean DeleteConfig resolved without
	// waiting for the create's compile to release the name's lock, which is
	// only possible through the liveness-keyed pre-check this test guards
	// against.
	select {
	case err := <-deleteDone:
		t.Fatalf("DeleteConfig returned (err=%v) before the racing create released its compile", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()

	select {
	case err := <-updateDone:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("UpdateConfig did not finish after release")
	}

	select {
	case err := <-deleteDone:
		require.NoError(t, err, "the delete must succeed once it can see the create's published config")
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("DeleteConfig did not finish after the racing create released its compile")
	}

	svc.mu.RLock()
	entry, ok := svc.configs[name]
	svc.mu.RUnlock()
	require.True(t, ok, "the entry created by the racing update must survive the delete as a tombstone")
	assert.Nil(t, entry.published, "the delete must have tombstoned the config the racing create just published")

	assert.Equal(t, 1, backend.PublishCalls(), "the create must have published its module before the delete could act on it")

	created := backend.CreatedHandles()
	require.Len(t, created, 1, "the racing create must have produced exactly one handle")
	assert.Equal(t, 1, created[0].FreeCount(), "the delete must free the just-published handle exactly once")
}

// TestUpdateConfig_ResurrectsThroughTombstone verifies that Update, Delete,
// then Update of the same name reuses the entry DeleteConfig tombstoned
// instead of racing a fresh one into existence: Show returns the
// resurrected rules, List reports the name exactly once, and every handle
// the fakeBackend produced along the way is freed exactly once, except the
// one still live at the end.
func TestUpdateConfig_ResurrectsThroughTombstone(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)

	name := "acl0"
	firstRules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: name, Rules: firstRules})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.NoError(t, err)

	_, err = svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: name})
	require.Equal(t, codes.NotFound, status.Code(err), "a deleted name must read back as not found")

	newRules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}}
	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: name, Rules: newRules})
	require.NoError(t, err)

	resp, err := svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: name})
	require.NoError(t, err)
	assert.True(t, rulesEqual(newRules, resp.Rules), "Show must return the resurrected config's rules")

	listResp, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{name}, listResp.Configs, "List must report the resurrected name exactly once")

	svc.mu.RLock()
	liveHandle := svc.configs[name].published.acl
	svc.mu.RUnlock()

	for _, h := range backend.CreatedHandles() {
		if h == liveHandle {
			assert.Equal(t, 0, h.FreeCount(), "the live handle must not have been freed")
			continue
		}
		assert.Equal(t, 1, h.FreeCount(), "every superseded or deleted handle must be freed exactly once")
	}
}

// TestUpdateConfig_ConcurrentRace exercises UpdateConfig and ShowConfig under
// concurrent access to surface data races under go test -race.
func TestUpdateConfig_ConcurrentRace(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	var wg errgroup.Group
	for range 8 {
		wg.Go(func() error {
			name := "acl0"
			_, _ = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
				Name:  name,
				Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
			})
			_, _ = svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: name})
			_, _ = svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
			return nil
		})
	}

	require.NoError(t, wg.Wait())
}

// TestMetrics_DoesNotWaitForUpdateConfig verifies that a config update holding
// ACLService.mu cannot stall metrics collection.
func TestMetrics_DoesNotWaitForUpdateConfig(t *testing.T) {
	backend := newBlockingUpdateBackend(0)
	svc := NewACLService(backend, WithMetrics(grpcmetrics.NewFactory(
		grpcmetrics.WithLabeler(labeler),
	)))

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	// The fake backend has no dataplane config, so record a gRPC series first
	// to verify that Metrics returns actual data while the update is blocked.
	_, err = svc.UnaryServerInterceptor()(
		t.Context(),
		&aclpb.ShowConfigRequest{Name: "acl0"},
		&grpc.UnaryServerInfo{FullMethod: aclpb.ACLService_ShowConfig_FullMethodName},
		func(context.Context, any) (any, error) {
			return &aclpb.ShowConfigResponse{}, nil
		},
	)
	require.NoError(t, err)

	updateEntered, releaseUpdate := backend.blockNextUpdate()
	t.Cleanup(releaseUpdate)

	updateDone := make(chan error, 1)
	go func() {
		_, updateErr := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
			Name: "acl0",
			Rules: []*aclpb.Rule{{
				Actions: []*aclpb.Action{{
					Kind: aclpb.ActionKind_ACTION_KIND_DENY,
				}},
			}},
		})
		updateDone <- updateErr
	}()

	select {
	case <-updateEntered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("UpdateConfig did not reach the blocked backend update")
	}

	type metricsResult struct {
		count int
		err   error
	}
	metricsDone := make(chan metricsResult, 1)
	go func() {
		collected, metricsErr := svc.Metrics()
		metricsDone <- metricsResult{count: len(collected), err: metricsErr}
	}()

	select {
	case result := <-metricsDone:
		require.NoError(t, result.err)
		assert.Greater(t, result.count, 0)
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("metrics collection waited for UpdateConfig")
	}

	releaseUpdate()
	select {
	case updateErr := <-updateDone:
		require.NoError(t, updateErr)
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("UpdateConfig did not finish after the backend was released")
	}
}

func TestMetricsSnapshotTracksConfigLifecycle(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
	})
	require.NoError(t, err)

	beforeDelete := svc.metricsState.load()
	info, ok := beforeDelete.configInfo("acl0")
	require.True(t, ok)
	assert.Equal(t, uint64(42), info.CompilationTimeNs)
	assert.Equal(t, uint64(7), info.FilterRuleCountIp4)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{
		Name: "acl0",
	})
	require.NoError(t, err)

	afterDelete := svc.metricsState.load()
	assert.False(t, afterDelete.containsConfig("acl0"))
	assert.True(t, beforeDelete.containsConfig("acl0"), "published snapshots must remain immutable")
}

// TestMetricsSnapshotOrderingSurvivesConcurrentBarrage verifies that once a
// concurrent barrage of UpdateConfig and DeleteConfig calls across several
// config names has fully joined, the metrics snapshot's config-name set
// equals the configs map's key set exactly. Because publishMetricsSnapshotLocked
// only ever runs inside the same mu.Lock section that installs the map
// mutation it reflects, every publish is totally ordered with the mutations:
// the snapshot published by the last critical section to run is necessarily
// the last one published, so it must match the final map state. Before that
// fix, a snapshot computed from an earlier map state (for example a delete
// of one name) could publish after a snapshot computed from a later state
// (an update of a different name), overwriting the newer one and leaving
// the equality checked here false — the exact interleaving this test guards
// against, without pinning any particular schedule.
func TestMetricsSnapshotOrderingSurvivesConcurrentBarrage(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)

	names := []string{"a", "b", "c", "d"}
	rules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}

	var wg errgroup.Group
	for round := range 60 {
		name := names[round%len(names)]
		wg.Go(func() error {
			if round%3 == 0 {
				_, _ = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
			} else {
				_, _ = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: name, Rules: rules})
			}
			return nil
		})
	}
	require.NoError(t, wg.Wait())

	svc.mu.RLock()
	wantNames := make(map[string]struct{}, len(svc.configs))
	for name, entry := range svc.configs {
		if entry.published == nil {
			continue
		}
		wantNames[name] = struct{}{}
	}
	svc.mu.RUnlock()

	snapshot := svc.metricsState.load()
	gotNames := make(map[string]struct{}, len(snapshot.configInfos))
	for name := range snapshot.configInfos {
		gotNames[name] = struct{}{}
	}

	assert.Equal(t, wantNames, gotNames,
		"metrics snapshot config set must match the configs map exactly after the barrage settles")
}

// TestUpdateConfig_CompilesInParallel verifies that UpdateConfig calls for
// different names compile concurrently: both are proven to be inside
// UpdateRules at the same time via channel synchronization, not timing.
func TestUpdateConfig_CompilesInParallel(t *testing.T) {
	backend := newCompileBlockingBackend(0)
	svc := newTestService(backend)

	rules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}

	enteredA, releaseA := backend.blockCompile("a")
	enteredB, releaseB := backend.blockCompile("b")
	t.Cleanup(releaseA)
	t.Cleanup(releaseB)

	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "a", Rules: rules})
		doneA <- err
	}()
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "b", Rules: rules})
		doneB <- err
	}()

	// Neither release fires until both signal that they are inside
	// UpdateRules, so a pass here proves the two compiles overlapped.
	waitOnChan(t, enteredA, "config \"a\" did not reach UpdateRules")
	waitOnChan(t, enteredB, "config \"b\" did not reach UpdateRules")

	releaseA()
	releaseB()

	select {
	case err := <-doneA:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("UpdateConfig for \"a\" did not finish after release")
	}
	select {
	case err := <-doneB:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("UpdateConfig for \"b\" did not finish after release")
	}
}

// TestShowAndListConfigs_DoNotWaitForCompile verifies that ShowConfig
// returns the old published state, and ListConfigs completes, while another
// UpdateConfig call is parked inside a compile. Under a single global mutex
// both would hang until the compile released it.
func TestShowAndListConfigs_DoNotWaitForCompile(t *testing.T) {
	backend := newCompileBlockingBackend(0)
	svc := newTestService(backend)

	initialRules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "a", Rules: initialRules})
	require.NoError(t, err)

	newRules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}}
	entered, release := backend.blockCompile("a")
	t.Cleanup(release)

	updateDone := make(chan error, 1)
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "a", Rules: newRules})
		updateDone <- err
	}()

	waitOnChan(t, entered, "update did not reach UpdateRules")

	type showResult struct {
		resp *aclpb.ShowConfigResponse
		err  error
	}
	showDone := make(chan showResult, 1)
	go func() {
		resp, err := svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: "a"})
		showDone <- showResult{resp, err}
	}()

	select {
	case result := <-showDone:
		require.NoError(t, result.err)
		assert.True(t, rulesEqual(initialRules, result.resp.Rules),
			"ShowConfig must return the old published state while a compile is in flight")
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("ShowConfig waited for the in-flight compile")
	}

	listDone := make(chan error, 1)
	go func() {
		_, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
		listDone <- err
	}()

	select {
	case err := <-listDone:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("ListConfigs waited for the in-flight compile")
	}

	release()

	select {
	case err := <-updateDone:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("UpdateConfig did not finish after release")
	}
}

// TestUpdateConfig_SameNameSerializes verifies that two UpdateConfig calls
// for the same name serialize: the second cannot reach UpdateRules until the
// first has released the per-name lock, which is proven by lock acquisition
// order rather than a sleep-based race check.
func TestUpdateConfig_SameNameSerializes(t *testing.T) {
	backend := newCompileBlockingBackend(0)
	svc := newTestService(backend)

	rulesA := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}}
	rulesB := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}}

	enteredFirst, releaseFirst := backend.blockCompile("a")

	doneFirst := make(chan error, 1)
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "a", Rules: rulesA})
		doneFirst <- err
	}()

	waitOnChan(t, enteredFirst, "first update did not reach UpdateRules")

	// Arm a second block before starting the second call: since the first
	// block was already consumed, this block will only fire once the second
	// call reaches UpdateRules on its own turn.
	enteredSecond, releaseSecond := backend.blockCompile("a")
	t.Cleanup(releaseSecond)

	doneSecond := make(chan error, 1)
	go func() {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "a", Rules: rulesB})
		doneSecond <- err
	}()

	releaseFirst()

	select {
	case err := <-doneFirst:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("first UpdateConfig did not finish after release")
	}

	// The second call can only reach UpdateRules by acquiring the per-name
	// lock, which the first call releases only when it returns above — so
	// this signal firing at all proves the ordering, not merely its timing.
	waitOnChan(t, enteredSecond, "second update did not reach UpdateRules after the first finished")

	releaseSecond()

	select {
	case err := <-doneSecond:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("second UpdateConfig did not finish after release")
	}

	assert.Equal(t, []string{"a", "a"}, backend.entryOrder())

	resp, err := svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: "a"})
	require.NoError(t, err)
	assert.True(t, rulesEqual(rulesB, resp.Rules))
}

// TestUpdateAndDeleteConfig_NoDoubleFree verifies that concurrent
// UpdateConfig and DeleteConfig calls racing on the same name never free a
// handle twice and never leave a handle still referenced from configs freed.
func TestUpdateAndDeleteConfig_NoDoubleFree(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)

	name := "a"
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  name,
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	var wg errgroup.Group
	for idx := range 20 {
		wg.Go(func() error {
			if idx%2 == 0 {
				_, _ = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
					Name:  name,
					Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
				})
			} else {
				_, _ = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
			}
			return nil
		})
	}
	require.NoError(t, wg.Wait())

	svc.mu.RLock()
	entry := svc.configs[name]
	svc.mu.RUnlock()

	hasLive := entry.published != nil
	var liveHandle ModuleHandle
	if hasLive {
		liveHandle = entry.published.acl
	}

	for _, h := range backend.CreatedHandles() {
		if hasLive && h == liveHandle {
			assert.Equal(t, 0, h.FreeCount(), "the live handle must not have been freed")
			continue
		}
		assert.Equal(t, 1, h.FreeCount(), "every replaced or deleted handle must be freed exactly once")
	}
}

// TestRelinkConfigs_ConcurrentWithUpdateOfSharedName verifies that
// ACLAdapter.RelinkConfigs racing an UpdateConfig call on one of its linked
// names completes without deadlock and leaves both configs present with a
// handle from one of the two racing operations, never a freed-but-still-
// referenced handle.
func TestRelinkConfigs_ConcurrentWithUpdateOfSharedName(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)
	adapter := NewACLAdapter(svc)

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	for _, name := range []string{"a", "b"} {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
			Name:  name,
			Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
		})
		require.NoError(t, err)
	}

	require.NoError(t, adapter.LinkConfigs([]string{"a", "b"}, fwstateConfig, publish))

	svc.mu.RLock()
	handleB0 := svc.configs["b"].published.acl
	svc.mu.RUnlock()

	var wg errgroup.Group
	wg.Go(func() error {
		return adapter.RelinkConfigs(fwstateConfig, publish)
	})
	wg.Go(func() error {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
			Name:  "a",
			Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
		})
		return err
	})

	raceDone := make(chan error, 1)
	go func() { raceDone <- wg.Wait() }()

	select {
	case err := <-raceDone:
		require.NoError(t, err)
	case <-time.After(concurrencyWaitTimeout):
		t.Fatal("RelinkConfigs racing an UpdateConfig on a shared name deadlocked")
	}

	svc.mu.RLock()
	entryA := svc.configs["a"]
	entryB := svc.configs["b"]
	svc.mu.RUnlock()

	require.NotNil(t, entryA.published, "config \"a\" must survive the race")
	require.NotNil(t, entryB.published, "config \"b\" must survive the race")
	assert.NotNil(t, entryA.published.acl)
	assert.True(t, entryB.published.acl != handleB0, "RelinkConfigs must have installed a fresh handle for \"b\"")

	for _, h := range backend.CreatedHandles() {
		if h == entryA.published.acl || h == entryB.published.acl {
			assert.Equal(t, 0, h.FreeCount(), "a still-referenced handle must not be freed")
			continue
		}
		assert.Equal(t, 1, h.FreeCount(), "every replaced handle must be freed exactly once")
	}
}

// TestACLAdapterLinkConfigs_UnknownName verifies that LinkConfigs with an
// unknown name in the list fails without interning an entry for it, and
// without disturbing an already-live name also present in the list.
func TestACLAdapterLinkConfigs_UnknownName(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)
	adapter := NewACLAdapter(svc)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "a",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	err = adapter.LinkConfigs([]string{"a", "missing"}, fwstateConfig, publish)
	require.Error(t, err)

	svc.mu.RLock()
	_, ok := svc.configs["missing"]
	svc.mu.RUnlock()
	assert.False(t, ok, "an unknown name in a LinkConfigs request must not create an entry")
}

// TestACLAdapterLinkConfigs_TombstonedName verifies that LinkConfigs with a
// tombstoned name in the list — one whose entry exists but whose published
// config was deleted — reports a not-found error. checkLinkable's
// existence-only pre-check lets the tombstoned name through since its entry
// is still present, and createLinkedHandles' own check of published, under
// the name's lock, is what actually rejects it.
func TestACLAdapterLinkConfigs_TombstonedName(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)
	adapter := NewACLAdapter(svc)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "a",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "a"})
	require.NoError(t, err)

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	err = adapter.LinkConfigs([]string{"a"}, fwstateConfig, publish)
	require.ErrorContains(t, err, "not found")
}

// TestACLAdapterLinkConfigs_DuplicateName verifies that a name repeated in
// the LinkConfigs list creates and publishes exactly one handle for it,
// instead of leaking the handle a naive per-occurrence loop would create
// and then drop.
func TestACLAdapterLinkConfigs_DuplicateName(t *testing.T) {
	backend := newFakeBackend(0)
	svc := newTestService(backend)
	adapter := NewACLAdapter(svc)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "a",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	createdBefore := len(backend.CreatedHandles())

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}

	var publishedCount int
	publish := func(linkedFFI []ffi.ModuleConfig) error {
		publishedCount = len(linkedFFI)
		return nil
	}

	err = adapter.LinkConfigs([]string{"a", "a"}, fwstateConfig, publish)
	require.NoError(t, err)

	assert.Equal(t, createdBefore+1, len(backend.CreatedHandles()), "a duplicated name must create exactly one handle")
	assert.Equal(t, 1, publishedCount, "a duplicated name must publish exactly one module config")

	svc.mu.RLock()
	liveHandle := svc.configs["a"].published.acl
	svc.mu.RUnlock()

	for _, h := range backend.CreatedHandles() {
		if h == liveHandle {
			assert.Equal(t, 0, h.FreeCount(), "the live handle must not have been freed")
			continue
		}
		assert.Equal(t, 1, h.FreeCount(), "every superseded handle must be freed exactly once")
	}
}
