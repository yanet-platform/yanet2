package dscp

import (
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

var errBackendFailure = fmt.Errorf("backend failure")

// mustPrefixes4 returns family-typed IPv4 prefix messages for valid CIDR
// test inputs.
func mustPrefixes4(t *testing.T, prefixes ...string) []*commonpb.IPv4Prefix {
	t.Helper()

	networks, err := commonpb.NewIPv4PrefixesFromPrefixes(parsePrefixes(prefixes))
	require.NoError(t, err)
	return networks
}

// mustPrefixes6 is mustPrefixes4 for IPv6 prefix messages.
func mustPrefixes6(t *testing.T, prefixes ...string) []*commonpb.IPv6Prefix {
	t.Helper()

	networks, err := commonpb.NewIPv6PrefixesFromPrefixes(parsePrefixes(prefixes))
	require.NoError(t, err)
	return networks
}

// parsePrefixes parses valid CIDR test inputs.
func parsePrefixes(prefixes []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, netip.MustParsePrefix(prefix))
	}
	return out
}

// prefixStrings returns canonical CIDR text for valid prefix messages of
// either family.
func prefixStrings[T interface{ ToPrefix() (netip.Prefix, error) }](t *testing.T, networks []T) []string {
	t.Helper()

	prefixes := make([]string, 0, len(networks))
	for _, network := range networks {
		prefix, err := network.ToPrefix()
		require.NoError(t, err)
		prefixes = append(prefixes, prefix.String())
	}

	return prefixes
}

// mockModuleHandle counts Free calls so tests can assert a release happened.
type mockModuleHandle struct {
	freed atomic.Int64
}

func (m *mockModuleHandle) Free() error {
	m.freed.Add(1)
	return nil
}

// refusingOnceHandle refuses its first Free with ffi.ErrStillReferenced, then succeeds.
type refusingOnceHandle struct {
	numCalls atomic.Int64
	freed    atomic.Int64
}

func (m *refusingOnceHandle) Free() error {
	if m.numCalls.Add(1) == 1 {
		return ffi.ErrStillReferenced
	}
	m.freed.Add(1)
	return nil
}

// mockBackend records the last handle it minted and the name it last saw in DeleteModule.
type mockBackend struct {
	lastHandle  atomic.Pointer[mockModuleHandle]
	deletedName string
}

func (m *mockBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	handle := &mockModuleHandle{}
	m.lastHandle.Store(handle)
	return handle, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	m.deletedName = name
	return nil
}

func newTestService(t *testing.T) *DscpService {
	t.Helper()
	return NewDscpService(&mockBackend{})
}

// refusingDeleteBackend updates like mockBackend but refuses every delete,
// modeling a config still referenced by a live generation.
type refusingDeleteBackend struct {
	mockBackend
}

func (m *refusingDeleteBackend) DeleteModule(name string) error {
	m.deletedName = name
	return errBackendFailure
}

// parkingBackend refuses to release the first handle it mints, then releases
// normally, so that handle must be parked before it can be reclaimed.
type parkingBackend struct {
	mockBackend
	numCalls atomic.Int64
	first    refusingOnceHandle
}

func (m *parkingBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	if m.numCalls.Add(1) == 1 {
		return &m.first, nil
	}
	return &mockModuleHandle{}, nil
}

type flakyBackend struct {
	mu       sync.Mutex
	numCalls int
	backend  mockBackend
}

func (m *flakyBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.numCalls++
	if m.numCalls > 1 {
		return nil, errBackendFailure
	}

	return m.backend.UpdateModule(name, prefixes, flag, mark)
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

// Test_DscpService_ListShowAddRemoveSetMarking verifies that prefix mutations
// and marking updates remain visible through the structured response.
func Test_DscpService_ListShowAddRemoveSetMarking(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	ctx := t.Context()

	{
		response, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Configs)
	}

	{
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:      "dscp0",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
			Prefixes6: mustPrefixes6(t, "2001:db8::/32"),
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Equal(t, []string{"dscp0"}, response.Configs)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Config.DscpConfig)
		assert.Equal(t, []string{"10.0.0.0/24"}, prefixStrings(t, response.Config.Prefixes4))
		assert.Equal(t, []string{"2001:db8::/32"}, prefixStrings(t, response.Config.Prefixes6))
	}

	{
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 2,
				Mark: 8,
			},
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Equal(t, uint32(2), response.Config.DscpConfig.Flag)
		assert.Equal(t, uint32(8), response.Config.DscpConfig.Mark)
	}

	{
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:      "dscp0",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Config.Prefixes4)
		assert.Equal(t, []string{"2001:db8::/32"}, prefixStrings(t, response.Config.Prefixes6))
	}
}

