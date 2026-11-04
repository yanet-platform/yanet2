package blackhole

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

type mockBackend struct{}

func (m *mockBackend) Update(name string, payload struct{}) (crud.Handle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) Delete(name string) error {
	return nil
}

func newTestService(t *testing.T) *BlackholeService {
	t.Helper()
	return NewBlackholeService(&mockBackend{})
}

// flakyBackend succeeds on the first update call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) Update(name string, payload struct{}) (crud.Handle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) Delete(name string) error {
	return nil
}

// Test_BlackholeService_UpdateAndShow verifies that a created config is
// visible to a later read under the same name.
func Test_BlackholeService_UpdateAndShow(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.UpdateConfig(t.Context(), &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NotNil(t, resp)
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &blackholepb.ShowConfigRequest{Name: "blackhole0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, "blackhole0", show.Name)
}

// Test_BlackholeService_ListUpdateList verifies that listing reflects an
// empty store, then the entry added by an update, in that order.
func Test_BlackholeService_ListUpdateList(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Equal(t, []string{"blackhole0"}, list.Configs)
}

// Test_BlackholeService_DeleteMissing verifies that deleting an absent config
// returns NotFound without mutating the store.
func Test_BlackholeService_DeleteMissing(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.DeleteConfig(t.Context(), &blackholepb.DeleteConfigRequest{Name: "absent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_BlackholeService_UpdateFailureAtomic verifies that a failed update
// leaves the previously applied config intact and queryable.
func Test_BlackholeService_UpdateFailureAtomic(t *testing.T) {
	svc := NewBlackholeService(&flakyBackend{})
	ctx := t.Context()
	name := "blackhole0"

	_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: name})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, name, show.Name)
}
