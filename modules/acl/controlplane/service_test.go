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
)

const metricsConcurrencyTestTimeout = 5 * time.Second

// fakeHandle is an in-memory implementation of ModuleHandle for tests.
type fakeHandle struct {
	mu          sync.Mutex
	name        string
	rules       []cacl.AclRule
	freed       bool
	transferred bool
}

func (m *fakeHandle) Free() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.freed = true
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
	return h, nil
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
// codes.NotFound for a config name that was never applied.
func TestDeleteConfig_UnknownConfig(t *testing.T) {
	svc := newTestService(newFakeBackend(0))

	_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
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
