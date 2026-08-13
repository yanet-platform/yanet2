package acl_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
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

// fakeHandle is an in-memory implementation of acl.ModuleHandle for tests.
type fakeHandle struct {
	mu          sync.Mutex
	name        string
	rules       []cacl.AclRule
	emitConfig  *cfwstate.SyncEmitConfig
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

// Name returns the config name assigned when the handle was allocated.
func (m *fakeHandle) Name() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.name
}

// Rules returns a copy of the rules passed to UpdateRules.
func (m *fakeHandle) Rules() []cacl.AclRule {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]cacl.AclRule(nil), m.rules...)
}

func (m *fakeHandle) AsFFIModule() ffi.ModuleConfig {
	return ffi.ModuleConfig{}
}

func (m *fakeHandle) UpdateRules(rules []cacl.AclRule, emitConfig *cfwstate.SyncEmitConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rules = rules
	m.emitConfig = emitConfig
	return nil
}

// EmitConfig returns the emit config passed to the last UpdateRules call,
// so a test can assert a relink carried the stored sync config over.
func (m *fakeHandle) EmitConfig() *cfwstate.SyncEmitConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.emitConfig
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

// fakeBackend is an in-memory implementation of acl.Backend for tests.
type fakeBackend struct {
	mu           sync.Mutex
	modules      map[string]*fakeHandle
	created      []*fakeHandle
	publishCalls int
	newModuleErr error
	deleteErr    error
	deleteCalls  int
	dpConfig     *ffi.DPConfig
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

// blockingDPConfigBackend pauses one DPConfig call after metrics capture its
// metadata snapshot, allowing deletion to race with metric collection.
type blockingDPConfigBackend struct {
	*fakeBackend
	blockMu   sync.Mutex
	nextBlock *updateBlock
}

func newBlockingUpdateBackend() *blockingUpdateBackend {
	return &blockingUpdateBackend{fakeBackend: newFakeBackend()}
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

func (m *blockingUpdateBackend) UpdateModule(handle acl.ModuleHandle) error {
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

func newBlockingDPConfigBackend(dpConfig *ffi.DPConfig) *blockingDPConfigBackend {
	backend := newFakeBackend()
	backend.dpConfig = dpConfig
	return &blockingDPConfigBackend{fakeBackend: backend}
}

func (m *blockingDPConfigBackend) blockNextDPConfig() (<-chan struct{}, func()) {
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

func (m *blockingDPConfigBackend) DPConfig() *ffi.DPConfig {
	m.blockMu.Lock()
	block := m.nextBlock
	m.nextBlock = nil
	m.blockMu.Unlock()

	if block != nil {
		close(block.entered)
		<-block.release
	}

	return m.fakeBackend.DPConfig()
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		modules: map[string]*fakeHandle{},
	}
}

func newMetricsSnapshotHarness(testingTB testing.TB) (*dataplaneut.Harness, *ffi.Agent) {
	testingTB.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(128 * datasize.MB),
		DPMemory:      uint64(64 * datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(testingTB, err)
	testingTB.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"acl-metrics-snapshot",
		0,
		64*datasize.MB,
	)
	require.NoError(testingTB, err)
	testingTB.Cleanup(func() { _ = agent.CleanUp() })

	moduleNames := []string{"acl0", "a", "b", "c", "d"}
	moduleConfigs := make([]ffi.ModuleConfig, 0, len(moduleNames))
	for _, name := range moduleNames {
		moduleConfig, moduleErr := cacl.NewModuleConfig(agent, name)
		require.NoError(testingTB, moduleErr)
		testingTB.Cleanup(moduleConfig.Free)
		require.NoError(testingTB, moduleConfig.UpdateRules(nil, nil))
		moduleConfigs = append(moduleConfigs, moduleConfig.AsFFIModule())
	}
	require.NoError(testingTB, agent.UpdateModules(moduleConfigs))

	require.NoError(testingTB, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "function0",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "chain0",
				Modules: func() []ffi.ChainModuleConfig {
					modules := make([]ffi.ChainModuleConfig, 0, len(moduleNames))
					for _, name := range moduleNames {
						modules = append(modules, ffi.ChainModuleConfig{
							Type: "acl",
							Name: name,
						})
					}
					return modules
				}(),
			},
		}},
	}))
	require.NoError(testingTB, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "pipeline0",
		Functions: []string{"function0"},
	}))
	require.NoError(testingTB, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:  "port0",
		Input: []ffi.DevicePipelineConfig{{Name: "pipeline0", Weight: 1}},
	}}))

	return harness, agent
}

