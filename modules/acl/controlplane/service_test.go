package acl

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

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
	return NewACLService(b, zap.NewNop())
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
		Rules: []*aclpb.Rule{},
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
	_, err := svc.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  "acl0",
		Rules: []*aclpb.Rule{},
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
	assert.Empty(t, resp.Rules, "config rules must not have changed after failed update")
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
				Rules: []*aclpb.Rule{},
			})
			_, _ = svc.ShowConfig(t.Context(), &aclpb.ShowConfigRequest{Name: name})
			_, _ = svc.ListConfigs(t.Context(), &aclpb.ListConfigsRequest{})
			return nil
		})
	}

	require.NoError(t, wg.Wait())
}
