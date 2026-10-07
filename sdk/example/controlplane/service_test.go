package example_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	example "github.com/yanet-platform/yanet2/sdk/example/controlplane"
	examplepb "github.com/yanet-platform/yanet2/sdk/example/controlplane/examplepb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string) (example.ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

func newTestService(t *testing.T) *example.ExampleService {
	t.Helper()
	return example.NewExampleService(&mockBackend{})
}

// flakyBackend succeeds on the first update call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(name string) (example.ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

// refusingDeleteBackend updates like mockBackend but refuses every delete
// with err.
type refusingDeleteBackend struct {
	mockBackend
	err error
}

func (m *refusingDeleteBackend) DeleteModule(name string) error {
	return m.err
}

// Test_ExampleService_UpdateAndShow verifies that a created config is
// visible to a later read under the same name.
func Test_ExampleService_UpdateAndShow(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.UpdateConfig(t.Context(), &examplepb.UpdateConfigRequest{Name: "example0"})
	require.NotNil(t, resp)
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &examplepb.ShowConfigRequest{Name: "example0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, "example0", show.Name)
}

// Test_ExampleService_ListUpdateList verifies that listing reflects an
// empty store, then the entry added by an update, in that order.
func Test_ExampleService_ListUpdateList(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &examplepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: "example0"})
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &examplepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Equal(t, []string{"example0"}, list.Configs)
}

// Test_ExampleService_DeleteConfig verifies that deleting a config removes
// it so a later read returns NotFound.
func Test_ExampleService_DeleteConfig(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: "example0"})
	require.NoError(t, err)

	_, err = svc.DeleteConfig(ctx, &examplepb.DeleteConfigRequest{Name: "example0"})
	require.NoError(t, err)

	_, err = svc.ShowConfig(ctx, &examplepb.ShowConfigRequest{Name: "example0"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_ExampleService_DeleteMissing verifies that deleting an absent config
// returns NotFound without mutating the store.
func Test_ExampleService_DeleteMissing(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.DeleteConfig(t.Context(), &examplepb.DeleteConfigRequest{Name: "absent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_ExampleService_DeleteConfig_Refused verifies that a refused delete
// maps its error kind to the status code and leaves the config in place.
func Test_ExampleService_DeleteConfig_Refused(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{
			name: "referenced by a chain",
			err:  fmt.Errorf("module 'example:example0' not found in chain 'chain0': %w", ffi.ErrFailedPrecondition),
			code: codes.FailedPrecondition,
		},
		{
			name: "backend failure",
			err:  errInjectedBackend,
			code: codes.Internal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := example.NewExampleService(&refusingDeleteBackend{err: tc.err})
			ctx := t.Context()

			_, err := svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: "example0"})
			require.NoError(t, err)

			resp, err := svc.DeleteConfig(ctx, &examplepb.DeleteConfigRequest{Name: "example0"})
			require.Nil(t, resp)
			require.Equal(t, tc.code, status.Code(err))

			show, err := svc.ShowConfig(ctx, &examplepb.ShowConfigRequest{Name: "example0"})
			require.NoError(t, err)
			require.NotNil(t, show)
		})
	}
}

// Test_ExampleService_UpdateFailureAtomic verifies that a failed update
// leaves the previously applied config intact and queryable.
func Test_ExampleService_UpdateFailureAtomic(t *testing.T) {
	svc := example.NewExampleService(&flakyBackend{})
	ctx := t.Context()
	name := "example0"

	_, err := svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: name})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: name})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &examplepb.ShowConfigRequest{Name: name})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, name, show.Name)
}

// Test_ExampleService_ConcurrentAccess verifies that interleaved creates,
// lists, and reads from many goroutines do not race or lose entries.
func Test_ExampleService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.UpdateConfig(ctx, &examplepb.UpdateConfigRequest{Name: name})
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
				_, err := svc.ListConfigs(ctx, &examplepb.ListConfigsRequest{})
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
				svc.ShowConfig(ctx, &examplepb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())

	list, err := svc.ListConfigs(ctx, &examplepb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Len(t, list.Configs, goroutines*iterations)
}