func (m *fakeBackend) NewModule(name string) (acl.ModuleHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.newModuleErr != nil {
		return nil, m.newModuleErr
	}

	h := &fakeHandle{name: name}
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

func (m *fakeBackend) UpdateModule(handle acl.ModuleHandle) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if unwrapped := unwrapFakeHandle(handle); unwrapped != nil {
		m.modules[unwrapped.Name()] = unwrapped
	}
	m.publishCalls++
	return nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	m.deleteCalls++
	delete(m.modules, name)
	return nil
}

func (m *fakeBackend) DPConfig() *ffi.DPConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.dpConfig
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

// ModuleCount returns the number of currently registered module handles.
func (m *fakeBackend) ModuleCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.modules)
}

func unwrapFakeHandle(handle acl.ModuleHandle) *fakeHandle {
	switch typedHandle := handle.(type) {
	case *fakeHandle:
		return typedHandle
	case *compileBlockingHandle:
		return unwrapFakeHandle(typedHandle.ModuleHandle)
	default:
		return nil
	}
}

func assertAllHandlesFreed(t *testing.T, backend *fakeBackend) {
	t.Helper()

	for idx, handle := range backend.CreatedHandles() {
		assert.Equal(t, 1, handle.FreeCount(), "allocated handle %d must be freed exactly once", idx)
	}
}

// DeleteCalls returns the number of successful DeleteModule calls.
func (m *fakeBackend) DeleteCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deleteCalls
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

func newCompileBlockingBackend() *compileBlockingBackend {
	return &compileBlockingBackend{
		fakeBackend: newFakeBackend(),
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

func (m *compileBlockingBackend) NewModule(name string) (acl.ModuleHandle, error) {
	handle, err := m.fakeBackend.NewModule(name)
	if err != nil {
		return nil, err
	}

	return &compileBlockingHandle{ModuleHandle: handle, backend: m, name: name}, nil
}

// compileBlockingHandle records and optionally blocks its UpdateRules call
// before delegating to the wrapped fakeHandle.
type compileBlockingHandle struct {
	acl.ModuleHandle
	backend *compileBlockingBackend
	name    string
}

func (m *compileBlockingHandle) UpdateRules(rules []cacl.AclRule, emitConfig *cfwstate.SyncEmitConfig) error {
	block := m.backend.recordEntry(m.name)
	if block != nil {
		close(block.entered)
		<-block.release
	}
	return m.ModuleHandle.UpdateRules(rules, emitConfig)
}

func newTestService(b acl.Backend) *acl.ACLService {
	return acl.NewACLService(b)
}

func testLabeler(_ string, req any) metrics.Labels {
	switch request := req.(type) {
	case *aclpb.UpdateConfigRequest:
		return metrics.Labels{"config": request.GetName()}
	case *aclpb.DeleteConfigRequest:
		return metrics.Labels{"config": request.GetName()}
	case *aclpb.ShowConfigRequest:
		return metrics.Labels{"config": request.GetName()}
	default:
		return nil
	}
}

func findMetricWithLabels(all []*commonpb.Metric, name string, want map[string]string) *commonpb.Metric {
	for _, metric := range all {
		if metric.GetName() != name {
			continue
		}

		labels := map[string]string{}
		for _, label := range metric.GetLabels() {
			labels[label.GetName()] = label.GetValue()
		}

		matches := true
		for key, value := range want {
			if labels[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return metric
		}
	}

	return nil
}

// TestUpdateConfigCounter verifies that a rule counter reaches the backend.
func TestUpdateConfigCounter(t *testing.T) {
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
			backend := newFakeBackend()
			svc := newTestService(backend)
			_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
				Name:  "acl0",
				Rules: tc.rules,
			})
			require.NoError(t, err)

			created := backend.CreatedHandles()
			require.Len(t, created, 1)
			handle := created[0]
			require.Len(t, handle.Rules(), len(tc.wantCnts))
			for idx, want := range tc.wantCnts {
				assert.Equal(t, want, handle.Rules()[idx].Counter)
			}
		})
	}
}

