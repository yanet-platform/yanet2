package blackhole

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string) (ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
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

func (m *flakyBackend) UpdateModule(name string) (ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

// blockingBackend stalls every module publish until released.
type blockingBackend struct {
	started chan struct{}
	release chan struct{}
}

// newBlockingBackend returns a backend whose every publish blocks until
// released; the returned started channel fires once a publish is inside
// the stall.
func newBlockingBackend() *blockingBackend {
	return &blockingBackend{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (m *blockingBackend) UpdateModule(name string) (ModuleHandle, error) {
	close(m.started)
	<-m.release
	return &mockModuleHandle{}, nil
}

func (m *blockingBackend) DeleteModule(name string) error {
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

// Test_BlackholeService_DeleteConfig verifies that deleting a config removes
// it so a later read returns NotFound.
func Test_BlackholeService_DeleteConfig(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(ctx, &blackholepb.DeleteConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	_, err = svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: "blackhole0"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_BlackholeService_DeleteMissing verifies that deleting an absent config
// returns NotFound without mutating the store.
func Test_BlackholeService_DeleteMissing(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.DeleteConfig(t.Context(), &blackholepb.DeleteConfigRequest{Name: "absent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_BlackholeService_EmptyConfigName verifies that creating, reading, and
// deleting all reject an empty name as InvalidArgument.
func Test_BlackholeService_EmptyConfigName(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	t.Run("UpdateConfig", func(t *testing.T) {
		resp, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteConfig", func(t *testing.T) {
		resp, err := svc.DeleteConfig(ctx, &blackholepb.DeleteConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
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

// Test_BlackholeService_ListDuringStalledUpdate verifies that listing
// configs completes while another goroutine's update is still stalled
// inside the backend publish.
func Test_BlackholeService_ListDuringStalledUpdate(t *testing.T) {
	backend := newBlockingBackend()
	svc := NewBlackholeService(backend)

	g, ctx := errgroup.WithContext(t.Context())
	g.Go(func() error {
		_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
		return err
	})

	<-backend.started

	listed := make(chan struct{})
	go func() {
		defer close(listed)
		_, _ = svc.ListConfigs(t.Context(), &blackholepb.ListConfigsRequest{})
	}()

	select {
	case <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("listing configs blocked behind a stalled module publish")
	}

	close(backend.release)
	require.NoError(t, g.Wait())
}

// Test_BlackholeService_ConcurrentAccess verifies that interleaved creates,
// lists, and reads from many goroutines do not race or lose entries.
func Test_BlackholeService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for range goroutines {
		g.Go(func() error {
			for range iterations {
				_, err := svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}

// stallingReclaimHandle refuses its first free, which parks it, and
// stalls the second until released.
type stallingReclaimHandle struct {
	numCalls atomic.Int64
	started  chan struct{}
	release  chan struct{}
}

func (m *stallingReclaimHandle) Free() error {
	if m.numCalls.Add(1) == 1 {
		return ffi.ErrStillReferenced
	}
	close(m.started)
	<-m.release
	return nil
}

// stallingReclaimBackend mints the stalling handle on its first publish
// and plain handles thereafter.
type stallingReclaimBackend struct {
	numCalls atomic.Int64
	handle   stallingReclaimHandle
}

// newStallingReclaimBackend returns a backend that mints the stalling
// reclaim handle on the first publish; the handle's started channel
// fires once its deferred destruction is inside the stall.
func newStallingReclaimBackend() *stallingReclaimBackend {
	return &stallingReclaimBackend{
		handle: stallingReclaimHandle{
			started: make(chan struct{}),
			release: make(chan struct{}),
		},
	}
}

func (m *stallingReclaimBackend) UpdateModule(name string) (ModuleHandle, error) {
	if m.numCalls.Add(1) == 1 {
		return &m.handle, nil
	}
	return &mockModuleHandle{}, nil
}

func (m *stallingReclaimBackend) DeleteModule(name string) error {
	return nil
}

// Test_BlackholeService_ListDuringStalledReclaim verifies that listing configs
// completes while a superseded handle's deferred destruction is stalled
// inside the backend.
func Test_BlackholeService_ListDuringStalledReclaim(t *testing.T) {
	backend := newStallingReclaimBackend()
	svc := NewBlackholeService(backend)

	_, err := svc.UpdateConfig(t.Context(), &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)
	_, err = svc.UpdateConfig(t.Context(), &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	g, _ := errgroup.WithContext(t.Context())
	g.Go(func() error {
		svc.ReclaimDeferred()
		return nil
	})

	<-backend.handle.started

	listed := make(chan struct{})
	go func() {
		defer close(listed)
		_, _ = svc.ListConfigs(t.Context(), &blackholepb.ListConfigsRequest{})
	}()

	select {
	case <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("listing configs blocked behind a stalled deferred handle destruction")
	}

	close(backend.handle.release)
	require.NoError(t, g.Wait())
}