// Test_DscpService_RequestValidation verifies that invalid names, networks,
// and marking values are rejected with InvalidArgument.
func Test_DscpService_RequestValidation(t *testing.T) {
	t.Parallel()
	service := newTestService(t)
	ctx := t.Context()

	t.Run("ShowConfigInvalidName", func(t *testing.T) {
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: ""})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("AddPrefixesInvalidName", func(t *testing.T) {
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:      "",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixesInvalidName", func(t *testing.T) {
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:      "",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteConfigInvalidName", func(t *testing.T) {
		response, err := service.DeleteConfig(ctx, &dscppb.DeleteConfigRequest{Name: ""})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingNoDSCPConfig", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("AddPrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:      "dscp0",
			Prefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:      "dscp0",
			Prefixes6: []*commonpb.IPv6Prefix{{PrefixLen: 64}},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingInvalidFlag", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 3,
				Mark: 8,
			},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingInvalidMark", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 1,
				Mark: 64,
			},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// Test_DscpService_NoUpdateOnFailure verifies that a failed backend update
// leaves the last successfully published configuration visible.
func Test_DscpService_NoUpdateOnFailure(t *testing.T) {
	t.Parallel()

	backend := &flakyBackend{}
	service := NewDscpService(backend)
	ctx := t.Context()
	name := "dscp0"

	_, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      name,
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	_, err = service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      name,
		Prefixes4: mustPrefixes4(t, "20.0.0.0/24"),
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: name})
	require.NotNil(t, response)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24"}, prefixStrings(t, response.Config.Prefixes4))
}

// Test_DscpService_DeleteConfig_MissingConfig verifies that deleting a
// config that was never created returns NotFound.
func Test_DscpService_DeleteConfig_MissingConfig(t *testing.T) {
	t.Parallel()
	service := newTestService(t)

	response, err := service.DeleteConfig(t.Context(), &dscppb.DeleteConfigRequest{Name: "dscp0"})
	require.Nil(t, response)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DscpService_DeleteConfig_RemovesConfig verifies that deleting an
// unreferenced config removes it from ListConfigs and from Show.
func Test_DscpService_DeleteConfig_RemovesConfig(t *testing.T) {
	t.Parallel()
	backend := &mockBackend{}
	service := NewDscpService(backend)
	ctx := t.Context()

	_, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      "dscp0",
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	handle := backend.lastHandle.Load()
	require.NotNil(t, handle)

	response, err := service.DeleteConfig(ctx, &dscppb.DeleteConfigRequest{Name: "dscp0"})
	require.NotNil(t, response)
	require.NoError(t, err)
	assert.Equal(t, "dscp0", backend.deletedName)
	assert.Equal(t, int64(1), handle.freed.Load())

	list, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	show, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
	require.Nil(t, show)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DscpService_DeleteConfig_Referenced verifies that a backend refusal
// surfaces as Internal and leaves the config in place.
func Test_DscpService_DeleteConfig_Referenced(t *testing.T) {
	t.Parallel()
	backend := &refusingDeleteBackend{}
	service := NewDscpService(backend)
	ctx := t.Context()

	_, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      "dscp0",
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	response, err := service.DeleteConfig(ctx, &dscppb.DeleteConfigRequest{Name: "dscp0"})
	require.Nil(t, response)
	require.Equal(t, codes.Internal, status.Code(err))
	assert.Equal(t, "dscp0", backend.deletedName)

	list, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"dscp0"}, list.Configs)

	show, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
	require.NotNil(t, show)
	require.NoError(t, err)
}

// Test_DscpService_DeleteConfig_ParksThenReclaims verifies that a handle
// refused on delete is parked and reclaimed by the next successful update.
func Test_DscpService_DeleteConfig_ParksThenReclaims(t *testing.T) {
	t.Parallel()
	backend := &parkingBackend{}
	service := NewDscpService(backend)
	ctx := t.Context()

	_, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      "dscp0",
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	response, err := service.DeleteConfig(ctx, &dscppb.DeleteConfigRequest{Name: "dscp0"})
	require.NotNil(t, response)
	require.NoError(t, err)
	require.Len(t, service.deferred, 1)
	assert.Equal(t, int64(0), backend.first.freed.Load())

	// A later successful update reclaims deferred handles first.
	_, err = service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      "dscp1",
		Prefixes4: mustPrefixes4(t, "10.0.1.0/24"),
	})
	require.NoError(t, err)
	assert.Empty(t, service.deferred)
	assert.Equal(t, int64(1), backend.first.freed.Load())
}

// Test_DscpService_ConcurrentAccess verifies that concurrent mutations and
// reads complete without a data race.
func Test_DscpService_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	service := newTestService(t)
	ctx := t.Context()

	const goroutines = 10
	const iterations = 100

	group, _ := errgroup.WithContext(ctx)
	for i := range goroutines {
		group.Go(func() error {
			name := fmt.Sprintf("cfg-%d", i%3)
			for j := range iterations {
				if j%3 == 0 {
					if _, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
						Name:      name,
						Prefixes4: mustPrefixes4(t, fmt.Sprintf("10.%d.%d.0/24", i, j)),
					}); err != nil {
						return err
					}
					continue
				}
				if j%3 == 1 {
					if _, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
						Name:      name,
						Prefixes4: mustPrefixes4(t, fmt.Sprintf("10.%d.%d.0/24", i, j)),
					}); err != nil {
						return err
					}
					continue
				}
				_, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
					Name: name,
					DscpConfig: &dscppb.DscpConfig{
						Flag: uint32(j % 2),
						Mark: uint32(j % 16),
					},
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	require.NoError(t, group.Wait())
}