// TestUpdateConfig_Idempotency verifies that calling UpdateConfig twice with
// identical rules does not publish a second time.
func TestUpdateConfig_Idempotency(t *testing.T) {
	b := newFakeBackend()
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

// validTestSyncConfig returns a sync config that passes validation.
func validTestSyncConfig() *aclpb.SyncConfig {
	return &aclpb.SyncConfig{
		DstEther:         &commonpb.MACAddress{Addr: 0x333300000001},
		DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
		PortMulticast:    9999,
	}
}

// TestUpdateConfig_IdempotencyCountsSyncConfig verifies that the
// idempotence short-circuit compares the sync config, not only the rules.
// The ruleset only reads state, so a config with no sync section stays
// legal and dropping the section is observable as a publish.
func TestUpdateConfig_IdempotencyCountsSyncConfig(t *testing.T) {
	b := newFakeBackend()
	svc := newTestService(b)

	rules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_CHECK_STATE}}}}
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:       "acl0",
		Rules:      rules,
		SyncConfig: validTestSyncConfig(),
	})
	require.NoError(t, err)
	publishBefore := b.PublishCalls()

	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:       "acl0",
		Rules:      rules,
		SyncConfig: validTestSyncConfig(),
	})
	require.NoError(t, err)
	assert.Equal(t, publishBefore, b.PublishCalls(), "identical sync config must not publish")

	altered := validTestSyncConfig()
	altered.PortMulticast = 1000
	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:       "acl0",
		Rules:      rules,
		SyncConfig: altered,
	})
	require.NoError(t, err)
	assert.Equal(t, publishBefore+1, b.PublishCalls(), "a changed sync config must publish")

	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: rules,
	})
	require.NoError(t, err)
	assert.Equal(t, publishBefore+2, b.PublishCalls(), "dropping the sync config must publish")
}

// TestUpdateConfig_CheckStateOnlyRulesNeedNoSyncConfig verifies that a
// ruleset which only reads state carries no sync config requirement.
func TestUpdateConfig_CheckStateOnlyRulesNeedNoSyncConfig(t *testing.T) {
	b := newFakeBackend()
	svc := newTestService(b)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name: "acl0",
		Rules: []*aclpb.Rule{
			{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_CHECK_STATE}}},
		},
	})
	require.NoError(t, err)
}

// TestUpdateConfig_RequiresSyncConfigForCreateState verifies that a ruleset
// using CREATE_STATE is rejected without a sync config and accepted with one.
func TestUpdateConfig_RequiresSyncConfigForCreateState(t *testing.T) {
	b := newFakeBackend()
	svc := newTestService(b)

	rules := []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_CREATE_STATE}}}}

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{Name: "acl0", Rules: rules})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "sync_config is required")

	_, err = svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:       "acl0",
		Rules:      rules,
		SyncConfig: validTestSyncConfig(),
	})
	require.NoError(t, err)
}

// TestUpdateConfig_RejectsInvalidSyncConfig verifies that a supplied sync
// config is validated even when no rule requires one.
func TestUpdateConfig_RejectsInvalidSyncConfig(t *testing.T) {
	b := newFakeBackend()
	svc := newTestService(b)

	cfg := validTestSyncConfig()
	cfg.PortUnicast = 65536

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:       "acl0",
		Rules:      []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
		SyncConfig: cfg,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "port_unicast")
}

// TestUpdateConfig_ValidatesSyncConfig covers the sync config validation
// through the update RPC: every field the craft path requires, and the
// 16-bit port bounds.
func TestUpdateConfig_ValidatesSyncConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*aclpb.SyncConfig)
		wantErr string
	}{
		{
			name:   "valid config passes",
			mutate: func(*aclpb.SyncConfig) {},
		},
		{
			name: "valid config with unicast destination passes",
			mutate: func(c *aclpb.SyncConfig) {
				c.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}}
				c.PortUnicast = 1000
			},
		},
		{
			name:    "missing dst_ether",
			mutate:  func(c *aclpb.SyncConfig) { c.DstEther = nil },
			wantErr: "dst_ether",
		},
		{
			name:    "all-zero dst_ether",
			mutate:  func(c *aclpb.SyncConfig) { c.DstEther = &commonpb.MACAddress{} },
			wantErr: "dst_ether",
		},
		{
			name:    "missing multicast address",
			mutate:  func(c *aclpb.SyncConfig) { c.DstAddrMulticast = nil },
			wantErr: "dst_addr_multicast",
		},
		{
			name:    "short multicast address",
			mutate:  func(c *aclpb.SyncConfig) { c.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4}} },
			wantErr: "dst_addr_multicast",
		},
		{
			name:    "all-zero multicast address",
			mutate:  func(c *aclpb.SyncConfig) { c.DstAddrMulticast = &commonpb.IPAddress{} },
			wantErr: "dst_addr_multicast",
		},
		{
			name:    "zero multicast port",
			mutate:  func(c *aclpb.SyncConfig) { c.PortMulticast = 0 },
			wantErr: "port_multicast",
		},
		{
			name:    "multicast port over 16 bits",
			mutate:  func(c *aclpb.SyncConfig) { c.PortMulticast = 65536 },
			wantErr: "port_multicast",
		},
		{
			name:    "unicast port over 16 bits",
			mutate:  func(c *aclpb.SyncConfig) { c.PortUnicast = 65536 },
			wantErr: "port_unicast",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newFakeBackend()
			svc := newTestService(b)

			cfg := validTestSyncConfig()
			tc.mutate(cfg)

			resp, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
				Name: "acl0",
				Rules: []*aclpb.Rule{
					{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_CREATE_STATE}}},
				},
				SyncConfig: cfg,
			})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Nil(t, resp)
		})
	}
}

// TestUpdateConfig_ErrorPropagation verifies that a backend failure from
// NewModule returns codes.Internal and leaves the service config unchanged.
func TestUpdateConfig_ErrorPropagation(t *testing.T) {
	b := newFakeBackend()
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
	require.Len(t, resp.Rules, len(initialRules))
	for idx := range initialRules {
		assert.True(t, proto.Equal(initialRules[idx], resp.Rules[idx]), "config rules must not have changed after failed update")
	}
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
			b := newFakeBackend()
			svc := newTestService(b)

			_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
				Name:  "acl0",
				Rules: tc.rules,
			})
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Equal(t, 0, b.PublishCalls(), "backend must not be asked to publish")
			assert.Len(t, b.CreatedHandles(), 0, "backend must not allocate a module")
			assert.Equal(t, 0, b.ModuleCount(), "no module must be created")
		})
	}
}

// TestConvertRules_RejectsUnknownActionKind ensures unrecognized action kinds
// become a client error rather than silently mapping to ALLOW.
func TestConvertRules_RejectsUnknownActionKind(t *testing.T) {
	svc := newTestService(newFakeBackend())
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind(999)}}}},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateConfig_RejectsNonContiguousMask verifies that a rule with a
// non-contiguous network mask is rejected with codes.InvalidArgument before
// reaching the backend.
func TestUpdateConfig_RejectsNonContiguousMask(t *testing.T) {
	svc := newTestService(newFakeBackend())

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
// codes.NotFound for a config name that was never applied and does not call
// the backend.
func TestDeleteConfig_UnknownConfig(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)

	_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
	assert.Equal(t, 0, backend.DeleteCalls(), "an unknown target must not reach the backend")
}

// TestDeleteConfig_LiveNameTombstones verifies that deleting a name with a
// live published config succeeds, removes the backend module, and makes a
// second delete return NotFound.
func TestDeleteConfig_LiveNameTombstones(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)

	name := "acl0"
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  name,
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.NoError(t, err)
	assert.Equal(t, 0, backend.ModuleCount(), "delete must remove the backend module")
	assert.Equal(t, 1, backend.DeleteCalls())

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.Equal(t, codes.NotFound, status.Code(err), "deleting an already-deleted name must report NotFound")
}

// TestDeleteConfig_WaitsBehindInFlightCreate verifies that a DeleteConfig
// racing an UpdateConfig that is creating a brand-new name waits for the
// create to finish instead of returning NotFound while compilation is in
// flight. This test forces that window with compileBlockingBackend and
// asserts that the delete resolves after the create publishes, then removes
// the config it just published.
func TestDeleteConfig_WaitsBehindInFlightCreate(t *testing.T) {
	backend := newCompileBlockingBackend()
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

	assert.Equal(t, 0, backend.ModuleCount(), "the racing delete must remove the published module")
	assert.Equal(t, 1, backend.DeleteCalls(), "the racing delete must reach the backend once")

	assert.Equal(t, 1, backend.PublishCalls(), "the create must have published its module before the delete could act on it")

	created := backend.CreatedHandles()
	require.Len(t, created, 1, "the racing create must have produced exactly one handle")
	assert.Equal(t, 1, created[0].FreeCount(), "the delete must free the just-published handle exactly once")
}

// TestUpdateConfig_ResurrectsThroughTombstone verifies that Update, Delete,
// then Update of the same name restores the config: Show returns the
// resurrected rules, List reports the name exactly once, and cleanup frees
// every handle the fakeBackend produced exactly once.
func TestUpdateConfig_ResurrectsThroughTombstone(t *testing.T) {
	backend := newFakeBackend()
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
	require.Len(t, resp.Rules, len(newRules))
	for idx := range newRules {
		assert.True(t, proto.Equal(newRules[idx], resp.Rules[idx]), "Show must return the resurrected config's rules")
	}

	listResp, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{name}, listResp.Configs, "List must report the resurrected name exactly once")

	require.Len(t, backend.CreatedHandles(), 2)
	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
	require.NoError(t, err)
	listResp, err = svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Empty(t, listResp.Configs)
	assertAllHandlesFreed(t, backend)
}

// TestUpdateConfig_ConcurrentRace exercises UpdateConfig and ShowConfig under
// concurrent access to surface data races under go test -race.
func TestUpdateConfig_ConcurrentRace(t *testing.T) {
	svc := newTestService(newFakeBackend())

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

// TestMetrics_DoesNotWaitForUpdateConfig verifies that metrics collection is
// not stalled by a blocked backend update.
func TestMetrics_DoesNotWaitForUpdateConfig(t *testing.T) {
	backend := newBlockingUpdateBackend()
	svc := acl.NewACLService(backend, acl.WithMetrics(grpcmetrics.NewFactory(
		grpcmetrics.WithLabeler(testLabeler),
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
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingDPConfigBackend(agent.DPConfig())
	svc := newTestService(backend)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_DENY}}}},
	})
	require.NoError(t, err)

	dpConfigEntered, releaseDPConfig := backend.blockNextDPConfig()
	t.Cleanup(releaseDPConfig)

	type metricsResult struct {
		metrics []*commonpb.Metric
		err     error
	}
	metricsDone := make(chan metricsResult, 1)
	go func() {
		collected, metricsErr := svc.Metrics()
		metricsDone <- metricsResult{metrics: collected, err: metricsErr}
	}()

	select {
	case <-dpConfigEntered:
	case <-time.After(metricsConcurrencyTestTimeout):
		releaseDPConfig()
		t.Fatal("Metrics did not reach the blocked dataplane config")
	}

	if _, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "acl0"}); err != nil {
		releaseDPConfig()
		t.Fatalf("DeleteConfig failed while Metrics was blocked: %v", err)
	}
	releaseDPConfig()

	var result metricsResult
	select {
	case result = <-metricsDone:
	case <-time.After(metricsConcurrencyTestTimeout):
		releaseDPConfig()
		t.Fatal("Metrics did not finish after the dataplane config was released")
	}
	require.NoError(t, result.err)
	compilationTime := findMetricWithLabels(
		result.metrics,
		"acl_compilation_time_ns",
		map[string]string{"config": "acl0"},
	)
	require.NotNil(t, compilationTime)
	assert.Equal(t, float64(42), compilationTime.GetGauge())
	ruleCount := findMetricWithLabels(
		result.metrics,
		"acl_filter_rule_count_ip4",
		map[string]string{"config": "acl0"},
	)
	require.NotNil(t, ruleCount)
	assert.Equal(t, float64(7), ruleCount.GetGauge())

	afterDelete, err := svc.Metrics()
	require.NoError(t, err)
	assert.Nil(t, findMetricWithLabels(
		afterDelete,
		"acl_compilation_time_ns",
		map[string]string{"config": "acl0"},
	))
	assert.NotNil(t, findMetricWithLabels(
		result.metrics,
		"acl_compilation_time_ns",
		map[string]string{"config": "acl0"},
	), "a returned metrics snapshot must remain immutable")
}

// TestMetricsSnapshotOrderingSurvivesConcurrentBarrage verifies that once a
// concurrent barrage of UpdateConfig and DeleteConfig calls across several
// config names has fully joined, the public metrics snapshot's config-name
// set equals the ListConfigs result exactly. Concurrent updates and deletes
// must not publish a stale snapshot after a later mutation. The test checks
// the settled state without relying on a particular scheduling interleaving.
func TestMetricsSnapshotOrderingSurvivesConcurrentBarrage(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newFakeBackend()
	backend.dpConfig = agent.DPConfig()
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

	listResponse, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	wantNames := make(map[string]struct{}, len(listResponse.Configs))
	for _, name := range listResponse.Configs {
		wantNames[name] = struct{}{}
	}

	collected, err := svc.Metrics()
	require.NoError(t, err)
	gotNames := map[string]struct{}{}
	for _, metric := range collected {
		if metric.GetName() != "acl_compilation_time_ns" {
			continue
		}
		for _, label := range metric.GetLabels() {
			if label.GetName() == "config" {
				gotNames[label.GetValue()] = struct{}{}
			}
		}
	}

	assert.Equal(t, wantNames, gotNames,
		"public metrics config set must match ListConfigs after the barrage settles")
}

// TestUpdateConfig_CompilesInParallel verifies that UpdateConfig calls for
// different names compile concurrently: both are proven to be inside
// UpdateRules at the same time via channel synchronization, not timing.
func TestUpdateConfig_CompilesInParallel(t *testing.T) {
	backend := newCompileBlockingBackend()
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
	backend := newCompileBlockingBackend()
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
		require.Len(t, result.resp.Rules, len(initialRules))
		for idx := range initialRules {
			assert.True(t, proto.Equal(initialRules[idx], result.resp.Rules[idx]),
				"ShowConfig must return the old published state while a compile is in flight")
		}
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
	backend := newCompileBlockingBackend()
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
	require.Len(t, resp.Rules, len(rulesB))
	for idx := range rulesB {
		assert.True(t, proto.Equal(rulesB[idx], resp.Rules[idx]))
	}
}

// TestUpdateAndDeleteConfig_NoDoubleFree verifies that concurrent
// UpdateConfig and DeleteConfig calls racing on the same name never free a
// handle twice or free a handle that remains active.
func TestUpdateAndDeleteConfig_NoDoubleFree(t *testing.T) {
	backend := newFakeBackend()
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

	listResp, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	if len(listResp.Configs) > 0 {
		require.Equal(t, []string{name}, listResp.Configs)
		_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
		require.NoError(t, err)
	}
	assertAllHandlesFreed(t, backend)
}

// TestRelinkConfigs_ConcurrentWithUpdateOfSharedName verifies that
// ACLAdapter.RelinkConfigs racing an UpdateConfig call on one linked name
// completes without deadlock and refreshes the non-racing b handle safely.
func TestRelinkConfigs_ConcurrentWithUpdateOfSharedName(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)
	adapter := acl.NewACLAdapter(svc)

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
	createdAfterLink := backend.CreatedHandles()
	var linkedB *fakeHandle
	for _, handle := range createdAfterLink {
		if handle.Name() == "b" && handle.FreeCount() == 0 {
			require.Nil(t, linkedB, "only one b handle may remain owned after linking")
			linkedB = handle
		}
	}
	require.NotNil(t, linkedB, "LinkConfigs must leave b owned by the adapter")

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

	var refreshedB *fakeHandle
	for _, handle := range backend.CreatedHandles() {
		if handle.Name() == "b" && handle.FreeCount() == 0 {
			require.Nil(t, refreshedB, "only one b handle may remain owned after relinking")
			refreshedB = handle
		}
	}
	require.NotNil(t, refreshedB, "RelinkConfigs must leave a refreshed b handle owned")
	assert.NotSame(t, linkedB, refreshedB)
	assert.Equal(t, 1, linkedB.FreeCount(), "RelinkConfigs must free the prior b handle once")

	listResp, err := svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, listResp.Configs)

	for _, name := range []string{"a", "b"} {
		_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: name})
		require.NoError(t, err)
	}
	assertAllHandlesFreed(t, backend)
}

// TestRelinkConfigs_PreservesSyncConfig verifies that relinking a name to a
// fwstate config keeps the stored sync config both in the service state and
// on the refreshed handle, so CREATE_STATE keeps emitting after a relink.
func TestRelinkConfigs_PreservesSyncConfig(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)
	adapter := acl.NewACLAdapter(svc)

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	syncCfg := validTestSyncConfig()
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name: "a",
		Rules: []*aclpb.Rule{
			{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_CREATE_STATE}}},
		},
		SyncConfig: syncCfg,
	})
	require.NoError(t, err)

	require.NoError(t, adapter.RelinkConfigs(fwstateConfig, publish))

	resp, err := svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: "a"})
	require.NoError(t, err)
	require.NotNil(t, resp.SyncConfig, "a relink must keep the stored sync config")
	assert.True(t, proto.Equal(syncCfg, resp.SyncConfig))

	var refreshed *fakeHandle
	for _, handle := range backend.CreatedHandles() {
		if handle.Name() == "a" && handle.FreeCount() == 0 {
			require.Nil(t, refreshed, "only one a handle may remain owned after relinking")
			refreshed = handle
		}
	}
	require.NotNil(t, refreshed, "RelinkConfigs must leave a refreshed a handle owned")
	require.NotNil(t, refreshed.EmitConfig(), "the refreshed handle must receive the stored emit config")
}

// TestACLAdapterLinkConfigs_UnknownName verifies that LinkConfigs with an
// unknown name in the list fails without allocating a backend module for it,
// and without disturbing an already-live name also present in the list.
func TestACLAdapterLinkConfigs_UnknownName(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)
	adapter := acl.NewACLAdapter(svc)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "a",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)
	createdBefore := backend.CreatedHandles()
	require.Len(t, createdBefore, 1)

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	err = adapter.LinkConfigs([]string{"a", "missing"}, fwstateConfig, publish)
	require.Error(t, err)
	createdAfter := backend.CreatedHandles()
	require.Len(t, createdAfter, len(createdBefore), "an unknown name must not allocate a handle")
	assert.Equal(t, 0, createdAfter[0].FreeCount(), "the live \"a\" handle must remain owned by the service")

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "a"})
	require.NoError(t, err)
	assertAllHandlesFreed(t, backend)
}

// TestACLAdapterLinkConfigs_TombstonedName verifies retained-name linking
// failure frees every temporary handle and preserves the live config.
func TestACLAdapterLinkConfigs_TombstonedName(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)
	adapter := acl.NewACLAdapter(svc)

	for _, name := range []string{"a", "deleted"} {
		_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
			Name:  name,
			Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
		})
		require.NoError(t, err)
	}
	createdBefore := backend.CreatedHandles()
	require.Len(t, createdBefore, 2)

	_, err := svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "deleted"})
	require.NoError(t, err)
	assert.Equal(t, 0, createdBefore[0].FreeCount(), "the live \"a\" handle must remain owned")
	assert.Equal(t, 1, createdBefore[1].FreeCount(), "the deleted handle must be freed once")

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}
	publish := func(_ []ffi.ModuleConfig) error { return nil }

	err = adapter.LinkConfigs([]string{"a", "deleted"}, fwstateConfig, publish)
	require.ErrorContains(t, err, "not found")

	createdAfter := backend.CreatedHandles()
	require.Len(t, createdAfter, len(createdBefore)+1)
	temporary := createdAfter[len(createdBefore)]
	assert.Equal(t, "a", temporary.Name())
	assert.Equal(t, 1, temporary.FreeCount(), "the temporary live handle must be freed once")
	assert.Equal(t, 0, createdAfter[0].FreeCount(), "the original live handle must remain owned")

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "a"})
	require.NoError(t, err)
	assertAllHandlesFreed(t, backend)
}

// TestACLAdapterLinkConfigs_DuplicateName verifies that a name repeated in
// the LinkConfigs list creates and publishes exactly one handle for it,
// instead of leaking the handle a naive per-occurrence loop would create
// and then drop.
func TestACLAdapterLinkConfigs_DuplicateName(t *testing.T) {
	backend := newFakeBackend()
	svc := newTestService(backend)
	adapter := acl.NewACLAdapter(svc)

	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "a",
		Rules: []*aclpb.Rule{{Actions: []*aclpb.Action{{Kind: aclpb.ActionKind_ACTION_KIND_PASS}}}},
	})
	require.NoError(t, err)

	createdBefore := backend.CreatedHandles()
	require.Len(t, createdBefore, 1)

	fwstateConfig := &fwstate.FwStateConfig{ModuleConfig: &cfwstate.ModuleConfig{}}

	var publishedCount int
	publish := func(linkedFFI []ffi.ModuleConfig) error {
		publishedCount = len(linkedFFI)
		return nil
	}

	err = adapter.LinkConfigs([]string{"a", "a"}, fwstateConfig, publish)
	require.NoError(t, err)

	createdAfter := backend.CreatedHandles()
	require.Len(t, createdAfter, len(createdBefore)+1, "a duplicated name must create exactly one handle")
	assert.Equal(t, 1, publishedCount, "a duplicated name must publish exactly one module config")

	assert.Equal(t, 1, createdAfter[0].FreeCount(), "the superseded handle must be freed once")
	assert.Equal(t, 0, createdAfter[1].FreeCount(), "the linked handle must remain owned")

	_, err = svc.DeleteConfig(t.Context(), &aclpb.DeleteConfigRequest{Name: "a"})
	require.NoError(t, err)
	assertAllHandlesFreed(t, backend)
}
